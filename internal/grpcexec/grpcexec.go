// Package grpcexec is everything a gRPC call needs before and after the wire:
// resolving a method from .proto files or server reflection, building request
// messages and metadata, rendering message templates from descriptors, and
// generating the equivalent grpcurl command.
//
// US-063. The dialling and streaming themselves stay on *App for now -- those
// are methods and hold session state; this is the part that was already free.
package grpcexec

import (
	"LiteAPI/internal/auth/wsse"
	"LiteAPI/internal/scalar"
	"LiteAPI/internal/transport"
	"LiteAPI/internal/types"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func UserAgentFromHeaders(headers []types.KeyValue, vars map[string]string) string {
	for _, header := range headers {
		if !header.Enabled {
			continue
		}
		name := strings.TrimSpace(interpolate(header.Name, vars))
		if !isGRPCUserAgentHeaderName(name) {
			continue
		}
		if value := strings.TrimSpace(interpolate(header.Value, vars)); value != "" {
			return value
		}
	}
	return ""
}

func UnixSocketPath(parsed *url.URL) (string, error) {
	rawPath := parsed.Path
	if rawPath == "" {
		rawPath = parsed.Opaque
	}
	if rawPath == "" && parsed.Host != "" {
		rawPath = parsed.Host
	}
	socketPath, err := url.PathUnescape(strings.TrimSpace(rawPath))
	if err != nil {
		return "", err
	}
	if socketPath == "" {
		return "", errors.New("gRPC Unix socket path is required")
	}
	if !filepath.IsAbs(socketPath) {
		return "", errors.New("gRPC Unix socket path must be absolute")
	}
	return socketPath, nil
}

func UnixDialConfig(socketPath string) DialConfig {
	return DialConfig{
		Target:      "passthrough:///liteapi-unix-socket",
		Credentials: insecure.NewCredentials(),
		Options: []grpc.DialOption{grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		})},
	}
}

type grpcurlTarget struct {
	Scheme     string
	Host       string
	PathPrefix string
	SocketPath string
}

func GenerateGrpcurlCommand(collection types.Collection, item types.RequestItem, vars map[string]string) (string, error) {
	targetURL := interpolate(item.URL, vars)
	target, err := grpcurlTargetForURL(targetURL)
	if err != nil {
		return "", err
	}
	method := interpolate(item.Method, vars)
	if strings.TrimSpace(method) == "" || strings.EqualFold(strings.TrimSpace(method), "CALL") {
		return "", errors.New("gRPC method is required")
	}
	parts := []string{"grpcurl"}
	switch target.Scheme {
	case "unix", "grpc+unix":
		parts = append(parts, "-plaintext", "-unix", "-authority localhost")
	case "grpcs", "https":
		if !item.Settings.VerifyTLS {
			parts = append(parts, "-insecure")
		}
		if cert, ok := matchingClientCertificateConfig(collection.ClientCertificates, targetURL, vars); ok && strings.EqualFold(scalar.FirstNonEmpty(cert.Type, "cert"), "cert") {
			certPath := transport.ResolveCollectionRelativePath(collection.Path, interpolate(cert.CertFilePath, vars))
			keyPath := transport.ResolveCollectionRelativePath(collection.Path, interpolate(cert.KeyFilePath, vars))
			if strings.TrimSpace(certPath) != "" && strings.TrimSpace(keyPath) != "" {
				parts = append(parts, "-cert "+ShellSingleQuote(certPath), "-key "+ShellSingleQuote(keyPath))
			}
		}
	default:
		parts = append(parts, "-plaintext")
	}
	for _, header := range EnabledKeyValues(item.Headers) {
		name := strings.TrimSpace(interpolate(header.Name, vars))
		if name == "" {
			continue
		}
		parts = append(parts, "-H "+ShellSingleQuote(name+": "+interpolate(header.Value, vars)))
	}
	if protoPath := strings.TrimSpace(interpolate(item.ProtoPath, vars)); protoPath != "" {
		resolvedProtoPath := transport.ResolveCollectionRelativePath(collection.Path, protoPath)
		parts = append(parts, "-import-path "+ShellSingleQuote(filepath.Dir(resolvedProtoPath)))
		parts = append(parts, "-proto "+ShellSingleQuote(filepath.Base(resolvedProtoPath)))
	}
	messages := GrpcurlRequestMessages(item, vars)
	streamingClient := item.GrpcMethodType == "client-streaming" || item.GrpcMethodType == "bidi-streaming"
	if len(messages) > 0 {
		if streamingClient {
			parts = append(parts, "-d @")
		} else {
			parts = append(parts, "-d "+ShellSingleQuote(GrpcurlMessageContent(messages[0])))
		}
	}
	if target.SocketPath != "" {
		parts = append(parts, ShellSingleQuote(target.SocketPath))
	} else {
		parts = append(parts, target.Host)
	}
	parts = append(parts, grpcurlFullMethod(target.PathPrefix, method))
	if streamingClient && len(messages) > 0 {
		stdinMessages := make([]string, 0, len(messages))
		for _, message := range messages {
			stdinMessages = append(stdinMessages, GrpcurlMessageContent(message))
		}
		parts = append(parts, "<< EOF\n"+strings.Join(stdinMessages, "\n")+"\nEOF")
	}
	return strings.Join(parts, " "), nil
}

func grpcurlTargetForURL(rawURL string) (grpcurlTarget, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return grpcurlTarget{}, errors.New("gRPC URL is required")
	}
	if !strings.Contains(rawURL, "://") && !strings.HasPrefix(strings.ToLower(rawURL), "unix:") {
		return grpcurlTarget{Scheme: "grpc", Host: rawURL}, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return grpcurlTarget{}, err
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "unix", "grpc+unix":
		socketPath, err := UnixSocketPath(parsed)
		if err != nil {
			return grpcurlTarget{}, err
		}
		return grpcurlTarget{Scheme: scheme, SocketPath: socketPath}, nil
	case "grpc", "grpcs", "http", "https":
		host := parsed.Host
		if host == "" {
			host = strings.TrimPrefix(parsed.Opaque, "//")
		}
		if host == "" {
			return grpcurlTarget{}, errors.New("gRPC URL host is required")
		}
		return grpcurlTarget{Scheme: scheme, Host: host, PathPrefix: strings.Trim(parsed.Path, "/")}, nil
	default:
		return grpcurlTarget{}, fmt.Errorf("unsupported gRPC URL scheme %q", parsed.Scheme)
	}
}

func matchingClientCertificateConfig(certs []types.ClientCertificateConfig, requestURL string, vars map[string]string) (types.ClientCertificateConfig, bool) {
	for _, cert := range transport.NormalizeClientCertificates(certs) {
		if transport.ClientCertificateDomainMatches(requestURL, interpolate(cert.Domain, vars)) {
			return cert, true
		}
	}
	return types.ClientCertificateConfig{}, false
}

func GrpcurlRequestMessages(item types.RequestItem, vars map[string]string) []types.GrpcMessage {
	if len(item.GrpcMessages) > 0 {
		messages := make([]types.GrpcMessage, 0, len(item.GrpcMessages))
		for index, message := range item.GrpcMessages {
			name := strings.TrimSpace(message.Name)
			if name == "" {
				name = fmt.Sprintf("message %d", index+1)
			}
			messages = append(messages, types.GrpcMessage{Name: name, Content: interpolate(message.Content, vars)})
		}
		return messages
	}
	if content := strings.TrimSpace(RequestContent(item, vars)); content != "" {
		return []types.GrpcMessage{{Name: "message 1", Content: content}}
	}
	return nil
}

func GrpcurlMessageContent(message types.GrpcMessage) string {
	return strings.ReplaceAll(message.Content, "\t", "  ")
}

func grpcurlFullMethod(pathPrefix, method string) string {
	method = strings.TrimPrefix(strings.TrimSpace(method), "/")
	pathPrefix = strings.Trim(pathPrefix, "/")
	if pathPrefix == "" {
		return method
	}
	return pathPrefix + "/" + method
}

func CompileMethod(ctx context.Context, item types.RequestItem, collection types.Collection, vars map[string]string) (MethodBinding, error) {
	files, compileFiles, err := compileGRPCFiles(ctx, item, collection, vars)
	if err != nil {
		return MethodBinding{}, err
	}
	serviceName, methodName, err := grpcServiceAndMethod(item.Method, vars)
	if err != nil {
		return MethodBinding{}, err
	}
	for _, file := range files {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			fullServiceName := string(service.FullName())
			if fullServiceName != serviceName && string(service.Name()) != serviceName && !strings.HasSuffix(fullServiceName, "."+serviceName) {
				continue
			}
			method := service.Methods().ByName(protoreflect.Name(methodName))
			if method == nil {
				return MethodBinding{}, fmt.Errorf("gRPC method %q not found on service %q", methodName, fullServiceName)
			}
			return MethodBinding{
				Descriptor: method,
				FullMethod: "/" + fullServiceName + "/" + string(method.Name()),
			}, nil
		}
	}
	return MethodBinding{}, fmt.Errorf("gRPC service %q not found in %s", serviceName, strings.Join(compileFiles, ", "))
}

func compileGRPCFiles(ctx context.Context, item types.RequestItem, collection types.Collection, vars map[string]string) ([]protoreflect.FileDescriptor, []string, error) {
	compileFiles, importPaths, _, err := grpcProtoCompileInputs(item, collection, vars)
	if err != nil {
		return nil, nil, err
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: importPaths}),
	}
	files, err := compiler.Compile(ctx, compileFiles...)
	if err != nil {
		return nil, nil, err
	}
	result := make([]protoreflect.FileDescriptor, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	return result, compileFiles, nil
}

func ResolveMethod(ctx context.Context, conn grpc.ClientConnInterface, item types.RequestItem, collection types.Collection, vars map[string]string) (MethodBinding, error) {
	if HasProtoInputs(item, collection, vars) {
		return CompileMethod(ctx, item, collection, vars)
	}
	return ReflectMethod(ctx, conn, item, vars)
}

func ReflectMethod(ctx context.Context, conn grpc.ClientConnInterface, item types.RequestItem, vars map[string]string) (MethodBinding, error) {
	serviceName, methodName, err := grpcServiceAndMethod(item.Method, vars)
	if err != nil {
		return MethodBinding{}, err
	}
	resolvedService, err := grpcReflectionServiceName(ctx, conn, serviceName)
	if err != nil {
		return MethodBinding{}, err
	}
	response, err := grpcReflectionRequest(ctx, conn, &reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: resolvedService},
	})
	if err != nil {
		return MethodBinding{}, err
	}
	fileResponse := response.GetFileDescriptorResponse()
	if fileResponse == nil {
		if reflectionErr := response.GetErrorResponse(); reflectionErr != nil {
			return MethodBinding{}, fmt.Errorf("gRPC reflection failed for %q: %s", resolvedService, reflectionErr.GetErrorMessage())
		}
		return MethodBinding{}, fmt.Errorf("gRPC reflection returned no descriptor for %q", resolvedService)
	}
	files, err := grpcFilesFromReflectionResponse(fileResponse)
	if err != nil {
		return MethodBinding{}, err
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(resolvedService))
	if err != nil {
		return MethodBinding{}, fmt.Errorf("gRPC reflected service %q not found: %w", resolvedService, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return MethodBinding{}, fmt.Errorf("gRPC reflected symbol %q is not a service", resolvedService)
	}
	method := service.Methods().ByName(protoreflect.Name(methodName))
	if method == nil {
		return MethodBinding{}, fmt.Errorf("gRPC method %q not found on reflected service %q", methodName, resolvedService)
	}
	return MethodBinding{
		Descriptor: method,
		FullMethod: "/" + string(service.FullName()) + "/" + string(method.Name()),
	}, nil
}

func grpcReflectionServiceName(ctx context.Context, conn grpc.ClientConnInterface, requested string) (string, error) {
	if strings.Contains(requested, ".") {
		return requested, nil
	}
	response, err := grpcReflectionRequest(ctx, conn, &reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	})
	if err != nil {
		return "", err
	}
	list := response.GetListServicesResponse()
	if list == nil {
		return requested, nil
	}
	for _, service := range list.GetService() {
		name := strings.TrimSpace(service.GetName())
		if name == requested || strings.HasSuffix(name, "."+requested) {
			return name, nil
		}
	}
	return requested, nil
}

func grpcReflectionRequest(ctx context.Context, conn grpc.ClientConnInterface, req *reflectionpb.ServerReflectionRequest) (*reflectionpb.ServerReflectionResponse, error) {
	client := reflectionpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("open gRPC reflection stream: %w", err)
	}
	// Half-closing an already-drained reflection stream has no recoverable failure.
	defer func() { _ = stream.CloseSend() }()
	if err := stream.Send(req); err != nil {
		return nil, fmt.Errorf("send gRPC reflection request: %w", err)
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receive gRPC reflection response: %w", err)
	}
	if reflectionErr := response.GetErrorResponse(); reflectionErr != nil {
		return response, fmt.Errorf("gRPC reflection error %d: %s", reflectionErr.GetErrorCode(), reflectionErr.GetErrorMessage())
	}
	return response, nil
}

func ListMethodsFromProto(ctx context.Context, item types.RequestItem, collection types.Collection, vars map[string]string) ([]types.GRPCMethodInfo, error) {
	files, _, err := compileGRPCFiles(ctx, item, collection, vars)
	if err != nil {
		return nil, err
	}
	return grpcMethodInfosFromFiles(files), nil
}

func ListMethodsFromReflection(ctx context.Context, conn grpc.ClientConnInterface) ([]types.GRPCMethodInfo, error) {
	response, err := grpcReflectionRequest(ctx, conn, &reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	})
	if err != nil {
		return nil, err
	}
	list := response.GetListServicesResponse()
	if list == nil {
		return nil, errors.New("gRPC reflection returned no services")
	}
	result := []types.GRPCMethodInfo{}
	seenMethods := map[string]bool{}
	for _, service := range list.GetService() {
		serviceName := strings.TrimSpace(service.GetName())
		if serviceName == "" || strings.HasPrefix(serviceName, "grpc.reflection.") {
			continue
		}
		methods, err := grpcMethodsForReflectedService(ctx, conn, serviceName)
		if err != nil {
			continue
		}
		for _, method := range methods {
			if !seenMethods[method.Path] {
				seenMethods[method.Path] = true
				result = append(result, method)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func grpcMethodsForReflectedService(ctx context.Context, conn grpc.ClientConnInterface, serviceName string) ([]types.GRPCMethodInfo, error) {
	response, err := grpcReflectionRequest(ctx, conn, &reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: serviceName},
	})
	if err != nil {
		return nil, err
	}
	files, err := grpcFilesFromReflectionResponse(response.GetFileDescriptorResponse())
	if err != nil {
		return nil, err
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil, err
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("gRPC reflected symbol %q is not a service", serviceName)
	}
	return grpcMethodInfosForService(service), nil
}

func grpcFilesFromReflectionResponse(fileResponse *reflectionpb.FileDescriptorResponse) (*protoregistry.Files, error) {
	if fileResponse == nil {
		return nil, errors.New("gRPC reflection returned no descriptor")
	}
	descriptorSet := &descriptorpb.FileDescriptorSet{}
	for _, raw := range fileResponse.GetFileDescriptorProto() {
		fd := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, fd); err != nil {
			return nil, fmt.Errorf("parse reflected descriptor: %w", err)
		}
		descriptorSet.File = append(descriptorSet.File, fd)
	}
	files, err := protodesc.NewFiles(descriptorSet)
	if err != nil {
		return nil, fmt.Errorf("link reflected descriptors: %w", err)
	}
	return files, nil
}

func grpcMethodInfosFromFiles(files []protoreflect.FileDescriptor) []types.GRPCMethodInfo {
	result := []types.GRPCMethodInfo{}
	for _, file := range files {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			result = append(result, grpcMethodInfosForService(services.Get(i))...)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func grpcMethodInfosForService(service protoreflect.ServiceDescriptor) []types.GRPCMethodInfo {
	result := make([]types.GRPCMethodInfo, 0, service.Methods().Len())
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		template, _ := TemplateForMessage(method.Input())
		result = append(result, types.GRPCMethodInfo{
			Path:       string(service.FullName()) + "/" + string(method.Name()),
			Service:    string(service.FullName()),
			Name:       string(method.Name()),
			Type:       GRPCMethodStorageType(method),
			InputType:  string(method.Input().FullName()),
			OutputType: string(method.Output().FullName()),
			Template:   template,
		})
	}
	return result
}

func TemplateForMessage(message protoreflect.MessageDescriptor) (string, error) {
	value := grpcTemplateObject(message, map[protoreflect.FullName]int{})
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func grpcTemplateObject(message protoreflect.MessageDescriptor, seen map[protoreflect.FullName]int) map[string]interface{} {
	if seen[message.FullName()] > 0 {
		return map[string]interface{}{}
	}
	seen[message.FullName()]++
	defer func() { seen[message.FullName()]-- }()

	result := map[string]interface{}{}
	usedOneofs := map[protoreflect.FullName]bool{}
	fields := message.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		oneof := field.ContainingOneof()
		if oneof != nil && !oneof.IsSynthetic() {
			if usedOneofs[oneof.FullName()] {
				continue
			}
			usedOneofs[oneof.FullName()] = true
		}
		result[field.JSONName()] = grpcTemplateValue(field, seen)
	}
	return result
}

func grpcTemplateValue(field protoreflect.FieldDescriptor, seen map[protoreflect.FullName]int) interface{} {
	if field.IsMap() {
		return map[string]interface{}{"key": grpcTemplateSingularValue(field.MapValue(), seen)}
	}
	if field.IsList() {
		return []interface{}{grpcTemplateSingularValue(field, seen)}
	}
	return grpcTemplateSingularValue(field, seen)
}

func grpcTemplateSingularValue(field protoreflect.FieldDescriptor, seen map[protoreflect.FullName]int) interface{} {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return true
	case protoreflect.EnumKind:
		if field.Enum().Values().Len() > 0 {
			return string(field.Enum().Values().Get(0).Name())
		}
		return ""
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return 0
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return ""
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if field.Message() == nil {
			return map[string]interface{}{}
		}
		return grpcTemplateObject(field.Message(), seen)
	default:
		return nil
	}
}

func HasProtoInputs(item types.RequestItem, collection types.Collection, vars map[string]string) bool {
	if strings.TrimSpace(interpolate(item.ProtoPath, vars)) != "" {
		return true
	}
	for _, protoFile := range collection.Protobuf.ProtoFiles {
		if strings.TrimSpace(interpolate(protoFile.Path, vars)) != "" {
			return true
		}
	}
	return false
}

func grpcProtoCompileInputs(item types.RequestItem, collection types.Collection, vars map[string]string) ([]string, []string, []string, error) {
	baseDirs := grpcProtoBaseDirs(item, collection)
	importPaths := []string{}
	seenImports := map[string]bool{}
	addImportPath := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		cleaned := filepath.Clean(path)
		if seenImports[cleaned] {
			return
		}
		seenImports[cleaned] = true
		importPaths = append(importPaths, cleaned)
	}
	for _, base := range baseDirs {
		addImportPath(base)
	}
	for _, importPath := range collection.Protobuf.ImportPaths {
		if !importPath.Enabled {
			continue
		}
		resolved := transport.ResolveCollectionRelativePath(collection.Path, interpolate(importPath.Path, vars))
		addImportPath(resolved)
	}

	rawRequestPath := strings.TrimSpace(interpolate(item.ProtoPath, vars))
	compileFiles := []string{}
	fullPaths := []string{}
	addCompilePath := func(rawPath string) {
		compileFile, fullPath, protoDir := grpcProtoCompileInput(rawPath, baseDirs)
		if compileFile == "" {
			return
		}
		compileFiles = append(compileFiles, compileFile)
		if fullPath != "" {
			fullPaths = append(fullPaths, fullPath)
		}
		addImportPath(protoDir)
	}
	if rawRequestPath != "" {
		addCompilePath(rawRequestPath)
	} else {
		for _, protoFile := range collection.Protobuf.ProtoFiles {
			addCompilePath(interpolate(protoFile.Path, vars))
		}
	}
	if len(compileFiles) == 0 {
		return nil, nil, nil, errors.New("gRPC proto path is required for execution")
	}
	return compileFiles, importPaths, fullPaths, nil
}

func grpcProtoBaseDirs(item types.RequestItem, collection types.Collection) []string {
	baseDirs := []string{}
	if strings.TrimSpace(item.FilePath) != "" {
		baseDirs = append(baseDirs, filepath.Dir(item.FilePath))
	}
	if strings.TrimSpace(collection.Path) != "" {
		baseDirs = append(baseDirs, collection.Path)
	}
	baseDirs = append(baseDirs, ".")
	return baseDirs
}

func grpcProtoCompileInput(rawPath string, baseDirs []string) (string, string, string) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", "", ""
	}
	if filepath.IsAbs(rawPath) {
		return filepath.Base(rawPath), rawPath, filepath.Dir(rawPath)
	}
	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}
	for _, base := range baseDirs {
		if strings.TrimSpace(base) == "" {
			continue
		}
		fullPath := filepath.Join(base, rawPath)
		if _, err := os.Stat(fullPath); err == nil {
			return filepath.ToSlash(rawPath), fullPath, filepath.Dir(fullPath)
		}
	}
	return filepath.ToSlash(rawPath), filepath.Join(baseDirs[0], rawPath), filepath.Dir(filepath.Join(baseDirs[0], rawPath))
}

func grpcServiceAndMethod(method string, vars map[string]string) (string, string, error) {
	method = strings.Trim(strings.TrimSpace(interpolate(method, vars)), "/")
	if method == "" || strings.EqualFold(method, "CALL") {
		return "", "", errors.New("gRPC method is required in package.Service/Method form")
	}
	serviceName, methodName, ok := strings.Cut(method, "/")
	if !ok || strings.TrimSpace(serviceName) == "" || strings.TrimSpace(methodName) == "" {
		return "", "", errors.New("gRPC method must use package.Service/Method form")
	}
	return strings.TrimSpace(serviceName), strings.TrimSpace(methodName), nil
}

func RequestContent(item types.RequestItem, vars map[string]string) string {
	content := ""
	if len(item.GrpcMessages) > 0 {
		content = item.GrpcMessages[0].Content
	}
	content = strings.TrimSpace(interpolate(content, vars))
	if content == "" {
		return "{}"
	}
	return content
}

func RequestMessages(item types.RequestItem, binding MethodBinding, vars map[string]string) ([]*dynamicpb.Message, error) {
	contents := []string{}
	for _, message := range item.GrpcMessages {
		if strings.TrimSpace(message.Content) != "" {
			contents = append(contents, message.Content)
		}
	}
	if len(contents) == 0 {
		contents = []string{"{}"}
	}
	if !binding.Descriptor.IsStreamingClient() && len(contents) > 1 {
		contents = contents[:1]
	}
	requests := make([]*dynamicpb.Message, 0, len(contents))
	for index, content := range contents {
		req := dynamicpb.NewMessage(binding.Descriptor.Input())
		content = strings.TrimSpace(interpolate(content, vars))
		if content == "" {
			content = "{}"
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(content), req); err != nil {
			return nil, fmt.Errorf("parse gRPC request message %d JSON: %w", index+1, err)
		}
		requests = append(requests, req)
	}
	return requests, nil
}

func OutgoingContext(ctx context.Context, item types.RequestItem, vars map[string]string, oauth2Fetcher func(types.OAuth2Auth, map[string]string) (string, error)) (context.Context, error) {
	pairs := []string{}
	for _, header := range item.Headers {
		if header.Enabled && strings.TrimSpace(header.Name) != "" {
			name := interpolate(header.Name, vars)
			if isGRPCUserAgentHeaderName(name) {
				continue
			}
			name, value := grpcOutgoingMetadataValue(name, interpolate(header.Value, vars))
			pairs = append(pairs, name, value)
		}
	}
	switch strings.ToLower(item.Auth.Mode) {
	case "bearer":
		if token := strings.TrimSpace(interpolate(item.Auth.Token, vars)); token != "" {
			pairs = append(pairs, "authorization", "Bearer "+token)
		}
	case "oauth2":
		token := strings.TrimSpace(interpolate(item.Auth.Token, vars))
		if token == "" && strings.TrimSpace(item.Auth.OAuth2.GrantType) != "" && oauth2Fetcher != nil {
			fetchedToken, err := oauth2Fetcher(item.Auth.OAuth2, vars)
			if err != nil {
				return ctx, err
			}
			token = strings.TrimSpace(fetchedToken)
		}
		if token != "" {
			prefix := strings.TrimSpace(interpolate(item.Auth.OAuth2.TokenHeaderPrefix, vars))
			if prefix == "" {
				prefix = "Bearer"
			}
			pairs = append(pairs, "authorization", strings.TrimSpace(prefix+" "+token))
		}
	case "basic":
		username := interpolate(item.Auth.Username, vars)
		password := interpolate(item.Auth.Password, vars)
		if username != "" || password != "" {
			pairs = append(pairs, "authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
		}
	case "apikey":
		if strings.EqualFold(scalar.FirstNonEmpty(item.Auth.APILocation, "header"), "header") && strings.TrimSpace(item.Auth.APIKey) != "" {
			name, value := grpcOutgoingMetadataValue(interpolate(item.Auth.APIKey, vars), interpolate(item.Auth.APIValue, vars))
			pairs = append(pairs, name, value)
		}
	case "wsse":
		headers := http.Header{}
		wsse.ApplyHeader(headers, interpolate(item.Auth.Username, vars), interpolate(item.Auth.Password, vars), time.Now().UTC())
		for name, values := range headers {
			for _, value := range values {
				pairName, pairValue := grpcOutgoingMetadataValue(name, value)
				pairs = append(pairs, pairName, pairValue)
			}
		}
	}
	if len(pairs) == 0 {
		return ctx, nil
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...), nil
}

func grpcOutgoingMetadataValue(name, value string) (string, string) {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), "-bin") {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value)); err == nil {
			value = string(decoded)
		}
	}
	return name, value
}

func isGRPCUserAgentHeaderName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "User-Agent")
}

func AddMetadata(headers map[string]string, prefix string, md metadata.MD) {
	for name, values := range md {
		if len(values) > 0 {
			headers[prefix+name] = grpcMetadataDisplayValue(name, values)
		}
	}
}

func MetadataRows(md metadata.MD) []types.KeyValue {
	if len(md) == 0 {
		return nil
	}
	rows := make([]types.KeyValue, 0, len(md))
	for name, values := range md {
		if len(values) == 0 {
			continue
		}
		rows = append(rows, types.KeyValue{Name: name, Value: grpcMetadataDisplayValue(name, values), Enabled: true})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}

func MetadataRowsFromMap(values map[string]string) []types.KeyValue {
	if len(values) == 0 {
		return nil
	}
	rows := make([]types.KeyValue, 0, len(values))
	for name, value := range values {
		if strings.TrimSpace(name) == "" {
			continue
		}
		rows = append(rows, types.KeyValue{Name: name, Value: value, Enabled: true})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}

func grpcMetadataDisplayValue(name string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	display := make([]string, 0, len(values))
	binary := strings.HasSuffix(strings.ToLower(name), "-bin")
	for _, value := range values {
		if binary {
			display = append(display, base64.StdEncoding.EncodeToString([]byte(value)))
		} else {
			display = append(display, value)
		}
	}
	return strings.Join(display, ", ")
}

func EnabledKeyValues(rows []types.KeyValue) []types.KeyValue {
	result := []types.KeyValue{}
	for _, row := range rows {
		if row.Enabled && strings.TrimSpace(row.Name) != "" {
			result = append(result, types.KeyValue{Name: strings.TrimSpace(row.Name), Value: row.Value, Enabled: true})
		}
	}
	return result
}

func GRPCMethodStorageType(method protoreflect.MethodDescriptor) string {
	switch {
	case method.IsStreamingClient() && method.IsStreamingServer():
		return "bidi-streaming"
	case method.IsStreamingClient():
		return "client-streaming"
	case method.IsStreamingServer():
		return "server-streaming"
	default:
		return "unary"
	}
}

func ShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type MethodBinding struct {
	Descriptor protoreflect.MethodDescriptor
	FullMethod string
}

type DialConfig struct {
	Target      string
	Credentials credentials.TransportCredentials
	TLSConfig   *tls.Config
	Options     []grpc.DialOption
}

func (config DialConfig) DialOptions() []grpc.DialOption {
	creds := config.Credentials
	if config.TLSConfig != nil {
		creds = credentials.NewTLS(config.TLSConfig)
	}
	options := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	options = append(options, config.Options...)
	return options
}

// interpolate expands template variables in URLs, metadata and message bodies.
// Settable for the same reason as internal/transport's: the interpolator is
// still in package main, and this package must not wait on US-069.
var interpolate = func(value string, vars map[string]string) string { return value }

// SetInterpolator installs the template-variable expander.
func SetInterpolator(fn func(string, map[string]string) string) {
	if fn != nil {
		interpolate = fn
	}
}
