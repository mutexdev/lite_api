package main

import (
	"LiteAPI/internal/auth/oauth1"
	"LiteAPI/internal/auth/wsse"
	"LiteAPI/internal/codegen"
	"LiteAPI/internal/grpcexec"
	"LiteAPI/internal/importers"
	"LiteAPI/internal/transport"
	"LiteAPI/internal/types"
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"

	"LiteAPI/internal/auth/awsv4"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpc_testing "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"
	"software.sslmate.com/src/go-pkcs12"
)

func exactFileExists(t *testing.T, dir, name string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return true
		}
	}
	return false
}

func variablesByName(values []Variable) map[string]Variable {
	result := map[string]Variable{}
	for _, variable := range values {
		result[variable.Name] = variable
	}
	return result
}

func testClientCertificate(t *testing.T) ([]byte, []byte, *rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "liteapi-client"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, key, cert
}

func testServerTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "liteapi-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

type dynamicGreeterService interface {
	dynamicGreeterService()
}

type dynamicGreeter struct{}

func (dynamicGreeter) dynamicGreeterService() {}

type reflectedTestService struct {
	grpc_testing.UnimplementedTestServiceServer
	gotMetadata map[string]string
}

func (s *reflectedTestService) UnaryCall(ctx context.Context, req *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	if err := grpc.SendHeader(ctx, metadata.Pairs("x-grpc-initial", "unary-header")); err != nil {
		return nil, err
	}
	_ = grpc.SetTrailer(ctx, metadata.Pairs("x-grpc-trailer", "unary-trailer"))
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		for name, values := range incoming {
			if len(values) > 0 {
				if s.gotMetadata != nil {
					s.gotMetadata[name] = values[0]
				}
			}
		}
	}
	if peerInfo, ok := peer.FromContext(ctx); ok {
		if tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo); ok && len(tlsInfo.State.PeerCertificates) > 0 {
			if s.gotMetadata != nil {
				s.gotMetadata["peer-cert-cn"] = tlsInfo.State.PeerCertificates[0].Subject.CommonName
			}
		}
	}
	return &grpc_testing.SimpleResponse{Username: "reflected", ServerId: "liteapi-test"}, nil
}

func (s *reflectedTestService) StreamingOutputCall(req *grpc_testing.StreamingOutputCallRequest, stream grpc.ServerStreamingServer[grpc_testing.StreamingOutputCallResponse]) error {
	if string(req.GetPayload().GetBody()) == "error-after-one" {
		if err := stream.SendHeader(metadata.Pairs("x-error-initial", "one", "x-error-initial", "two", "x-error-bin", "binary-initial")); err != nil {
			return err
		}
		stream.SetTrailer(metadata.Pairs("x-error-trailer", "trail-one", "x-error-trailer", "trail-two", "x-error-trailer-bin", "binary-trailer"))
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{Body: []byte("partial")}}); err != nil {
			return err
		}
		return status.Error(codes.ResourceExhausted, "stream quota")
	}
	for _, body := range [][]byte{[]byte("server-one"), []byte("server-two")} {
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{Body: body}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *reflectedTestService) StreamingInputCall(stream grpc.ClientStreamingServer[grpc_testing.StreamingInputCallRequest, grpc_testing.StreamingInputCallResponse]) error {
	total := 0
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&grpc_testing.StreamingInputCallResponse{AggregatedPayloadSize: int32(total)})
		}
		if err != nil {
			return err
		}
		total += len(req.GetPayload().GetBody())
	}
}

func (s *reflectedTestService) FullDuplexCall(stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse]) error {
	if err := stream.SendHeader(metadata.Pairs("x-grpc-initial", "bidi-header")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-grpc-trailer", "bidi-trailer"))
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: req.GetPayload()}); err != nil {
			return err
		}
	}
}

func startDynamicGreeterServer(t *testing.T, protoPath string, gotMetadata map[string]string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener.Addr().String(), startDynamicGreeterServerOnListener(t, protoPath, listener, gotMetadata)
}

func startDynamicGreeterServerOnListener(t *testing.T, protoPath string, listener net.Listener, gotMetadata map[string]string) func() {
	t.Helper()
	item := types.NewRequestItem("Greeter", "grpc", 1)
	item.ProtoPath = protoPath
	item.Method = "helloworld.Greeter/SayHello"
	binding, err := grpcexec.CompileMethod(context.Background(), item, Collection{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nameField := binding.Descriptor.Input().Fields().ByName("name")
	messageField := binding.Descriptor.Output().Fields().ByName("message")
	if nameField == nil || messageField == nil {
		t.Fatalf("test proto fields were not compiled: input=%v output=%v", nameField, messageField)
	}
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "helloworld.Greeter",
		HandlerType: (*dynamicGreeterService)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "SayHello",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				req := dynamicpb.NewMessage(binding.Descriptor.Input())
				if err := dec(req); err != nil {
					return nil, err
				}
				handler := func(ctx context.Context, request any) (any, error) {
					if incoming, ok := metadata.FromIncomingContext(ctx); ok {
						for name, values := range incoming {
							if len(values) > 0 {
								gotMetadata[name] = values[0]
							}
						}
					}
					msg := request.(*dynamicpb.Message)
					res := dynamicpb.NewMessage(binding.Descriptor.Output())
					res.Set(messageField, protoreflect.ValueOfString("hello "+msg.Get(nameField).String()))
					return res, nil
				}
				if interceptor == nil {
					return handler(ctx, req)
				}
				return interceptor(ctx, req, &grpc.UnaryServerInfo{Server: srv, FullMethod: binding.FullMethod}, handler)
			},
		}},
	}, dynamicGreeter{})
	go func() {
		_ = server.Serve(listener)
	}()
	return func() {
		server.Stop()
		_ = listener.Close()
	}
}

func startReflectedTestService(t *testing.T, gotMetadata map[string]string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(server, &reflectedTestService{gotMetadata: gotMetadata})
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}
}

func startAuthenticatedReflectedTestService(t *testing.T, expectedMetadata map[string]string) (string, *int32, func()) {
	t.Helper()
	var reflectionHits int32
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	interceptor := func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if strings.Contains(info.FullMethod, "ServerReflectionInfo") {
			atomic.AddInt32(&reflectionHits, 1)
			incoming, _ := metadata.FromIncomingContext(stream.Context())
			for name, expected := range expectedMetadata {
				if got := firstMetadataValue(incoming, name); got != expected {
					return status.Errorf(codes.Unauthenticated, "reflection metadata %s = %q, want %q", name, got, expected)
				}
			}
		}
		return handler(srv, stream)
	}
	server := grpc.NewServer(grpc.StreamInterceptor(interceptor))
	grpc_testing.RegisterTestServiceServer(server, &reflectedTestService{})
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), &reflectionHits, func() {
		server.Stop()
		_ = listener.Close()
	}
}

func firstMetadataValue(md metadata.MD, name string) string {
	values := md.Get(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func sendReflectedGRPCRequest(t *testing.T, address, method string, messages []GrpcMessage) *Response {
	t.Helper()
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Reflected Stream")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	protoPath := ""
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("gRPC response was not recorded: %#v", updated)
	}
	return updated.Response
}

func TestSendRequestInterpolatesExecutesAndRecordsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "Ada" {
			t.Fatalf("expected query interpolation, got %q", got)
		}
		if got := r.Header.Get("X-Token"); got != "abc123" {
			t.Fatalf("expected header interpolation, got %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "Ada" {
			t.Fatalf("expected body interpolation, got %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]

	state, err = app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
		{ID: "name", Name: "name", Value: "Ada", DataType: "string", Enabled: true},
		{ID: "token", Name: "token", Value: "abc123", DataType: "string", Enabled: true, Secret: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	method := http.MethodPost
	targetURL := "{{host}}/post"
	params := []KeyValue{{Name: "q", Value: "{{name}}", Enabled: true}}
	headers := []KeyValue{{Name: "X-Token", Value: "{{token}}", Enabled: true}}
	body := item.Body
	body.Mode = "json"
	body.JSON = `{"name":"{{name}}"}`
	vars := RequestVars{Req: []Variable{{ID: "name", Name: "name", Value: "Ada", DataType: "string", Enabled: true}}}
	assertions := []Assertion{{Expression: "res.status", Operator: "equals", Value: "201", Enabled: true}}
	tests := "expect status equals 201"
	_, err = app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		Method:     &method,
		URL:        &targetURL,
		Params:     &params,
		Headers:    &headers,
		Body:       &body,
		Vars:       &vars,
		Assertions: &assertions,
		Tests:      &tests,
	})
	if err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing response")
	}
	if item.Response.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", item.Response.Status, item.Response.Error)
	}
	if item.Response.Body != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", item.Response.Body)
	}
	decoded, err := base64.StdEncoding.DecodeString(item.Response.BodyBase64)
	if err != nil || string(decoded) != item.Response.Body {
		t.Fatalf("body base64 did not round-trip")
	}
	if len(item.Response.Assertions) != 1 || !item.Response.Assertions[0].Passed {
		t.Fatalf("assertion did not pass: %#v", item.Response.Assertions)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("test DSL did not pass: %#v", item.Response.TestResults)
	}
	if len(state.NetworkLog) != 1 || state.NetworkLog[0].Status != http.StatusCreated {
		t.Fatalf("network log was not recorded: %#v", state.NetworkLog)
	}
	logRow := state.NetworkLog[0]
	if logRow.Method != http.MethodPost || logRow.URL != server.URL+"/post?q=Ada" {
		t.Fatalf("network log request summary mismatch: %#v", logRow)
	}
	if logRow.StatusText != "201 Created" || logRow.Size != len(`{"ok":true}`) || logRow.ResponseBody != `{"ok":true}` {
		t.Fatalf("network log response details mismatch: %#v", logRow)
	}
	if logRow.RequestHeaders["X-Token"] != "abc123" || logRow.RequestHeaders["Content-Type"] != "application/json" {
		t.Fatalf("network log request headers mismatch: %#v", logRow.RequestHeaders)
	}
	if logRow.RequestBody != `{"name":"Ada"}` {
		t.Fatalf("network log request body mismatch: %q", logRow.RequestBody)
	}
	if !strings.Contains(logRow.ResponseHeaders["Content-Type"], "application/json") {
		t.Fatalf("network log response headers mismatch: %#v", logRow.ResponseHeaders)
	}
}

func TestSendGraphQLRequestUsesJSONEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("GraphQL method = %s, want POST", r.Method)
		}
		if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
			t.Fatalf("GraphQL Content-Type = %q, parse error = %v", r.Header.Get("Content-Type"), err)
		}
		var payload struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL payload: %v", err)
		}
		if payload.Query != `query Search($term: String!) { search(term: "Ada") { id } }` {
			t.Fatalf("unexpected GraphQL query: %q", payload.Query)
		}
		if payload.Variables["term"] != "Ada" || payload.Variables["limit"] != float64(2) {
			t.Fatalf("unexpected GraphQL variables: %#v", payload.Variables)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"search":[{"id":"1"}]}}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.UpdateCollectionVariables(collection.ID, []Variable{{ID: "term", Name: "term", Value: "Ada", DataType: "string", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "graphql", "GraphQL transport")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := server.URL + "/graphql"
	body := item.Body
	body.Mode = "graphql"
	body.GraphQLQuery = `query Search($term: String!) { search(term: "{{term}}") { id } }`
	body.GraphQLVariables = `{"term":"{{term}}","limit":2}`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil || updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("GraphQL response was not successful: %#v", updated)
	}
}

func TestEncodeRequestURLMatchesBrunoToggleBehavior(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "query special characters",
			raw:  "https://example.com/api?name=john doe&email=john@example.com",
			want: "https://example.com/api?name=john%20doe&email=john%40example.com",
		},
		{
			name: "path segments",
			raw:  "https://example.com/api/a<b>c/with space",
			want: "https://example.com/api/a%3Cb%3Ec/with%20space",
		},
		{
			name: "hash is data",
			raw:  "https://example.com/api?token=abc#def",
			want: "https://example.com/api?token=abc%23def",
		},
		{
			name: "query values double encode pre-encoded data",
			raw:  "https://example.com/api?name=john%20doe&email=john%40example.com",
			want: "https://example.com/api?name=john%2520doe&email=john%2540example.com",
		},
		{
			name: "path stays single encoded while query double encodes",
			raw:  "https://example.com/path%20with%20spaces?name=john%20doe",
			want: "https://example.com/path%20with%20spaces?name=john%2520doe",
		},
		{
			name: "empty query marker survives",
			raw:  "https://example.com/path?",
			want: "https://example.com/path?",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeRequestURL(tt.raw); got != tt.want {
				t.Fatalf("encodeRequestURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}

	withParamRows := codegen.RequestURLWithParams("https://example.com/api", []KeyValue{{Name: "name", Value: "John Doe", Enabled: true}}, nil, nil)
	if got, want := encodeRequestURL(withParamRows), "https://example.com/api?name=John%20Doe"; got != want {
		t.Fatalf("query param rows were encoded incorrectly: %q, want %q", got, want)
	}
}

func TestHTTPEncodeURLSettingControlsExecutionURL(t *testing.T) {
	seen := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.RequestURI
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]

	rawURL := server.URL + "/api path?name=John Doe&redirect=https://example.test/a?b=c#frag"
	settings := item.Settings
	settings.EncodeURL = true
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &rawURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing encoded response")
	}
	expectedURL := encodeRequestURL(rawURL)
	if item.Response.RequestedURL != expectedURL {
		t.Fatalf("requested URL = %q, want %q", item.Response.RequestedURL, expectedURL)
	}
	encodedURI := readSeenRequestURI(t, seen)
	if !strings.Contains(encodedURI, "/api%20path") || !strings.Contains(encodedURI, "name=John%20Doe") || !strings.Contains(encodedURI, "redirect=https%3A%2F%2Fexample.test%2Fa%3Fb%3Dc%23frag") {
		t.Fatalf("server saw unencoded request URI: %q", encodedURI)
	}

	rawModeURL := server.URL + "/raw path?name=John Doe"
	settings.EncodeURL = false
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &rawModeURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing raw response")
	}
	if item.Response.RequestedURL != rawModeURL {
		t.Fatalf("raw mode rewrote requested URL = %q, want %q", item.Response.RequestedURL, rawModeURL)
	}
}

func readSeenRequestURI(t *testing.T, seen <-chan string) string {
	t.Helper()
	select {
	case uri := <-seen:
		return uri
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request URI")
		return ""
	}
}

func TestHTTPVerifyTLSSettingAllowsSelfSignedServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tls":"ok"}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL

	settings := item.Settings
	settings.VerifyTLS = true
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing verified TLS response")
	}
	if item.Response.Status != 0 || item.Response.Error == "" {
		t.Fatalf("expected self-signed certificate error with verifyTls=true, got status=%d error=%q", item.Response.Status, item.Response.Error)
	}
	if !strings.Contains(strings.ToLower(item.Response.Error), "certificate") {
		t.Fatalf("expected certificate error, got %q", item.Response.Error)
	}

	settings.VerifyTLS = false
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing unverified TLS response")
	}
	if item.Response.Status != http.StatusOK || item.Response.Body != `{"tls":"ok"}` {
		t.Fatalf("expected verifyTls=false to allow self-signed server, got status=%d body=%q error=%q", item.Response.Status, item.Response.Body, item.Response.Error)
	}
}

func TestHTTPCustomCaCertificateAllowsSelfSignedServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"customCa":"ok"}`))
	}))
	defer server.Close()

	caPath := filepath.Join(t.TempDir(), "server-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Request.CustomCaCertificate = CustomCaCertificatePreferences{Enabled: true, FilePath: caPath}
	preferences.Request.KeepDefaultCaCertificates.Enabled = boolPtr(false)
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}

	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL
	settings := item.Settings
	settings.VerifyTLS = true
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing custom CA response")
	}
	if item.Response.Status != http.StatusOK || item.Response.Body != `{"customCa":"ok"}` {
		t.Fatalf("expected custom CA to trust self-signed server, got status=%d body=%q error=%q", item.Response.Status, item.Response.Body, item.Response.Error)
	}

	preferences = state.Preferences
	preferences.Request.CustomCaCertificate.Enabled = false
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Error == "" {
		t.Fatalf("expected disabled custom CA to fail certificate verification, got %#v", item.Response)
	}
}

func TestSSLSessionCacheEnablesTLSResumption(t *testing.T) {
	resumed := make(chan bool, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("expected TLS request")
		} else {
			resumed <- r.TLS.DidResume
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	caPath := filepath.Join(t.TempDir(), "server-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Request.CustomCaCertificate = CustomCaCertificatePreferences{Enabled: true, FilePath: caPath}
	preferences.Request.KeepDefaultCaCertificates.Enabled = boolPtr(false)
	preferences.Cache.SSLSession.Enabled = true
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL
	settings := item.Settings
	settings.VerifyTLS = true
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}

	var ok bool
	for i := 0; i < 2; i++ {
		if i > 0 {
			// US-016: sends of the same posture now share one transport and
			// reuse its pooled connection, so the second request would ride
			// the first request's handshake and never exercise resumption at
			// all. Dropping the pool reproduces the situation resumption
			// exists for — a new connection to a server we have spoken to
			// before — while leaving the app's TLS session cache intact.
			app.transportCache.flush()
		}
		state, err = app.SendRequest(collection.ID, item.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		item, ok = findItemInState(state, collection.ID, item.ID)
		if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
			t.Fatalf("request %d failed: %#v", i+1, item.Response)
		}
	}
	first := readTLSResumeFlag(t, resumed)
	second := readTLSResumeFlag(t, resumed)
	if first {
		t.Fatalf("first TLS handshake should not be resumed")
	}
	if !second {
		t.Fatalf("second TLS handshake should resume when SSL session cache is enabled")
	}

	if _, err := app.ClearSSLSessionCache(); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("request after clearing cache failed: %#v", item.Response)
	}
	if resumedAfterClear := readTLSResumeFlag(t, resumed); resumedAfterClear {
		t.Fatalf("expected clear cache to force a fresh TLS handshake")
	}
}

func TestPreferencesStoreAndSendCookiesAreSeparate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set-alpha":
			http.SetCookie(w, &http.Cookie{Name: "alpha", Value: "one", Path: "/"})
			_, _ = w.Write([]byte("set alpha"))
		case "/echo":
			_, _ = w.Write([]byte(r.Header.Get("Cookie")))
		case "/set-beta":
			http.SetCookie(w, &http.Cookie{Name: "beta", Value: "two", Path: "/"})
			_, _ = w.Write([]byte(r.Header.Get("Cookie")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	setAlphaURL := server.URL + "/set-alpha"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &setAlphaURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cookieNamed(state.Cookies, "alpha") {
		t.Fatalf("expected alpha cookie to be stored, got %#v", state.Cookies)
	}

	preferences := state.Preferences
	preferences.Request.SendCookies = boolPtr(false)
	preferences.Request.StoreCookies = boolPtr(true)
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	echoURL := server.URL + "/echo"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &echoURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing echo response")
	}
	if strings.Contains(item.Response.Body, "alpha=") {
		t.Fatalf("sendCookies=false should suppress stored cookies, got body %q", item.Response.Body)
	}

	preferences = state.Preferences
	preferences.Request.SendCookies = boolPtr(true)
	preferences.Request.StoreCookies = boolPtr(false)
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	setBetaURL := server.URL + "/set-beta"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &setBetaURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing beta response")
	}
	if !strings.Contains(item.Response.Body, "alpha=one") {
		t.Fatalf("sendCookies=true should still send existing cookies, got body %q", item.Response.Body)
	}
	if cookieNamed(state.Cookies, "beta") {
		t.Fatalf("storeCookies=false should not persist new beta cookie, got %#v", state.Cookies)
	}
}

func TestPreferencesRequestTimeoutOverridesHTTPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("slow ok"))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Request.Timeout = 25
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL
	settings := item.Settings
	settings.TimeoutMs = 1000
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Error == "" {
		t.Fatalf("expected global request timeout error, got %#v", item.Response)
	}

	preferences = state.Preferences
	preferences.Request.Timeout = 0
	if _, err := app.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || item.Response.Body != "slow ok" {
		t.Fatalf("expected per-request timeout to allow slow response, got %#v", item.Response)
	}
}

func cookieNamed(cookies []CookieEntry, name string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return true
		}
	}
	return false
}

func readTLSResumeFlag(t *testing.T, resumed <-chan bool) bool {
	t.Helper()
	select {
	case value := <-resumed:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TLS resume flag")
		return false
	}
}

func TestCollectionManualProxyExecutesHTTPRequest(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		if r.Header.Get("X-Through-Proxy") != "yes" {
			t.Fatalf("request header was not forwarded through proxy: %q", r.Header.Get("X-Through-Proxy"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"proxied":true}`))
	}))
	defer target.Close()

	var proxyHits int32
	var proxiedURL string
	var proxyAuth string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		proxiedURL = r.URL.String()
		proxyAuth = r.Header.Get("Proxy-Authorization")
		if !r.URL.IsAbs() {
			t.Fatalf("expected absolute-form proxy request URL, got %q", r.URL.String())
		}
		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			t.Fatal(err)
		}
		outReq.Header = r.Header.Clone()
		outReq.Header.Del("Proxy-Authorization")
		outReq.Header.Set("X-Through-Proxy", "yes")
		res, err := http.DefaultClient.Do(outReq)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		for name, values := range res.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.Header().Set("X-Proxy-Seen", "true")
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	defer proxy.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHost, proxyPort, err := net.SplitHostPort(proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.state.Workspaces[0].Collections[0].Proxy = ProxyConfig{
		Inherit:  false,
		Protocol: "http",
		Hostname: proxyHost,
		Port:     proxyPort,
		Auth:     ProxyAuthConfig{Username: "proxy-user", Password: "proxy-pass"},
	}
	app.mu.Unlock()

	method := http.MethodGet
	targetURL := target.URL + "/proxied?via=proxy"
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing response")
	}
	if item.Response.Status != http.StatusOK || !strings.Contains(item.Response.Body, `"proxied":true`) {
		t.Fatalf("expected proxied success, got %#v", item.Response)
	}
	if item.Response.Headers["X-Proxy-Seen"] != "true" {
		t.Fatalf("expected proxy response header, got %#v", item.Response.Headers)
	}
	if atomic.LoadInt32(&proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected one proxy and target hit, proxy=%d target=%d", proxyHits, targetHits)
	}
	if proxiedURL != targetURL {
		t.Fatalf("proxy saw URL %q, want %q", proxiedURL, targetURL)
	}
	if proxyAuth != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-pass")) {
		t.Fatalf("proxy auth header mismatch: %q", proxyAuth)
	}
}

func TestCollectionManualProxyBypassSkipsProxy(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		_, _ = w.Write([]byte(`{"direct":true}`))
	}))
	defer target.Close()

	var proxyHits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		http.Error(w, "proxy should have been bypassed", http.StatusBadGateway)
	}))
	defer proxy.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyHost, proxyPort, err := net.SplitHostPort(proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.state.Workspaces[0].Collections[0].Proxy = ProxyConfig{
		Inherit:     false,
		Protocol:    "http",
		Hostname:    proxyHost,
		Port:        proxyPort,
		BypassProxy: targetURL.Hostname(),
	}
	app.mu.Unlock()

	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &target.URL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("expected direct success, got %#v", item.Response)
	}
	if atomic.LoadInt32(&proxyHits) != 0 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected bypassed proxy and one target hit, proxy=%d target=%d", proxyHits, targetHits)
	}
}

func TestCollectionProxyInheritUsesGlobalManualProxy(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		if r.Header.Get("X-Global-Proxy") != "yes" {
			t.Fatalf("expected request through global proxy, got %q", r.Header.Get("X-Global-Proxy"))
		}
		_, _ = w.Write([]byte(`{"globalProxy":true}`))
	}))
	defer target.Close()

	proxy, proxyHits, proxiedURL, _ := testForwardingHTTPProxy(t, "X-Global-Proxy")
	defer proxy.Close()
	proxyHost, proxyPort := splitTestServerHostPort(t, proxy.URL)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	prefs := state.Preferences
	prefs.Proxy = ProxyPreferences{
		Source: "manual",
		Config: ProxyConfig{Protocol: "http", Hostname: proxyHost, Port: proxyPort},
	}
	if state, err = app.UpdatePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]
	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &target.URL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || !strings.Contains(item.Response.Body, `"globalProxy":true`) {
		t.Fatalf("expected proxied global response, got %#v", item.Response)
	}
	if atomic.LoadInt32(proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected one proxy and target hit, proxy=%d target=%d", atomic.LoadInt32(proxyHits), targetHits)
	}
	if *proxiedURL != target.URL && *proxiedURL != target.URL+"/" {
		t.Fatalf("proxy saw URL %q, want %q", *proxiedURL, target.URL)
	}
}

func TestGlobalProxyOffDisablesEnvironmentProxy(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		_, _ = w.Write([]byte(`{"direct":true}`))
	}))
	defer target.Close()

	var proxyHits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		http.Error(w, "proxy should be disabled", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	prefs := state.Preferences
	prefs.Proxy = ProxyPreferences{Disabled: true, Source: "manual", Config: ProxyConfig{Protocol: "http"}}
	state, err = app.UpdatePreferences(prefs)
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &target.URL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || !strings.Contains(item.Response.Body, `"direct":true`) {
		t.Fatalf("expected direct response, got %#v", item.Response)
	}
	if atomic.LoadInt32(&proxyHits) != 0 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected direct request with proxy disabled, proxy=%d target=%d", proxyHits, targetHits)
	}
}

func TestCollectionProxyInheritUsesSystemEnvironmentProxy(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		if r.Header.Get("X-System-Proxy") != "yes" {
			t.Fatalf("expected request through system proxy, got %q", r.Header.Get("X-System-Proxy"))
		}
		_, _ = w.Write([]byte(`{"systemProxy":true}`))
	}))
	defer target.Close()

	proxy, proxyHits, _, _ := testForwardingHTTPProxy(t, "X-System-Proxy")
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &target.URL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || !strings.Contains(item.Response.Body, `"systemProxy":true`) {
		t.Fatalf("expected system proxy response, got %#v", item.Response)
	}
	if atomic.LoadInt32(proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected one system proxy and target hit, proxy=%d target=%d", atomic.LoadInt32(proxyHits), targetHits)
	}
}

func TestCollectionManualProxyOverridesGlobalProxy(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		if r.Header.Get("X-Collection-Proxy") != "yes" {
			t.Fatalf("expected collection proxy marker, headers=%#v", r.Header)
		}
		if r.Header.Get("X-Global-Proxy") != "" {
			t.Fatalf("global proxy should not have forwarded request")
		}
		_, _ = w.Write([]byte(`{"collectionProxy":true}`))
	}))
	defer target.Close()

	globalProxy, globalHits, _, _ := testForwardingHTTPProxy(t, "X-Global-Proxy")
	defer globalProxy.Close()
	collectionProxy, collectionHits, _, _ := testForwardingHTTPProxy(t, "X-Collection-Proxy")
	defer collectionProxy.Close()
	globalHost, globalPort := splitTestServerHostPort(t, globalProxy.URL)
	collectionHost, collectionPort := splitTestServerHostPort(t, collectionProxy.URL)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	prefs := state.Preferences
	prefs.Proxy = ProxyPreferences{
		Source: "manual",
		Config: ProxyConfig{Protocol: "http", Hostname: globalHost, Port: globalPort},
	}
	if state, err = app.UpdatePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if state, err = app.UpdateCollectionProxy(collection.ID, ProxyConfig{Inherit: false, Protocol: "http", Hostname: collectionHost, Port: collectionPort}); err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &target.URL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("expected collection proxy response, got %#v", item.Response)
	}
	if atomic.LoadInt32(globalHits) != 0 || atomic.LoadInt32(collectionHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected collection proxy override, global=%d collection=%d target=%d", atomic.LoadInt32(globalHits), atomic.LoadInt32(collectionHits), targetHits)
	}
}

func TestPACProxyRoutesFromFileAndFallsBackDirect(t *testing.T) {
	var targetHits int32
	var lastProxyMarker string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		lastProxyMarker = r.Header.Get("X-PAC-Proxy")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	proxy, proxyHits, _, _ := testForwardingHTTPProxy(t, "X-PAC-Proxy")
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	pacPath := filepath.Join(t.TempDir(), "proxy.pac")
	if err := os.WriteFile(pacPath, []byte(fmt.Sprintf(`function FindProxyForURL(url, host) { return "PROXY %s"; }`, proxyURL.Host)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	prefs := state.Preferences
	prefs.Proxy = ProxyPreferences{
		Source: "pac",
		PAC:    ProxyPACConfig{Source: "file://" + pacPath},
		Config: ProxyConfig{Protocol: "http"},
	}
	if state, err = app.UpdatePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &target.URL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || lastProxyMarker != "yes" {
		t.Fatalf("expected PAC proxied response, marker=%q response=%#v", lastProxyMarker, item.Response)
	}
	if atomic.LoadInt32(proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected PAC proxy hit, proxy=%d target=%d", atomic.LoadInt32(proxyHits), targetHits)
	}

	prefs = state.Preferences
	prefs.Proxy.PAC.Source = "file://" + filepath.Join(t.TempDir(), "missing.pac")
	if state, err = app.UpdatePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	lastProxyMarker = ""
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || lastProxyMarker != "" {
		t.Fatalf("expected failed PAC lookup to fall back direct, marker=%q response=%#v", lastProxyMarker, item.Response)
	}
	if atomic.LoadInt32(proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 2 {
		t.Fatalf("expected direct PAC fallback, proxy=%d target=%d", atomic.LoadInt32(proxyHits), targetHits)
	}
}

func TestPACProxyEvaluatesFindProxyForURLLogic(t *testing.T) {
	var targetHits int32
	var lastProxyMarker string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		lastProxyMarker = r.Header.Get("X-PAC-Eval-Proxy")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	proxy, proxyHits, _, _ := testForwardingHTTPProxy(t, "X-PAC-Eval-Proxy")
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	pacPath := filepath.Join(t.TempDir(), "conditional.pac")
	pacScript := fmt.Sprintf(`function FindProxyForURL(url, host) {
  if (shExpMatch(url, "*/proxied") && isInNet(host, "127.0.0.1", "255.255.255.255")) {
    return "PROXY %s";
  }
  return "DIRECT";
}`, proxyURL.Host)
	if err := os.WriteFile(pacPath, []byte(pacScript), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	prefs := state.Preferences
	prefs.Proxy = ProxyPreferences{
		Source: "pac",
		PAC:    ProxyPACConfig{Source: "file://" + pacPath},
		Config: ProxyConfig{Protocol: "http"},
	}
	if state, err = app.UpdatePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	body := item.Body
	body.Mode = "none"
	proxiedURL := target.URL + "/proxied"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &proxiedURL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || lastProxyMarker != "yes" {
		t.Fatalf("expected PAC evaluated proxied response, marker=%q response=%#v", lastProxyMarker, item.Response)
	}
	if atomic.LoadInt32(proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected one evaluated PAC proxy hit, proxy=%d target=%d", atomic.LoadInt32(proxyHits), targetHits)
	}

	directURL := target.URL + "/direct"
	lastProxyMarker = ""
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &directURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK || lastProxyMarker != "" {
		t.Fatalf("expected PAC DIRECT response, marker=%q response=%#v", lastProxyMarker, item.Response)
	}
	if atomic.LoadInt32(proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 2 {
		t.Fatalf("expected PAC direct second request, proxy=%d target=%d", atomic.LoadInt32(proxyHits), targetHits)
	}
}

func TestMacOSScutilProxyOutputResolvesProxyAndBypass(t *testing.T) {
	output := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.internal
    1 : localhost
  }
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : proxy.example.test
  HTTPSEnable : 1
  HTTPSPort : 8443
  HTTPSProxy : secure-proxy.example.test
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : socks.example.test
}`
	httpProxy, err := transport.ProxyURLFromMacOSScutilOutput(output, "http://api.example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	if httpProxy == nil || httpProxy.String() != "http://proxy.example.test:8080" {
		t.Fatalf("unexpected HTTP proxy: %#v", httpProxy)
	}
	httpsProxy, err := transport.ProxyURLFromMacOSScutilOutput(output, "https://api.example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	if httpsProxy == nil || httpsProxy.String() != "http://secure-proxy.example.test:8443" {
		t.Fatalf("unexpected HTTPS proxy: %#v", httpsProxy)
	}
	bypassed, err := transport.ProxyURLFromMacOSScutilOutput(output, "http://service.internal/v1")
	if err != nil {
		t.Fatal(err)
	}
	if bypassed != nil {
		t.Fatalf("expected ExceptionsList bypass, got %#v", bypassed)
	}

	socksOnly := `<dictionary> {
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : socks.example.test
}`
	socksProxy, err := transport.ProxyURLFromMacOSScutilOutput(socksOnly, "http://api.example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	if socksProxy == nil || socksProxy.String() != "socks5://socks.example.test:1080" {
		t.Fatalf("unexpected SOCKS fallback proxy: %#v", socksProxy)
	}
}

func testForwardingHTTPProxy(t *testing.T, markerHeader string) (*httptest.Server, *int32, *string, *string) {
	t.Helper()
	var hits int32
	var proxiedURL string
	var proxyAuth string
	client := &http.Client{Transport: transport.WithoutProxy(http.DefaultTransport)}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		proxiedURL = r.URL.String()
		proxyAuth = r.Header.Get("Proxy-Authorization")
		if !r.URL.IsAbs() {
			t.Fatalf("expected absolute-form proxy request URL, got %q", r.URL.String())
		}
		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			t.Fatal(err)
		}
		outReq.Header = r.Header.Clone()
		outReq.Header.Del("Proxy-Authorization")
		if markerHeader != "" {
			outReq.Header.Set(markerHeader, "yes")
		}
		res, err := client.Do(outReq)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		for name, values := range res.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	return proxy, &hits, &proxiedURL, &proxyAuth
}

func splitTestServerHostPort(t *testing.T, rawURL string) (string, string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestCollectionProxyMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	bruPath := filepath.Join(root, "Proxy BRU")
	if err := os.MkdirAll(bruPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "bruno.json"), []byte(`{
  "version": "1",
  "name": "Proxy BRU",
  "type": "collection",
  "proxy": {
    "inherit": false,
    "config": {
      "protocol": "http",
      "hostname": "{{proxyHost}}",
      "port": 4000,
      "auth": {
        "username": "proxy-user",
        "password": "proxy-pass",
        "disabled": true
      },
      "bypassProxy": "localhost,.internal"
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	ymlPath := filepath.Join(root, "Proxy YML")
	if err := os.MkdirAll(ymlPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Proxy YML
config:
  proxy:
    inherit: false
    disabled: false
    config:
      protocol: http
      hostname: proxy.example.test
      port: 8080
      auth:
        username: yaml-user
        password: yaml-pass
      bypassProxy: "*.internal"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "ping.yml"), []byte(`info:
  name: Ping
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/ping
settings:
  encodeUrl: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, bruPath)
	if err != nil {
		t.Fatal(err)
	}
	bruCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if bruCollection.Proxy.Inherit || bruCollection.Proxy.Hostname != "{{proxyHost}}" || bruCollection.Proxy.Port != "4000" || !bruCollection.Proxy.Auth.Disabled {
		t.Fatalf("unexpected bruno.json proxy: %#v", bruCollection.Proxy)
	}
	if _, err := app.SaveRequest(bruCollection.ID, bruCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	brunoJSON, err := os.ReadFile(filepath.Join(bruPath, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brunoJSON), `"proxy"`) || !strings.Contains(string(brunoJSON), `"hostname": "{{proxyHost}}"`) || !strings.Contains(string(brunoJSON), `"disabled": true`) {
		t.Fatalf("saved bruno.json missing proxy metadata:\n%s", brunoJSON)
	}

	state, err = app.OpenCollection(state.Workspaces[0].ID, ymlPath)
	if err != nil {
		t.Fatal(err)
	}
	ymlCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if ymlCollection.Proxy.Inherit || ymlCollection.Proxy.Hostname != "proxy.example.test" || ymlCollection.Proxy.Port != "8080" || ymlCollection.Proxy.Auth.Username != "yaml-user" {
		t.Fatalf("unexpected opencollection proxy: %#v", ymlCollection.Proxy)
	}
	if _, err := app.SaveRequest(ymlCollection.ID, ymlCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	ymlConfig, err := os.ReadFile(filepath.Join(ymlPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"proxy:", "hostname: proxy.example.test", "port: 8080", "bypassProxy: '*.internal'"} {
		if !strings.Contains(string(ymlConfig), expected) {
			t.Fatalf("saved opencollection missing %q:\n%s", expected, ymlConfig)
		}
	}
}

func TestCollectionClientCertificateExecutesMTLSRequest(t *testing.T) {
	certPEM, keyPEM, privateKey, leaf := testClientCertificate(t)
	pfxData, err := pkcs12.Encode(rand.Reader, privateKey, leaf, nil, "pfx-pass")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"clientCert":true,"subject":%q}`, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	targetURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name       string
		cert       ClientCertificateConfig
		writeCerts func(t *testing.T, collectionPath string)
	}{
		{
			name: "pem",
			cert: ClientCertificateConfig{
				Domain:       targetURL.Host,
				Type:         "cert",
				CertFilePath: "certs/client.pem",
				KeyFilePath:  "certs/client.key",
			},
			writeCerts: func(t *testing.T, collectionPath string) {
				t.Helper()
				certDir := filepath.Join(collectionPath, "certs")
				if err := os.MkdirAll(certDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(certDir, "client.pem"), certPEM, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(certDir, "client.key"), keyPEM, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "pfx",
			cert: ClientCertificateConfig{
				Domain:      targetURL.Host,
				Type:        "pfx",
				PFXFilePath: "certs/client.p12",
				Passphrase:  "pfx-pass",
			},
			writeCerts: func(t *testing.T, collectionPath string) {
				t.Helper()
				certDir := filepath.Join(collectionPath, "certs")
				if err := os.MkdirAll(certDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(certDir, "client.p12"), pfxData, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newAppForTest(t)
			state, err := app.GetState()
			if err != nil {
				t.Fatal(err)
			}
			collection := state.Workspaces[0].Collections[0]
			item := collection.Items[0]
			tc.writeCerts(t, collection.Path)
			if _, err := app.UpdateCollectionClientCertificates(collection.ID, []ClientCertificateConfig{tc.cert}); err != nil {
				t.Fatal(err)
			}

			method := http.MethodGet
			body := item.Body
			body.Mode = "none"
			settings := item.Settings
			settings.VerifyTLS = false
			if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &server.URL, Body: &body, Settings: &settings}); err != nil {
				t.Fatal(err)
			}
			state, err = app.SendRequest(collection.ID, item.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			item, ok := findItemInState(state, collection.ID, item.ID)
			if !ok || item.Response == nil {
				t.Fatalf("missing response")
			}
			if item.Response.Status != http.StatusOK || !strings.Contains(item.Response.Body, `"clientCert":true`) || !strings.Contains(item.Response.Body, `"subject":"liteapi-client"`) {
				t.Fatalf("expected mTLS success, got status=%d body=%q error=%q", item.Response.Status, item.Response.Body, item.Response.Error)
			}
		})
	}
}

func TestCollectionClientCertificateMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	bruPath := filepath.Join(root, "Cert BRU")
	if err := os.MkdirAll(bruPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "bruno.json"), []byte(`{
  "version": "1",
  "name": "Cert BRU",
  "type": "collection",
  "clientCertificates": {
    "enabled": true,
    "certs": [
      {
        "domain": "{{host}}:9443",
        "type": "cert",
        "certFilePath": "certs/client.pem",
        "keyFilePath": "certs/client.key",
        "passphrase": "secret"
      },
      {
        "domain": "*.example.test",
        "type": "pfx",
        "pfxFilePath": "certs/client.p12"
      }
    ]
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	ymlPath := filepath.Join(root, "Cert YML")
	if err := os.MkdirAll(ymlPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Cert YML
config:
  clientCertificates:
    - domain: api.example.test
      type: pem
      certificateFilePath: certs/client.pem
      privateKeyFilePath: certs/client.key
      passphrase: pem-pass
    - domain: "*.grpc.test"
      type: pkcs12
      pkcs12FilePath: certs/client.p12
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "ping.yml"), []byte(`info:
  name: Ping
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/ping
settings:
  encodeUrl: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, bruPath)
	if err != nil {
		t.Fatal(err)
	}
	bruCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(bruCollection.ClientCertificates) != 2 || bruCollection.ClientCertificates[0].CertFilePath != "certs/client.pem" || bruCollection.ClientCertificates[1].PFXFilePath != "certs/client.p12" {
		t.Fatalf("unexpected bruno.json client certificates: %#v", bruCollection.ClientCertificates)
	}
	if _, err := app.SaveRequest(bruCollection.ID, bruCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	brunoJSON, err := os.ReadFile(filepath.Join(bruPath, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"clientCertificates"`, `"certFilePath": "certs/client.pem"`, `"keyFilePath": "certs/client.key"`, `"pfxFilePath": "certs/client.p12"`} {
		if !strings.Contains(string(brunoJSON), expected) {
			t.Fatalf("saved bruno.json missing %q:\n%s", expected, brunoJSON)
		}
	}

	state, err = app.OpenCollection(state.Workspaces[0].ID, ymlPath)
	if err != nil {
		t.Fatal(err)
	}
	ymlCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(ymlCollection.ClientCertificates) != 2 || ymlCollection.ClientCertificates[0].Type != "cert" || ymlCollection.ClientCertificates[1].Type != "pfx" {
		t.Fatalf("unexpected opencollection client certificates: %#v", ymlCollection.ClientCertificates)
	}
	if _, err := app.SaveRequest(ymlCollection.ID, ymlCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	ymlConfig, err := os.ReadFile(filepath.Join(ymlPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"clientCertificates:", "type: pem", "certificateFilePath: certs/client.pem", "privateKeyFilePath: certs/client.key", "type: pkcs12", "pkcs12FilePath: certs/client.p12"} {
		if !strings.Contains(string(ymlConfig), expected) {
			t.Fatalf("saved opencollection missing %q:\n%s", expected, ymlConfig)
		}
	}
}

func TestUpdateCollectionClientCertificatesPreservesBlankEditorRows(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.UpdateCollectionClientCertificates(collection.ID, []ClientCertificateConfig{{}})
	if err != nil {
		t.Fatal(err)
	}
	updated := state.Workspaces[0].Collections[0]
	if len(updated.ClientCertificates) != 1 {
		t.Fatalf("expected blank editor row to remain, got %#v", updated.ClientCertificates)
	}
	if updated.ClientCertificates[0].Type != "cert" {
		t.Fatalf("expected blank editor row type to default to cert, got %#v", updated.ClientCertificates[0])
	}
	if transport.HasClientCertificates(updated.ClientCertificates) {
		t.Fatalf("blank editor row should not count as an executable client certificate: %#v", updated.ClientCertificates)
	}
}

func TestCollectionPresetsApplyToNewRequests(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.UpdateCollectionPresets(collection.ID, CollectionPresets{
		RequestType: "ws",
		RequestURL:  "wss://example.test/socket",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	presetItem := collection.Items[len(collection.Items)-1]
	if presetItem.Type != "websocket" || presetItem.Method != "CONNECT" || presetItem.URL != "wss://example.test/socket" {
		t.Fatalf("preset request mismatch: type=%q method=%q url=%q", presetItem.Type, presetItem.Method, presetItem.URL)
	}

	state, err = app.UpdateCollectionPresets(collection.ID, CollectionPresets{
		RequestType: "graphql",
		RequestURL:  "https://example.test/graphql",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "http", "Explicit HTTP")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	explicitItem := collection.Items[len(collection.Items)-1]
	if explicitItem.Type != "http" || explicitItem.Method != http.MethodGet || explicitItem.URL != "https://example.test/graphql" {
		t.Fatalf("explicit type should keep selected type and use preset url, got type=%q method=%q url=%q", explicitItem.Type, explicitItem.Method, explicitItem.URL)
	}
}

func TestWorkspaceScratchCollectionIsTransientAndFileBacked(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	if workspace.ScratchCollectionID == "" || workspace.ScratchTempDirectory == "" {
		t.Fatalf("scratch metadata was not mounted: %#v", workspace)
	}
	var scratch Collection
	for _, collection := range workspace.Collections {
		if collection.ID == workspace.ScratchCollectionID {
			scratch = collection
			break
		}
	}
	if !scratch.Scratch || scratch.Name != "Scratch" || scratch.Path != workspace.ScratchTempDirectory {
		t.Fatalf("scratch collection mismatch: %#v workspace=%#v", scratch, workspace)
	}
	for _, name := range []string{"opencollection.yml", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(scratch.Path, name)); err != nil {
			t.Fatalf("scratch %s missing: %v", name, err)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(scratch.Path, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metadata), `"type": "scratch"`) || !strings.Contains(string(metadata), workspace.ID) {
		t.Fatalf("scratch metadata missing workspace/type: %s", metadata)
	}

	state, err = app.CreateRequest(scratch.ID, "http", "Scratch GET")
	if err != nil {
		t.Fatal(err)
	}
	workspace = state.Workspaces[0]
	for _, collection := range workspace.Collections {
		if collection.ID == workspace.ScratchCollectionID {
			scratch = collection
			break
		}
	}
	item := scratch.Items[len(scratch.Items)-1]
	if !item.Transient || !pathInside(scratch.Path, item.FilePath) {
		t.Fatalf("scratch request should be transient and file-backed: %#v scratch=%s", item, scratch.Path)
	}
	activeTab := state.OpenTabs[len(state.OpenTabs)-1]
	if !activeTab.Transient || activeTab.CollectionID != scratch.ID || activeTab.ItemID != item.ID {
		t.Fatalf("scratch tab should be transient: %#v", activeTab)
	}
	state, err = app.SaveRequest(scratch.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace = state.Workspaces[0]
	for _, collection := range workspace.Collections {
		if collection.ID == workspace.ScratchCollectionID {
			scratch = collection
			break
		}
	}
	item = scratch.Items[len(scratch.Items)-1]
	if _, err := os.Stat(item.FilePath); err != nil {
		t.Fatalf("scratch request file was not written: %v", err)
	}
	flushPersistForTest(t, app)
	stored, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{workspace.ScratchCollectionID, workspace.ScratchTempDirectory, scratch.ID, `"scratchCollectionId"`} {
		if strings.Contains(string(stored), unexpected) {
			t.Fatalf("persisted state should not contain scratch data %q:\n%s", unexpected, stored)
		}
	}
	var storedState AppState
	if err := json.Unmarshal(stored, &storedState); err != nil {
		t.Fatal(err)
	}
	for _, storedWorkspace := range storedState.Workspaces {
		for _, storedCollection := range storedWorkspace.Collections {
			if storedCollection.Scratch || storedCollection.ID == scratch.ID {
				t.Fatalf("persisted workspace contains scratch collection: %#v", storedCollection)
			}
			for _, storedItem := range storedCollection.Items {
				if storedItem.Name == "Scratch GET" || storedItem.Transient {
					t.Fatalf("persisted regular collection contains scratch request: %#v", storedItem)
				}
			}
		}
	}
	for _, storedTab := range storedState.OpenTabs {
		if storedTab.Transient || storedTab.CollectionID == scratch.ID {
			t.Fatalf("persisted tabs should exclude scratch tabs: %#v", storedTab)
		}
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	reloadedWorkspace := reloadedState.Workspaces[0]
	if reloadedWorkspace.ScratchCollectionID == "" {
		t.Fatal("reloaded workspace did not remount scratch collection")
	}
	for _, collection := range reloadedWorkspace.Collections {
		if collection.ID != reloadedWorkspace.ScratchCollectionID {
			continue
		}
		if len(collection.Items) != 0 {
			t.Fatalf("scratch requests should not be restored from persisted state: %#v", collection.Items)
		}
		return
	}
	t.Fatalf("reloaded scratch collection %q not found", reloadedWorkspace.ScratchCollectionID)
}

func TestSetActiveWorkspacePersistsSelection(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	initialWorkspaceID := state.ActiveWorkspaceID
	state, err = app.CreateWorkspace("Second Workspace")
	if err != nil {
		t.Fatal(err)
	}
	secondWorkspaceID := state.ActiveWorkspaceID
	if secondWorkspaceID == initialWorkspaceID {
		t.Fatal("CreateWorkspace did not select the new workspace")
	}

	state, err = app.SetActiveWorkspace(initialWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveWorkspaceID != initialWorkspaceID {
		t.Fatalf("active workspace was not updated: got %q want %q", state.ActiveWorkspaceID, initialWorkspaceID)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.ActiveWorkspaceID != initialWorkspaceID {
		t.Fatalf("active workspace selection did not persist: got %q want %q", reloadedState.ActiveWorkspaceID, initialWorkspaceID)
	}
}

func TestSetActiveWorkspaceRejectsUnknownIDWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	initialWorkspaceID := state.ActiveWorkspaceID
	if _, err := app.SetActiveWorkspace("missing-workspace"); err == nil {
		t.Fatal("expected unknown workspace to be rejected")
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveWorkspaceID != initialWorkspaceID {
		t.Fatalf("invalid workspace selection mutated state: got %q want %q", state.ActiveWorkspaceID, initialWorkspaceID)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.ActiveWorkspaceID != initialWorkspaceID {
		t.Fatalf("invalid workspace selection persisted a mutation: got %q want %q", reloadedState.ActiveWorkspaceID, initialWorkspaceID)
	}
}

func TestPreferencesThemeModeAndVariantsPersist(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Theme = "dark"
	preferences.ThemeVariantLight = "vscode-light"
	preferences.ThemeVariantDark = "nord"
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Theme != "dark" || state.Preferences.ThemeVariantLight != "vscode-light" || state.Preferences.ThemeVariantDark != "nord" {
		t.Fatalf("theme preferences were not stored: %#v", state.Preferences)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.Theme != "dark" || reloadedState.Preferences.ThemeVariantLight != "vscode-light" || reloadedState.Preferences.ThemeVariantDark != "nord" {
		t.Fatalf("theme preferences were not persisted: %#v", reloadedState.Preferences)
	}

	preferences = reloadedState.Preferences
	preferences.Theme = "neon"
	preferences.ThemeVariantLight = "missing-light"
	preferences.ThemeVariantDark = "missing-dark"
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Theme != "system" || state.Preferences.ThemeVariantLight != "light" || state.Preferences.ThemeVariantDark != "dark" {
		t.Fatalf("invalid theme preferences were not normalized: %#v", state.Preferences)
	}
}

func TestPreferencesKeybindingsPersistAndNormalize(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.KeybindingsEnabled == nil || !*state.Preferences.KeybindingsEnabled {
		t.Fatalf("keybindings should default to enabled: %#v", state.Preferences.KeybindingsEnabled)
	}

	disabled := false
	preferences := state.Preferences
	preferences.KeybindingsEnabled = &disabled
	preferences.KeyBindings = map[string]KeyBinding{
		"globalSearch": {Name: "Global Search", Mac: "command+bind+shift+bind+k"},
		"sendRequest":  {Name: "Send Request", Windows: "ctrl+bind+shift+bind+enter"},
	}
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.KeybindingsEnabled == nil || *state.Preferences.KeybindingsEnabled {
		t.Fatalf("keybindings enabled flag was not stored: %#v", state.Preferences.KeybindingsEnabled)
	}
	if got := state.Preferences.KeyBindings["globalSearch"].Mac; got != "command+bind+shift+bind+k" {
		t.Fatalf("global search shortcut not stored: %q", got)
	}
	if got := state.Preferences.KeyBindings["sendRequest"].Windows; got != "ctrl+bind+shift+bind+enter" {
		t.Fatalf("send request shortcut not stored: %q", got)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.KeybindingsEnabled == nil || *reloadedState.Preferences.KeybindingsEnabled {
		t.Fatalf("keybindings enabled flag was not persisted: %#v", reloadedState.Preferences.KeybindingsEnabled)
	}
	if got := reloadedState.Preferences.KeyBindings["globalSearch"].Mac; got != "command+bind+shift+bind+k" {
		t.Fatalf("global search shortcut not persisted: %q", got)
	}

	preferences = reloadedState.Preferences
	preferences.KeyBindings = map[string]KeyBinding{
		"globalSearch":  {Name: "Global Search", Mac: "k"},
		"unknown":       {Name: "Unknown", Mac: "command+bind+u"},
		"sidebarSearch": {Name: "Search Sidebar", Mac: "command+bind+alt+bind+f"},
	}
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Preferences.KeyBindings["globalSearch"]; ok {
		t.Fatalf("invalid shortcut was not removed: %#v", state.Preferences.KeyBindings)
	}
	if _, ok := state.Preferences.KeyBindings["unknown"]; ok {
		t.Fatalf("unknown shortcut action was not removed: %#v", state.Preferences.KeyBindings)
	}
	if got := state.Preferences.KeyBindings["sidebarSearch"].Mac; got != "command+bind+alt+bind+f" {
		t.Fatalf("valid shortcut was not retained: %q", got)
	}
}

func TestPreferencesDevToolsPersistAndNormalize(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.DevTools.ActiveTab != "console" || state.Preferences.DevTools.DrawerHeight != 320 || state.Preferences.DevTools.DetailsPanelWidth != 400 {
		t.Fatalf("devtools preferences should default tab, drawer height, and details width: %#v", state.Preferences.DevTools)
	}
	if !reflect.DeepEqual(state.Preferences.DevTools.Network.ColumnWidths, devToolsNetworkDefaultColumnWidths) {
		t.Fatalf("network column widths should default to Bruno widths: %#v", state.Preferences.DevTools.Network.ColumnWidths)
	}

	preferences := state.Preferences
	preferences.DevTools.Open = true
	preferences.DevTools.ActiveTab = "network"
	preferences.DevTools.DrawerHeight = 456
	preferences.DevTools.DetailsPanelWidth = 512
	preferences.DevTools.Network.SortKey = "status"
	preferences.DevTools.Network.SortDirection = "desc"
	preferences.DevTools.Network.ColumnWidths = []int{90, 65, 220, 360, 125, 140, 95}
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Preferences.DevTools.Open || state.Preferences.DevTools.ActiveTab != "network" || state.Preferences.DevTools.DrawerHeight != 456 || state.Preferences.DevTools.DetailsPanelWidth != 512 {
		t.Fatalf("devtools shell preferences were not stored: %#v", state.Preferences.DevTools)
	}
	if state.Preferences.DevTools.Network.SortKey != "status" || state.Preferences.DevTools.Network.SortDirection != "desc" {
		t.Fatalf("network sort preference was not stored: %#v", state.Preferences.DevTools.Network)
	}
	if !reflect.DeepEqual(state.Preferences.DevTools.Network.ColumnWidths, []int{90, 65, 220, 360, 125, 140, 95}) {
		t.Fatalf("network column widths were not stored: %#v", state.Preferences.DevTools.Network.ColumnWidths)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !reloadedState.Preferences.DevTools.Open || reloadedState.Preferences.DevTools.ActiveTab != "network" || reloadedState.Preferences.DevTools.DrawerHeight != 456 || reloadedState.Preferences.DevTools.DetailsPanelWidth != 512 || reloadedState.Preferences.DevTools.Network.SortKey != "status" || reloadedState.Preferences.DevTools.Network.SortDirection != "desc" || !reflect.DeepEqual(reloadedState.Preferences.DevTools.Network.ColumnWidths, []int{90, 65, 220, 360, 125, 140, 95}) {
		t.Fatalf("devtools preferences were not persisted: %#v", reloadedState.Preferences.DevTools)
	}

	preferences = reloadedState.Preferences
	preferences.DevTools.ActiveTab = "bogus"
	preferences.DevTools.DrawerHeight = 80
	preferences.DevTools.DetailsPanelWidth = 1200
	preferences.DevTools.Network.SortKey = "bogus"
	preferences.DevTools.Network.SortDirection = "sideways"
	preferences.DevTools.Network.ColumnWidths = []int{12, 59, 60, 61, 70, 80, 90}
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.DevTools.Network.SortKey != "" || state.Preferences.DevTools.Network.SortDirection != "" {
		t.Fatalf("invalid sort preference was not cleared: %#v", state.Preferences.DevTools.Network)
	}
	if state.Preferences.DevTools.ActiveTab != "console" || state.Preferences.DevTools.DrawerHeight != 220 || state.Preferences.DevTools.DetailsPanelWidth != 800 {
		t.Fatalf("invalid shell preferences were not normalized: %#v", state.Preferences.DevTools)
	}
	if !reflect.DeepEqual(state.Preferences.DevTools.Network.ColumnWidths, []int{60, 60, 60, 61, 70, 80, 90}) {
		t.Fatalf("column width normalization did not clamp widths: %#v", state.Preferences.DevTools.Network.ColumnWidths)
	}

	preferences = state.Preferences
	preferences.DevTools.Network.ColumnWidths = []int{100, 200}
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Preferences.DevTools.Network.ColumnWidths, devToolsNetworkDefaultColumnWidths) {
		t.Fatalf("invalid column width count did not reset to defaults: %#v", state.Preferences.DevTools.Network.ColumnWidths)
	}
}

func TestPreferencesLayoutPersistAndNormalize(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Layout.ResponsePaneOrientation != "horizontal" {
		t.Fatalf("response pane orientation should default to horizontal: %#v", state.Preferences.Layout)
	}

	preferences := state.Preferences
	preferences.Layout.ResponsePaneOrientation = "vertical"
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Layout.ResponsePaneOrientation != "vertical" {
		t.Fatalf("response pane orientation was not stored: %#v", state.Preferences.Layout)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.Layout.ResponsePaneOrientation != "vertical" {
		t.Fatalf("response pane orientation was not persisted: %#v", reloadedState.Preferences.Layout)
	}

	preferences = reloadedState.Preferences
	preferences.Layout.ResponsePaneOrientation = "diagonal"
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Layout.ResponsePaneOrientation != "horizontal" {
		t.Fatalf("invalid response pane orientation was not normalized: %#v", state.Preferences.Layout)
	}
}

func TestPreferencesDisplayZoomPersistAndNormalize(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Display.ZoomPercentage != 100 {
		t.Fatalf("display zoom should default to 100: %#v", state.Preferences.Display)
	}

	preferences := state.Preferences
	preferences.Display.ZoomPercentage = 130
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Display.ZoomPercentage != 130 {
		t.Fatalf("display zoom was not stored: %#v", state.Preferences.Display)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.Display.ZoomPercentage != 130 {
		t.Fatalf("display zoom was not persisted: %#v", reloadedState.Preferences.Display)
	}

	preferences = reloadedState.Preferences
	preferences.Display.ZoomPercentage = 40
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Display.ZoomPercentage != 50 {
		t.Fatalf("low display zoom was not clamped: %#v", state.Preferences.Display)
	}

	preferences = state.Preferences
	preferences.Display.ZoomPercentage = 170
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Display.ZoomPercentage != 150 {
		t.Fatalf("high display zoom was not clamped: %#v", state.Preferences.Display)
	}
}

func TestPreferencesFontPersistAndNormalize(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Font.CodeFont != "default" || state.Preferences.Font.CodeFontSize != 13 || state.Preferences.CodeFontSize != 13 {
		t.Fatalf("font preferences should default to Bruno values: %#v", state.Preferences)
	}

	preferences := state.Preferences
	preferences.Font.CodeFont = "Fira Code"
	preferences.Font.CodeFontSize = 18
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Font.CodeFont != "Fira Code" || state.Preferences.Font.CodeFontSize != 18 || state.Preferences.CodeFontSize != 18 {
		t.Fatalf("font preferences were not stored and mirrored: %#v", state.Preferences)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.Font.CodeFont != "Fira Code" || reloadedState.Preferences.Font.CodeFontSize != 18 || reloadedState.Preferences.CodeFontSize != 18 {
		t.Fatalf("font preferences were not persisted: %#v", reloadedState.Preferences)
	}

	preferences = reloadedState.Preferences
	preferences.Font.CodeFont = "   "
	preferences.Font.CodeFontSize = 40
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Font.CodeFont != "default" || state.Preferences.Font.CodeFontSize != 32 || state.Preferences.CodeFontSize != 32 {
		t.Fatalf("blank font or high size was not normalized: %#v", state.Preferences)
	}

	preferences = state.Preferences
	preferences.Font.CodeFontSize = -2
	state, err = reloaded.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Font.CodeFontSize != 1 || state.Preferences.CodeFontSize != 1 {
		t.Fatalf("low font size was not clamped: %#v", state.Preferences)
	}
}

func TestPreferencesGeneralRequestAutoSaveCachePersistAndNormalize(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !boolPtrValue(state.Preferences.Request.SSLVerification, false) {
		t.Fatalf("SSL verification should default on: %#v", state.Preferences.Request)
	}
	if !boolPtrValue(state.Preferences.Request.KeepDefaultCaCertificates.Enabled, false) {
		t.Fatalf("keep-default CA certificates should default on: %#v", state.Preferences.Request)
	}
	if state.Preferences.AutoSave.Enabled || state.Preferences.AutoSave.Interval != 1000 || state.Preferences.Autosave {
		t.Fatalf("autosave should default off with Bruno delay: %#v", state.Preferences)
	}
	if state.Preferences.Cache.SSLSession.Enabled {
		t.Fatalf("SSL session cache should default off: %#v", state.Preferences.Cache)
	}
	if state.Preferences.Cache.File.Enabled {
		t.Fatalf("file cache should default off: %#v", state.Preferences.Cache)
	}
	if !boolPtrValue(state.Preferences.Request.StoreCookies, false) || !boolPtrValue(state.Preferences.Request.SendCookies, false) {
		t.Fatalf("store/send cookies should default on: %#v", state.Preferences.Request)
	}
	if state.Preferences.Request.Timeout != 0 {
		t.Fatalf("request timeout should default to Bruno's no-global-timeout value: %#v", state.Preferences.Request)
	}

	defaultRoot := t.TempDir()
	caPath := filepath.Join(t.TempDir(), "root.pem")
	preferences := state.Preferences
	preferences.Request.SSLVerification = boolPtr(false)
	preferences.Request.CustomCaCertificate = CustomCaCertificatePreferences{Enabled: true, FilePath: "  " + caPath + "  "}
	preferences.Request.KeepDefaultCaCertificates.Enabled = boolPtr(false)
	preferences.Request.StoreCookies = boolPtr(false)
	preferences.Request.SendCookies = boolPtr(false)
	preferences.Request.Timeout = -10
	preferences.General.DefaultLocation = defaultRoot
	preferences.AutoSave = AutoSavePreferences{Enabled: true, Interval: 100}
	preferences.Cache.SSLSession.Enabled = true
	preferences.Cache.File.Enabled = true
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if boolPtrValue(state.Preferences.Request.SSLVerification, true) {
		t.Fatalf("SSL verification preference was not stored: %#v", state.Preferences.Request)
	}
	if state.Preferences.Request.CustomCaCertificate.FilePath != caPath {
		t.Fatalf("custom CA path was not trimmed: %#v", state.Preferences.Request.CustomCaCertificate)
	}
	if boolPtrValue(state.Preferences.Request.KeepDefaultCaCertificates.Enabled, true) {
		t.Fatalf("keep-default CA preference was not stored: %#v", state.Preferences.Request.KeepDefaultCaCertificates)
	}
	if boolPtrValue(state.Preferences.Request.StoreCookies, true) || boolPtrValue(state.Preferences.Request.SendCookies, true) || state.Preferences.StoreCookies {
		t.Fatalf("store/send cookie preferences were not stored and mirrored: %#v", state.Preferences)
	}
	if state.Preferences.Request.Timeout != 0 {
		t.Fatalf("negative request timeout was not normalized: %#v", state.Preferences.Request)
	}
	if !state.Preferences.AutoSave.Enabled || !state.Preferences.Autosave || state.Preferences.AutoSave.Interval != 500 {
		t.Fatalf("autosave preference was not normalized and mirrored: %#v", state.Preferences)
	}
	if state.Preferences.General.DefaultLocation != defaultRoot || state.Preferences.DefaultCollectionPath != defaultRoot {
		t.Fatalf("default location was not mirrored: %#v", state.Preferences)
	}
	if !state.Preferences.Cache.SSLSession.Enabled {
		t.Fatalf("SSL session cache preference was not stored: %#v", state.Preferences.Cache)
	}
	if !state.Preferences.Cache.File.Enabled {
		t.Fatalf("file cache preference was not stored: %#v", state.Preferences.Cache)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if reloadedState.Preferences.General.DefaultLocation != defaultRoot || !reloadedState.Preferences.AutoSave.Enabled || !reloadedState.Preferences.Cache.SSLSession.Enabled || !reloadedState.Preferences.Cache.File.Enabled {
		t.Fatalf("general/autosave/cache preferences were not persisted: %#v", reloadedState.Preferences)
	}
	reloadedState, err = reloaded.CreateCollection(reloadedState.ActiveWorkspaceID, "Default Root API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	created := reloadedState.Workspaces[0].Collections[len(reloadedState.Workspaces[0].Collections)-1]
	expectedPath := filepath.Join(defaultRoot, "Default Root API")
	if filepath.Clean(created.Path) != filepath.Clean(expectedPath) {
		t.Fatalf("expected default location collection path %q, got %q", expectedPath, created.Path)
	}
}

func TestPreferencesOAuth2UseSystemBrowserPersists(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Preferences.OAuth2UseSystemBrowser {
		t.Fatalf("OAuth2 should default to in-app authorization: %#v", state.Preferences)
	}

	preferences := state.Preferences
	preferences.OAuth2UseSystemBrowser = true
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Preferences.OAuth2UseSystemBrowser {
		t.Fatalf("OAuth2 system-browser preference was not stored: %#v", state.Preferences)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if !reloadedState.Preferences.OAuth2UseSystemBrowser {
		t.Fatalf("OAuth2 system-browser preference was not persisted: %#v", reloadedState.Preferences)
	}
}

func TestCollectionPresetsMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	bruPath := filepath.Join(root, "Presets BRU")
	if err := os.MkdirAll(bruPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "bruno.json"), []byte(`{
  "version": "1",
  "name": "Presets BRU",
  "type": "collection",
  "presets": {
    "requestType": "ws",
    "requestUrl": "wss://example.test/socket"
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	ymlPath := filepath.Join(root, "Presets YML")
	if err := os.MkdirAll(ymlPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Presets YML
config:
  presets:
    requestType: grpc
    requestUrl: grpc://localhost:50051
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "ping.yml"), []byte(`info:
  name: Ping
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/ping
settings:
  encodeUrl: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, bruPath)
	if err != nil {
		t.Fatal(err)
	}
	bruCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if bruCollection.Presets.RequestType != "websocket" || bruCollection.Presets.RequestURL != "wss://example.test/socket" {
		t.Fatalf("unexpected bruno.json presets: %#v", bruCollection.Presets)
	}
	if _, err := app.SaveRequest(bruCollection.ID, bruCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	brunoJSON, err := os.ReadFile(filepath.Join(bruPath, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"presets"`, `"requestType": "ws"`, `"requestUrl": "wss://example.test/socket"`} {
		if !strings.Contains(string(brunoJSON), expected) {
			t.Fatalf("saved bruno.json missing %q:\n%s", expected, brunoJSON)
		}
	}

	state, err = app.OpenCollection(state.Workspaces[0].ID, ymlPath)
	if err != nil {
		t.Fatal(err)
	}
	ymlCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if ymlCollection.Presets.RequestType != "grpc" || ymlCollection.Presets.RequestURL != "grpc://localhost:50051" {
		t.Fatalf("unexpected opencollection presets: %#v", ymlCollection.Presets)
	}
	if _, err := app.SaveRequest(ymlCollection.ID, ymlCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	ymlConfig, err := os.ReadFile(filepath.Join(ymlPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"presets:", "requestType: grpc", "requestUrl: grpc://localhost:50051"} {
		if !strings.Contains(string(ymlConfig), expected) {
			t.Fatalf("saved opencollection missing %q:\n%s", expected, ymlConfig)
		}
	}
}

func TestCollectionProtobufMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	bruPath := filepath.Join(root, "Protobuf BRU")
	if err := os.MkdirAll(filepath.Join(bruPath, "protos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "protos", "greeter.proto"), []byte(`syntax = "proto3";`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "bruno.json"), []byte(`{
  "version": "1",
  "name": "Protobuf BRU",
  "type": "collection",
  "protobuf": {
    "protoFiles": [
      { "path": "protos/greeter.proto", "type": "file", "exists": false }
    ],
    "importPaths": [
      { "path": "protos", "enabled": true, "exists": false },
      { "path": "missing", "enabled": false, "exists": false }
    ]
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bruPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	ymlPath := filepath.Join(root, "Protobuf YML")
	if err := os.MkdirAll(filepath.Join(ymlPath, "proto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "proto", "service.proto"), []byte(`syntax = "proto3";`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Protobuf YML
config:
  protobuf:
    protoFiles:
      - path: proto/service.proto
        type: file
        exists: false
    importPaths:
      - path: proto
        enabled: true
        exists: false
      - path: missing
        enabled: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ymlPath, "ping.yml"), []byte(`info:
  name: Ping
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/ping
settings:
  encodeUrl: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, bruPath)
	if err != nil {
		t.Fatal(err)
	}
	bruCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(bruCollection.Protobuf.ProtoFiles) != 1 || !bruCollection.Protobuf.ProtoFiles[0].Exists {
		t.Fatalf("unexpected bruno.json protobuf proto files: %#v", bruCollection.Protobuf.ProtoFiles)
	}
	if len(bruCollection.Protobuf.ImportPaths) != 2 || !bruCollection.Protobuf.ImportPaths[0].Enabled || !bruCollection.Protobuf.ImportPaths[0].Exists || bruCollection.Protobuf.ImportPaths[1].Enabled || bruCollection.Protobuf.ImportPaths[1].Exists {
		t.Fatalf("unexpected bruno.json protobuf import paths: %#v", bruCollection.Protobuf.ImportPaths)
	}
	if _, err := app.SaveRequest(bruCollection.ID, bruCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	brunoJSON, err := os.ReadFile(filepath.Join(bruPath, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"protobuf"`, `"protoFiles"`, `"path": "protos/greeter.proto"`, `"importPaths"`, `"enabled": true`, `"enabled": false`} {
		if !strings.Contains(string(brunoJSON), expected) {
			t.Fatalf("saved bruno.json missing %q:\n%s", expected, brunoJSON)
		}
	}
	if strings.Contains(string(brunoJSON), `"exists"`) {
		t.Fatalf("saved bruno.json should not persist exists flags:\n%s", brunoJSON)
	}

	state, err = app.OpenCollection(state.Workspaces[0].ID, ymlPath)
	if err != nil {
		t.Fatal(err)
	}
	ymlCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(ymlCollection.Protobuf.ProtoFiles) != 1 || !ymlCollection.Protobuf.ProtoFiles[0].Exists || len(ymlCollection.Protobuf.ImportPaths) != 2 {
		t.Fatalf("unexpected opencollection protobuf config: %#v", ymlCollection.Protobuf)
	}
	if _, err := app.SaveRequest(ymlCollection.ID, ymlCollection.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	ymlConfig, err := os.ReadFile(filepath.Join(ymlPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"protobuf:", "protoFiles:", "path: proto/service.proto", "importPaths:", "enabled: true", "enabled: false"} {
		if !strings.Contains(string(ymlConfig), expected) {
			t.Fatalf("saved opencollection missing %q:\n%s", expected, ymlConfig)
		}
	}
	if strings.Contains(string(ymlConfig), "exists:") {
		t.Fatalf("saved opencollection should not persist exists flags:\n%s", ymlConfig)
	}
}

func TestRequestSettingsEncodeURLRoundTrip(t *testing.T) {
	item := types.NewRequestItem("Encoding", "http", 1)
	item.URL = "https://example.com/api?name=John Doe"
	item.Settings.EncodeURL = false
	item.Settings.StoreCookies = false
	item.Settings.VerifyTLS = false
	item.Settings.KeepAliveInterval = 2500

	bru := stringifyBru(item)
	for _, want := range []string{"settings {", "encodeUrl: false", "storeCookies: false", "verifyTls: false", "keepAliveInterval: 2500"} {
		if !strings.Contains(bru, want) {
			t.Fatalf("stringified bru missing %q:\n%s", want, bru)
		}
	}
	parsedBru, err := parseBru(bru)
	if err != nil {
		t.Fatal(err)
	}
	if parsedBru.Settings.EncodeURL || parsedBru.Settings.StoreCookies || parsedBru.Settings.VerifyTLS || parsedBru.Settings.KeepAliveInterval != 2500 {
		t.Fatalf("bru settings did not round-trip: %#v", parsedBru.Settings)
	}

	yamlContent, err := stringifyYAMLRequest(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"encodeUrl: false", "keepAliveInterval: 2500"} {
		if !strings.Contains(yamlContent, want) {
			t.Fatalf("stringified yaml missing %q:\n%s", want, yamlContent)
		}
	}
	parsedYAML, err := parseYAMLRequest(yamlContent)
	if err != nil {
		t.Fatal(err)
	}
	if parsedYAML.Settings.EncodeURL || parsedYAML.Settings.StoreCookies || parsedYAML.Settings.VerifyTLS || parsedYAML.Settings.KeepAliveInterval != 2500 {
		t.Fatalf("yaml settings did not round-trip: %#v", parsedYAML.Settings)
	}
}

func TestJavaScriptRuntimeMutatesRequestAndRunsTests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("pre-request script did not change method: %s", r.Method)
		}
		if r.URL.Path != "/scripted" {
			t.Fatalf("pre-request script did not change URL path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Script"); got != "ada-token" {
			t.Fatalf("pre-request script did not set header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"message":"done"}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
		{ID: "token", Name: "token", Value: "ada-token", DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateCollectionScripts(collection.ID, `bru.setVar("path", "scripted");`, "", `test("collection status", function () {
  expect(res.status).to.equal(202);
});`); err != nil {
		t.Fatal(err)
	}
	preScript := `req.setMethod("POST");
req.setUrl("{{host}}/" + bru.getVar("path"));
req.setHeader("X-Script", bru.interpolate("{{token}}"));`
	postScript := `bru.setVar("message", res.json.message);`
	tests := `expect status equals 202
test("js status", function () {
  expect(res.status).to.equal(202);
});
test("post var", function () {
  expect(bru.getVar("message")).to.equal("done");
});`
	wrongURL := "{{host}}/wrong"
	method := http.MethodGet
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		Method:     &method,
		URL:        &wrongURL,
		PreScript:  &preScript,
		PostScript: &postScript,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatal("missing response")
	}
	if item.Response.Status != http.StatusAccepted {
		t.Fatalf("expected scripted response status, got %#v", item.Response)
	}
	if item.Response.RequestedURL != server.URL+"/scripted" {
		t.Fatalf("scripted URL was not used: %s", item.Response.RequestedURL)
	}
	if len(item.Response.TestResults) != 4 {
		t.Fatalf("expected legacy plus JS test results, got %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("test failed: %#v", item.Response.TestResults)
		}
	}
}

func TestJavaScriptRuntimeCapturesConsoleLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `console.log("pre", { phase: "before" });`
	postScript := `console.warn("post", res.status);`
	tests := `test("console test", function () {
  console.error("test", ["done"]);
  expect(res.status).to.equal(200);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:        &server.URL,
		PreScript:  &preScript,
		PostScript: &postScript,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatal("missing response")
	}
	if len(item.Response.ScriptLogs) != 3 {
		t.Fatalf("expected pre/post/test console logs, got %#v", item.Response.ScriptLogs)
	}
	expected := []ScriptLog{
		{Level: "log", Message: `pre {"phase":"before"}`, Args: []string{"pre", `{"phase":"before"}`}},
		{Level: "warn", Message: "post 200", Args: []string{"post", "200"}},
		{Level: "error", Message: `test ["done"]`, Args: []string{"test", `["done"]`}},
	}
	for index, want := range expected {
		got := item.Response.ScriptLogs[index]
		if got.Level != want.Level || got.Message != want.Message || !reflect.DeepEqual(got.Args, want.Args) {
			t.Fatalf("console log %d mismatch: got %#v want %#v", index, got, want)
		}
	}
}

func TestDevToolsSnapshotReportsRuntimeAndStateCounts(t *testing.T) {
	app := newAppForTest(t)

	app.mu.Lock()
	app.state.NetworkLog = append(app.state.NetworkLog, NetworkLog{
		ID:         "net-1",
		Method:     http.MethodGet,
		URL:        "https://example.com/status",
		Status:     http.StatusOK,
		DurationMs: 42,
		At:         time.Now(),
	})
	app.state.Workspaces[0].Collections[0].Items[0].Response = &Response{
		ScriptLogs: []ScriptLog{
			{Level: "log", Message: "hello"},
			{Level: "warn", Message: "careful"},
		},
	}
	app.mu.Unlock()

	snapshot, err := app.GetDevToolsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PID != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), snapshot.PID)
	}
	if snapshot.NetworkRequests != 1 {
		t.Fatalf("expected 1 network request, got %d", snapshot.NetworkRequests)
	}
	if snapshot.ConsoleLogs != 2 {
		t.Fatalf("expected 2 console logs, got %d", snapshot.ConsoleLogs)
	}
	if snapshot.UptimeSeconds < 0 {
		t.Fatalf("expected non-negative uptime, got %d", snapshot.UptimeSeconds)
	}
	if snapshot.MemoryBytes == 0 || snapshot.HeapAllocBytes == 0 {
		t.Fatalf("expected memory metrics, got %#v", snapshot)
	}
	if snapshot.Goroutines == 0 {
		t.Fatalf("expected goroutine count, got %#v", snapshot)
	}
	if snapshot.CPUPercent < 0 {
		t.Fatalf("expected non-negative CPU percent, got %#v", snapshot)
	}
	if len(snapshot.Processes) != 1 {
		t.Fatalf("expected main process metric, got %#v", snapshot.Processes)
	}
	if snapshot.Processes[0].PID != os.Getpid() || snapshot.Processes[0].Title != "LiteAPI" || snapshot.Processes[0].Type != "main" {
		t.Fatalf("unexpected process metric: %#v", snapshot.Processes[0])
	}
	if snapshot.Processes[0].CPUPercent < 0 || snapshot.Processes[0].MemoryBytes == 0 {
		t.Fatalf("expected process CPU/memory metrics, got %#v", snapshot.Processes[0])
	}
	if snapshot.Timestamp.IsZero() {
		t.Fatalf("expected snapshot timestamp, got %#v", snapshot)
	}
}

func TestCalculateCPUPercent(t *testing.T) {
	start := time.Unix(100, 0)
	percent := calculateCPUPercent(100*time.Millisecond, start, 350*time.Millisecond, start.Add(500*time.Millisecond), 2)
	if percent != 25.0 {
		t.Fatalf("expected rounded CPU percent 25.0, got %.1f", percent)
	}
	if got := calculateCPUPercent(350*time.Millisecond, start, 100*time.Millisecond, start.Add(500*time.Millisecond), 2); got != 0 {
		t.Fatalf("expected reset/backwards CPU sample to clamp to 0, got %.1f", got)
	}
	if got := calculateCPUPercent(0, time.Time{}, 100*time.Millisecond, start, 2); got != 0 {
		t.Fatalf("expected first sample to report 0, got %.1f", got)
	}
	if got := calculateCPUPercent(100*time.Millisecond, start, 200*time.Millisecond, start.Add(500*time.Millisecond), 0); got != 0 {
		t.Fatalf("expected invalid cpu count to clamp to 0, got %.1f", got)
	}
}

func TestTerminalSessionLifecycleRunsShellCommand(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	session, err := app.CreateTerminalSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.PID == 0 {
		t.Fatalf("expected terminal session id and pid, got %#v", session)
	}
	if session.CWD != dir {
		t.Fatalf("expected cwd %q, got %q", dir, session.CWD)
	}
	defer func() {
		_ = app.KillTerminalSession(session.ID)
	}()

	if _, err := app.WriteTerminalSession(session.ID, "printf 'liteapi-terminal-smoke\\n'\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		latest, err := app.GetTerminalSession(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(latest.Output, "liteapi-terminal-smoke") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output never contained smoke marker: %#v", latest.Output)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := app.ResizeTerminalSession(session.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	if err := app.KillTerminalSession(session.ID); err != nil {
		t.Fatal(err)
	}
	sessions, err := app.ListTerminalSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range sessions {
		if candidate.ID == session.ID {
			t.Fatalf("killed terminal session still listed: %#v", sessions)
		}
	}
}

func TestRevealInFolderCommandUsesPlatformSelector(t *testing.T) {
	dir := t.TempDir()
	name, args, err := revealInFolderCommand(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) == 0 {
		t.Fatalf("expected reveal args, got none")
	}
	switch goruntime.GOOS {
	case "darwin":
		if name != "open" || len(args) != 2 || args[0] != "-R" || args[1] != dir {
			t.Fatalf("unexpected macOS reveal command: %s %#v", name, args)
		}
	case "windows":
		if name != "explorer.exe" || len(args) != 1 || !strings.HasPrefix(args[0], "/select,") || !strings.Contains(args[0], dir) {
			t.Fatalf("unexpected Windows reveal command: %s %#v", name, args)
		}
	default:
		if name != "xdg-open" || len(args) != 1 || args[0] != dir {
			t.Fatalf("unexpected desktop reveal command: %s %#v", name, args)
		}
	}
}

func TestRevealCollectionInFolderUsesCollectionPath(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Reveal API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	var collection Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.Name == "Reveal API" {
			collection = candidate
			break
		}
	}
	if collection.ID == "" {
		t.Fatalf("created collection not found: %#v", state.Workspaces[0].Collections)
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		t.Fatal(err)
	}

	var revealed string
	app.revealInFolder = func(path string) error {
		revealed = path
		return nil
	}
	if err := app.RevealCollectionInFolder(collection.ID); err != nil {
		t.Fatal(err)
	}
	if revealed != collection.Path {
		t.Fatalf("expected revealed path %q, got %q", collection.Path, revealed)
	}
}

func TestRevealCollectionInFolderRejectsMissingPath(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Missing Reveal API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	var collection Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.Name == "Missing Reveal API" {
			collection = candidate
			break
		}
	}
	if collection.ID == "" {
		t.Fatalf("created collection not found: %#v", state.Workspaces[0].Collections)
	}
	if err := os.RemoveAll(collection.Path); err != nil {
		t.Fatal(err)
	}
	called := false
	app.revealInFolder = func(path string) error {
		called = true
		return nil
	}
	if err := app.RevealCollectionInFolder(collection.ID); err == nil {
		t.Fatal("expected missing collection folder to fail")
	}
	if called {
		t.Fatal("reveal opener should not run for a missing collection path")
	}
}

func TestRevealCollectionItemActionsUseFolderAndRequestPaths(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Item Reveal API")
	folderPath := filepath.Join(collectionPath, "users")
	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Item Reveal API
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folderPath, "folder.yml"), []byte(`info:
  name: Team Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(folderPath, "list.yml")
	if err := os.WriteFile(requestPath, []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	if collection.ID == "" || len(collection.Folders) != 1 || len(collection.Items) != 1 {
		t.Fatalf("expected one folder and request, got collection=%#v", collection)
	}
	if collection.Folders[0].Path != "users" || collection.Folders[0].DisplayPath != "Team Users" {
		t.Fatalf("test fixture should split physical and display folder names: %#v", collection.Folders[0])
	}
	item := collection.Items[0]
	if item.FolderPath != "Team Users" || filepath.Clean(item.FilePath) != requestPath {
		t.Fatalf("request fixture mismatch: %#v", item)
	}

	resolved, err := app.ResolveCollectionFolderPath(collection.ID, "Team Users")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(resolved) != folderPath {
		t.Fatalf("expected terminal cwd to use physical folder %q, got %q", folderPath, resolved)
	}

	var revealed []string
	app.revealInFolder = func(path string) error {
		revealed = append(revealed, filepath.Clean(path))
		return nil
	}
	if err := app.RevealCollectionFolderInFolder(collection.ID, "Team Users"); err != nil {
		t.Fatal(err)
	}
	if err := app.RevealRequestInFolder(collection.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	expected := []string{folderPath, requestPath}
	if !reflect.DeepEqual(revealed, expected) {
		t.Fatalf("expected revealed paths %#v, got %#v", expected, revealed)
	}
}

func TestRevealCollectionItemActionsRejectMissingTargets(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Item Reveal Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Item Reveal Guard API")
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "http", "List Users")
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Item Reveal Guard API")
	item := collection.Items[len(collection.Items)-1]
	state, err = app.SaveRequest(collection.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Item Reveal Guard API")
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok {
		t.Fatalf("saved request not found: %#v", collection.Items)
	}

	called := false
	app.revealInFolder = func(path string) error {
		called = true
		return nil
	}
	if err := os.Remove(item.FilePath); err != nil {
		t.Fatal(err)
	}
	if err := app.RevealRequestInFolder(collection.ID, item.ID); err == nil || !strings.Contains(err.Error(), "file does not exist") {
		t.Fatalf("expected missing request file rejection, got %v", err)
	}
	if called {
		t.Fatal("reveal opener should not run for missing request file")
	}

	if err := os.RemoveAll(filepath.Join(collection.Path, "users")); err != nil {
		t.Fatal(err)
	}
	if err := app.RevealCollectionFolderInFolder(collection.ID, "Users"); err == nil {
		t.Fatal("expected missing folder rejection")
	}
	if called {
		t.Fatal("reveal opener should not run for missing folder")
	}
}

func TestRemoveCollectionUnmountsAndPreservesFilesAndTabs(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Remove API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	var collection Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.Name == "Remove API" {
			collection = candidate
			break
		}
	}
	if collection.ID == "" {
		t.Fatalf("created collection not found: %#v", state.Workspaces[0].Collections)
	}
	if err := os.MkdirAll(collection.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(collection.Path, "keep.txt")
	if err := os.WriteFile(markerPath, []byte("still here"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "http", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.OpenTabs) == 0 || state.OpenTabs[len(state.OpenTabs)-1].CollectionID != collection.ID {
		t.Fatalf("expected an open tab for collection, got %#v", state.OpenTabs)
	}

	state, err = app.RemoveCollection(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.ID == collection.ID {
			t.Fatalf("removed collection still mounted: %#v", state.Workspaces[0].Collections)
		}
	}
	for _, tab := range state.OpenTabs {
		if tab.CollectionID == collection.ID {
			t.Fatalf("removed collection open tab survived: %#v", state.OpenTabs)
		}
	}
	if content, err := os.ReadFile(markerPath); err != nil || string(content) != "still here" {
		t.Fatalf("remove should preserve collection files, content=%q err=%v", content, err)
	}
}

func TestRemoveCollectionRejectsScratchCollection(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	var scratchID string
	for _, collection := range state.Workspaces[0].Collections {
		if collection.Scratch {
			scratchID = collection.ID
			break
		}
	}
	if scratchID == "" {
		t.Skip("default state has no scratch collection")
	}
	if _, err := app.RemoveCollection(scratchID); err == nil {
		t.Fatal("expected scratch collection removal to fail")
	}
}

func TestRenameCollectionWritesYAMLMetadataAndKeepsPath(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Original API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	var collection Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.Name == "Original API" {
			collection = candidate
			break
		}
	}
	if collection.ID == "" {
		t.Fatalf("created collection not found: %#v", state.Workspaces[0].Collections)
	}
	originalPath := collection.Path

	state, err = app.RenameCollection(collection.ID, "Renamed API")
	if err != nil {
		t.Fatal(err)
	}
	renamed := state.Workspaces[0].Collections[0]
	if renamed.ID != collection.ID {
		for _, candidate := range state.Workspaces[0].Collections {
			if candidate.ID == collection.ID {
				renamed = candidate
				break
			}
		}
	}
	if renamed.Name != "Renamed API" {
		t.Fatalf("expected renamed collection, got %#v", renamed)
	}
	if renamed.Path != originalPath {
		t.Fatalf("rename should not move collection folder: before=%q after=%q", originalPath, renamed.Path)
	}
	config, err := os.ReadFile(filepath.Join(originalPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "name: Renamed API") {
		t.Fatalf("opencollection.yml did not receive renamed collection:\n%s", config)
	}
	fromDisk, err := readCollectionFromDisk(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	if fromDisk.Name != "Renamed API" {
		t.Fatalf("expected disk collection name Renamed API, got %q", fromDisk.Name)
	}
}

func TestRenameCollectionWritesBruConfigAndKeepsPath(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Bru API")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Bru API","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	originalPath := collection.Path

	state, err = app.RenameCollection(collection.ID, "Renamed Bru API")
	if err != nil {
		t.Fatal(err)
	}
	var renamed Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.ID == collection.ID {
			renamed = candidate
			break
		}
	}
	if renamed.Name != "Renamed Bru API" || renamed.Path != originalPath {
		t.Fatalf("unexpected renamed bru collection: %#v", renamed)
	}
	config, err := os.ReadFile(filepath.Join(originalPath, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `"name": "Renamed Bru API"`) {
		t.Fatalf("bruno.json did not receive renamed collection:\n%s", config)
	}
	if _, err := os.Stat(filepath.Join(originalPath, "collection.bru")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rename should not write collection.bru, got err=%v", err)
	}
	fromDisk, err := readCollectionFromDisk(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	if fromDisk.Name != "Renamed Bru API" {
		t.Fatalf("expected disk bru name Renamed Bru API, got %q", fromDisk.Name)
	}
}

func TestRenameCollectionRejectsBlankName(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Blank Rename API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.RenameCollection(collection.ID, ""); err == nil {
		t.Fatal("expected empty collection name to fail")
	}
}

func TestRenameCollectionPreservesWhitespaceName(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Whitespace Rename API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.RenameCollection(collection.ID, "  Spaced API  ")
	if err != nil {
		t.Fatal(err)
	}
	var renamed Collection
	for _, candidate := range state.Workspaces[0].Collections {
		if candidate.ID == collection.ID {
			renamed = candidate
			break
		}
	}
	if renamed.Name != "  Spaced API  " {
		t.Fatalf("expected whitespace to be preserved, got %q", renamed.Name)
	}
}

func TestCloneCollectionYAMLCopiesFormatFilesAndKeepsSource(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source API")
	if err := os.MkdirAll(filepath.Join(sourcePath, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Source API
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "ping.yml"), []byte(`info:
  name: Ping
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/ping
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "nested", "folder.yml"), []byte(`name: nested
seq: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "notes.txt"), []byte("do not copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	source := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]

	state, err = app.CloneCollection(source.ID, "Source API copy", "Source API copy", root)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, "Source API copy")
	cloned := findTestCollectionByPath(state, targetPath)
	if cloned.ID == "" {
		t.Fatalf("cloned collection not found at %s", targetPath)
	}
	if cloned.Name != "Source API copy" || cloned.Path != targetPath {
		t.Fatalf("unexpected cloned collection: %#v", cloned)
	}
	if _, err := os.Stat(filepath.Join(sourcePath, "opencollection.yml")); err != nil {
		t.Fatalf("source config should remain: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(targetPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "name: Source API copy") {
		t.Fatalf("cloned config did not receive clone name:\n%s", config)
	}
	for _, rel := range []string{"ping.yml", filepath.Join("nested", "folder.yml")} {
		if _, err := os.Stat(filepath.Join(targetPath, rel)); err != nil {
			t.Fatalf("expected cloned yml file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(targetPath, "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-yml file should not be copied, got err=%v", err)
	}
}

func TestCloneCollectionBruCopiesBruFilesAndWritesConfig(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Bru Source")
	if err := os.MkdirAll(filepath.Join(sourcePath, "Folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "bruno.json"), []byte(`{"version":"1","name":"Bru Source","type":"collection","ignore":["node_modules",".git"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "collection.bru"), []byte(`meta {
  name: Bru Source
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "Ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "Folder", "folder.bru"), []byte(`meta {
  name: Folder
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "Notes.yml"), []byte(`info:
  name: Notes
  type: http
  seq: 2
http:
  method: GET
  url: https://example.test/notes
`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	source := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]

	state, err = app.CloneCollection(source.ID, "Bru Source copy", "Bru Source copy", root)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, "Bru Source copy")
	cloned := findTestCollectionByPath(state, targetPath)
	if cloned.ID == "" {
		t.Fatalf("cloned BRU collection not found at %s", targetPath)
	}
	if cloned.Name != "Bru Source copy" || cloned.Format != "bru" {
		t.Fatalf("unexpected cloned BRU collection: %#v", cloned)
	}
	config, err := os.ReadFile(filepath.Join(targetPath, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `"name": "Bru Source copy"`) {
		t.Fatalf("cloned bruno.json did not receive clone name:\n%s", config)
	}
	for _, rel := range []string{"collection.bru", "Ping.bru", filepath.Join("Folder", "folder.bru")} {
		if _, err := os.Stat(filepath.Join(targetPath, rel)); err != nil {
			t.Fatalf("expected cloned bru file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(targetPath, "Notes.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-bru yml file should not be copied, got err=%v", err)
	}
}

func TestCloneCollectionRejectsExistingTargetAndInvalidFolder(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Clone Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	source := state.Workspaces[0].Collections[0]
	if err := os.MkdirAll(filepath.Join(root, "Existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneCollection(source.ID, "Existing", "Existing", root); err == nil {
		t.Fatal("expected existing target to fail")
	}
	if _, err := app.CloneCollection(source.ID, "Invalid", "CON", root); err == nil {
		t.Fatal("expected invalid folder name to fail")
	}
}

func TestCreateFolderYAMLWritesFolderConfigAndNestedFolder(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Folder Create API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Folder Create API")
	if collection.ID == "" {
		t.Fatalf("created collection not found: %#v", state.Workspaces[0].Collections)
	}
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	created := findTestCollectionByPath(state, collection.Path)
	if created.ID == "" || len(created.Folders) != 1 {
		t.Fatalf("expected created folder in state, got %#v", created.Folders)
	}
	folder := created.Folders[0]
	if folder.Path != "users" || folder.DisplayPath != "Users" || folder.Name != "Users" || folder.Seq != 1 || folder.Auth.Mode != "inherit" {
		t.Fatalf("unexpected YAML folder state: %#v", folder)
	}
	rootMap, err := parseYAMLMapFile(filepath.Join(collection.Path, "users", "folder.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := nestedString(rootMap, "info", "name"); name != "Users" {
		t.Fatalf("expected YAML folder name Users, got %q", name)
	}
	if typ, _ := nestedString(rootMap, "info", "type"); typ != "folder" {
		t.Fatalf("expected YAML folder type folder, got %q", typ)
	}
	info, _ := mapValue(rootMap["info"])
	if seq := intValue(info["seq"], 0); seq != 1 {
		t.Fatalf("expected YAML folder seq 1, got %d", seq)
	}
	request, _ := mapValue(rootMap["request"])
	if auth := yamlScalarString(request["auth"]); auth != "inherit" {
		t.Fatalf("expected YAML folder auth inherit, got %q", auth)
	}

	state, err = app.CreateFolder(collection.ID, "users", "Profiles", "profiles")
	if err != nil {
		t.Fatal(err)
	}
	nestedCollection := findTestCollectionByPath(state, collection.Path)
	var nested FolderConfig
	for _, candidate := range nestedCollection.Folders {
		if candidate.Path == "users/profiles" {
			nested = candidate
			break
		}
	}
	if nested.Path != "users/profiles" || nested.DisplayPath != "Users/Profiles" || nested.Seq != 1 {
		t.Fatalf("unexpected nested folder: %#v", nested)
	}
	if _, err := os.Stat(filepath.Join(collection.Path, "users", "profiles", "folder.yml")); err != nil {
		t.Fatalf("expected nested folder.yml: %v", err)
	}
}

func TestCreateFolderBruWritesBrunoFolderDefaultsAndSequence(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Folder Bru API", "bru")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Folder Bru API")
	if collection.ID == "" {
		t.Fatalf("created BRU collection not found: %#v", state.Workspaces[0].Collections)
	}
	state, err = app.CreateRequest(collection.ID, "http", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	created := findTestCollectionByPath(state, collection.Path)
	if created.ID == "" || len(created.Folders) != 1 {
		t.Fatalf("expected BRU folder in state, got %#v", created.Folders)
	}
	if created.Folders[0].Seq != 2 {
		t.Fatalf("expected BRU folder seq after one request to be 2, got %#v", created.Folders[0])
	}
	data, err := os.ReadFile(filepath.Join(collection.Path, "users", "folder.bru"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"meta {",
		"name: Users",
		"seq: 2",
		"auth {",
		"mode: inherit",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("folder.bru missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "type: folder") {
		t.Fatalf("Bruno folder.bru should not write meta type for new folders:\n%s", text)
	}
	parsed := readFolderConfig(filepath.Join(collection.Path, "users"))
	if parsed.Name != "Users" || parsed.Seq != 2 || parsed.Auth.Mode != "inherit" {
		t.Fatalf("folder.bru did not round trip: %#v", parsed)
	}
}

func TestCreateFolderRejectsDuplicateReservedInvalidAndExistingDirectory(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Folder Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Folder Guard API")
	if collection.ID == "" {
		t.Fatalf("created guard collection not found: %#v", state.Workspaces[0].Collections)
	}
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateFolder(collection.ID, "", "Other Users", "users"); err == nil || !strings.Contains(err.Error(), "duplicate folder names") {
		t.Fatalf("expected duplicate directory name rejection, got %v", err)
	}
	if _, err := app.CreateFolder(collection.ID, "", "Environments", "environments"); err == nil || !strings.Contains(err.Error(), "reserved in bruno") {
		t.Fatalf("expected root environments reservation, got %v", err)
	}
	if _, err := app.CreateFolder(collection.ID, "", "Invalid", "CON"); err == nil || !strings.Contains(err.Error(), "invalid pathname") {
		t.Fatalf("expected invalid folder name rejection, got %v", err)
	}
	if _, err := app.CreateFolder(collection.ID, "missing", "Child", "child"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing parent rejection, got %v", err)
	}
	if err := os.MkdirAll(filepath.Join(collection.Path, "manual"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateFolder(collection.ID, "", "Manual", "manual"); err == nil || !strings.Contains(err.Error(), "directory already exists") {
		t.Fatalf("expected existing directory rejection, got %v", err)
	}
}

func TestRenameFolderYAMLMovesDirectoryAndUpdatesNestedState(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Rename YAML")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Folder Rename YAML
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.yml"), []byte(`info:
  name: Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "v1", "folder.yml"), []byte(`info:
  name: V1
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "list.yml"), []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "v1", "details.yml"), []byte(`info:
  name: User Details
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users/1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	if len(collection.Folders) != 2 || len(collection.Items) != 2 {
		t.Fatalf("unexpected collection before rename: folders=%#v items=%#v", collection.Folders, collection.Items)
	}
	state, err = app.RenameFolder(collection.ID, "Users", "Members", "members")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "users")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old users directory should be gone, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "members", "v1", "details.yml")); err != nil {
		t.Fatalf("expected nested request under renamed folder: %v", err)
	}
	folderText, err := os.ReadFile(filepath.Join(collectionPath, "members", "folder.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(folderText), "name: Members") || !strings.Contains(string(folderText), "auth: inherit") {
		t.Fatalf("renamed folder.yml missing name/auth:\n%s", folderText)
	}
	renamed := findTestCollectionByPath(state, collectionPath)
	var rootFolder, nestedFolder FolderConfig
	for _, folder := range renamed.Folders {
		switch folder.DisplayPath {
		case "Members":
			rootFolder = folder
		case "Members/V1":
			nestedFolder = folder
		}
	}
	if rootFolder.Path != "members" || rootFolder.Name != "Members" {
		t.Fatalf("unexpected renamed root folder: %#v", rootFolder)
	}
	if nestedFolder.Path != "members/v1" || nestedFolder.Name != "V1" {
		t.Fatalf("unexpected renamed nested folder: %#v", nestedFolder)
	}
	for _, item := range renamed.Items {
		if item.Name == "List Users" && (item.FolderPath != "Members" || !strings.Contains(filepath.ToSlash(item.FilePath), "/members/list.yml")) {
			t.Fatalf("root request path not updated: %#v", item)
		}
		if item.Name == "User Details" && (item.FolderPath != "Members/V1" || !strings.Contains(filepath.ToSlash(item.FilePath), "/members/v1/details.yml")) {
			t.Fatalf("nested request path not updated: %#v", item)
		}
	}
	fromDisk, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromDisk.Folders) != 2 || fromDisk.Folders[0].DisplayPath == "Users" {
		t.Fatalf("disk read did not reflect renamed folder: %#v", fromDisk.Folders)
	}
}

func TestRenameFolderBruUpdatesMetadataWithoutMovingWhenFilenameSame(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Rename Bru")
	folderPath := filepath.Join(collectionPath, "users")
	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Folder Rename Bru","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folderPath, "folder.bru"), []byte(`meta {
  name: Users
  seq: 1
}

auth {
  mode: inherit
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	state, err = app.RenameFolder(collection.ID, "users", "Display Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folderPath); err != nil {
		t.Fatalf("metadata-only rename should keep folder path: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(folderPath, "folder.bru"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	if !strings.Contains(text, "name: Display Users") || !strings.Contains(text, "mode: inherit") {
		t.Fatalf("folder.bru missing renamed metadata:\n%s", text)
	}
	if strings.Contains(text, "type: folder") {
		t.Fatalf("folder.bru should preserve Bruno folder meta shape:\n%s", text)
	}
	renamed := findTestCollectionByPath(state, collectionPath)
	if len(renamed.Folders) != 1 || renamed.Folders[0].Path != "users" || renamed.Folders[0].DisplayPath != "Display Users" {
		t.Fatalf("unexpected metadata-only rename state: %#v", renamed.Folders)
	}
}

func TestRenameFolderRejectsDuplicateReservedInvalidAndBlank(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Folder Rename Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Folder Rename Guard API")
	if collection.ID == "" {
		t.Fatalf("created collection not found: %#v", state.Workspaces[0].Collections)
	}
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateFolder(collection.ID, "", "Admins", "admins")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RenameFolder(collection.ID, "users", "Admins", "admins"); err == nil || !strings.Contains(err.Error(), "duplicate folder names") {
		t.Fatalf("expected duplicate folder rejection, got %v", err)
	}
	if _, err := app.RenameFolder(collection.ID, "users", "Users", "folder"); err == nil || !strings.Contains(err.Error(), "reserved in bruno") {
		t.Fatalf("expected reserved folder filename rejection, got %v", err)
	}
	if _, err := app.RenameFolder(collection.ID, "users", "Users", "CON"); err == nil || !strings.Contains(err.Error(), "invalid pathname") {
		t.Fatalf("expected invalid folder filename rejection, got %v", err)
	}
	if _, err := app.RenameFolder(collection.ID, "users", "", "users-new"); err == nil || !strings.Contains(err.Error(), "folder name is required") {
		t.Fatalf("expected blank folder name rejection, got %v", err)
	}
}

func TestDeleteFolderYAMLRemovesDirectoryNestedItemsAndRequestTabs(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Delete YAML")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Folder Delete YAML
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.yml"), []byte(`info:
  name: Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "v1", "folder.yml"), []byte(`info:
  name: V1
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "list.yml"), []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "v1", "details.yml"), []byte(`info:
  name: User Details
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users/1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "root.yml"), []byte(`info:
  name: Root
  type: http
  seq: 2
http:
  method: GET
  url: https://example.test/root
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	var listItem RequestItem
	for _, item := range collection.Items {
		if item.Name == "List Users" {
			listItem = item
			break
		}
	}
	if listItem.ID == "" {
		t.Fatalf("List Users request not loaded: %#v", collection.Items)
	}
	state, err = app.OpenRequestTab(collection.ID, listItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateResponseExample(collection.ID, listItem.ID, "Deleted example", "")
	if err != nil {
		t.Fatal(err)
	}
	foundRequestTabBeforeDelete := false
	foundExampleTabBeforeDelete := false
	for _, tab := range state.OpenTabs {
		if tab.CollectionID == collection.ID && tab.ItemID == listItem.ID && tab.Kind != "response-example" {
			foundRequestTabBeforeDelete = true
		}
		if tab.CollectionID == collection.ID && tab.ItemID == listItem.ID && tab.Kind == "response-example" {
			foundExampleTabBeforeDelete = true
		}
	}
	if !foundRequestTabBeforeDelete || !foundExampleTabBeforeDelete {
		t.Fatalf("expected request and response-example tabs before delete, got %#v", state.OpenTabs)
	}

	state, err = app.DeleteFolder(collection.ID, "Users")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "users")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted folder should be gone, err=%v", err)
	}
	deleted := findTestCollectionByPath(state, collectionPath)
	if len(deleted.Folders) != 0 {
		t.Fatalf("expected nested folders pruned, got %#v", deleted.Folders)
	}
	if len(deleted.Items) != 1 || deleted.Items[0].Name != "Root" {
		t.Fatalf("expected only root request to remain, got %#v", deleted.Items)
	}
	for _, tab := range state.OpenTabs {
		if tab.Kind != "response-example" && tab.ItemID == listItem.ID {
			t.Fatalf("deleted request tab should be closed, got %#v", state.OpenTabs)
		}
	}
	foundExampleTab := false
	for _, tab := range state.OpenTabs {
		if tab.Kind == "response-example" && tab.ItemID == listItem.ID {
			foundExampleTab = true
		}
	}
	if !foundExampleTab {
		t.Fatalf("Bruno leaves response-example tabs open after folder delete, got %#v", state.OpenTabs)
	}
}

func TestDeleteFolderBruRemovesFolderAndKeepsSiblingsResequenced(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Delete Bru")
	if err := os.MkdirAll(filepath.Join(collectionPath, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(collectionPath, "bravo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Folder Delete Bru","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "alpha", "folder.bru"), []byte(`meta {
  name: Alpha
  seq: 1
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bravo", "folder.bru"), []byte(`meta {
  name: Bravo
  seq: 3
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "Root.bru"), []byte(`meta {
  name: Root
  type: http
  seq: 2
}

get {
  url: https://example.test/root
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	state, err = app.DeleteFolder(collection.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alpha folder should be deleted, err=%v", err)
	}
	deleted := findTestCollectionByPath(state, collectionPath)
	if len(deleted.Folders) != 1 || deleted.Folders[0].Name != "Bravo" || deleted.Folders[0].Seq != 2 {
		t.Fatalf("expected remaining folder resequenced to 2, got %#v", deleted.Folders)
	}
	if len(deleted.Items) != 1 || deleted.Items[0].Name != "Root" || deleted.Items[0].Seq != 1 {
		t.Fatalf("expected remaining request resequenced to 1, got %#v", deleted.Items)
	}
	bravoText, err := os.ReadFile(filepath.Join(collectionPath, "bravo", "folder.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bravoText), "seq: 2") {
		t.Fatalf("bravo folder.bru was not resequenced:\n%s", bravoText)
	}
	rootText, err := os.ReadFile(filepath.Join(collectionPath, "Root.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rootText), "seq: 1") {
		t.Fatalf("root request was not resequenced:\n%s", rootText)
	}
}

func TestDeleteFolderRejectsMissingNotFoundLocallyAndMissingDirectory(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Folder Delete Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Folder Delete Guard API")
	if _, err := app.DeleteFolder(collection.ID, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing folder rejection, got %v", err)
	}
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(collection.Path, "users")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteFolder(collection.ID, "users"); err == nil || !strings.Contains(err.Error(), "directory does not exist") {
		t.Fatalf("expected missing directory rejection, got %v", err)
	}

	app.mu.Lock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == collection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "https://example.test/folder-delete-guard.git"
			}
		}
	}
	app.mu.Unlock()
	if err := os.RemoveAll(collection.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteFolder(collection.ID, "users"); err == nil || !strings.Contains(err.Error(), "not cloned locally") {
		t.Fatalf("expected not-cloned collection rejection, got %v", err)
	}
}

func TestDeleteRequestYAMLRemovesFileResequencesAndClosesOnlyRequestTab(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Request Delete YAML")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users", "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Request Delete YAML
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.yml"), []byte(`info:
  name: Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "archive", "folder.yml"), []byte(`info:
  name: Archive
  type: folder
  seq: 2
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	listPath := filepath.Join(collectionPath, "users", "list.yml")
	if err := os.WriteFile(listPath, []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}
	detailsPath := filepath.Join(collectionPath, "users", "details.yml")
	if err := os.WriteFile(detailsPath, []byte(`info:
  name: User Details
  type: http
  seq: 3
http:
  method: GET
  url: https://example.test/users/1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	var listItem RequestItem
	for _, item := range collection.Items {
		if item.Name == "List Users" {
			listItem = item
			break
		}
	}
	if listItem.ID == "" {
		t.Fatalf("List Users request not loaded: %#v", collection.Items)
	}
	state, err = app.OpenRequestTab(collection.ID, listItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateResponseExample(collection.ID, listItem.ID, "Deleted example", "")
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByPath(state, collectionPath)
	listItem, _ = findItemInState(state, collection.ID, listItem.ID)
	if len(listItem.Examples) != 1 {
		t.Fatalf("expected response example before delete, got %#v", listItem.Examples)
	}
	exampleTabID := responseExampleTabID(collection.ID, listItem.ID, listItem.Examples[0].ID)
	state, err = app.OpenRequestTab(collection.ID, listItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != collection.ID+":"+listItem.ID {
		t.Fatalf("request tab should be active before delete, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	notificationCount := len(state.Notifications)

	state, err = app.DeleteRequest(collection.ID, listItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(listPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted request file should be gone, err=%v", err)
	}
	deleted := findTestCollectionByPath(state, collectionPath)
	if _, ok := findItemInState(state, deleted.ID, listItem.ID); ok {
		t.Fatalf("deleted request should be removed from state: %#v", deleted.Items)
	}
	var details RequestItem
	for _, item := range deleted.Items {
		if item.Name == "User Details" {
			details = item
			break
		}
	}
	if details.ID == "" || details.Seq != 2 {
		t.Fatalf("remaining request should be resequenced to 2, got %#v", deleted.Items)
	}
	archive, err := findFolderConfig(&deleted, "Users/Archive")
	if err != nil || archive.Seq != 1 {
		t.Fatalf("remaining child folder should be resequenced to 1, folder=%#v err=%v", archive, err)
	}
	detailsText, err := os.ReadFile(detailsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(detailsText), "seq: 2") {
		t.Fatalf("remaining request file was not resequenced:\n%s", detailsText)
	}
	archiveText, err := os.ReadFile(filepath.Join(collectionPath, "users", "archive", "folder.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archiveText), "seq: 1") {
		t.Fatalf("remaining folder file was not resequenced:\n%s", archiveText)
	}
	for _, tab := range state.OpenTabs {
		if tab.ID == collection.ID+":"+listItem.ID || (tab.Kind != "response-example" && tab.ItemID == listItem.ID) {
			t.Fatalf("deleted request tab should be closed, got %#v", state.OpenTabs)
		}
	}
	if _, ok := findOpenTab(state.OpenTabs, exampleTabID); !ok || state.ActiveTabID != exampleTabID {
		t.Fatalf("Bruno leaves response-example tab open and focused after deleting active request, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if len(state.Notifications) != notificationCount {
		t.Fatalf("Bruno request delete does not toast on success, before=%d after=%#v", notificationCount, state.Notifications)
	}
}

func TestDeleteRequestBruRemovesFileAndKeepsSiblingsResequenced(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Request Delete Bru")
	if err := os.MkdirAll(filepath.Join(collectionPath, "bravo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Request Delete Bru","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(collectionPath, "first.bru")
	if err := os.WriteFile(firstPath, []byte(`meta {
  name: First
  type: http
  seq: 1
}

get {
  url: https://example.test/first
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(collectionPath, "second.bru")
	if err := os.WriteFile(secondPath, []byte(`meta {
  name: Second
  type: http
  seq: 3
}

get {
  url: https://example.test/second
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bravo", "folder.bru"), []byte(`meta {
  name: Bravo
  seq: 2
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	var first RequestItem
	for _, item := range collection.Items {
		if item.Name == "First" {
			first = item
			break
		}
	}
	if first.ID == "" {
		t.Fatalf("First request not loaded: %#v", collection.Items)
	}
	state, err = app.DeleteRequest(collection.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted .bru request should be gone, err=%v", err)
	}
	deleted := findTestCollectionByPath(state, collectionPath)
	if len(deleted.Items) != 1 || deleted.Items[0].Name != "Second" || deleted.Items[0].Seq != 2 {
		t.Fatalf("remaining .bru request should be resequenced to 2, got %#v", deleted.Items)
	}
	bravo, err := findFolderConfig(&deleted, "Bravo")
	if err != nil || bravo.Seq != 1 {
		t.Fatalf("remaining folder should be resequenced to 1, folder=%#v err=%v", bravo, err)
	}
	secondText, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secondText), "seq: 2") {
		t.Fatalf("remaining .bru request was not resequenced:\n%s", secondText)
	}
	bravoText, err := os.ReadFile(filepath.Join(collectionPath, "bravo", "folder.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bravoText), "seq: 1") {
		t.Fatalf("remaining folder.bru was not resequenced:\n%s", bravoText)
	}
}

func TestDeleteRequestRejectsMissingFileAndNotFoundLocally(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Request Delete Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Request Delete Guard API")
	state, err = app.CreateRequest(collection.ID, "http", "Source Request")
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Request Delete Guard API")
	source := collection.Items[0]
	if _, err := app.DeleteRequest(collection.ID, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing request rejection, got %v", err)
	}
	state, err = app.SaveRequest(collection.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Request Delete Guard API")
	source, _ = findItemInState(state, collection.ID, source.ID)
	if err := os.Remove(source.FilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteRequest(collection.ID, source.ID); err == nil || !strings.Contains(err.Error(), "file does not exist") {
		t.Fatalf("expected missing file rejection, got %v", err)
	}

	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Request Delete Remote Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	remoteCollection := findTestCollectionByName(state, "Request Delete Remote Guard API")
	state, err = app.CreateRequest(remoteCollection.ID, "http", "Remote Source")
	if err != nil {
		t.Fatal(err)
	}
	remoteCollection = findTestCollectionByName(state, "Request Delete Remote Guard API")
	remoteSource := remoteCollection.Items[0]
	state, err = app.SaveRequest(remoteCollection.ID, remoteSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteCollection = findTestCollectionByName(state, "Request Delete Remote Guard API")
	remoteSource, _ = findItemInState(state, remoteCollection.ID, remoteSource.ID)
	app.mu.Lock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == remoteCollection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "https://example.test/request-delete-guard.git"
			}
		}
	}
	app.mu.Unlock()
	if err := os.RemoveAll(remoteCollection.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteRequest(remoteCollection.ID, remoteSource.ID); err == nil || !strings.Contains(err.Error(), "not cloned locally") {
		t.Fatalf("expected not-cloned collection rejection, got %v", err)
	}
}

func TestCloneFolderYAMLCopiesNestedFoldersRequestsAndFreshIDs(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Clone YAML")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Folder Clone YAML
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.yml"), []byte(`info:
  name: Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "v1", "folder.yml"), []byte(`info:
  name: V1
  type: folder
  seq: 1
request:
  headers:
    - name: X-Folder
      value: nested
      enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "list.yml"), []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "v1", "details.yml"), []byte(`info:
  name: User Details
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users/1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	sourceIDs := map[string]string{}
	for _, item := range collection.Items {
		sourceIDs[item.FolderPath+"/"+item.Name] = item.ID
	}
	state, err = app.CloneFolder(collection.ID, "Users", "Users copy", "users-copy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "users-copy", "v1", "details.yml")); err != nil {
		t.Fatalf("expected nested cloned request on disk: %v", err)
	}
	clonedFolderText, err := os.ReadFile(filepath.Join(collectionPath, "users-copy", "folder.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clonedFolderText), "name: Users copy") || !strings.Contains(string(clonedFolderText), "seq: 2") || !strings.Contains(string(clonedFolderText), "auth: inherit") {
		t.Fatalf("cloned root folder.yml missing name/seq/auth:\n%s", clonedFolderText)
	}
	cloned := findTestCollectionByPath(state, collectionPath)
	var rootClone, nestedClone FolderConfig
	for _, folder := range cloned.Folders {
		switch folder.DisplayPath {
		case "Users copy":
			rootClone = folder
		case "Users copy/V1":
			nestedClone = folder
		}
	}
	if rootClone.Path != "users-copy" || rootClone.Name != "Users copy" || rootClone.Seq != 2 {
		t.Fatalf("unexpected root clone folder: %#v", rootClone)
	}
	if nestedClone.Path != "users-copy/v1" || nestedClone.Name != "V1" || len(nestedClone.Headers) != 1 {
		t.Fatalf("unexpected nested clone folder: %#v", nestedClone)
	}
	for _, item := range cloned.Items {
		if item.FolderPath != "Users copy" && item.FolderPath != "Users copy/V1" {
			continue
		}
		if !strings.Contains(filepath.ToSlash(item.FilePath), "/users-copy/") {
			t.Fatalf("cloned request kept source file path: %#v", item)
		}
		sourceKey := strings.TrimSuffix(strings.TrimPrefix(strings.Replace(item.FolderPath, "Users copy", "Users", 1)+"/"+item.Name, "/"), "/")
		if sourceIDs[sourceKey] == item.ID {
			t.Fatalf("cloned request reused source id: source=%s item=%#v", sourceKey, item)
		}
	}
	fromDisk, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromDisk.Folders) != 4 || len(fromDisk.Items) != 4 {
		t.Fatalf("disk round-trip did not include cloned tree: folders=%#v items=%#v", fromDisk.Folders, fromDisk.Items)
	}
}

func TestCloneFolderBruCopiesExamplesAndSkipsWebSocketLikeBruno(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Clone Bru")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Folder Clone Bru","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.bru"), []byte(`meta {
  name: Users
  seq: 1
}

auth {
  mode: inherit
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "list.bru"), []byte(`meta {
  name: List Users
  type: http
  seq: 1
}

get {
  url: https://example.test/users
  body: none
  auth: none
}

example {
  name: Users example
  request {
    url: https://example.test/users
    method: get
  }
  response {
    status {
      code: 200
      text: OK
    }
    body:json {
      {"ok":true}
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "socket.bru"), []byte(`meta {
  name: Socket
  type: websocket
  seq: 2
}

websocket {
  url: wss://example.test/socket
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	var sourceExampleID string
	for _, item := range collection.Items {
		if item.Name == "List Users" && len(item.Examples) == 1 {
			sourceExampleID = item.Examples[0].ID
		}
	}
	if sourceExampleID == "" {
		t.Fatalf("source response example not parsed: %#v", collection.Items)
	}
	state, err = app.CloneFolder(collection.ID, "users", "Users copy", "users-copy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionPath, "users-copy", "socket.bru")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Bruno folder clone skips websocket request files, err=%v", err)
	}
	folderText, err := os.ReadFile(filepath.Join(collectionPath, "users-copy", "folder.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(folderText), "name: Users copy") || !strings.Contains(string(folderText), "seq: 2") || !strings.Contains(string(folderText), "mode: inherit") {
		t.Fatalf("cloned folder.bru missing name/seq/auth:\n%s", folderText)
	}
	listText, err := os.ReadFile(filepath.Join(collectionPath, "users-copy", "list.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(listText), "example {") || !strings.Contains(string(listText), "name: Users example") {
		t.Fatalf("cloned request lost response example:\n%s", listText)
	}
	cloned := findTestCollectionByPath(state, collectionPath)
	var clonedList RequestItem
	for _, item := range cloned.Items {
		if item.FolderPath == "Users copy" && item.Name == "List Users" {
			clonedList = item
		}
		if item.FolderPath == "Users copy" && item.Name == "Socket" {
			t.Fatalf("websocket item should not be cloned like Bruno IPC: %#v", item)
		}
	}
	if clonedList.ID == "" || len(clonedList.Examples) != 1 || clonedList.Examples[0].ID == sourceExampleID {
		t.Fatalf("cloned request/example identity not refreshed: %#v sourceExampleID=%s", clonedList, sourceExampleID)
	}
}

func TestCloneFolderRejectsDuplicateReservedInvalidMissingAndNotFoundLocally(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Folder Clone Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Folder Clone Guard API")
	state, err = app.CreateFolder(collection.ID, "", "Users", "users")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneFolder(collection.ID, "users", "Other Users", "users"); err == nil || !strings.Contains(err.Error(), "duplicate folder names") {
		t.Fatalf("expected duplicate folder rejection, got %v", err)
	}
	if _, err := app.CloneFolder(collection.ID, "users", "Users copy", "folder"); err == nil || !strings.Contains(err.Error(), "reserved in bruno") {
		t.Fatalf("expected reserved filename rejection, got %v", err)
	}
	if _, err := app.CloneFolder(collection.ID, "users", "Users copy", "CON"); err == nil || !strings.Contains(err.Error(), "invalid pathname") {
		t.Fatalf("expected invalid filename rejection, got %v", err)
	}
	if _, err := app.CloneFolder(collection.ID, "missing", "Missing copy", "missing-copy"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing folder rejection, got %v", err)
	}
	if _, err := app.CloneFolder(collection.ID, "users", "", "users-copy"); err == nil || !strings.Contains(err.Error(), "folder name is required") {
		t.Fatalf("expected blank folder name rejection, got %v", err)
	}

	app.mu.Lock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == collection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "https://example.test/folder-clone-guard.git"
			}
		}
	}
	app.mu.Unlock()
	if err := os.RemoveAll(collection.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneFolder(collection.ID, "users", "Users copy", "users-copy"); err == nil || !strings.Contains(err.Error(), "not cloned locally") {
		t.Fatalf("expected not-cloned collection rejection, got %v", err)
	}
}

func TestCloneRequestYAMLCopiesRequestOpensTabAndWritesUniqueFile(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Request Clone YAML")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Request Clone YAML
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.yml"), []byte(`info:
  name: Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "list.yml"), []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	source := collection.Items[0]
	state, err = app.CloneRequest(collection.ID, source.ID, "List Users copy", "list-users-copy")
	if err != nil {
		t.Fatal(err)
	}

	clonePath := filepath.Join(collectionPath, "users", "list-users-copy.yml")
	cloneText, err := os.ReadFile(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(cloneText); !strings.Contains(file, "name: List Users copy") || !strings.Contains(file, "seq: 2") || !strings.Contains(file, "https://example.test/users") {
		t.Fatalf("cloned request file missing metadata/body:\n%s", file)
	}
	clonedCollection := findTestCollectionByPath(state, collectionPath)
	var cloned RequestItem
	for _, item := range clonedCollection.Items {
		if item.Name == "List Users copy" {
			cloned = item
			break
		}
	}
	if cloned.ID == "" || cloned.ID == source.ID || cloned.FolderPath != "Users" || filepath.Clean(cloned.FilePath) != clonePath || cloned.Seq != 2 || cloned.Draft {
		t.Fatalf("cloned request state mismatch: source=%#v clone=%#v", source, cloned)
	}
	if state.ActiveTabID != collection.ID+":"+cloned.ID {
		t.Fatalf("cloned request should be active, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if tab, ok := findOpenTab(state.OpenTabs, collection.ID+":"+cloned.ID); !ok || tab.Kind != "request" || tab.ItemID != cloned.ID {
		t.Fatalf("cloned request tab mismatch: tab=%#v ok=%v", tab, ok)
	}
	if len(state.Notifications) == 0 || state.Notifications[0].Message != "Request cloned!" {
		t.Fatalf("clone notification mismatch: %#v", state.Notifications)
	}
	fromDisk, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromDisk.Items) != 2 {
		t.Fatalf("disk round-trip should include cloned request: %#v", fromDisk.Items)
	}
}

func TestCloneRequestBruRefreshesExampleIDsAndPreservesExamples(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Request Clone Bru")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Request Clone Bru","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "list.bru")
	if err := os.WriteFile(requestPath, []byte(`meta {
  name: List Users
  type: http
  seq: 1
}

get {
  url: https://example.test/users
  body: none
  auth: none
}

example {
  name: Users example
  request {
    url: https://example.test/users
    method: get
  }
  response {
    status {
      code: 200
      text: OK
    }
    body:json {
      {"ok":true}
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	source := collection.Items[0]
	if len(source.Examples) != 1 {
		t.Fatalf("source example not parsed: %#v", source)
	}
	sourceExampleID := source.Examples[0].ID
	state, err = app.CloneRequest(collection.ID, source.ID, "List Users copy", "list-users-copy")
	if err != nil {
		t.Fatal(err)
	}
	clonePath := filepath.Join(collectionPath, "list-users-copy.bru")
	cloneText, err := os.ReadFile(clonePath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(cloneText); !strings.Contains(file, "name: List Users copy") || !strings.Contains(file, "example {") || !strings.Contains(file, "name: Users example") || !strings.Contains(file, "code: 200") {
		t.Fatalf("cloned request lost example content:\n%s", file)
	}
	clonedCollection := findTestCollectionByPath(state, collectionPath)
	var cloned RequestItem
	for _, item := range clonedCollection.Items {
		if item.Name == "List Users copy" {
			cloned = item
			break
		}
	}
	if cloned.ID == "" || cloned.ID == source.ID || len(cloned.Examples) != 1 || cloned.Examples[0].ID == sourceExampleID {
		t.Fatalf("cloned request/example identity not refreshed: source=%#v clone=%#v", source, cloned)
	}
	if state.ActiveTabID != collection.ID+":"+cloned.ID {
		t.Fatalf("cloned request should be active, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
}

func TestCloneRequestRejectsDuplicateReservedInvalidMissingAndNotFoundLocally(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Request Clone Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Request Clone Guard API")
	state, err = app.CreateRequest(collection.ID, "http", "Source Request")
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Request Clone Guard API")
	source := collection.Items[len(collection.Items)-1]
	state, err = app.SaveRequest(collection.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Request Clone Guard API")
	sourcePtr, err := findItem(&collection, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	source = *sourcePtr

	if _, err := app.CloneRequest(collection.ID, source.ID, "Other Source", "Source Request"); err == nil || !strings.Contains(err.Error(), "duplicate request names") {
		t.Fatalf("expected duplicate request rejection, got %v", err)
	}
	if _, err := app.CloneRequest(collection.ID, source.ID, "Source copy", "folder"); err == nil || !strings.Contains(err.Error(), "reserved in bruno") {
		t.Fatalf("expected reserved filename rejection, got %v", err)
	}
	if _, err := app.CloneRequest(collection.ID, source.ID, "Source copy", "CON"); err == nil || !strings.Contains(err.Error(), "invalid pathname") {
		t.Fatalf("expected invalid filename rejection, got %v", err)
	}
	if _, err := app.CloneRequest(collection.ID, "missing", "Missing copy", "missing-copy"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing request rejection, got %v", err)
	}
	if _, err := app.CloneRequest(collection.ID, source.ID, "", "source-copy"); err == nil || !strings.Contains(err.Error(), "request name is required") {
		t.Fatalf("expected blank request name rejection, got %v", err)
	}

	app.mu.Lock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == collection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "https://example.test/request-clone-guard.git"
			}
		}
	}
	app.mu.Unlock()
	if err := os.RemoveAll(collection.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneRequest(collection.ID, source.ID, "Source copy", "source-copy"); err == nil || !strings.Contains(err.Error(), "not cloned locally") {
		t.Fatalf("expected not-cloned collection rejection, got %v", err)
	}
}

func TestRenameRequestYAMLUpdatesNameKeepsFilePathAndTab(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Request Rename YAML")
	if err := os.MkdirAll(filepath.Join(collectionPath, "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Request Rename YAML
  version: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.yml"), []byte(`info:
  name: Users
  type: folder
  seq: 1
request:
  auth: inherit
`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "users", "list.yml")
	if err := os.WriteFile(requestPath, []byte(`info:
  name: List Users
  type: http
  seq: 1
http:
  method: GET
  url: https://example.test/users
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	source := collection.Items[0]
	state, err = app.OpenRequestTab(collection.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	renamedURL := "https://example.test/renamed"
	state, err = app.UpdateRequest(collection.ID, source.ID, RequestPatch{URL: &renamedURL})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.RenameRequest(collection.ID, source.ID, "List Users Renamed", "list")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(requestPath); err != nil {
		t.Fatalf("metadata-only rename should keep original request path: %v", err)
	}
	text, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(text); !strings.Contains(file, "name: List Users Renamed") || !strings.Contains(file, "https://example.test/renamed") {
		t.Fatalf("renamed request file missing name or saved draft URL:\n%s", file)
	}
	renamedCollection := findTestCollectionByPath(state, collectionPath)
	renamed, ok := findItemInState(state, renamedCollection.ID, source.ID)
	if !ok || renamed.Name != "List Users Renamed" || filepath.Clean(renamed.FilePath) != requestPath || renamed.ID != source.ID || renamed.Draft {
		t.Fatalf("renamed request state mismatch: ok=%v source=%#v renamed=%#v", ok, source, renamed)
	}
	if state.ActiveTabID != collection.ID+":"+source.ID {
		t.Fatalf("rename should keep the existing request tab active, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if len(state.Notifications) == 0 || state.Notifications[0].Message != "Item renamed successfully" {
		t.Fatalf("rename notification mismatch: %#v", state.Notifications)
	}
}

func TestRenameRequestBruMovesFilePreservesIDExamplesAndTab(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Request Rename Bru")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Request Rename Bru","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(collectionPath, "list.bru")
	if err := os.WriteFile(oldPath, []byte(`meta {
  name: List Users
  type: http
  seq: 1
}

get {
  url: https://example.test/users
  body: none
  auth: none
}

example {
  name: Users example
  request {
    url: https://example.test/users
    method: get
  }
  response {
    status {
      code: 200
      text: OK
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.ActiveWorkspaceID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByPath(state, collectionPath)
	source := collection.Items[0]
	if len(source.Examples) != 1 {
		t.Fatalf("source example not parsed: %#v", source)
	}
	exampleID := source.Examples[0].ID
	state, err = app.OpenRequestTab(collection.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.RenameRequest(collection.ID, source.ID, "List Users Renamed", "users-renamed")
	if err != nil {
		t.Fatal(err)
	}

	newPath := filepath.Join(collectionPath, "users-renamed.bru")
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old request file should be removed, err=%v", err)
	}
	text, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(text); !strings.Contains(file, "name: List Users Renamed") || !strings.Contains(file, "example {") || !strings.Contains(file, "name: Users example") {
		t.Fatalf("renamed request file missing name/example:\n%s", file)
	}
	renamed, ok := findItemInState(state, collection.ID, source.ID)
	if !ok || renamed.Name != "List Users Renamed" || filepath.Clean(renamed.FilePath) != newPath || renamed.ID != source.ID || len(renamed.Examples) != 1 || renamed.Examples[0].ID != exampleID {
		t.Fatalf("renamed request identity/example mismatch: ok=%v source=%#v renamed=%#v", ok, source, renamed)
	}
	if state.ActiveTabID != collection.ID+":"+source.ID {
		t.Fatalf("rename should keep request tab active, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
}

func TestRenameRequestRejectsDuplicateReservedInvalidMissingAndNotFoundLocally(t *testing.T) {
	root := t.TempDir()
	app := newAppInDirForTest(t, root)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.ActiveWorkspaceID, "Request Rename Guard API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	collection := findTestCollectionByName(state, "Request Rename Guard API")
	state, err = app.CreateRequest(collection.ID, "http", "Source Request")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "http", "Other Request")
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Request Rename Guard API")
	source := collection.Items[len(collection.Items)-2]
	other := collection.Items[len(collection.Items)-1]
	state, err = app.SaveRequest(collection.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveRequest(collection.ID, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	collection = findTestCollectionByName(state, "Request Rename Guard API")
	sourcePtr, err := findItem(&collection, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	source = *sourcePtr

	if _, err := app.RenameRequest(collection.ID, source.ID, "Source Renamed", "Other Request"); err == nil || !strings.Contains(err.Error(), "duplicate request names") {
		t.Fatalf("expected duplicate request rejection, got %v", err)
	}
	if _, err := app.RenameRequest(collection.ID, source.ID, "Source Renamed", "folder"); err == nil || !strings.Contains(err.Error(), "reserved in bruno") {
		t.Fatalf("expected reserved filename rejection, got %v", err)
	}
	if _, err := app.RenameRequest(collection.ID, source.ID, "Source Renamed", "CON"); err == nil || !strings.Contains(err.Error(), "invalid pathname") {
		t.Fatalf("expected invalid filename rejection, got %v", err)
	}
	if _, err := app.RenameRequest(collection.ID, "missing", "Missing", "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing request rejection, got %v", err)
	}
	if _, err := app.RenameRequest(collection.ID, source.ID, "", "source-renamed"); err == nil || !strings.Contains(err.Error(), "request name is required") {
		t.Fatalf("expected blank request name rejection, got %v", err)
	}
	if err := os.Remove(source.FilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RenameRequest(collection.ID, source.ID, "Source Renamed", "source-renamed"); err == nil || !strings.Contains(err.Error(), "file does not exist") {
		t.Fatalf("expected missing file rejection, got %v", err)
	}

	app.mu.Lock()
	for wi := range app.state.Workspaces {
		for ci := range app.state.Workspaces[wi].Collections {
			if app.state.Workspaces[wi].Collections[ci].ID == collection.ID {
				app.state.Workspaces[wi].Collections[ci].Remote = "https://example.test/request-rename-guard.git"
			}
		}
	}
	app.mu.Unlock()
	if err := os.RemoveAll(collection.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RenameRequest(collection.ID, source.ID, "Source Renamed", "source-renamed"); err == nil || !strings.Contains(err.Error(), "not cloned locally") {
		t.Fatalf("expected not-cloned collection rejection, got %v", err)
	}
}

func findTestCollectionByPath(state AppState, collectionPath string) Collection {
	cleanPath := filepath.Clean(collectionPath)
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if filepath.Clean(collection.Path) == cleanPath {
				return collection
			}
		}
	}
	return Collection{}
}

func findTestCollectionByName(state AppState, name string) Collection {
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.Name == name {
				return collection
			}
		}
	}
	return Collection{}
}

func TestJavaScriptRuntimeSupportsChaiLikeExpectHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"count":3,"items":["alpha","beta"],"profile":{"name":"Ada"},"message":"hello world","empty":[]}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("chai helpers", function () {
  expect(res.json).to.be.json;
  expect(res.json.profile).to.deep.equal({ name: "Ada" });
  expect(res.json.profile).to.have.property("name", "Ada").and.to.be.a("string");
  expect(res.json.items).to.have.lengthOf(2).and.to.include("beta");
  expect(res.json.items).with.length.greaterThan(1);
  expect(res.json.count).to.be.above(2).and.to.be.below(4);
  expect(res.json.count).to.be.at.least(3).and.to.be.at.most(3);
  expect(res.json.message).to.match(/^hello/);
  expect(res.json.empty).to.be.empty;
  expect(res.json.ok).to.be.true;
  expect(res.json.missing).to.not.exist;
  expect(res.json.profile).to.be.an("object");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("chai helper request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("chai helper test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsChaiRequire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Chai-Module"); got != "chai-ok" {
			t.Fatalf("chai module header mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chai":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `const chai = require("chai");
chai.expect(chai.expect).to.equal(expect);
chai.assert.equal("chai-ok", "chai-ok");
req.setHeader("X-Chai-Module", "chai-ok");`
	tests := `test("chai require shim", function () {
  const chai = require("chai");
  chai.expect(chai.expect).to.equal(expect);
  chai.expect(chai.assert).to.equal(assert);
  chai.expect(res.status).to.equal(200);
  chai.assert.equal(res.json.chai, true);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("chai require request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("chai require test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsJSONSchemaAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Ada","age":30,"email":"ada@example.com","website":"https://example.com","address":{"zip":"62701"},"tags":["api","test"]}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("jsonSchema assertions", function () {
  expect(res.getBody()).to.have.jsonSchema({
    $schema: "http://json-schema.org/draft-07/schema#",
    type: "object",
    required: ["name", "age", "email", "website", "address"],
    properties: {
      name: { type: "string" },
      age: { type: "number" },
      email: { type: "string", format: "email" },
      website: { type: "string", format: "uri" },
      tags: { type: "array", items: { type: "string" } },
      address: {
        type: "object",
        required: ["zip"],
        properties: { zip: { type: "string", pattern: "^[0-9]+$" } }
      }
    }
  });
  expect(res.getBody()).to.not.have.jsonSchema({
    type: "object",
    properties: { name: { type: "string", format: "email" } },
    required: ["name"]
  }, { allErrors: true });
  expect(res.getBody().address).to.not.have.jsonSchema({
    type: "object",
    properties: { zip: { type: "integer" } },
    required: ["zip"]
  });
  expect(res.getBody().address).to.have.jsonSchema({
    type: "object",
    properties: { zip: { type: "integer" } },
    required: ["zip"]
  }, { coerceTypes: true });
  expect(function () {
    expect(res.getBody()).to.have.jsonSchema({
      type: "object",
      properties: { name: { type: "string", customKeyword: true } }
    });
  }).to.throw("JSON schema compile error");
  expect(res.getBody()).to.have.jsonSchema({
    type: "object",
    properties: { name: { type: "string", customKeyword: true } }
  }, { strict: false });
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("jsonSchema request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("jsonSchema test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsJSONBodyAssertions(t *testing.T) {
	body := `{"hello":"bruno","data":{"items":[{"name":"first"},{"name":"second"}]},"matrix":[[1,2],[3,4]],"tags":["api","test"],"some-key":"hyphenated","a.b":"dotted-key","nested":{"x.y":{"z":"deep-dotted"}},"it's":"apostrophe-key","say \"hi\"":"quoted-key"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("jsonBody assertions", function () {
  const body = res.getBody();
  expect(body).to.have.jsonBody();
  expect(body).to.have.jsonBody({
    hello: "bruno",
    data: { items: [{ name: "first" }, { name: "second" }] },
    matrix: [[1, 2], [3, 4]],
    tags: ["api", "test"],
    "some-key": "hyphenated",
    "a.b": "dotted-key",
    nested: { "x.y": { z: "deep-dotted" } },
    "it's": "apostrophe-key",
    'say "hi"': "quoted-key"
  });
  expect(body).to.have.jsonBody("hello", "bruno");
  expect(body).to.have.jsonBody("data.items[0].name", "first");
  expect(body).to.have.jsonBody("data.items[1]", { name: "second" });
  expect(body).to.have.jsonBody("matrix[1][0]", 3);
  expect(body).to.have.jsonBody("matrix[0]", [1, 2]);
  expect(body).to.have.jsonBody('["some-key"]', "hyphenated");
  expect(body).to.have.jsonBody("['some-key']", "hyphenated");
  expect(body).to.have.jsonBody('["a.b"]', "dotted-key");
  expect(body).to.have.jsonBody('nested["x.y"].z', "deep-dotted");
  expect(body).to.have.jsonBody("[\"it's\"]", "apostrophe-key");
  expect(body).to.have.jsonBody('["say \\"hi\\""]', "quoted-key");
  expect(body).to.not.have.jsonBody("tags[5]");
  expect(body).not.to.have.jsonBody("hello", "wrong");
  expect(body).to.have.not.jsonBody({ wrong: "data" });
  let failed = false;
  try {
    expect(body).to.have.not.jsonBody("hello");
  } catch (err) {
    failed = true;
  }
  expect(failed).to.be.true;
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("jsonBody request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("jsonBody test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsJWTLibrary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `const jwt = require("jsonwebtoken");
const payload = { sub: "user123", role: "admin" };
const secret = "supersecret";
const token = jwt.sign(payload, secret, { algorithm: "HS256", expiresIn: "15m", issuer: "lite-api" });
bru.setEnvVar("jwt_token", token);
bru.setEnvVar("jwt_decoded", JSON.stringify(jwt.verify(token, secret, { algorithms: ["HS256"] })));
let callbackToken = "";
let callbackErr = "unset";
jwt.sign(payload, secret, { algorithm: "HS256" }, function (err, signed) {
  callbackErr = err;
  callbackToken = signed;
});
bru.setEnvVar("jwt_callback_token", callbackToken);
bru.setEnvVar("jwt_callback_err", callbackErr === null ? "null" : String(callbackErr && callbackErr.message));
let verifyCallbackSub = "";
jwt.verify(callbackToken, secret, { algorithms: ["HS256"] }, function (err, decoded) {
  if (err) throw err;
  verifyCallbackSub = decoded.sub;
});
bru.setEnvVar("jwt_verify_callback_sub", verifyCallbackSub);`
	tests := `test("jsonwebtoken sign verify decode", function () {
  const jwtFromRequire = require("jsonwebtoken");
  expect(jwt).to.equal(jwtFromRequire);
  const token = bru.getEnvVar("jwt_token");
  expect(token).to.be.a("string");
  expect(token.split(".")).to.have.lengthOf(3);
  const header = JSON.parse(atob(token.split(".")[0]));
  expect(header.alg).to.equal("HS256");
  expect(header.typ).to.equal("JWT");
  const decoded = JSON.parse(bru.getEnvVar("jwt_decoded"));
  expect(decoded.sub).to.equal("user123");
  expect(decoded.role).to.equal("admin");
  expect(decoded.iss).to.equal("lite-api");
  expect(decoded.exp).to.be.a("number");
  expect(jwtFromRequire.decode(token).sub).to.equal("user123");
  const complete = jwtFromRequire.decode(token, { complete: true });
  expect(complete.header.alg).to.equal("HS256");
  expect(complete.payload.sub).to.equal("user123");
  expect(complete.signature).to.be.a("string");
  expect(bru.getEnvVar("jwt_callback_err")).to.equal("null");
  expect(jwtFromRequire.verify(bru.getEnvVar("jwt_callback_token"), "supersecret").sub).to.equal("user123");
  expect(bru.getEnvVar("jwt_verify_callback_sub")).to.equal("user123");
});
test("jsonwebtoken error paths", function () {
  const jwtFromRequire = require("jsonwebtoken");
  const token = bru.getEnvVar("jwt_token");
  expect(function () {
    jwtFromRequire.verify(token, "wrong-secret");
  }).to.throw("invalid signature");
  expect(function () {
    jwtFromRequire.verify(token, "supersecret", { algorithms: ["HS512"] });
  }).to.throw("invalid algorithm");
  const expired = jwtFromRequire.sign({ sub: "old" }, "supersecret", { expiresIn: "-1s" });
  expect(function () {
    jwtFromRequire.verify(expired, "supersecret");
  }).to.throw("jwt expired");
  expect(jwtFromRequire.verify(expired, "supersecret", { ignoreExpiration: true }).sub).to.equal("old");
  let callbackMessage = "";
  jwtFromRequire.verify(token, "wrong-secret", { algorithms: ["HS256"] }, function (err, decoded) {
    callbackMessage = err.message;
    expect(decoded).to.be.undefined;
  });
  expect(callbackMessage).to.equal("invalid signature");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("jwt request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 2 {
		t.Fatalf("expected two jwt tests, got %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("jwt test failed: %#v", item.Response.TestResults)
		}
	}
}

func TestJavaScriptRuntimeSupportsCommonRequireShims(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		payload := map[string]interface{}{
			"ok":     true,
			"method": r.Method,
			"via":    r.URL.Query().Get("via"),
			"header": r.Header.Get("X-Axios"),
		}
		if len(bodyBytes) > 0 {
			var body map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &body); err == nil {
				for key, value := range body {
					payload[key] = value
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("common require shims", async function () {
  const uuid = require("uuid");
  const id = uuid.v4();
  expect(id).to.match(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  expect(uuid.version(id)).to.equal(4);
  expect(uuid.validate(id)).to.equal(true);
  expect(uuid.validate("not-a-uuid")).to.equal(false);
  const v1Uuid = uuid.v1();
  const v6Uuid = uuid.v6();
  const v7Uuid = uuid.v7();
  expect(uuid.version(v1Uuid)).to.equal(1);
  expect(uuid.version(v6Uuid)).to.equal(6);
  expect(uuid.version(v7Uuid)).to.equal(7);
  expect(uuid.version(uuid.v1ToV6(v1Uuid))).to.equal(6);
  expect(uuid.version(uuid.v6ToV1(v6Uuid))).to.equal(1);
  const { v7: uuidv7 } = require("uuid");
  const orderedV7A = uuidv7();
  const orderedV7B = uuidv7();
  expect(uuid.version(orderedV7A)).to.equal(7);
  expect(orderedV7A <= orderedV7B).to.equal(true);
  const dnsNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8";
  expect(uuid.v3.DNS).to.equal(dnsNamespace);
  expect(uuid.version(uuid.v3("example.com", dnsNamespace))).to.equal(3);
  expect(uuid.version(uuid.v5("example.com", uuid.v5.DNS))).to.equal(5);
  const parsedUUID = uuid.parse(id);
  expect(Object.keys(parsedUUID)).to.have.lengthOf(16);
  expect(uuid.stringify(parsedUUID)).to.equal(id);

	  const lodash = require("lodash");
	  expect(require("underscore")).to.equal(lodash);
	  expect(globalThis._).to.equal(lodash);
	  expect(typeof lodash.get).to.equal("function");
	  const lodashGet = require("lodash/get");
	  const lodashCloneDeep = require("lodash/cloneDeep.js");
	  const lodashSortBy = require("lodash/sortBy");
	  expect(lodashGet).to.equal(lodash.get);
	  expect(lodashCloneDeep).to.equal(lodash.cloneDeep);
	  expect(lodashSortBy([{ n: 2 }, { n: 1 }], "n")[0].n).to.equal(1);
	  const lodashSource = { users: [{ id: "a", score: 2 }, { id: "b", score: 5 }], nested: { value: 1 } };
	  expect(lodash.get(lodashSource, "users[1].score")).to.equal(5);
	  expect(lodashGet(lodashSource, "users[1].score")).to.equal(5);
  expect(lodash.get(lodashSource, ["missing", "value"], "fallback")).to.equal("fallback");
  lodash.set(lodashSource, "nested.extra.flag", true);
  expect(lodash.has(lodashSource, "nested.extra.flag")).to.equal(true);
  expect(lodash.unset(lodashSource, "nested.value")).to.equal(true);
  expect(lodash.has(lodashSource, "nested.value")).to.equal(false);
  const clonedLodashSource = lodash.cloneDeep(lodashSource);
  clonedLodashSource.users[0].score = 99;
  expect(lodashSource.users[0].score).to.equal(2);
  expect(lodash.isEqual({ a: [1, 2] }, { a: [1, 2] })).to.equal(true);
  expect(lodash.map(lodashSource.users, "id")).to.eql(["a", "b"]);
  expect(lodash.filter(lodashSource.users, { id: "b" })[0].score).to.equal(5);
  expect(lodash.find(lodashSource.users, ["id", "a"]).score).to.equal(2);
  expect(lodash.reduce([1, 2, 3], function (sum, value) { return sum + value; }, 0)).to.equal(6);
  expect(lodash.groupBy([{ type: "a" }, { type: "a" }, { type: "b" }], "type").a).to.have.lengthOf(2);
  expect(Object.keys(lodash.keyBy(lodashSource.users, "id"))).to.eql(["a", "b"]);
  expect(lodash.sortBy([{ n: 2 }, { n: 1 }], "n")[0].n).to.equal(1);
  expect(lodash.uniq([1, 1, 2, 2])).to.eql([1, 2]);
  expect(lodash.flattenDeep([1, [2, [3]]])).to.eql([1, 2, 3]);
  expect(lodash.chunk([1, 2, 3], 2)).to.eql([[1, 2], [3]]);
  expect(lodash.pick({ a: 1, b: 2 }, ["a"])).to.eql({ a: 1 });
	  expect(lodash.omit({ a: 1, b: 2 }, ["b"])).to.eql({ a: 1 });
	  expect(lodash.merge({ a: { b: 1 } }, { a: { c: 2 } })).to.eql({ a: { b: 1, c: 2 } });
	  expect(lodash.chain([{ n: 2 }, { n: 1 }]).sortBy("n").map("n").value()).to.eql([1, 2]);

	  const xmlFormat = require("xml-formatter");
	  expect(xmlFormat.default).to.equal(xmlFormat);
	  expect(xmlFormat("<root><item id=\"1\">ok</item><empty/></root>")).to.equal("<root>\n  <item id=\"1\">ok</item>\n  <empty/>\n</root>");
	  expect(xmlFormat("<root>\n  <item> ok </item>\n</root>", { indentation: "", lineSeparator: "" })).to.equal("<root><item> ok </item></root>");
	  expect(function () { xmlFormat("<root>"); }).to.throw();

	  const cheerio = require("cheerio");
	  expect(cheerio.default).to.equal(cheerio);
	  const $cheerio = cheerio.load("<h2 class=\"title\">Hello world</h2>");
	  $cheerio("h2.title").text("Hello pre-request!");
	  $cheerio("h2").addClass("welcome");
	  expect($cheerio.html()).to.equal("<html><head></head><body><h2 class=\"title welcome\">Hello pre-request!</h2></body></html>");
	  expect($cheerio("h2.title").text()).to.equal("Hello pre-request!");
	  expect($cheerio("h2").attr("class")).to.equal("title welcome");
	  $cheerio("h2").attr("data-check", "ok");
	  expect($cheerio("h2").attr("data-check")).to.equal("ok");
	  expect($cheerio("h2").html()).to.equal("Hello pre-request!");
	  expect($cheerio("h2").length).to.equal(1);

	  const xml2js = require("xml2js");
	  expect(xml2js.default).to.equal(xml2js);
	  let xml2jsSimple;
	  xml2js.parseString("<root>Hello xml2js!</root>", function (err, result) {
	    expect(err).to.equal(null);
	    xml2jsSimple = result;
	  });
	  expect(xml2jsSimple).to.eql({ root: "Hello xml2js!" });
	  let xml2jsNested;
	  xml2js.parseString("<root><item id=\"1\">one</item><item>two</item></root>", { explicitArray: false }, function (err, result) {
	    expect(err).to.equal(null);
	    xml2jsNested = result;
	  });
	  expect(xml2jsNested.root.item[0]._).to.equal("one");
	  expect(xml2jsNested.root.item[0].$.id).to.equal("1");
	  expect(xml2jsNested.root.item[1]).to.equal("two");
	  const xml2jsParser = new xml2js.Parser({ explicitArray: false, trim: true });
	  let xml2jsParserResult;
	  xml2jsParser.parseString("<root><item> ok </item></root>", function (err, result) {
	    expect(err).to.equal(null);
	    xml2jsParserResult = result;
	  });
	  expect(xml2jsParserResult.root.item).to.equal("ok");
	  const xml2jsPromiseResult = await xml2js.parseStringPromise("<root><ok>true</ok></root>", { explicitArray: false });
	  expect(xml2jsPromiseResult.root.ok).to.equal("true");
	  let xml2jsError;
	  xml2js.parseString("<root>", function (err) {
	    xml2jsError = err;
	  });
	  expect(String(xml2jsError.message || xml2jsError)).to.include("EOF");

	  const YAML = require("yaml");
	  expect(YAML.default).to.equal(YAML);
	  const parsedYAML = YAML.parse("name: bruno\nitems:\n  - one\n  - two\nnested:\n  count: 2\n");
	  expect(parsedYAML.name).to.equal("bruno");
	  expect(parsedYAML.items).to.eql(["one", "two"]);
	  expect(parsedYAML.nested.count).to.equal(2);
	  const stringifiedYAML = YAML.stringify({ name: "liteapi", enabled: true, items: ["a", "b"] });
	  expect(stringifiedYAML).to.include("name: liteapi");
	  expect(stringifiedYAML).to.include("enabled: true");
	  expect(stringifiedYAML).to.include("- a");
	  const yamlLineCounter = new YAML.LineCounter();
	  const yamlDoc = YAML.parseDocument("runtime:\n  scripts:\n    - type: before-request\n      code: |\n        bru.setVar('ok', true)\n", { lineCounter: yamlLineCounter });
	  const yamlScripts = yamlDoc.getIn(["runtime", "scripts"], true);
	  expect(YAML.isSeq(yamlScripts)).to.equal(true);
	  expect(YAML.isMap(yamlScripts.items[0])).to.equal(true);
	  expect(yamlScripts.items[0].get("type")).to.equal("before-request");
	  const yamlCodeNode = yamlScripts.items[0].get("code", true);
	  expect(YAML.isScalar(yamlCodeNode)).to.equal(true);
	  expect(yamlCodeNode.value).to.include("bru.setVar");
	  expect(yamlLineCounter.linePos(yamlCodeNode.range[0]).line).to.be.above(0);
	  expect(yamlDoc.toJSON().runtime.scripts[0].type).to.equal("before-request");
	  expect(yamlDoc.toString()).to.include("before-request");
	  expect(function () { YAML.parse("bad: ["); }).to.throw();

	  const crypto = require("node:crypto");
	  const cryptoAlias = require("crypto");
  expect(cryptoAlias).to.equal(crypto);
  expect(typeof crypto.createHash).to.equal("function");
  expect(crypto.createHash("sha256").update("hello bruno").digest("hex")).to.equal("b109f0a91199b99e240133e0a4faa5ebb345fe4de1cbb1666a7653ebadd11add");
  expect(crypto.createHash("sha1").update(Buffer.from("hello bruno")).digest("base64")).to.equal("agIg01NZD0oYTApblbTwW7npVa8=");
  const hashBuffer = crypto.createHash("sha256").update("hello").digest();
  expect(Buffer.isBuffer(hashBuffer)).to.equal(true);
  expect(hashBuffer.toString("hex")).to.equal("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824");
  expect(crypto.createHash("sha-256").update("68656c6c6f206272756e6f", "hex").digest("hex")).to.equal("b109f0a91199b99e240133e0a4faa5ebb345fe4de1cbb1666a7653ebadd11add");
  expect(crypto.createHash("sha1").update("nonce").update("created").update("password").digest("hex")).to.equal("05f0c69140e496ced8ff0eb222ef991b7bceb265");
  expect(crypto.createHmac("sha256", "secret").update("hello bruno").digest("hex")).to.equal("0b88e37cb009ff6f8d9b599e57417bb429c5045a85bfba204e761364bef2240c");
  expect(crypto.randomUUID()).to.match(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  const bytes = crypto.randomBytes(4);
  expect(Buffer.isBuffer(bytes)).to.equal(true);
  expect(bytes.length).to.equal(4);
  expect(bytes.byteLength).to.equal(4);
  const target = new Uint8Array(6);
  expect(crypto.getRandomValues(target)).to.equal(target);
  expect(target.length).to.equal(6);
  expect(crypto.getHashes()).to.include("sha256");
  expect(crypto.getCiphers().some((name) => name.includes("aes"))).to.equal(true);
  const pbkdf2Key = crypto.pbkdf2Sync("password", "salt", 1000, 32, "sha256");
  expect(Buffer.isBuffer(pbkdf2Key)).to.equal(true);
  expect(pbkdf2Key.length).to.equal(32);
  const scryptKey = crypto.scryptSync("password", "salt", 32);
  expect(Buffer.isBuffer(scryptKey)).to.equal(true);
  expect(scryptKey.length).to.equal(32);
  expect(crypto.timingSafeEqual(Buffer.from("hello"), Buffer.from("hello"))).to.equal(true);
  expect(crypto.timingSafeEqual(Buffer.from("hello"), Buffer.from("world"))).to.equal(false);
  const cipherKey = crypto.randomBytes(32);
  const cipherIv = crypto.randomBytes(16);
  const cipher = crypto.createCipheriv("aes-256-cbc", cipherKey, cipherIv);
  let encrypted = cipher.update("secret message", "utf8", "hex");
  encrypted += cipher.final("hex");
  expect(encrypted).to.have.length.greaterThan(0);
  const decipher = crypto.createDecipheriv("aes-256-cbc", cipherKey, cipherIv);
  let decrypted = decipher.update(encrypted, "hex", "utf8");
  decrypted += decipher.final("utf8");
  expect(decrypted).to.equal("secret message");

  const nanoid = require("nanoid");
  expect(nanoid.nanoid()).to.have.lengthOf(21);
  expect(nanoid.nanoid(8)).to.have.lengthOf(8);

  const encodeBase64 = require("btoa");
  const decodeBase64 = require("atob");
  expect(encodeBase64).to.equal(btoa);
  expect(decodeBase64).to.equal(atob);
  expect(btoa("hello bruno")).to.equal("aGVsbG8gYnJ1bm8=");
  expect(atob("aGVsbG8gYnJ1bm8=")).to.equal("hello bruno");
  const binary = String.fromCharCode(0, 1, 255, 254);
  const encodedBinary = btoa(binary);
  expect(encodedBinary).to.equal("AAH//g==");
  const decodedBinary = atob(encodedBinary);
  expect(decodedBinary.charCodeAt(0)).to.equal(0);
  expect(decodedBinary.charCodeAt(1)).to.equal(1);
  expect(decodedBinary.charCodeAt(2)).to.equal(255);
  expect(decodedBinary.charCodeAt(3)).to.equal(254);

  const bufferModule = require("buffer");
  const nodeBufferModule = require("node:buffer");
  expect(Buffer).to.equal(bufferModule.Buffer);
  expect(nodeBufferModule).to.equal(bufferModule);
  const buf = Buffer.from("hello bruno", "utf8");
  expect(buf.toString()).to.equal("hello bruno");
  expect(buf.toString("base64")).to.equal("aGVsbG8gYnJ1bm8=");
  expect(buf.toString("hex")).to.equal("68656c6c6f206272756e6f");
  expect(buf.length).to.equal(11);
  expect(Buffer.from("aGVsbG8=", "base64").toString()).to.equal("hello");
  expect(Buffer.from("68656c6c6f", "hex").toString()).to.equal("hello");
  const allocated = Buffer.alloc(5, "ab");
  expect(allocated.toString()).to.equal("ababa");
  const concatenated = Buffer.concat([Buffer.from("hello "), Buffer.from("world")]);
  expect(concatenated.toString()).to.equal("hello world");
  expect(Buffer.isBuffer(Buffer.from("test"))).to.equal(true);
  expect(Buffer.isBuffer("string")).to.equal(false);
  expect(Buffer.isBuffer(new Uint8Array(4))).to.equal(false);
  expect(Buffer.byteLength("hello bruno")).to.equal(11);
  expect(Buffer.byteLength("aGVsbG8=", "base64")).to.equal(5);
  expect(Buffer.from("hello bruno").subarray(0, 5).toString()).to.equal("hello");
  expect(Buffer.from([0, 1, 255, 254]).toString("base64")).to.equal("AAH//g==");

  expect(bru.isSafeMode()).to.equal(true);
  expect(function () { require("fs"); }).to.throw("Cannot find module");
  expect(function () { require("node:fs"); }).to.throw("Cannot find module");

  const url = require("node:url");
  expect(require("url")).to.equal(url);
  const parsedUrl = url.parse("https://user:pass@example.com:8443/a/b?x=1&x=2#frag", true);
  expect(parsedUrl.protocol).to.equal("https:");
  expect(parsedUrl.auth).to.equal("user:pass");
  expect(parsedUrl.host).to.equal("example.com:8443");
  expect(parsedUrl.hostname).to.equal("example.com");
  expect(parsedUrl.port).to.equal("8443");
  expect(parsedUrl.pathname).to.equal("/a/b");
  expect(parsedUrl.path).to.equal("/a/b?x=1&x=2");
  expect(parsedUrl.hash).to.equal("#frag");
  expect(parsedUrl.query.x).to.eql(["1", "2"]);
  expect(url.format({ protocol: "https:", hostname: "example.com", pathname: "/a", query: { x: "1", y: "two words" } })).to.equal("https://example.com/a?x=1&y=two+words");
  expect(url.resolve("https://example.com/a/b?x=1", "../c?y=2")).to.equal("https://example.com/c?y=2");
  expect(url.resolveObject("https://example.com/a/b?x=1", "../c?y=2").pathname).to.equal("/c");
  const whatwgUrl = new URL("child?x=1", "https://example.com/base/path");
  expect(whatwgUrl.hostname).to.equal("example.com");
  expect(whatwgUrl.pathname).to.equal("/base/child");
  expect(whatwgUrl.searchParams.get("x")).to.equal("1");
  whatwgUrl.searchParams.set("x", "two words");
  expect(whatwgUrl.href).to.equal("https://example.com/base/child?x=two+words");
  const params = new URLSearchParams({ a: "1", b: "two words" });
  params.append("a", "2");
  expect(params.get("a")).to.equal("1");
  expect(params.getAll("a")).to.eql(["1", "2"]);
  expect(params.toString()).to.equal("a=1&b=two+words&a=2");

  const querystring = require("node:querystring");
  expect(require("querystring")).to.equal(querystring);
  const parsedQuery = querystring.parse("a=1&a=2&space=two+words&empty&encoded=h%C3%A9");
  expect(parsedQuery.a).to.eql(["1", "2"]);
  expect(parsedQuery.space).to.equal("two words");
  expect(parsedQuery.empty).to.equal("");
  expect(parsedQuery.encoded).to.equal("hé");
  expect(querystring.stringify({ a: ["1", "2"], space: "two words", empty: "", nil: null })).to.equal("a=1&a=2&space=two%20words&empty=&nil=");
  expect(querystring.parse("x:1;y:2", ";", ":").y).to.equal("2");
  expect(Object.keys(querystring.parse("a=1&b=2&c=3", "&", "=", { maxKeys: 2 }))).to.eql(["a", "b"]);
  expect(querystring.escape("a b&hé")).to.equal("a%20b%26h%C3%A9");
  expect(querystring.unescape("a%20b%26h%C3%A9")).to.equal("a b&hé");

  const os = require("node:os");
  expect(require("os")).to.equal(os);
  expect(["darwin", "linux", "win32"]).to.include(os.platform());
  expect(["x64", "arm64", "arm", "ia32"]).to.include(os.arch());
  expect(os.type()).to.be.a("string");
  expect(os.release()).to.be.a("string");
  expect(os.hostname()).to.be.a("string");
  expect(os.homedir()).to.be.a("string").with.length.greaterThan(0);
  expect(os.tmpdir()).to.be.a("string").with.length.greaterThan(0);
  const cpus = os.cpus();
  expect(cpus).to.be.an("array").with.length.greaterThan(0);
  expect(cpus[0]).to.have.property("model");
  expect(os.availableParallelism()).to.be.above(0);
  expect(os.totalmem()).to.be.a("number").greaterThan(0);
  expect(os.freemem()).to.be.a("number").greaterThan(0);
  expect(os.uptime()).to.be.a("number").greaterThan(0);
  expect(os.loadavg()).to.be.an("array").with.lengthOf(3);
  expect(os.networkInterfaces()).to.be.an("object");
  const userInfo = os.userInfo();
  expect(userInfo.username).to.be.a("string");
  expect(userInfo.homedir).to.be.a("string");
  expect(["\n", "\r\n"]).to.include(os.EOL);
  expect(os.constants).to.have.property("signals");

  const EventEmitter = require("node:events");
  expect(require("events")).to.equal(EventEmitter);
  expect(EventEmitter.EventEmitter).to.equal(EventEmitter);
  expect(EventEmitter.default).to.equal(EventEmitter);
  expect(EventEmitter.defaultMaxListeners).to.equal(10);
  const emitter = new EventEmitter();
  const calls = [];
  function first(value) { calls.push("first:" + value); }
  function normal(value) { calls.push("normal:" + value); }
  function onceOnly(value) { calls.push("once:" + value); }
  emitter.on("removeListener", function (name, listener) {
    if (name === "work") calls.push("removed:" + (listener === normal));
  });
  expect(emitter.on("work", normal)).to.equal(emitter);
  emitter.prependListener("work", first);
  emitter.once("work", onceOnly);
  expect(emitter.emit("work", "a")).to.equal(true);
  expect(emitter.emit("work", "b")).to.equal(true);
  emitter.removeListener("work", normal);
  expect(calls).to.eql(["first:a", "normal:a", "removed:false", "once:a", "first:b", "normal:b", "removed:true"]);
  expect(emitter.listenerCount("work")).to.equal(1);
  expect(EventEmitter.listenerCount(emitter, "work")).to.equal(1);
  expect(emitter.listeners("work")).to.have.lengthOf(1);
  expect(emitter.listeners("work")[0]).to.equal(first);
  expect(EventEmitter.getEventListeners(emitter, "work")).to.have.lengthOf(1);
  expect(EventEmitter.getEventListeners(emitter, "work")[0]).to.equal(first);
  expect(emitter.eventNames()).to.include("work");
  emitter.removeAllListeners("work");
  expect(emitter.emit("work", "c")).to.equal(false);
  expect(emitter.listenerCount("work")).to.equal(0);
  const peek = new EventEmitter();
  function peekOnce() {}
  peek.once("peek", peekOnce);
  expect(peek.rawListeners("peek")[0]).to.not.equal(peekOnce);
  expect(peek.listeners("peek")[0]).to.equal(peekOnce);
  const oncePromise = EventEmitter.once(peek, "ready");
  peek.emit("ready", "alpha", 7);
  expect(await oncePromise).to.eql(["alpha", 7]);
  EventEmitter.setMaxListeners(2, peek);
  expect(EventEmitter.getMaxListeners(peek)).to.equal(2);
  const asyncEvents = EventEmitter.on(peek, "tick");
  peek.emit("tick", "one");
  expect((await asyncEvents.next()).value).to.eql(["one"]);
  await asyncEvents.return();
  const eventTarget = new EventTarget();
  function targetHandler() {}
  eventTarget.addEventListener("ping", targetHandler);
  expect(EventEmitter.getEventListeners(eventTarget, "ping")[0]).to.equal(targetHandler);
  expect(function () { new EventEmitter().emit("error", new Error("boom")); }).to.throw("boom");

  const stream = require("node:stream");
  expect(require("stream")).to.equal(stream);
  const { Readable, Writable, Transform, Duplex, pipeline } = stream;
  expect(Readable).to.be.a("function");
  expect(Writable).to.be.a("function");
  expect(Transform).to.be.a("function");
  expect(Duplex).to.be.a("function");
  expect(pipeline).to.be.a("function");
  const readable = Readable.from(["hello", " ", "bruno"]);
  expect(readable).to.be.an("object");
  expect(readable.read).to.be.a("function");
  expect(readable.on).to.be.a("function");
  expect(readable.read()).to.equal("hello");
  const chunks = [];
  const writable = new Writable({
    write(chunk, enc, cb) { chunks.push(chunk); cb(); }
  });
  expect(writable.write).to.be.a("function");
  expect(writable.end).to.be.a("function");
  writable.write("one");
  expect(chunks).to.eql(["one"]);
  const transform = new Transform({
    transform(chunk, enc, cb) { cb(null, String(chunk).toUpperCase()); }
  });
  transform.write("ok");
  expect(transform.read()).to.equal("OK");
  const duplex = new Duplex({
    read() {},
    write(chunk, enc, cb) { cb(); }
  });
  expect(duplex.read).to.be.a("function");
  expect(duplex.write).to.be.a("function");

  const zlib = require("node:zlib");
  expect(require("zlib")).to.equal(zlib);
  const zlibData = Buffer.from("Hello Bruno! ".repeat(100));
  const gzipped = zlib.gzipSync(zlibData);
  expect(Buffer.isBuffer(gzipped)).to.equal(true);
  expect(gzipped.length).to.be.lessThan(zlibData.length);
  expect(zlib.gunzipSync(gzipped).toString()).to.equal(zlibData.toString());
  const deflated = zlib.deflateSync(zlibData);
  expect(deflated.length).to.be.lessThan(zlibData.length);
  expect(zlib.inflateSync(deflated).toString()).to.equal(zlibData.toString());
  expect(zlib.unzipSync(gzipped).toString()).to.equal(zlibData.toString());
  expect(zlib.unzipSync(deflated).toString()).to.equal(zlibData.toString());
  const rawDeflated = zlib.deflateRawSync(zlibData);
  expect(zlib.inflateRawSync(rawDeflated).toString()).to.equal(zlibData.toString());
  const brotliCompressed = zlib.brotliCompressSync(zlibData);
  expect(brotliCompressed.length).to.be.lessThan(zlibData.length);
  expect(zlib.brotliDecompressSync(brotliCompressed).toString()).to.equal(zlibData.toString());
  const high = zlib.gzipSync(zlibData, { level: 9 });
  const low = zlib.gzipSync(zlibData, { level: 1 });
  expect(high.length).to.be.at.most(low.length);
  expect(zlib.constants).to.have.property("Z_BEST_COMPRESSION");
  expect(zlib.Z_BEST_COMPRESSION).to.equal(zlib.constants.Z_BEST_COMPRESSION);
  let callbackRoundTrip = "";
  zlib.gzip(zlibData, function(err, compressed) {
    if (err) throw err;
    callbackRoundTrip = zlib.gunzipSync(compressed).toString();
  });
  await Promise.resolve();
  expect(callbackRoundTrip).to.equal(zlibData.toString());

  const util = require("node:util");
  expect(require("util")).to.equal(util);
  expect(util.format("hello %s %d %% %j", "bruno", 42, { ok: true })).to.equal("hello bruno 42 % {\"ok\":true}");
  expect(util.format({ ok: true })).to.include("ok");
  expect(util.inspect({ ok: true, list: [1, 2] })).to.include("list");
  expect(util.types.isUint8Array(Buffer.from("x"))).to.equal(true);
  expect(util.types.isArrayBuffer(new ArrayBuffer(2))).to.equal(true);
  expect(util.types.isTypedArray(new Uint8Array(2))).to.equal(true);
  expect(util.isDeepStrictEqual({ a: [1], b: Buffer.from("x") }, { a: [1], b: Buffer.from("x") })).to.equal(true);
  expect(util.isDeepStrictEqual({ a: [1] }, { a: [2] })).to.equal(false);
  const promisified = util.promisify(function (value, cb) { cb(null, value + 1); });
  expect(await promisified(41)).to.equal(42);

  const tv4Module = require("tv4");
  expect(tv4).to.equal(tv4Module);
  expect(tv4.validate({ ok: true }, { type: "object", properties: { ok: { type: "boolean" } } })).to.equal(true);
  expect(tv4.error).to.be.null;
  expect(tv4.validate({ ok: "yes" }, { type: "object", properties: { ok: { type: "boolean" } } })).to.equal(false);
  expect(tv4.error.message).to.be.a("string");
  expect(tv4.validate({ ok: true }, { type: "object", properties: { ok: { type: "boolean" } } })).to.equal(true);
  expect(tv4.error).to.be.null;

  const AjvFromRequire = require("ajv");
  const addFormatsFromRequire = require("ajv-formats");
  expect(Ajv).to.equal(AjvFromRequire);
  expect(addFormats).to.equal(addFormatsFromRequire);
  const ajv = new AjvFromRequire();
  addFormatsFromRequire(ajv);
  const validateObject = ajv.compile({
    type: "object",
    properties: {
      name: { type: "string", minLength: 1 },
      age: { type: "integer", minimum: 0 }
    },
    required: ["name", "age"]
  });
  expect(validateObject({ name: "Bruno User", age: 25 })).to.equal(true);
  expect(validateObject({ name: "", age: -1 })).to.equal(false);
  expect(validateObject.errors[0].message).to.be.a("string");
  const validateDate = ajv.compile({ type: "string", format: "date-time" });
  expect(validateDate(new Date().toISOString())).to.equal(true);
  expect(validateDate("not-a-date")).to.equal(false);

  const axios = require("axios");
  expect(axios.default).to.equal(axios);
  const axiosPost = await axios.post(req.getUrl(), { hello: "bruno" }, { headers: { "X-Axios": "yes" } });
  expect(axiosPost.status).to.equal(200);
  expect(axiosPost.data.method).to.equal("POST");
  expect(axiosPost.data.hello).to.equal("bruno");
  expect(axiosPost.data.header).to.equal("yes");
  const axiosGet = await axios.get(req.getUrl(), { params: { via: "axios" } });
  expect(axiosGet.data.method).to.equal("GET");
  expect(axiosGet.data.via).to.equal("axios");

  const path = require("node:path");
  expect(require("path")).to.equal(path);
  expect(path.basename("/tmp/example.json", ".json")).to.equal("example");
  expect(path.extname("/tmp/example.json")).to.equal(".json");
  expect(path.join("folder", "nested", "..", "file.bru")).to.equal("folder/file.bru");
  expect(path.normalize("folder/../file.bru")).to.equal("file.bru");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("require shim request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("require shim test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsPathSubmodules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"identity": r.Header.Get("X-Path-Identity"),
			"checks":   r.Header.Get("X-Path-Checks"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]

	safeTests := `test("safe mode hides path submodules", function () {
  expect(bru.isSafeMode()).to.equal(true);
  expect(function () { require("path/posix"); }).to.throw("Cannot find module");
  expect(function () { require("node:path/posix"); }).to.throw("Cannot find module");
  expect(function () { require("path/win32"); }).to.throw("Cannot find module");
  expect(function () { require("node:path/win32"); }).to.throw("Cannot find module");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &safeTests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("safe path submodule request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("safe path submodule test did not pass: %#v", item.Response.TestResults)
	}

	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok {
		t.Fatal("request not found after enabling developer mode")
	}
	preRequest := `const path = require("path");
const nodePath = require("node:path");
const posix = require("path/posix");
const nodePosix = require("node:path/posix");
const win32 = require("path/win32");
const nodeWin32 = require("node:path/win32");
if (path !== nodePath || path.posix !== posix || nodePosix !== posix || path.win32 !== win32 || nodeWin32 !== win32) {
  throw new Error("path submodule aliases do not match root path properties");
}
const identity = [
  path === nodePath,
  path.posix === posix,
  path.win32 === win32,
  posix.posix === posix,
  posix.win32 === win32,
  win32.posix === posix,
  win32.win32 === win32
].join(":");
const checks = [
  posix.sep === "/",
  posix.delimiter === ":",
  posix.join("folder", "nested", "..", "file.bru") === "folder/file.bru",
  posix.normalize("/tmp/api/../api/") === "/tmp/api/",
  posix.basename("/tmp/example.json", ".json") === "example",
  posix.extname(".profile") === "",
  posix.relative("/tmp/api", "/tmp/api/v1/users") === "v1/users",
  posix.format(posix.parse("/tmp/api/file.bru")) === "/tmp/api/file.bru",
  win32.sep === "\\",
  win32.delimiter === ";",
  win32.join("folder", "nested", "..", "file.bru") === "folder\\file.bru",
  win32.normalize("C:/tmp/../file.bru") === "C:\\file.bru",
  win32.basename("C:\\tmp\\example.json", ".json") === "example",
  win32.extname("C:\\tmp\\.profile") === "",
  win32.isAbsolute("C:\\tmp") === true,
  win32.relative("C:\\tmp\\api", "C:\\tmp\\api\\v1\\users") === "v1\\users",
  win32.format(win32.parse("C:\\tmp\\api\\file.bru")) === "C:\\tmp\\api\\file.bru"
].join(":");
req.setHeader("X-Path-Identity", identity);
req.setHeader("X-Path-Checks", checks);`
	developerTests := `test("developer path submodules visible", function () {
  expect(bru.isSafeMode()).to.equal(false);
  const body = res.getBody();
  expect(body.identity).to.equal("true:true:true:true:true:true:true");
  expect(body.checks).to.equal("true:true:true:true:true:true:true:true:true:true:true:true:true:true:true:true:true");
  const win32 = require("node:path/win32");
  expect(require("path/win32")).to.equal(win32);
  const posix = require("node:path/posix");
  expect(require("path/posix")).to.equal(posix);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preRequest, Tests: &developerTests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer path submodule request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer path submodule test did not pass: %#v", item.Response.TestResults)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(item.Response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["identity"] != "true:true:true:true:true:true:true" {
		t.Fatalf("unexpected path identity header: %q", payload["identity"])
	}
	if payload["checks"] != "true:true:true:true:true:true:true:true:true:true:true:true:true:true:true:true:true" {
		t.Fatalf("unexpected path checks header: %q", payload["checks"])
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsStreamPromisesBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"identity": r.Header.Get("X-Stream-Promises-Identity"),
			"pipeline": r.Header.Get("X-Stream-Promises-Pipeline"),
			"finished": r.Header.Get("X-Stream-Promises-Finished"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]

	safeTests := `test("safe mode hides stream promises builtin", function () {
  expect(bru.isSafeMode()).to.equal(true);
  expect(function () { require("stream/promises"); }).to.throw("Cannot find module");
  expect(function () { require("node:stream/promises"); }).to.throw("Cannot find module");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &safeTests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("safe stream promises request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("safe stream promises test did not pass: %#v", item.Response.TestResults)
	}

	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok {
		t.Fatal("request not found after enabling developer mode")
	}
	preRequest := `const stream = require("stream");
const nodeStream = require("node:stream");
const promises = require("stream/promises");
const nodePromises = require("node:stream/promises");
if (stream !== nodeStream || stream.promises !== promises || nodePromises !== promises) {
  throw new Error("stream promises aliases do not match stream.promises");
}
const identity = [
  stream === nodeStream,
  stream.promises === promises,
  nodePromises === promises,
  promises.default === promises,
  typeof promises.pipeline === "function",
  typeof promises.finished === "function"
].join(":");
const chunks = [];
const readable = stream.Readable.from(["a", "b"]);
const upper = new stream.Transform({
  transform(chunk, enc, cb) { cb(null, String(chunk).toUpperCase()); }
});
const writable = new stream.Writable({
  write(chunk, enc, cb) { chunks.push(String(chunk)); cb(); }
});
const pipelineResult = await promises.pipeline(readable, upper, writable);
const finishing = new stream.Writable({
  write(chunk, enc, cb) { cb(); }
});
const finishedPromise = promises.finished(finishing).then(function () { return "done"; });
finishing.end("x");
const finishedResult = await finishedPromise;
const failing = new stream.Writable({
  write(chunk, enc, cb) { cb(); }
});
const failurePromise = promises.finished(failing).then(function () { return "resolved"; }, function (err) { return err.message; });
failing.emit("error", new Error("boom"));
const failureResult = await failurePromise;
req.setHeader("X-Stream-Promises-Identity", identity);
req.setHeader("X-Stream-Promises-Pipeline", chunks.join("") + ":" + String(pipelineResult === undefined));
req.setHeader("X-Stream-Promises-Finished", finishedResult + ":" + failureResult);`
	developerTests := `test("developer stream promises visible", function () {
  expect(bru.isSafeMode()).to.equal(false);
  const body = res.getBody();
  expect(body.identity).to.equal("true:true:true:true:true:true");
  expect(body.pipeline).to.equal("AB:true");
  expect(body.finished).to.equal("done:boom");
  const promises = require("node:stream/promises");
  expect(require("stream/promises")).to.equal(promises);
  expect(require("stream").promises).to.equal(promises);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preRequest, Tests: &developerTests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer stream promises request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer stream promises test did not pass: %#v", item.Response.TestResults)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(item.Response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["identity"] != "true:true:true:true:true:true" {
		t.Fatalf("unexpected stream promises identity header: %q", payload["identity"])
	}
	if payload["pipeline"] != "AB:true" {
		t.Fatalf("unexpected stream promises pipeline header: %q", payload["pipeline"])
	}
	if payload["finished"] != "done:boom" {
		t.Fatalf("unexpected stream promises finished header: %q", payload["finished"])
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsFSBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	outsideDir := filepath.Join(filepath.Dir(collection.Path), "developer-fs")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "input.txt"), []byte("outside input\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]

	tests := `test("developer fs builtin can access trusted filesystem", async function () {
  expect(bru.isSafeMode()).to.equal(false);
  const fs = require("fs");
  expect(require("node:fs")).to.equal(fs);
  expect(typeof globalThis.fs).to.equal("undefined");
  expect(fs.readFileSync("../developer-fs/input.txt", "utf8")).to.equal("outside input\n");
  const inputBuffer = fs.readFileSync("../developer-fs/input.txt");
  expect(Buffer.isBuffer(inputBuffer)).to.equal(true);
  expect(inputBuffer.toString()).to.equal("outside input\n");
  expect(fs.existsSync("../developer-fs/input.txt")).to.equal(true);
  expect(fs.statSync("../developer-fs/input.txt").isFile()).to.equal(true);
  expect(fs.readdirSync("../developer-fs")).to.include("input.txt");
  fs.writeFileSync("../developer-fs/sync.txt", "sync write", "utf8");
  expect(fs.readFileSync("../developer-fs/sync.txt", "utf8")).to.equal("sync write");
  fs.mkdirSync("../developer-fs/nested", { recursive: true });
  await fs.promises.writeFile("../developer-fs/nested/promise.txt", Buffer.from("promise write"));
  expect(await fs.promises.readFile("../developer-fs/nested/promise.txt", { encoding: "utf8" })).to.equal("promise write");
  fs.unlinkSync("../developer-fs/sync.txt");
  expect(fs.existsSync("../developer-fs/sync.txt")).to.equal(false);
  await fs.promises.rm("../developer-fs/nested", { recursive: true, force: true });
  expect(fs.existsSync("../developer-fs/nested")).to.equal(false);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer fs request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer fs test did not pass: %#v", item.Response.TestResults)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "sync.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected sync file to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "nested")); !os.IsNotExist(err) {
		t.Fatalf("expected nested directory to be removed, stat err=%v", err)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsFSPromisesBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"identity":%q,"methods":%q,"read":%q}`, r.Header.Get("X-FS-Promises-Identity"), r.Header.Get("X-FS-Promises-Methods"), r.Header.Get("X-FS-Promises-Read"))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	outsideDir := filepath.Join(filepath.Dir(collection.Path), "developer-fs-promises")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	safeTests := `test("safe mode hides fs promises builtin", function () {
  let blocked = false;
  try {
    require("fs/promises");
  } catch (err) {
    blocked = /Cannot find module/.test(String(err && err.message || err));
  }
  expect(blocked).to.equal(true);
  blocked = false;
  try {
    require("node:fs/promises");
  } catch (err) {
    blocked = /Cannot find module/.test(String(err && err.message || err));
  }
  expect(blocked).to.equal(true);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &safeTests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("safe fs/promises request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("safe fs/promises test did not pass: %#v", item.Response.TestResults)
	}

	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok {
		t.Fatal("request missing after enabling developer mode")
	}

	preRequest := `const fs = require("fs");
const fsPromises = require("fs/promises");
const nodeFsPromises = require("node:fs/promises");
if (fsPromises !== fs.promises || nodeFsPromises !== fsPromises) {
  throw new Error("fs/promises aliases do not match fs.promises");
}
await fsPromises.mkdir("../developer-fs-promises/nested", { recursive: true });
await fsPromises.writeFile("../developer-fs-promises/nested/out.txt", "promise alias", "utf8");
const readValue = await nodeFsPromises.readFile("../developer-fs-promises/nested/out.txt", "utf8");
req.setHeader("X-FS-Promises-Identity", String(fsPromises === fs.promises && nodeFsPromises === fsPromises));
req.setHeader("X-FS-Promises-Methods", [typeof fsPromises.readFile, typeof fsPromises.writeFile, typeof fsPromises.mkdir, typeof fsPromises.rm].join(":"));
req.setHeader("X-FS-Promises-Read", readValue);
await fsPromises.rm("../developer-fs-promises/nested", { recursive: true, force: true });`
	tests := `test("developer fs promises builtin visible", function () {
  expect(bru.isSafeMode()).to.equal(false);
  const fs = require("node:fs");
  const fsPromises = require("node:fs/promises");
  expect(require("fs/promises")).to.equal(fsPromises);
  expect(fs.promises).to.equal(fsPromises);
  expect(res.json.identity).to.equal("true");
  expect(res.json.methods).to.equal("function:function:function:function");
  expect(res.json.read).to.equal("promise alias");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{PreScript: &preRequest, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer fs/promises request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer fs/promises test did not pass: %#v", item.Response.TestResults)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "nested")); !os.IsNotExist(err) {
		t.Fatalf("expected nested fs/promises directory to be removed, stat err=%v", err)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsUtilTypesBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"identity":%q,"checks":%q}`, r.Header.Get("X-Util-Types-Identity"), r.Header.Get("X-Util-Types-Checks"))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]

	safeTests := `test("safe mode hides util types submodule", function () {
  expect(bru.isSafeMode()).to.equal(true);
  let blocked = false;
  try {
    require("util/types");
  } catch (err) {
    blocked = /Cannot find module/.test(String(err && err.message || err));
  }
  expect(blocked).to.equal(true);
  blocked = false;
  try {
    require("node:util/types");
  } catch (err) {
    blocked = /Cannot find module/.test(String(err && err.message || err));
  }
  expect(blocked).to.equal(true);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &safeTests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("safe util/types request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("safe util/types test did not pass: %#v", item.Response.TestResults)
	}

	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok {
		t.Fatal("request missing after enabling developer mode")
	}

	preRequest := `const util = require("util");
const utilTypes = require("util/types");
const nodeUtilTypes = require("node:util/types");
if (utilTypes !== util.types || nodeUtilTypes !== utilTypes) {
  throw new Error("util/types aliases do not match util.types");
}
const checks = [
  utilTypes.isUint8Array(Buffer.from("x")),
  utilTypes.isArrayBuffer(new ArrayBuffer(2)),
  utilTypes.isTypedArray(new Uint16Array(2)),
  utilTypes.isDate(new Date(0)),
  utilTypes.isRegExp(/ok/),
  utilTypes.isNativeError(new Error("boom")),
  utilTypes.isPromise(Promise.resolve("ok"))
].join(":");
req.setHeader("X-Util-Types-Identity", String(utilTypes === util.types && nodeUtilTypes === utilTypes));
req.setHeader("X-Util-Types-Checks", checks);`
	tests := `test("developer util types submodule visible", function () {
  expect(bru.isSafeMode()).to.equal(false);
  const util = require("node:util");
  const utilTypes = require("node:util/types");
  expect(require("util/types")).to.equal(utilTypes);
  expect(util.types).to.equal(utilTypes);
  expect(res.json.identity).to.equal("true");
  expect(res.json.checks).to.equal("true:true:true:true:true:true:true");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{PreScript: &preRequest, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer util/types request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer util/types test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsHTTPBuiltins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Node-Reply", "reply-ok")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
			"query":  r.URL.RawQuery,
			"body":   string(body),
			"header": r.Header.Get("X-Node-HTTP"),
			"extra":  r.Header.Get("X-Extra"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	mainURL := server.URL + "/main"
	tests := fmt.Sprintf(`test("developer http builtin performs client requests", async function () {
  expect(bru.isSafeMode()).to.equal(false);
  const http = require("http");
  expect(require("node:http")).to.equal(http);
  const https = require("https");
  expect(require("node:https")).to.equal(https);
  expect(typeof http.request).to.equal("function");
  expect(typeof http.get).to.equal("function");
  expect(typeof https.request).to.equal("function");
  expect(http.STATUS_CODES[200]).to.equal("OK");

  const requestResult = await new Promise(function (resolve, reject) {
    const nodeReq = http.request(%[1]s + "/node?via=request", {
      method: "POST",
      headers: { "X-Node-HTTP": "yes" }
    }, function (nodeRes) {
      let body = "";
      nodeRes.setEncoding("utf8");
      nodeRes.on("data", function (chunk) {
        body += chunk;
      });
      nodeRes.on("end", function () {
        resolve({
          statusCode: nodeRes.statusCode,
          statusMessage: nodeRes.statusMessage,
          headers: nodeRes.headers,
          rawHeaders: nodeRes.rawHeaders,
          complete: nodeRes.complete,
          body
        });
      });
    });
    nodeReq.on("error", reject);
    expect(nodeReq.getHeader("x-node-http")).to.equal("yes");
    nodeReq.setHeader("X-Extra", "late");
    expect(nodeReq.hasHeader("x-extra")).to.equal(true);
    nodeReq.write(Buffer.from("payload"));
    nodeReq.end("-tail");
  });
  const requestJSON = JSON.parse(requestResult.body);
  expect(requestResult.statusCode).to.equal(200);
  expect(requestResult.statusMessage).to.equal("OK");
  expect(requestResult.headers["x-node-reply"]).to.equal("reply-ok");
  expect(requestResult.rawHeaders).to.include("x-node-reply");
  expect(requestResult.complete).to.equal(true);
  expect(requestJSON.method).to.equal("POST");
  expect(requestJSON.path).to.equal("/node");
  expect(requestJSON.query).to.equal("via=request");
  expect(requestJSON.body).to.equal("payload-tail");
  expect(requestJSON.header).to.equal("yes");
  expect(requestJSON.extra).to.equal("late");

  const getResult = await new Promise(function (resolve, reject) {
    const nodeReq = http.get(new URL(%[1]s + "/get?via=get"), function (nodeRes) {
      let body = "";
      nodeRes.on("data", function (chunk) {
        body += Buffer.isBuffer(chunk) ? chunk.toString("utf8") : chunk;
      });
      nodeRes.on("end", function () {
        resolve({ statusCode: nodeRes.statusCode, body });
      });
    });
    nodeReq.on("error", reject);
  });
  const getJSON = JSON.parse(getResult.body);
  expect(getResult.statusCode).to.equal(200);
  expect(getJSON.method).to.equal("GET");
  expect(getJSON.path).to.equal("/get");
  expect(getJSON.query).to.equal("via=get");
});`, importers.JSStringLiteral(server.URL))
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &mainURL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer http builtin request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer http builtin test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsDNSBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"lookup":  r.Header.Get("X-DNS-Lookup"),
			"resolve": r.Header.Get("X-DNS-Resolve"),
			"all":     r.Header.Get("X-DNS-All"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	preRequest := `const dns = require("dns");
const lookup4 = await dns.promises.lookup("localhost", { family: 4 });
const resolved4 = await require("dns/promises").resolve4("localhost");
const all = await dns.promises.lookup("localhost", { all: true });
req.setHeader("X-DNS-Lookup", lookup4.address + ":" + lookup4.family);
req.setHeader("X-DNS-Resolve", resolved4.join(","));
req.setHeader("X-DNS-All", String(all.length));`
	tests := `test("developer dns builtin resolves localhost", async function () {
  expect(bru.isSafeMode()).to.equal(false);
  const dns = require("dns");
  expect(require("node:dns")).to.equal(dns);
  expect(require("dns/promises")).to.equal(dns.promises);
  expect(require("node:dns/promises")).to.equal(dns.promises);
  expect(typeof dns.lookup).to.equal("function");
  expect(typeof dns.resolve4).to.equal("function");
  expect(dns.ADDRCONFIG).to.equal(32);
  expect(res.json.lookup).to.include(":4");
  expect(Number(res.json.all)).to.be.above(0);

  const lookup4 = await dns.promises.lookup("localhost", { family: 4 });
  expect(lookup4.family).to.equal(4);
  expect(lookup4.address).to.match(/^\d+\.\d+\.\d+\.\d+$/);

  const callbackLookup = await new Promise(function (resolve, reject) {
    dns.lookup("localhost", { family: 4 }, function (err, address, family) {
      if (err) reject(err);
      else resolve({ address, family });
    });
  });
  expect(callbackLookup.family).to.equal(4);
  expect(callbackLookup.address).to.equal(lookup4.address);

  const all = await dns.promises.lookup("localhost", { all: true });
  expect(all.length).to.be.above(0);
  expect(all[0]).to.have.property("address");
  expect(all[0]).to.have.property("family");

  const callbackAll = await new Promise(function (resolve, reject) {
    dns.lookup("localhost", { all: true }, function (err, records) {
      if (err) reject(err);
      else resolve(records);
    });
  });
  expect(callbackAll.length).to.equal(all.length);

  const resolved4 = await dns.promises.resolve4("localhost");
  expect(resolved4).to.include(lookup4.address);
  const callbackResolved = await new Promise(function (resolve, reject) {
    dns.resolve("localhost", "A", function (err, records) {
      if (err) reject(err);
      else resolve(records);
    });
  });
  expect(callbackResolved).to.include(lookup4.address);

  dns.setDefaultResultOrder("ipv4first");
  expect(dns.getDefaultResultOrder()).to.equal("ipv4first");
  const resolver = new dns.Resolver();
  resolver.setServers(["8.8.8.8"]);
  expect(resolver.getServers()[0]).to.equal("8.8.8.8");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preRequest, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer dns builtin request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer dns builtin test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsAssertBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"module":  r.Header.Get("X-Assert-Module"),
			"strict":  r.Header.Get("X-Assert-Strict"),
			"failure": r.Header.Get("X-Assert-Failure"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	preScript := `const assert = require("assert");
const strict = require("assert/strict");
assert.strictEqual(require("node:assert"), assert);
assert.strictEqual(require("node:assert/strict"), strict);
assert.ok(true);
assert.equal(1, "1");
assert.notEqual(1, 2);
strict.equal(2, 2);
strict.deepEqual({ b: [2], a: 1 }, { a: 1, b: [2] });
assert.throws(function () { throw new TypeError("boom"); }, TypeError);
assert.doesNotThrow(function () { strict.notEqual("a", "b"); });
assert.match("liteapi", /^lite/);
await assert.rejects(Promise.reject(new Error("reject-ok")), /reject-ok/);
await assert.doesNotReject(Promise.resolve("ok"));
try {
  assert.fail("expected failure");
} catch (err) {
  assert.strictEqual(err.name, "AssertionError");
  assert.strictEqual(err.code, "ERR_ASSERTION");
  req.setHeader("X-Assert-Failure", err.code + ":" + err.name);
}
req.setHeader("X-Assert-Module", typeof assert.strictEqual);
req.setHeader("X-Assert-Strict", typeof strict.deepStrictEqual);`
	tests := `test("developer assert builtin works", async function () {
  const assert = require("assert");
  const strict = require("assert/strict");
  assert.strictEqual(require("node:assert"), assert);
  assert.strictEqual(require("node:assert/strict"), strict);
  assert.strictEqual(res.json.module, "function");
  assert.strictEqual(res.json.strict, "function");
  assert.strictEqual(res.json.failure, "ERR_ASSERTION:AssertionError");
  assert.deepStrictEqual({ one: [1, 2] }, { one: [1, 2] });
  assert.notDeepStrictEqual({ one: [1] }, { one: [2] });
  await assert.rejects(Promise.reject(new TypeError("typed")), TypeError);
  await assert.doesNotReject(Promise.resolve(true));
  assert.match("assert builtin", /builtin$/);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preScript, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer assert builtin request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer assert builtin test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsTimersBuiltins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"callback":  r.Header.Get("X-Timers-Callback"),
			"handle":    r.Header.Get("X-Timers-Handle"),
			"interval":  r.Header.Get("X-Timers-Interval"),
			"promises":  r.Header.Get("X-Timers-Promises"),
			"scheduler": r.Header.Get("X-Timers-Scheduler"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	preScript := `const assert = require("assert");
const timers = require("timers");
const timersPromises = require("timers/promises");
assert.strictEqual(require("node:timers"), timers);
assert.strictEqual(require("node:timers/promises"), timersPromises);
assert.strictEqual(timers.promises, timersPromises);
assert.strictEqual(typeof timers.setTimeout, "function");
assert.strictEqual(typeof timersPromises.scheduler.wait, "function");

const callbackParts = [];
let timeoutHandleType = "";
await new Promise(function (resolve) {
  const handle = timers.setTimeout(function (left, right) {
    callbackParts.push(left + right);
    resolve();
  }, 1, "call", "back");
  timeoutHandleType = typeof handle.hasRef() + ":" + typeof handle.ref + ":" + typeof handle.unref;
});
await new Promise(function (resolve) {
  timers.setImmediate(function (label) {
    callbackParts.push(label);
    resolve();
  }, "immediate");
});
let intervalTicks = 0;
await new Promise(function (resolve) {
  const interval = timers.setInterval(function () {
    intervalTicks++;
    if (intervalTicks === 2) {
      timers.clearInterval(interval);
      resolve();
    }
  }, 1);
});
const timeoutValue = await timersPromises.setTimeout(1, "timeout", { ref: false });
const immediateValue = await timersPromises.setImmediate("promise-immediate", { ref: false });
const iterator = timersPromises.setInterval(1, "promise-interval", { ref: false });
const firstTick = await iterator.next();
const secondTick = await iterator.next();
await iterator.return();
await timersPromises.scheduler.wait(1, { ref: false });
await timersPromises.scheduler.yield();
req.setHeader("X-Timers-Callback", callbackParts.join(","));
req.setHeader("X-Timers-Handle", timeoutHandleType);
req.setHeader("X-Timers-Interval", String(intervalTicks));
req.setHeader("X-Timers-Promises", [timeoutValue, immediateValue, firstTick.value, secondTick.value].join("|"));
req.setHeader("X-Timers-Scheduler", "wait-yield");`
	tests := `test("developer timers builtins work", async function () {
  const assert = require("assert");
  const timers = require("node:timers");
  const timersPromises = require("node:timers/promises");
  assert.strictEqual(require("timers"), timers);
  assert.strictEqual(require("timers/promises"), timersPromises);
  assert.strictEqual(timers.promises, timersPromises);
  assert.strictEqual(res.json.callback, "callback,immediate");
  assert.strictEqual(res.json.handle, "boolean:function:function");
  assert.strictEqual(res.json.interval, "2");
  assert.strictEqual(res.json.promises, "timeout|promise-immediate|promise-interval|promise-interval");
  assert.strictEqual(res.json.scheduler, "wait-yield");
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(timersPromises.setTimeout(1, "late", { signal: controller.signal }), /aborted/i);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preScript, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer timers builtin request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer timers builtin test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsConsoleBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"module": r.Header.Get("X-Console-Module"),
			"stream": r.Header.Get("X-Console-Stream"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	preScript := `const assert = require("assert");
const consoleModule = require("console");
assert.strictEqual(require("node:console"), consoleModule);
assert.strictEqual(typeof consoleModule.log, "function");
assert.strictEqual(typeof consoleModule.Console, "function");
consoleModule.log("module pre", { ok: true });
consoleModule.warn("module warn", 7);
const stream = {
  value: "",
  write(chunk) {
    this.value += chunk;
  }
};
const customConsole = new consoleModule.Console(stream);
customConsole.log("stream", { ok: true });
req.setHeader("X-Console-Module", typeof consoleModule.log + ":" + typeof consoleModule.Console);
req.setHeader("X-Console-Stream", stream.value.trim());`
	tests := `test("developer console builtin works", function () {
  const assert = require("assert");
  const consoleModule = require("node:console");
  assert.strictEqual(require("console"), consoleModule);
  assert.strictEqual(res.json.module, "function:function");
  assert.strictEqual(res.json.stream, "stream {\"ok\":true}");
  consoleModule.error("module test", ["done"]);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preScript, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer console builtin request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer console builtin test did not pass: %#v", item.Response.TestResults)
	}
	expectedLogs := []ScriptLog{
		{Level: "log", Message: `module pre {"ok":true}`, Args: []string{"module pre", `{"ok":true}`}},
		{Level: "warn", Message: "module warn 7", Args: []string{"module warn", "7"}},
		{Level: "error", Message: `module test ["done"]`, Args: []string{"module test", `["done"]`}},
	}
	if len(item.Response.ScriptLogs) != len(expectedLogs) {
		t.Fatalf("expected console module logs, got %#v", item.Response.ScriptLogs)
	}
	for index, want := range expectedLogs {
		got := item.Response.ScriptLogs[index]
		if got.Level != want.Level || got.Message != want.Message || !reflect.DeepEqual(got.Args, want.Args) {
			t.Fatalf("console module log %d mismatch: got %#v want %#v", index, got, want)
		}
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsPackageRequire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	mustWrite := func(path string, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(collection.Path, "local-pkg", "package.json"), `{"name":"local-pkg","main":"src/main.js"}`)
	mustWrite(filepath.Join(collection.Path, "local-pkg", "data.json"), `{"value":7}`)
	mustWrite(filepath.Join(collection.Path, "local-pkg", "src", "main.js"), `const data = require("../data.json");
module.exports = { name: "local-pkg", value: data.value };`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "test-module", "package.json"), `{"name":"test-module","main":"lib/main.js"}`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "test-module", "config.json"), `{"answer":42}`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "test-module", "lib", "helper.js"), `exports.value = "helper";`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "test-module", "lib", "main.js"), `const helper = require("./helper");
const config = require("../config.json");
const dep = require("dep");
module.exports = {
  name: "test-module",
  helper: helper.value,
  answer: config.answer,
  dep: dep.name,
  collection: bru.getCollectionName()
};`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "test-module", "node_modules", "dep", "index.js"), `module.exports = { name: "nested-dep" };`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "@scope", "scoped", "package.json"), `{"name":"@scope/scoped","main":"index.js"}`)
	mustWrite(filepath.Join(collection.Path, "node_modules", "@scope", "scoped", "index.js"), `module.exports = { name: "scoped" };`)

	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	tests := `test("developer package require", function () {
  expect(bru.isSafeMode()).to.equal(false);
  const localPkg = require("./local-pkg");
  expect(localPkg.name).to.equal("local-pkg");
  expect(localPkg.value).to.equal(7);
  expect(require("./local-pkg/data.json").value).to.equal(7);
  const modulePkg = require("test-module");
  expect(modulePkg.name).to.equal("test-module");
  expect(modulePkg.helper).to.equal("helper");
  expect(modulePkg.answer).to.equal(42);
  expect(modulePkg.dep).to.equal("nested-dep");
  expect(modulePkg.collection).to.equal("Sample API");
  expect(require("test-module/package.json").main).to.equal("lib/main.js");
  expect(require("test-module/config").answer).to.equal(42);
  expect(require("@scope/scoped").name).to.equal("scoped");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer package request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer package test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsLocalCommonJSRequire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if err := os.MkdirAll(filepath.Join(collection.Path, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(collection.Path, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "helpers.js"), []byte(`const child = require("./nested/child");
const pkg = require("./pkg");
const path = require("node:path");
module.exports = {
  answer: 42,
  child: child.value,
  cachedChild: child === require("./nested/child.js"),
  pkg: pkg.name,
  file: path.basename(__filename),
  dir: path.basename(__dirname),
};`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "nested", "child.js"), []byte(`exports.value = "child";`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "pkg", "index.js"), []byte(`module.exports.name = "pkg-index";`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "circularA.js"), []byte(`exports.name = "A";
const B = require("./circularB");
exports.fromB = B.name;
exports.bSawA = B.fromA;`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "circularB.js"), []byte(`exports.name = "B";
const A = require("./circularA");
exports.fromA = A.name;`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "reassignA.js"), []byte(`module.exports = { name: "A" };
const B = require("./reassignB");
module.exports.fromB = B.name;
module.exports.seenByB = B.fromA;`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "reassignB.js"), []byte(`exports.name = "B";
const A = require("./reassignA");
exports.fromA = A.name;
exports.aHadFromB = A.fromB || null;`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(collection.Path), "outside.js"), []byte(`module.exports = "outside";`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := `test("local CommonJS require", function () {
  const helper = require("./helpers");
  expect(helper.answer).to.equal(42);
  expect(helper.child).to.equal("child");
  expect(helper.cachedChild).to.equal(true);
  expect(helper.pkg).to.equal("pkg-index");
  expect(helper.file).to.equal("helpers.js");
  expect(helper.dir).to.equal("Sample API");
  expect(require("./helpers.js")).to.equal(helper);
  expect(require(bru.cwd() + "/helpers.js")).to.equal(helper);
  const circular = require("./circularA");
  expect(circular.name).to.equal("A");
  expect(circular.fromB).to.equal("B");
  expect(circular.bSawA).to.equal("A");
  const reassigned = require("./reassignA");
  expect(reassigned.name).to.equal("A");
  expect(reassigned.fromB).to.equal("B");
  expect(reassigned.seenByB).to.equal("A");
  expect(require("./reassignA")).to.equal(reassigned);
  expect(function () { require("../outside"); }).to.throw("Cannot find module");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("local require request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("local require test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeExposesBrunoResponseHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/helpers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"count":2}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL + "/helpers"
	tests := `test("bruno response helpers", function () {
  expect(res.statusCode).to.equal(201);
  expect(res.getStatus()).to.equal(201);
  expect(res.getStatusText()).to.include("Created");
  expect(res.getHeader("X-Test")).to.equal("ok");
  expect(res.getHeaders()).to.have.property("x-test", "ok");
  expect(res.getBody()).to.deep.equal({ ok: true, count: 2 });
  expect(res.data).to.deep.equal({ ok: true, count: 2 });
  expect(res.responseTime).to.be.a("number");
  expect(res.getResponseTime()).to.be.a("number");
  expect(res.url).to.include("/helpers");
  expect(res.getUrl()).to.include("/helpers");
  expect(res.getSize().body).to.be.above(0);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusCreated {
		t.Fatalf("response helper request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("response helper test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeExposesResponseDataBuffer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("€A"))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("response dataBuffer mirrors received bytes", function () {
  expect(res.dataBuffer.length).to.equal(4);
  expect(res.dataBuffer.byteLength).to.equal(4);
  expect(Array.from(res.dataBuffer)).to.deep.equal([226, 130, 172, 65]);
  expect(Array.from(res.getDataBuffer())).to.deep.equal([226, 130, 172, 65]);
  expect(res.getSize().body).to.equal(4);
});
test("setBody refreshes response dataBuffer", function () {
  res.setBody("hé");
  expect(res.getBody()).to.equal("hé");
  expect(Array.from(res.dataBuffer)).to.deep.equal([104, 195, 169]);
  expect(Array.from(res.getDataBuffer())).to.deep.equal([104, 195, 169]);
  expect(res.getSize().body).to.equal(3);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("response dataBuffer request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 2 || !item.Response.TestResults[0].Passed || !item.Response.TestResults[1].Passed {
		t.Fatalf("response dataBuffer tests did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsCallableResponseParser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"order":{"items":[{"id":1,"amount":10},{"id":2,"amount":20}]}}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("callable response parser", function () {
  expect(typeof res).to.equal("function");
  expect(res.status).to.equal(200);
  const brunoQueryValue = res("..items[?].amount[0]", function (item) {
    return item.amount > 10;
  });
  expect(brunoQueryValue).to.equal(20);
  expect(res("order.items[0].id")).to.equal(1);
  expect(res.jq("order.items[amount > 10].amount")).to.equal(20);
  expect(res.jq("order.items[id = 1].amount")).to.equal(10);
  expect(res.jq("order.items[id = 99].amount")).to.be.null;
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &server.URL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("callable res request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("callable res test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsSendRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte("ok"))
		case "/ping":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("pong"))
		case "/echo":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if r.Method != http.MethodPost || r.URL.Query().Get("q") != "Ada" || r.Header.Get("X-Test") != "yes" {
				t.Fatalf("unexpected sendRequest echo request: method=%s url=%s headers=%#v", r.Method, r.URL.String(), r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"body":%q,"header":%q}`, string(body), r.Header.Get("X-Test"))
		case "/missing":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("missing"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := fmt.Sprintf(`test("bru.sendRequest core", function () {
  bru.setVar("who", "Ada");
  const ping = bru.sendRequest(%s + "/ping");
  expect(ping.status).to.equal(200);
  expect(ping.statusCode).to.equal(200);
  expect(ping.statusText).to.include("OK");
  expect(ping.data).to.equal("pong");
  expect(ping.body).to.equal("pong");
  expect(ping.headers["content-type"]).to.include("text/plain");
  expect(ping.dataBuffer).to.be.a("string");

  const echo = bru.sendRequest({
    url: %s + "/echo",
    method: "post",
    params: { q: "{{who}}" },
    headers: { "X-Test": "yes" },
    data: "hello {{who}}"
  });
  expect(echo.data.body).to.equal("hello Ada");
  expect(echo.data.header).to.equal("yes");

  let callbackData = "";
  const callbackReturn = bru.sendRequest(%s + "/ping", function (err, response) {
    expect(err).to.be.null;
    callbackData = response.data;
  });
  expect(callbackReturn.data).to.equal("pong");
  expect(callbackData).to.equal("pong");

  let callbackStatus = 0;
  bru.sendRequest({ url: %s + "/missing" }, function (err, response) {
    callbackStatus = err.status;
    expect(err.response.status).to.equal(404);
    expect(response).to.be.null;
  });
  expect(callbackStatus).to.equal(404);
});`, importers.JSStringLiteral(server.URL), importers.JSStringLiteral(server.URL), importers.JSStringLiteral(server.URL), importers.JSStringLiteral(server.URL))
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &server.URL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("sendRequest host request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("sendRequest test did not pass: %#v", item.Response.TestResults)
	}
}

func TestTimelineCapturesPreRequestSendRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"main":true}`))
		case "/pre":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected pre-request method: %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"pre":true}`))
		default:
			t.Fatalf("unexpected timeline path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	mainURL := server.URL + "/main"
	method := http.MethodGet
	preScript := fmt.Sprintf(`const response = await bru.sendRequest({ url: %s + "/pre", method: "POST" });
bru.setVar("preStatus", response.status);`, importers.JSStringLiteral(server.URL))
	tests := `test("pre sendRequest ran", function () {
  expect(bru.getVar("preStatus")).to.equal(200);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &mainURL,
		Method:    &method,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("timeline request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("timeline test did not pass: %#v", item.Response.TestResults)
	}
	if len(item.Timeline) < 2 {
		t.Fatalf("expected scripted + main timeline rows, got %#v", item.Timeline)
	}
	scripted := item.Timeline[0]
	if scripted.Kind != "scripted-request" || scripted.Source != "sendRequest" || scripted.Phase != "pre-request" {
		t.Fatalf("unexpected scripted timeline metadata: %#v", scripted)
	}
	if scripted.Method != http.MethodPost || !strings.HasSuffix(scripted.URL, "/pre") || scripted.Status != http.StatusOK {
		t.Fatalf("unexpected scripted request summary: %#v", scripted)
	}
	if scripted.RequestID != item.ID || scripted.SourceFile == "" {
		t.Fatalf("scripted timeline should point back to the request: %#v", scripted)
	}
	main := item.Timeline[1]
	if main.Kind != "request" || main.Source != "main" || main.Method != http.MethodGet || !strings.HasSuffix(main.URL, "/main") || main.Status != http.StatusOK {
		t.Fatalf("unexpected main timeline row: %#v", main)
	}
}

func TestTimelineCapturesRunRequestAndBubbledSendRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/outer":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"outer":true}`))
		case "/inner":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"inner":true}`))
		case "/inner-pre":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"innerPre":true}`))
		default:
			t.Fatalf("unexpected runRequest timeline path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	outer := collection.Items[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Inner"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	outer = collection.Items[0]
	inner := collection.Items[1]
	outerURL := server.URL + "/outer"
	innerURL := server.URL + "/inner"
	innerPreScript := fmt.Sprintf(`await bru.sendRequest({ url: %s + "/inner-pre", method: "GET" });`, importers.JSStringLiteral(server.URL))
	outerPreScript := `const innerResponse = await bru.runRequest("Inner");
bru.setVar("innerStatus", innerResponse.status);`
	tests := `test("runRequest returned inner response", function () {
  expect(bru.getVar("innerStatus")).to.equal(200);
});`
	if _, err := app.UpdateRequest(collection.ID, inner.ID, RequestPatch{
		URL:       &innerURL,
		PreScript: &innerPreScript,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, outer.ID, RequestPatch{
		URL:       &outerURL,
		PreScript: &outerPreScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, outer.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, outer.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("runRequest timeline driver failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("runRequest timeline test did not pass: %#v", item.Response.TestResults)
	}
	if len(item.Timeline) < 3 {
		t.Fatalf("expected bubbled sendRequest + runRequest + main rows, got %#v", item.Timeline)
	}
	bubbled := item.Timeline[0]
	if bubbled.Kind != "scripted-request" || bubbled.Source != "sendRequest" || bubbled.Phase != "pre-request" {
		t.Fatalf("unexpected bubbled sendRequest metadata: %#v", bubbled)
	}
	if bubbled.RequestID != inner.ID || !strings.HasSuffix(bubbled.URL, "/inner-pre") || bubbled.Status != http.StatusOK {
		t.Fatalf("unexpected bubbled sendRequest row: %#v", bubbled)
	}
	runRequest := item.Timeline[1]
	if runRequest.Kind != "scripted-request" || runRequest.Source != "runRequest" || runRequest.Phase != "pre-request" {
		t.Fatalf("unexpected runRequest metadata: %#v", runRequest)
	}
	if runRequest.RequestID != inner.ID || !strings.HasSuffix(runRequest.URL, "/inner") || runRequest.Status != http.StatusOK || runRequest.SourceFile == "" {
		t.Fatalf("unexpected runRequest row: %#v", runRequest)
	}
	main := item.Timeline[2]
	if main.Kind != "request" || main.Source != "main" || main.RequestID != outer.ID || !strings.HasSuffix(main.URL, "/outer") {
		t.Fatalf("unexpected main row: %#v", main)
	}
}

func TestTimelineCapturesSkippedRunRequestTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/driver" {
			t.Fatalf("unexpected skipped runRequest path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"driver":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	driver := collection.Items[0]
	if _, err := app.CreateRequest(collection.ID, "websocket", "Socket"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	driver = collection.Items[0]
	socket := collection.Items[1]
	driverURL := server.URL + "/driver"
	socketURL := "ws://127.0.0.1:18080/socket"
	preScript := `const socketResponse = await bru.runRequest("Socket");
bru.setVar("socketStatus", socketResponse.status);`
	tests := `test("unsupported runRequest returns skipped response", function () {
  expect(bru.getVar("socketStatus")).to.equal("skipped");
});`
	if _, err := app.UpdateRequest(collection.ID, socket.ID, RequestPatch{URL: &socketURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, driver.ID, RequestPatch{
		URL:       &driverURL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, driver.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, driver.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("skipped runRequest driver failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("skipped runRequest test did not pass: %#v", item.Response.TestResults)
	}
	if len(item.Timeline) < 2 {
		t.Fatalf("expected skipped runRequest + main rows, got %#v", item.Timeline)
	}
	skipped := item.Timeline[0]
	if skipped.Kind != "scripted-request" || skipped.Source != "runRequest" || skipped.Phase != "pre-request" {
		t.Fatalf("unexpected skipped runRequest metadata: %#v", skipped)
	}
	if skipped.RequestID != socket.ID || skipped.URL != socketURL || skipped.StatusText != "Skipped" || !strings.Contains(skipped.Error, "WebSocket") {
		t.Fatalf("unexpected skipped runRequest row: %#v", skipped)
	}
}

func TestJavaScriptRuntimeSupportsAsyncAwaitHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			if r.Header.Get("X-Async-Pre") != "pre" {
				t.Fatalf("pre-request await did not mutate header: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"main":true}`))
		case "/pre":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mode":"pre"}`))
		case "/ping":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"path":"/ping"}`))
		case "/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("pong"))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("missing"))
		default:
			t.Fatalf("unexpected async helper path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := fmt.Sprintf(`const pre = await bru.sendRequest(%s + "/pre");
await bru.sleep(1);
req.setHeader("X-Async-Pre", pre.data.mode);`, importers.JSStringLiteral(server.URL))
	tests := fmt.Sprintf(`const topLevel = await bru.sendRequest(%s + "/ping");
let thenValue = "";
await bru.sendRequest(%s + "/text").then(function (response) {
  thenValue = response.data;
});

test("top-level await sendRequest", function () {
  expect(topLevel.data.ok).to.equal(true);
  expect(topLevel.data.path).to.equal("/ping");
  expect(thenValue).to.equal("pong");
});

test("async test callback awaits helpers", async function () {
  const before = Date.now();
  await bru.sleep(1);
  expect(Date.now()).to.be.at.least(before);
  const response = await bru.sendRequest(%s + "/ping");
  expect(response.status).to.equal(200);
  expect(response.data.ok).to.equal(true);
});

test("async sendRequest failures can be caught", async function () {
  try {
    await bru.sendRequest({ url: %s + "/missing" });
    throw new Error("expected request failure");
  } catch (err) {
    expect(err.status).to.equal(404);
    expect(err.response.status).to.equal(404);
  }
});`, importers.JSStringLiteral(server.URL), importers.JSStringLiteral(server.URL), importers.JSStringLiteral(server.URL), importers.JSStringLiteral(server.URL))
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("async helper request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 3 {
		t.Fatalf("unexpected async test count: %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("async helper test did not pass: %#v", item.Response.TestResults)
		}
	}
}

func TestJavaScriptRuntimeSupportsSafeSetTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Timer-Pre") != "yes" {
			t.Fatalf("timer pre-request header was not set: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"timer":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `bru.setVar("fireAndForgetTimer", "pending");
setTimeout(function () {
  bru.setVar("fireAndForgetTimer", "done");
}, 1);
await new Promise(function (resolve) {
  setTimeout(function () {
    req.setHeader("X-Timer-Pre", "yes");
    resolve();
  }, 1);
});`
	tests := `test("safe setTimeout matches Bruno wrapper", function () {
  expect(bru.getVar("fireAndForgetTimer")).to.equal("done");
  expect(bru.isSafeMode()).to.equal(true);
  expect(typeof setTimeout).to.equal("function");
  expect(typeof globalThis.setTimeout).to.equal("undefined");
  expect(typeof globalThis.__bruSetTimeout).to.equal("undefined");
  expect(typeof clearTimeout).to.equal("undefined");
  expect(typeof setInterval).to.equal("undefined");
  expect(typeof clearInterval).to.equal("undefined");
  expect(typeof setImmediate).to.equal("undefined");
  expect(typeof clearImmediate).to.equal("undefined");
  expect(typeof queueMicrotask).to.equal("undefined");
  const timeoutId = setTimeout(function () {}, 1);
  expect(timeoutId).to.not.be.undefined;
});

test("async tests can settle from setTimeout", async function () {
  let value = "";
  await new Promise(function (resolve) {
    setTimeout(function () {
      value = "late";
      resolve();
    }, 1);
  });
  expect(value).to.equal("late");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("safe setTimeout request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 2 {
		t.Fatalf("unexpected safe setTimeout test count: %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("safe setTimeout test did not pass: %#v", item.Response.TestResults)
		}
	}
}

func TestJavaScriptRuntimeSupportsDeveloperTimerGlobals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Timer-Pre") != "yes" {
			t.Fatalf("timer pre-request header was not set: %#v", r.Header)
		}
		if r.Header.Get("X-Timer-Immediate") != "yes" {
			t.Fatalf("setImmediate pre-request header was not set: %#v", r.Header)
		}
		if r.Header.Get("X-Timer-Microtask") != "yes" {
			t.Fatalf("queueMicrotask pre-request header was not set: %#v", r.Header)
		}
		if r.Header.Get("X-Timer-Canceled") != "" {
			t.Fatalf("canceled timer still ran: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"timer":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	preScript := `bru.setVar("fireAndForgetTimer", "pending");
setTimeout(function () {
  bru.setVar("fireAndForgetTimer", "done");
}, 1);
const canceled = setTimeout(function () {
  req.setHeader("X-Timer-Canceled", "bad");
}, 0);
clearTimeout(canceled);
setImmediate(function () {
  req.setHeader("X-Timer-Immediate", "yes");
});
queueMicrotask(function () {
  req.setHeader("X-Timer-Microtask", "yes");
});
await new Promise(function (resolve) {
  setTimeout(function () {
    req.setHeader("X-Timer-Pre", "yes");
    resolve();
  }, 1);
});`
	tests := `test("developer timer globals match Bruno Node VM", function () {
  expect(bru.isSafeMode()).to.equal(false);
  expect(bru.getVar("fireAndForgetTimer")).to.equal("done");
  expect(typeof setTimeout).to.equal("function");
  expect(typeof globalThis.setTimeout).to.equal("function");
  expect(typeof clearTimeout).to.equal("function");
  expect(typeof setInterval).to.equal("function");
  expect(typeof clearInterval).to.equal("function");
  expect(typeof setImmediate).to.equal("function");
  expect(typeof clearImmediate).to.equal("function");
  expect(typeof queueMicrotask).to.equal("function");
  const timeoutId = setTimeout(function () {}, 1000);
  expect(timeoutId).to.not.be.undefined;
  clearTimeout(timeoutId);
  const intervalId = setInterval(function () {}, 1000);
  expect(intervalId).to.not.be.undefined;
  clearInterval(intervalId);
  const immediateId = setImmediate(function () {});
  expect(immediateId).to.not.be.undefined;
  clearImmediate(immediateId);
});

test("developer async tests can settle from timer globals", async function () {
  let value = "";
  let microtask = "";
  queueMicrotask(function () {
    microtask = "queued";
  });
  await new Promise(function (resolve) {
    setTimeout(function () {
      value = "late";
      resolve();
    }, 1);
  });
  expect(value).to.equal("late");
  expect(microtask).to.equal("queued");
});`
	if collection.SecurityConfig.JSSandboxMode != "developer" {
		t.Fatalf("developer sandbox mode was not persisted: %#v", collection.SecurityConfig)
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer timer request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 2 {
		t.Fatalf("unexpected developer timer test count: %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("developer timer test did not pass: %#v", item.Response.TestResults)
		}
	}
}

func TestJavaScriptRuntimeSupportsRepeatingSetInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Interval-Ticks") != "3" {
			t.Fatalf("repeating interval did not finish before request: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"interval":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	preScript := `bru.setVar("intervalTicks", "0");
await new Promise(function (resolve) {
  let ticks = 0;
  const interval = setInterval(function () {
    ticks++;
    bru.setVar("intervalTicks", String(ticks));
    if (ticks === 3) {
      clearInterval(interval);
      resolve();
    }
  }, 1);
});
req.setHeader("X-Interval-Ticks", bru.getVar("intervalTicks"));`
	tests := `test("setInterval repeats until cleared", function () {
  expect(bru.getVar("intervalTicks")).to.equal("3");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("setInterval request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("setInterval test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsProcessShim(t *testing.T) {
	t.Setenv("LITEAPI_PROCESS_SHIM", "process-env-ok")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Process-Tick") != "tick-ok" {
			t.Fatalf("process.nextTick header was not set: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"process":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]
	preScript := `process.nextTick(function (prefix, suffix) {
  req.setHeader("X-Process-Tick", prefix + suffix);
}, "tick", "-ok");`
	tests := fmt.Sprintf(`test("process shim matches Bruno developer globals", async function () {
  expect(typeof process).to.equal("object");
  expect(typeof global.process).to.equal("object");
  expect(global.process).to.equal(process);
  expect(typeof process.version).to.equal("string");
  expect(process.version).to.match(/^v/);
  expect(typeof process.versions.node).to.equal("string");
  expect(typeof process.platform).to.equal("string");
  expect(typeof process.arch).to.equal("string");
  expect(process.env.LITEAPI_PROCESS_SHIM).to.equal("process-env-ok");
  expect(process.cwd()).to.equal(%s);
  let tick = "";
  process.nextTick(function (prefix, suffix) {
    tick = prefix + suffix;
  }, "next", "-tick");
  await setTimeout(function () {}, 0);
  expect(tick).to.equal("next-tick");
});`, importers.JSStringLiteral(collection.Path))
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("process shim request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("process shim test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeDeveloperModeSupportsProcessBuiltin(t *testing.T) {
	t.Setenv("LITEAPI_PROCESS_BUILTIN", "process-builtin-ok")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"module":   r.Header.Get("X-Process-Module"),
			"env":      r.Header.Get("X-Process-Env"),
			"cwd":      r.Header.Get("X-Process-Cwd"),
			"platform": r.Header.Get("X-Process-Platform"),
			"tick":     r.Header.Get("X-Process-Tick"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.UpdateCollectionSecurityConfig(collection.ID, CollectionSecurityConfig{JSSandboxMode: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[0]

	preScript := `const assert = require("assert");
const processModule = require("process");
assert.strictEqual(require("node:process"), processModule);
assert.strictEqual(processModule, process);
assert.strictEqual(global.process, processModule);
let tick = "";
processModule.nextTick(function (prefix, suffix) {
  tick = prefix + suffix;
}, "module", "-tick");
await setTimeout(function () {}, 0);
req.setHeader("X-Process-Module", typeof processModule.cwd + ":" + typeof processModule.nextTick);
req.setHeader("X-Process-Env", processModule.env.LITEAPI_PROCESS_BUILTIN);
req.setHeader("X-Process-Cwd", processModule.cwd());
req.setHeader("X-Process-Platform", processModule.platform + ":" + processModule.arch);
req.setHeader("X-Process-Tick", tick);`
	tests := fmt.Sprintf(`test("developer process builtin works", function () {
  const assert = require("assert");
  const processModule = require("node:process");
  assert.strictEqual(require("process"), processModule);
  assert.strictEqual(processModule, process);
  assert.strictEqual(global.process, processModule);
  assert.strictEqual(res.json.module, "function:function");
  assert.strictEqual(res.json.env, "process-builtin-ok");
  assert.strictEqual(res.json.cwd, %s);
  assert.match(res.json.platform, /^[a-z0-9]+:[A-Za-z0-9_]+$/);
  assert.strictEqual(res.json.tick, "module-tick");
});`, importers.JSStringLiteral(collection.Path))
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preScript, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("developer process builtin request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("developer process builtin test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSafeModeHidesProcessGlobal(t *testing.T) {
	t.Setenv("LITEAPI_SAFE_PROCESS_SHIM", "safe-process-env")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Safe-Process"); got != "undefined" {
			t.Fatalf("safe mode exposed process global: %q", got)
		}
		if got := r.Header.Get("X-Safe-Global-Process"); got != "undefined" {
			t.Fatalf("safe mode exposed global.process: %q", got)
		}
		if got := r.Header.Get("X-Safe-Env"); got != "safe-process-env" {
			t.Fatalf("safe mode bru.getProcessEnv mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"safeProcess":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `const globalProcessType = typeof global === "undefined" ? "undefined" : typeof global.process;
req.setHeader("X-Safe-Process", typeof process);
req.setHeader("X-Safe-Global-Process", globalProcessType);
req.setHeader("X-Safe-Env", bru.getProcessEnv("LITEAPI_SAFE_PROCESS_SHIM"));`
	tests := `test("safe mode hides node process global", function () {
  expect(bru.isSafeMode()).to.equal(true);
  expect(typeof process).to.equal("undefined");
  const globalProcessType = typeof global === "undefined" ? "undefined" : typeof global.process;
  expect(globalProcessType).to.equal("undefined");
  expect(bru.getProcessEnv("LITEAPI_SAFE_PROCESS_SHIM")).to.equal("safe-process-env");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("safe process request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("safe process test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsTextEncodingGlobals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Text-Ascii"); got != "ascii-ok" {
			t.Fatalf("TextDecoder ASCII header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Text-Bytes"); got != "104,195,169,226,156,147" {
			t.Fatalf("TextEncoder bytes header mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"encoding":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `const encoder = new TextEncoder();
const bytes = encoder.encode("hé✓");
req.setHeader("X-Text-Bytes", Array.from(bytes).join(","));
req.setHeader("X-Text-Ascii", new TextDecoder().decode(encoder.encode("ascii-ok")));`
	tests := `test("TextEncoder and TextDecoder globals", function () {
  const encoder = new TextEncoder();
  expect(encoder.encoding).to.equal("utf-8");
  const bytes = encoder.encode("hé✓");
  expect(bytes instanceof Uint8Array).to.equal(true);
  expect(Array.from(bytes)).to.eql([104, 195, 169, 226, 156, 147]);
  expect(new TextDecoder("utf-8").decode(bytes)).to.equal("hé✓");
  expect(new TextDecoder().decode(bytes.buffer)).to.equal("hé✓");
  expect(new TextDecoder().decode(bytes.subarray(1))).to.equal("é✓");
  const destination = new Uint8Array(4);
  const result = encoder.encodeInto("a✓b", destination);
  expect(result.read).to.equal(2);
  expect(result.written).to.equal(4);
  expect(Array.from(destination)).to.eql([97, 226, 156, 147]);
  expect(new TextDecoder("utf8").encoding).to.equal("utf-8");
  expect(function () { new TextDecoder("latin1"); }).to.throw("Unsupported TextDecoder encoding");
  expect(function () { new TextDecoder("utf-8", { fatal: true }).decode(new Uint8Array([0xff])); }).to.throw("not valid utf-8");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("TextEncoder/TextDecoder request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("TextEncoder/TextDecoder test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsFetchAPIGlobals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fetch":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if r.Method != http.MethodPost {
				t.Fatalf("fetch method mismatch: %s", r.Method)
			}
			if got := r.Header.Get("X-Fetch-Test"); got != "yes" {
				t.Fatalf("fetch header mismatch: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Fetch-Reply", "ok")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"body": string(body)})
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ping":true}`))
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := fmt.Sprintf(`test("Fetch API globals match Bruno fixture", async function () {
  expect(fetch).to.be.a("function");
  expect(Request).to.be.a("function");
  expect(Response).to.be.a("function");
  expect(Headers).to.be.a("function");
  const nodeFetch = require("node-fetch");
  const nodeFetchCommonJS = require("node-fetch/commonjs");
  const { Headers: NodeFetchHeaders, Request: NodeFetchRequest, Response: NodeFetchResponse, default: defaultFetch } = nodeFetch;
  expect(nodeFetch).to.equal(fetch);
  expect(nodeFetchCommonJS).to.equal(nodeFetch);
  expect(defaultFetch).to.equal(nodeFetch);
  expect(NodeFetchHeaders).to.equal(Headers);
  expect(NodeFetchRequest).to.equal(Request);
  expect(NodeFetchResponse).to.equal(Response);

  const headers = new Headers();
  headers.set("Content-Type", "application/json");
  headers.append("X-Custom", "value");
  expect(headers.get("Content-Type")).to.equal("application/json");
  expect(headers.has("X-Custom")).to.equal(true);
  expect(headers.has("Missing")).to.equal(false);

  const req = new Request(%[1]q + "/fetch", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Fetch-Test": "yes" },
    body: JSON.stringify({ ok: true })
  });
  expect(req.url).to.equal(%[1]q + "/fetch");
  expect(req.method).to.equal("POST");
  expect(req.headers.get("Content-Type")).to.equal("application/json");

  const localResponse = new Response("body", { status: 201, statusText: "Created" });
  expect(localResponse.status).to.equal(201);
  expect(localResponse.statusText).to.equal("Created");
  expect(localResponse.ok).to.equal(true);
  expect(localResponse.json).to.be.a("function");
  expect(localResponse.text).to.be.a("function");
  expect(localResponse.arrayBuffer).to.be.a("function");
  expect(localResponse.blob).to.be.a("function");

  const parsed = await new Response("{\"ok\":true}").json();
  expect(parsed.ok).to.equal(true);
  const arrayBuffer = await new Response("test").arrayBuffer();
  expect(arrayBuffer.byteLength).to.equal(4);
  const blob = new Blob(["hello"], { type: "text/plain" });
  expect(blob.size).to.equal(5);
  expect(blob.type).to.equal("text/plain");
  expect(await blob.text()).to.equal("hello");

  const controller = new AbortController();
  expect(controller.signal.aborted).to.equal(false);
  controller.abort();
  expect(controller.signal.aborted).to.equal(true);

  const fd = new FormData();
  fd.append("field", "value");
  expect(fd.get("field")).to.equal("value");
  expect(fd.has("field")).to.equal(true);

  const fetched = await fetch(req);
  expect(fetched.status).to.equal(201);
  expect(fetched.statusText).to.equal("Created");
  expect(fetched.headers.get("x-fetch-reply")).to.equal("ok");
  const json = await fetched.json();
  expect(json.body).to.equal("{\"ok\":true}");
});`, server.URL)
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &server.URL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("fetch API request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("fetch API test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsEventTargetGlobals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Event-Count"); got != "3" {
			t.Fatalf("EventTarget listener count header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Event-Detail"); got != "hello" {
			t.Fatalf("CustomEvent detail header mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `const target = new EventTarget();
let count = 0;
let detail = "";
target.addEventListener("inc", function () { count++; });
target.addEventListener("inc", function (event) { count++; detail = event.detail; }, { once: true });
target.dispatchEvent(new CustomEvent("inc", { detail: "hello" }));
target.dispatchEvent(new CustomEvent("inc", { detail: "ignored" }));
req.setHeader("X-Event-Count", String(count));
req.setHeader("X-Event-Detail", detail);`
	tests := `test("Event globals match Bruno fixture", function () {
  expect(Event).to.be.a("function");
  expect(EventTarget).to.be.a("function");
  expect(CustomEvent).to.be.a("function");

  const event = new Event("click", { bubbles: true, cancelable: true });
  expect(event.type).to.equal("click");
  expect(event.bubbles).to.equal(true);
  expect(event.cancelable).to.equal(true);
  expect(event.defaultPrevented).to.equal(false);
  event.preventDefault();
  expect(event.defaultPrevented).to.equal(true);

  const custom = new CustomEvent("custom", { detail: { foo: "bar" } });
  expect(custom.type).to.equal("custom");
  expect(custom.detail).to.deep.equal({ foo: "bar" });

  let eventFired = false;
  let eventDetail = null;
  const target = new EventTarget();
  target.addEventListener("test", function (event) {
    eventFired = true;
    eventDetail = event.detail;
  });
  expect(target.dispatchEvent(new CustomEvent("test", { detail: "hello" }))).to.equal(true);
  expect(eventFired).to.equal(true);
  expect(eventDetail).to.equal("hello");

  let count = 0;
  target.addEventListener("inc", function () { count++; });
  target.addEventListener("inc", function () { count++; });
  target.dispatchEvent(new Event("inc"));
  expect(count).to.equal(2);

  let removed = true;
  const handler = function () { removed = false; };
  target.addEventListener("remove", handler);
  target.removeEventListener("remove", handler);
  target.dispatchEvent(new Event("remove"));
  expect(removed).to.equal(true);

  let onceCount = 0;
  target.addEventListener("once", function () { onceCount++; }, { once: true });
  target.dispatchEvent(new Event("once"));
  target.dispatchEvent(new Event("once"));
  expect(onceCount).to.equal(1);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("EventTarget request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("EventTarget test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsGlobalCrypto(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Crypto-Hash"); got != "15cedf0eb89eac4a0ec83b4b6caa4bc3e4fb3f88a1634952b1df8e27b45cd214" {
			t.Fatalf("global crypto hash header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Crypto-Length"); got != "6" {
			t.Fatalf("global crypto random header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Crypto-UUID"); !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(got) {
			t.Fatalf("global crypto UUID header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Crypto-Subtle"); got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
			t.Fatalf("global crypto subtle digest header mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"crypto":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `req.setHeader("X-Crypto-Hash", crypto.createHash("sha256").update("global crypto").digest("hex"));
req.setHeader("X-Crypto-Length", String(crypto.randomBytes(6).length));
req.setHeader("X-Crypto-UUID", crypto.randomUUID());
const subtleDigest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode("hello"));
req.setHeader("X-Crypto-Subtle", Array.from(new Uint8Array(subtleDigest)).map((value) => value.toString(16).padStart(2, "0")).join(""));`
	tests := `test("global crypto shim", async function () {
  expect(globalThis.crypto).to.equal(crypto);
  expect(require("crypto")).to.equal(crypto);
  expect(require("node:crypto")).to.equal(crypto);
  expect(typeof crypto.subtle).to.equal("object");
  expect(crypto.createHash("sha256").update("global crypto").digest("hex")).to.equal("15cedf0eb89eac4a0ec83b4b6caa4bc3e4fb3f88a1634952b1df8e27b45cd214");
  expect(crypto.createHmac("sha256", "secret").update("global").digest("hex")).to.equal("ec6ab38389fb04722cfd3bea35f60ef9223ea53ad408c5ad3a43e4648519cae6");
  expect(crypto.randomUUID()).to.match(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  const bytes = crypto.randomBytes(4);
  expect(Buffer.isBuffer(bytes)).to.equal(true);
  expect(bytes.length).to.equal(4);
  const target = new Uint8Array(8);
  expect(crypto.getRandomValues(target)).to.equal(target);
  expect(target.length).to.equal(8);
  expect(crypto.subtle.digest).to.be.a("function");
  expect(crypto.subtle.generateKey).to.be.a("function");
  expect(crypto.subtle.sign).to.be.a("function");
  expect(crypto.subtle.verify).to.be.a("function");
  expect(crypto.subtle.encrypt).to.be.a("function");
  expect(crypto.subtle.decrypt).to.be.a("function");
  expect(crypto.subtle.importKey).to.be.a("function");
  expect(crypto.subtle.exportKey).to.be.a("function");
  const digestPromise = crypto.subtle.digest("SHA-256", new TextEncoder().encode("hello"));
  expect(digestPromise).to.be.a("promise");
  const digest = await digestPromise;
  expect(digest.byteLength).to.equal(32);
  const keyPromise = crypto.subtle.generateKey({ name: "AES-GCM", length: 256 }, true, ["encrypt", "decrypt"]);
  expect(keyPromise).to.be.a("promise");
  const key = await keyPromise;
  expect(key.algorithm.name).to.equal("AES-GCM");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("global crypto request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("global crypto test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsCryptoJS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-CryptoJS-Plain"); got != "my message" {
			t.Fatalf("crypto-js AES header mismatch: %q", got)
		}
		if got := r.Header.Get("X-CryptoJS-Hash"); got != "b109f0a91199b99e240133e0a4faa5ebb345fe4de1cbb1666a7653ebadd11add" {
			t.Fatalf("crypto-js hash header mismatch: %q", got)
		}
		if got := r.Header.Get("X-CryptoJS-Hmac"); got != "ec6ab38389fb04722cfd3bea35f60ef9223ea53ad408c5ad3a43e4648519cae6" {
			t.Fatalf("crypto-js hmac header mismatch: %q", got)
		}
		if got := r.Header.Get("X-CryptoJS-Base64"); got != "aMOp" {
			t.Fatalf("crypto-js Base64 header mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cryptojs":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `var CryptoJS = require("crypto-js");
var ciphertext = CryptoJS.AES.encrypt("my message", "secret key 123").toString();
var bytes = CryptoJS.AES.decrypt(ciphertext, "secret key 123");
var originalText = bytes.toString(CryptoJS.enc.Utf8);
req.setHeader("X-CryptoJS-Plain", originalText);
req.setHeader("X-CryptoJS-Hash", CryptoJS.SHA256("hello bruno").toString());
req.setHeader("X-CryptoJS-Hmac", CryptoJS.HmacSHA256("global", "secret").toString());
req.setHeader("X-CryptoJS-Base64", CryptoJS.enc.Base64.stringify(CryptoJS.enc.Utf8.parse("hé")));`
	tests := `test("crypto-js shim", function () {
  var CryptoJS = require("crypto-js");
  expect(globalThis.CryptoJS).to.equal(CryptoJS);
  expect(CryptoJS.SHA256("hello bruno").toString()).to.equal("b109f0a91199b99e240133e0a4faa5ebb345fe4de1cbb1666a7653ebadd11add");
  expect(CryptoJS.SHA256(CryptoJS.enc.Utf8.parse("hello bruno")).toString(CryptoJS.enc.Base64)).to.equal("sQnwqRGZuZ4kATPgpPql67NF/k3hy7FmanZT663RGt0=");
  expect(CryptoJS.MD5("hello bruno").toString()).to.equal("21bf248791527b6b6febcbde49416864");
  expect(CryptoJS.HmacSHA256("global", "secret").toString()).to.equal("ec6ab38389fb04722cfd3bea35f60ef9223ea53ad408c5ad3a43e4648519cae6");
  var ciphertext = CryptoJS.AES.encrypt("my message", "secret key 123").toString();
  expect(CryptoJS.AES.decrypt(ciphertext, "secret key 123").toString(CryptoJS.enc.Utf8)).to.equal("my message");
  expect(CryptoJS.enc.Hex.stringify(CryptoJS.enc.Utf8.parse("hé"))).to.equal("68c3a9");
  expect(CryptoJS.lib.WordArray.create([0x6869ff00], 2).toString(CryptoJS.enc.Utf8)).to.equal("hi");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("crypto-js request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("crypto-js test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsMoment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Moment-Stamp"); got != "2026-07-02 03:04:05.678 UTC" {
			t.Fatalf("moment format header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Moment-Add"); got != "2026-01-04 22:30" {
			t.Fatalf("moment add/subtract header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Moment-Unix"); got != "2026-01-01T00:00:00Z" {
			t.Fatalf("moment unix header mismatch: %q", got)
		}
		if got := r.Header.Get("X-Moment-Duration"); got != "1590" {
			t.Fatalf("moment duration header mismatch: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"moment":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `var moment = require("moment");
req.setHeader("X-Moment-Stamp", moment.utc("2026-07-02T03:04:05.678Z").format("YYYY-MM-DD HH:mm:ss.SSS [UTC]"));
req.setHeader("X-Moment-Add", moment.utc([2026, 0, 2, 0, 0]).add(3, "days").subtract(90, "minutes").format("YYYY-MM-DD HH:mm"));
req.setHeader("X-Moment-Unix", moment.unix(1767225600).utc().format("YYYY-MM-DDTHH:mm:ss[Z]"));
req.setHeader("X-Moment-Duration", String(moment.duration({ days: 1, hours: 2, minutes: 30 }).asMinutes()));`
	tests := `test("moment shim", function () {
  var moment = require("moment");
  expect(globalThis.moment).to.equal(moment);
  expect(moment.version).to.include("2.29.4");
  var base = moment.utc("2026-07-02T03:04:05.678Z");
  expect(moment.isMoment(base)).to.equal(true);
  expect(base.format("YYYY-MM-DD HH:mm:ss.SSS [UTC]")).to.equal("2026-07-02 03:04:05.678 UTC");
  expect(base.clone().add(1, "day").format("YYYY-MM-DD")).to.equal("2026-07-03");
  expect(base.clone().startOf("day").format("HH:mm:ss.SSS")).to.equal("00:00:00.000");
  expect(base.diff(moment.utc("2026-07-01T03:04:05.678Z"), "days")).to.equal(1);
  expect(moment.duration(90, "minutes").asHours()).to.equal(1.5);
  expect(moment.utc([2026, 6, 2, 3, 4, 5, 678]).year()).to.equal(2026);
  expect(moment.utc("not a date").isValid()).to.equal(false);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("moment request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("moment test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsRunRequest(t *testing.T) {
	var pingCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/driver":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"driver":true}`))
		case "/ping":
			atomic.AddInt32(&pingCalls, 1)
			if r.Header.Get("X-Nested-Pre") != "yes" {
				t.Fatalf("nested pre-request script did not mutate target header: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("pong"))
		default:
			t.Fatalf("unexpected runRequest path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Ping"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateRequest(collection.ID, "websocket", "Socket"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	driver := collection.Items[0]
	ping := collection.Items[1]
	driverURL := server.URL + "/driver"
	pingURL := server.URL + "/ping"
	targetPreScript := `req.setHeader("X-Nested-Pre", "yes");
bru.setVar("nested_pre", "yes");`
	targetTests := `test("nested target tests run", function () {
  bru.setVar("nested_test", res.data);
  expect(res.status).to.equal(200);
});`
	if _, err := app.UpdateRequest(collection.ID, ping.ID, RequestPatch{
		URL:       &pingURL,
		PreScript: &targetPreScript,
		Tests:     &targetTests,
	}); err != nil {
		t.Fatal(err)
	}
	preScript := `const pingRes = await bru.runRequest("Ping");
bru.setVar("pre_ping", {
  data: pingRes.data,
  statusText: pingRes.statusText,
  status: pingRes.status
	});`
	postScript := `const pingRes = await bru.runRequest("Ping");
bru.setVar("post_ping", pingRes.data);`
	tests := `const pingRes = await bru.runRequest("Ping");
const socketRes = await bru.runRequest("Socket");
let invalidMessage = "";
try {
  await bru.runRequest("missing");
} catch (err) {
  invalidMessage = err.message;
}

test("runRequest returns response in every phase", function () {
  expect(bru.getVar("pre_ping")).to.deep.equal({ data: "pong", statusText: "OK", status: 200 });
  expect(bru.getVar("post_ping")).to.equal("pong");
  expect(pingRes.status).to.equal(200);
  expect(pingRes.statusText).to.equal("OK");
  expect(pingRes.data).to.equal("pong");
  expect(socketRes.status).to.equal("skipped");
  expect(socketRes.statusText).to.include("WebSocket");
  expect(socketRes.data).to.be.null;
});

test("runRequest shares runtime variables and reports invalid paths", function () {
  expect(bru.getVar("nested_pre")).to.equal("yes");
  expect(bru.getVar("nested_test")).to.equal("pong");
  expect(invalidMessage).to.include("invalid request path");
});`
	if _, err := app.UpdateRequest(collection.ID, driver.ID, RequestPatch{
		URL:        &driverURL,
		PreScript:  &preScript,
		PostScript: &postScript,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, driver.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, driver.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("runRequest driver failed: %#v", item.Response)
	}
	if got := atomic.LoadInt32(&pingCalls); got != 3 {
		t.Fatalf("expected nested request to run in pre/post/tests, got %d calls", got)
	}
	if len(item.Response.TestResults) != 2 {
		t.Fatalf("unexpected runRequest test count: %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("runRequest test did not pass: %#v", item.Response.TestResults)
		}
	}
}

func TestJavaScriptRuntimeSupportsBruVariableHelpers(t *testing.T) {
	t.Setenv("PROC_ENV_VAR", "from-process")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("bru variable helper aliases", function () {
  expect(bru.getProcessEnv("PROC_ENV_VAR")).to.equal("from-process");
  expect(bru.getProcessEnv("MISSING_PROC_ENV_VAR")).to.be.undefined;

  bru.setEnvVar("env_runtime", "one");
  expect(bru.hasEnvVar("env_runtime")).to.be.true;
  expect(bru.getEnvVar("env_runtime")).to.equal("one");
  expect(bru.getAllEnvVars().env_runtime).to.equal("one");
  bru.deleteEnvVar("env_runtime");
  expect(bru.getEnvVar("env_runtime")).to.be.undefined;

  bru.setGlobalEnvVar("global_runtime", "two");
  expect(bru.hasGlobalEnvVar("global_runtime")).to.be.true;
  expect(bru.getGlobalEnvVar("global_runtime")).to.equal("two");
  expect(bru.getAllGlobalEnvVars().global_runtime).to.equal("two");
  bru.deleteGlobalEnvVar("global_runtime");
  expect(bru.getGlobalEnvVar("global_runtime")).to.be.undefined;

  bru.setCollectionVar("collection_runtime", "three");
  expect(bru.hasCollectionVar("collection_runtime")).to.be.true;
  expect(bru.getCollectionVar("collection_runtime")).to.equal("three");
  expect(bru.getAllCollectionVars().collection_runtime).to.equal("three");
  bru.deleteCollectionVar("collection_runtime");
  expect(bru.getCollectionVar("collection_runtime")).to.be.undefined;

  expect(function () { bru.setGlobalEnvVar("", "bad"); }).to.throw("without specifying a name");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &server.URL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("variable helper request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("variable helper test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimePersistsScopedVariableMutations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	envID := collection.Environments[0].ID
	preScript := `bru.setCollectionVar("scope_collision", "collection");
bru.setEnvVar("scope_collision", "env");
bru.deleteAllEnvVars();
if (bru.getCollectionVar("scope_collision") !== "collection") {
  throw new Error("deleteAllEnvVars touched collection scope");
}
bru.setEnvVar("persist_env", true);
bru.setGlobalEnvVar("persist_global", 7);
bru.setCollectionVar("persist_collection", { nested: { count: 2 } });
bru.setVar("runtime_only", "pre");
bru.deleteAllVars();
if (bru.hasVar("runtime_only")) {
  throw new Error("runtime vars were not cleared");
}
if (bru.getCollectionVar("persist_collection").nested.count !== 2) {
  throw new Error("collection object var was not readable");
}`
	postScript := `bru.setEnvVar("post_env", "from-post");
bru.setCollectionVar("post_collection", ["a", "b"]);`
	tests := `test("scoped variables remain separated", function () {
  expect(bru.getEnvVar("scope_collision")).to.be.undefined;
  expect(bru.getCollectionVar("scope_collision")).to.equal("collection");
  expect(bru.getEnvVar("persist_env")).to.equal(true);
  expect(bru.getEnvVar("post_env")).to.equal("from-post");
  expect(bru.getGlobalEnvVar("persist_global")).to.equal(7);
  expect(bru.getCollectionVar("persist_collection").nested.count).to.equal(2);
  expect(bru.getCollectionVar("post_collection")).to.deep.equal(["a", "b"]);
  expect(bru.getEnvVar("host")).to.be.undefined;
  expect(bru.getAllCollectionVars()).to.have.property("scope_collision", "collection");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:        &server.URL,
		PreScript:  &preScript,
		PostScript: &postScript,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, envID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("scoped variable request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("scoped variable test did not pass: %#v", item.Response.TestResults)
	}
	collection = state.Workspaces[0].Collections[0]
	collectionVars := variablesByName(collection.Variables)
	if collectionVars["scope_collision"].Value != "collection" {
		t.Fatalf("collection scope was not persisted separately: %#v", collection.Variables)
	}
	if nested, ok := collectionVars["persist_collection"].Value.(map[string]interface{}); !ok || fmt.Sprint(nested["nested"]) == "" {
		t.Fatalf("typed collection object was not persisted: %#v", collectionVars["persist_collection"])
	}
	if values, ok := collectionVars["post_collection"].Value.([]interface{}); !ok || len(values) != 2 {
		t.Fatalf("typed collection array was not persisted: %#v", collectionVars["post_collection"])
	}
	env := state.Workspaces[0].Collections[0].Environments[0]
	envVars := variablesByName(env.Variables)
	if envVars["scope_collision"].Name != "" || envVars["host"].Name != "" {
		t.Fatalf("deleteAllEnvVars did not persist selected env cleanup: %#v", env.Variables)
	}
	if envVars["persist_env"].Value != true || envVars["post_env"].Value != "from-post" {
		t.Fatalf("env scope mutations were not persisted: %#v", env.Variables)
	}
	if len(state.Workspaces[0].GlobalEnvironments) == 0 {
		t.Fatal("global env mutation did not create a workspace global environment")
	}
	globalVars := variablesByName(state.Workspaces[0].GlobalEnvironments[0].Variables)
	if fmt.Sprint(globalVars["persist_global"].Value) != "7" {
		t.Fatalf("global scope mutation was not persisted: %#v", state.Workspaces[0].GlobalEnvironments[0].Variables)
	}
}

func TestWorkspaceGlobalEnvironmentSelectionPrecedenceSecretsAndDiskRoundTrip(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "test-global-environment-key")
	var seen url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	collection := workspace.Collections[0]
	item := collection.Items[0]
	envID := collection.Environments[0].ID

	state, err = app.CreateGlobalEnvironment(workspace.ID, "Inactive")
	if err != nil {
		t.Fatal(err)
	}
	inactiveID := state.Workspaces[0].ActiveGlobalEnvironmentID
	if _, err := app.UpdateGlobalEnvironmentVariables(workspace.ID, inactiveID, []Variable{
		{ID: newID("var"), Name: "inactive_only", Value: "inactive", DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "global_only", Value: "inactive-global", DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateGlobalEnvironment(workspace.ID, "Active")
	if err != nil {
		t.Fatal(err)
	}
	activeID := state.Workspaces[0].ActiveGlobalEnvironmentID
	if _, err := app.UpdateGlobalEnvironmentVariables(workspace.ID, activeID, []Variable{
		{ID: newID("var"), Name: "global_only", Value: "active-global", DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "priority", Value: "global", DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "global_secret", Value: "hidden-global", DataType: "string", Enabled: true, Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: newID("var"), Name: "host", Value: server.URL, DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "priority", Value: "collection", DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateEnvironmentVariables(collection.ID, envID, []Variable{
		{ID: newID("var"), Name: "priority", Value: "environment", DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	requestURL := "{{host}}/global?global={{global_only}}&priority={{priority}}&secret={{global_secret}}"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL: &requestURL,
		Vars: &RequestVars{Req: []Variable{
			{ID: newID("var"), Name: "priority", Value: "request", DataType: "string", Enabled: true},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	workspacePath := state.Workspaces[0].Path
	envFile := filepath.Join(workspacePath, "environments", "Active.yml")
	envData, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(envData), "hidden-global") {
		t.Fatalf("global environment yaml leaked secret value: %s", envData)
	}
	flushPersistForTest(t, app)
	secretsData, err := os.ReadFile(filepath.Join(dir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretsData), `"workspaces"`) || !strings.Contains(string(secretsData), "$01:") || strings.Contains(string(secretsData), "hidden-global") {
		t.Fatalf("global secret store was not encrypted as expected: %s", secretsData)
	}
	stateData, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateData), "hidden-global") {
		t.Fatalf("state.json leaked global secret value: %s", stateData)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	_, _, vars, err := reloaded.effectiveRequestContextForExecution(collection.ID, item.ID, envID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vars["inactive_only"]; ok {
		t.Fatalf("inactive global environment was merged: %#v", vars)
	}
	state, err = reloaded.SendRequest(collection.ID, item.ID, envID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("global env request failed: %#v", item.Response)
	}
	if seen.Get("global") != "active-global" {
		t.Fatalf("active global env value was not used: %s", seen.Encode())
	}
	if seen.Get("priority") != "request" {
		t.Fatalf("variable precedence mismatch: %s", seen.Encode())
	}
	if seen.Get("secret") != "hidden-global" {
		t.Fatalf("global secret was not hydrated for execution: %s", seen.Encode())
	}
}

func TestWorkspaceActiveGlobalEnvironmentUIDMigratesFromWorkspaceYML(t *testing.T) {
	findEnvironment := func(environments []Environment, environmentID string) *Environment {
		for index := range environments {
			if environments[index].ID == environmentID {
				return &environments[index]
			}
		}
		return nil
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := state.Workspaces[0].Path
	envDir := filepath.Join(workspacePath, "environments")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alphaPath := filepath.Join(envDir, "Alpha.yml")
	if err := os.WriteFile(alphaPath, []byte(`name: Alpha
variables:
  - name: mode
    value: alpha
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "Beta.yml"), []byte(`name: Beta
variables:
  - name: mode
    value: beta
`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceConfigPath := filepath.Join(workspacePath, "workspace.yml")
	if err := os.WriteFile(workspaceConfigPath, []byte(fmt.Sprintf(`opencollection: 1.0.0
info:
  name: "My Workspace"
  type: workspace

activeEnvironmentUid: "%s"

collections: []
specs:
docs: ''
`, brunoWorkspaceEnvironmentUIDForPath(alphaPath))), 0o600); err != nil {
		t.Fatal(err)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, app.dataDir)
	state, err = reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	active := findEnvironment(workspace.GlobalEnvironments, workspace.ActiveGlobalEnvironmentID)
	if active == nil || active.Name != "Alpha" {
		t.Fatalf("legacy workspace.yml activeEnvironmentUid did not select Alpha: active=%#v envs=%#v", active, workspace.GlobalEnvironments)
	}
	updatedConfig, err := os.ReadFile(workspaceConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updatedConfig), "activeEnvironmentUid") {
		t.Fatalf("workspace.yml was not migrated: %s", updatedConfig)
	}
	flushPersistForTest(t, reloaded)
	stateData, err := os.ReadFile(filepath.Join(app.dataDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stateData), workspace.ActiveGlobalEnvironmentID) {
		t.Fatalf("migrated active global environment was not persisted: %s", stateData)
	}

	flushPersistForTest(t, reloaded)
	restarted := newAppInDirForTest(t, app.dataDir)
	restartedState, err := restarted.GetState()
	if err != nil {
		t.Fatal(err)
	}
	restartedWorkspace := restartedState.Workspaces[0]
	restartedActive := findEnvironment(restartedWorkspace.GlobalEnvironments, restartedWorkspace.ActiveGlobalEnvironmentID)
	if restartedActive == nil || restartedActive.Name != "Alpha" {
		t.Fatalf("migrated active global environment did not survive restart: active=%#v envs=%#v", restartedActive, restartedWorkspace.GlobalEnvironments)
	}
}

func TestGlobalEnvironmentCopyImportExportScrubsSecretsAndRoundTrips(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "test-global-import-export-key")
	findGlobalEnvironment := func(t *testing.T, state AppState, environmentID string) Environment {
		t.Helper()
		for _, env := range state.Workspaces[0].GlobalEnvironments {
			if env.ID == environmentID {
				return env
			}
		}
		t.Fatalf("global environment %s not found", environmentID)
		return Environment{}
	}

	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	state, err = app.CreateGlobalEnvironment(workspace.ID, "Source")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := state.Workspaces[0].ActiveGlobalEnvironmentID
	state, err = app.UpdateGlobalEnvironmentVariables(workspace.ID, sourceID, []Variable{
		{ID: newID("var"), Name: "plain", Value: "visible", DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "secret", Value: "hidden", DataType: "string", Enabled: true, Secret: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	exported, err := app.ExportGlobalEnvironment(workspace.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported, `"type": "bruno-environment"`) || !strings.Contains(exported, `"name": "Source"`) || !strings.Contains(exported, `"plain"`) || !strings.Contains(exported, `"visible"`) || !strings.Contains(exported, `"secret": true`) {
		t.Fatalf("exported global environment did not include expected Bruno JSON fields: %s", exported)
	}
	if strings.Contains(exported, "hidden") {
		t.Fatalf("exported global environment leaked secret value: %s", exported)
	}

	state, err = app.CopyGlobalEnvironment(workspace.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	copyID := state.Workspaces[0].ActiveGlobalEnvironmentID
	if copyID == sourceID {
		t.Fatal("copy reused the source environment id")
	}
	copied := findGlobalEnvironment(t, state, copyID)
	if copied.Name != "Source - Copy" {
		t.Fatalf("copy name mismatch: %q", copied.Name)
	}
	if copied.Color != "" {
		t.Fatalf("copy should not preserve color, got %q", copied.Color)
	}
	copiedVars := variablesByName(copied.Variables)
	if copiedVars["plain"].Value != "visible" || copiedVars["secret"].Value != "hidden" || !copiedVars["secret"].Secret {
		t.Fatalf("copy did not preserve values in memory: %#v", copiedVars)
	}
	workspacePath := state.Workspaces[0].Path
	copyData, err := os.ReadFile(filepath.Join(workspacePath, "environments", "Source - Copy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(copyData), "hidden") {
		t.Fatalf("copied global environment yaml leaked secret value: %s", copyData)
	}
	flushPersistForTest(t, app)
	secretsData, err := os.ReadFile(filepath.Join(dir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretsData), "$01:") || strings.Contains(string(secretsData), "hidden") {
		t.Fatalf("copied global environment secret was not encrypted: %s", secretsData)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	reloadedCopied := findGlobalEnvironment(t, reloadedState, copyID)
	reloadedVars := variablesByName(reloadedCopied.Variables)
	if reloadedVars["secret"].Value != "hidden" {
		t.Fatalf("copied secret was not hydrated after reload: %#v", reloadedVars["secret"])
	}

	importedPayload := strings.Replace(exported, `"name": "Source"`, `"name": "Imported"`, 1)
	importedPayload = strings.Replace(importedPayload, `"value": "visible"`, `"value": "imported-visible"`, 1)
	state, err = reloaded.ImportGlobalEnvironment(workspace.ID, importedPayload)
	if err != nil {
		t.Fatal(err)
	}
	importedID := state.Workspaces[0].ActiveGlobalEnvironmentID
	imported := findGlobalEnvironment(t, state, importedID)
	if imported.Name != "Imported" {
		t.Fatalf("imported environment name mismatch: %q", imported.Name)
	}
	importedVars := variablesByName(imported.Variables)
	if importedVars["plain"].Value != "imported-visible" || !importedVars["secret"].Secret || importedVars["secret"].Value != "" {
		t.Fatalf("imported round-trip values mismatch: %#v", importedVars)
	}
	importedData, err := os.ReadFile(filepath.Join(workspacePath, "environments", "Imported.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(importedData), "hidden") {
		t.Fatalf("imported global environment yaml leaked secret value: %s", importedData)
	}

	multiPayload := `{
  "info": { "type": "bruno-environment", "exportedAt": "2026-01-01T00:00:00Z", "exportedUsing": "Bruno/v2.0.0" },
  "environments": [
    { "name": "Imported", "variables": [{ "name": "multi_one", "value": "one", "type": "text", "enabled": true, "secret": false }] },
    { "name": "Second", "variables": [{ "name": "multi_two", "value": 2, "type": "text", "enabled": true, "secret": false, "dataType": "number" }] }
  ]
}`
	state, err = reloaded.ImportGlobalEnvironment(workspace.ID, multiPayload)
	if err != nil {
		t.Fatal(err)
	}
	importedNames := map[string]bool{}
	for _, env := range state.Workspaces[0].GlobalEnvironments {
		importedNames[env.Name] = true
	}
	if !importedNames["Imported copy"] || !importedNames["Second"] {
		t.Fatalf("multi environment import did not apply unique Bruno names: %#v", importedNames)
	}

	postmanPayload := `{
  "id": "postman-env",
  "name": "Postman Env",
  "values": [
    { "key": "postman secret", "value": "postman-hidden", "type": "secret", "enabled": true },
    { "key": "postman_plain", "value": "plain-postman", "type": "default", "enabled": true }
  ]
}`
	state, err = reloaded.ImportGlobalEnvironment(workspace.ID, postmanPayload)
	if err != nil {
		t.Fatal(err)
	}
	postmanEnv := findGlobalEnvironment(t, state, state.Workspaces[0].ActiveGlobalEnvironmentID)
	if postmanEnv.Name != "Postman Env" {
		t.Fatalf("postman environment name mismatch: %q", postmanEnv.Name)
	}
	postmanVars := variablesByName(postmanEnv.Variables)
	if postmanVars["postman_secret"].Value != "postman-hidden" || !postmanVars["postman_secret"].Secret {
		t.Fatalf("postman secret variable was not imported before persistence: %#v", postmanVars)
	}
	flushPersistForTest(t, reloaded)
	reloadedAgain := newAppInDirForTest(t, dir)
	reloadedAgainState, err := reloadedAgain.GetState()
	if err != nil {
		t.Fatal(err)
	}
	postmanReloaded := findGlobalEnvironment(t, reloadedAgainState, state.Workspaces[0].ActiveGlobalEnvironmentID)
	postmanReloadedVars := variablesByName(postmanReloaded.Variables)
	if postmanReloadedVars["postman_secret"].Value != "postman-hidden" {
		t.Fatalf("postman secret was not encrypted and hydrated after reload: %#v", postmanReloadedVars["postman_secret"])
	}
}

func TestGlobalEnvironmentMultiExportAndCopyNameUsesBrunoConflictStyle(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "test-global-export-manager-key")
	findGlobalEnvironment := func(t *testing.T, state AppState, environmentID string) Environment {
		t.Helper()
		for _, env := range state.Workspaces[0].GlobalEnvironments {
			if env.ID == environmentID {
				return env
			}
		}
		t.Fatalf("global environment %s not found", environmentID)
		return Environment{}
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	state, err = app.CreateGlobalEnvironment(workspace.ID, "Team")
	if err != nil {
		t.Fatal(err)
	}
	teamID := state.Workspaces[0].ActiveGlobalEnvironmentID
	if _, err := app.UpdateGlobalEnvironmentVariables(workspace.ID, teamID, []Variable{
		{ID: newID("var"), Name: "plain", Value: "team-visible", DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "secret", Value: "team-hidden", DataType: "string", Enabled: true, Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateGlobalEnvironment(workspace.ID, "Team")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate := findGlobalEnvironment(t, state, state.Workspaces[0].ActiveGlobalEnvironmentID); duplicate.Name != "Team copy" {
		t.Fatalf("duplicate global environment name mismatch: %q", duplicate.Name)
	}
	state, err = app.CopyGlobalEnvironmentAs(workspace.ID, teamID, "QA")
	if err != nil {
		t.Fatal(err)
	}
	qaID := state.Workspaces[0].ActiveGlobalEnvironmentID
	if copied := findGlobalEnvironment(t, state, qaID); copied.Name != "QA" {
		t.Fatalf("custom copy name mismatch: %q", copied.Name)
	}
	state, err = app.CopyGlobalEnvironmentAs(workspace.ID, teamID, "QA")
	if err != nil {
		t.Fatal(err)
	}
	if copied := findGlobalEnvironment(t, state, state.Workspaces[0].ActiveGlobalEnvironmentID); copied.Name != "QA copy" {
		t.Fatalf("custom copy conflict name mismatch: %q", copied.Name)
	}

	singleFile, err := app.ExportGlobalEnvironments(workspace.ID, []string{teamID, qaID}, "single-file")
	if err != nil {
		t.Fatal(err)
	}
	if singleFile.Format != "single-file" || singleFile.Filename != "bruno-global-environments.json" {
		t.Fatalf("single-file export metadata mismatch: %#v", singleFile)
	}
	if !strings.Contains(singleFile.Content, `"environments"`) || !strings.Contains(singleFile.Content, `"name": "Team"`) || !strings.Contains(singleFile.Content, `"name": "QA"`) {
		t.Fatalf("single-file export missing environments: %s", singleFile.Content)
	}
	if strings.Contains(singleFile.Content, "team-hidden") {
		t.Fatalf("single-file export leaked secret value: %s", singleFile.Content)
	}
	if strings.Count(singleFile.Content, `"type": "bruno-environment"`) != 1 {
		t.Fatalf("single-file export should include one top-level info block: %s", singleFile.Content)
	}

	folder, err := app.ExportGlobalEnvironments(workspace.ID, []string{teamID, qaID}, "folder")
	if err != nil {
		t.Fatal(err)
	}
	if folder.Format != "folder" || folder.Filename != "bruno-global-environments" || len(folder.Files) != 2 {
		t.Fatalf("folder export metadata mismatch: %#v", folder)
	}
	fileNames := []string{folder.Files[0].Name, folder.Files[1].Name}
	if !reflect.DeepEqual(fileNames, []string{"Team.json", "QA.json"}) {
		t.Fatalf("folder export file names mismatch: %#v", fileNames)
	}
	for _, file := range folder.Files {
		if !strings.Contains(file.Content, `"info"`) || strings.Contains(file.Content, "team-hidden") {
			t.Fatalf("folder export content mismatch for %s: %s", file.Name, file.Content)
		}
	}
	if _, err := app.ExportGlobalEnvironments(workspace.ID, []string{teamID, qaID}, "single-object"); err == nil {
		t.Fatal("single-object export with multiple environments succeeded")
	}
}

func TestSaveGlobalEnvironmentExportWritesSingleFileAndFolder(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "test-global-export-save-key")
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	state, err = app.CreateGlobalEnvironment(workspace.ID, "Team")
	if err != nil {
		t.Fatal(err)
	}
	teamID := state.Workspaces[0].ActiveGlobalEnvironmentID
	if _, err := app.UpdateGlobalEnvironmentVariables(workspace.ID, teamID, []Variable{
		{ID: newID("var"), Name: "plain", Value: "team-visible", DataType: "string", Enabled: true},
		{ID: newID("var"), Name: "secret", Value: "team-hidden", DataType: "string", Enabled: true, Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateGlobalEnvironment(workspace.ID, "QA")
	if err != nil {
		t.Fatal(err)
	}
	qaID := state.Workspaces[0].ActiveGlobalEnvironmentID

	if _, err := app.SaveGlobalEnvironmentExport(workspace.ID, []string{teamID}, "single-object", ""); err == nil {
		t.Fatal("save export without a target path succeeded outside Wails")
	}

	exportDir := t.TempDir()
	singlePath := filepath.Join(exportDir, "Team.json")
	single, err := app.SaveGlobalEnvironmentExport(workspace.ID, []string{teamID}, "single-object", singlePath)
	if err != nil {
		t.Fatal(err)
	}
	if single.Format != "single-object" || single.Path != singlePath || !reflect.DeepEqual(single.Files, []string{singlePath}) {
		t.Fatalf("single-object save result mismatch: %#v", single)
	}
	singleData, err := os.ReadFile(singlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(singleData), `"type": "bruno-environment"`) || !strings.Contains(string(singleData), `"name": "plain"`) {
		t.Fatalf("single-object export file missing Bruno environment content: %s", singleData)
	}
	if strings.Contains(string(singleData), "team-hidden") {
		t.Fatalf("single-object export file leaked secret value: %s", singleData)
	}

	folderPath := filepath.Join(exportDir, "bruno-global-environments")
	folder, err := app.SaveGlobalEnvironmentExport(workspace.ID, []string{teamID, qaID}, "folder", folderPath)
	if err != nil {
		t.Fatal(err)
	}
	if folder.Format != "folder" || folder.Path != folderPath || len(folder.Files) != 2 {
		t.Fatalf("folder save result mismatch: %#v", folder)
	}
	for _, name := range []string{"Team.json", "QA.json"} {
		data, err := os.ReadFile(filepath.Join(folderPath, name))
		if err != nil {
			t.Fatalf("expected folder export file %s: %v", name, err)
		}
		if !strings.Contains(string(data), `"type": "bruno-environment"`) {
			t.Fatalf("folder export file missing Bruno info for %s: %s", name, data)
		}
		if strings.Contains(string(data), "team-hidden") {
			t.Fatalf("folder export file leaked secret value for %s: %s", name, data)
		}
	}
}

func TestJavaScriptRuntimeSupportsBruMetadataBulkVarsAndUtils(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	envID := collection.Environments[0].ID
	tests := fmt.Sprintf(`test("bru metadata, bulk vars, and utils", function () {
  expect(bru.cwd()).to.equal(%s);
  expect(bru.getCollectionName()).to.equal(%s);
  expect(bru.getEnvName()).to.equal("Development");
  expect(bru.isSafeMode()).to.equal(true);

  bru.setVar("runtime_bulk", "alpha");
  expect(bru.hasVar("runtime_bulk")).to.be.true;
  expect(bru.getVar("runtime_bulk")).to.equal("alpha");
  expect(bru.getAllVars().runtime_bulk).to.equal("alpha");
  bru.deleteVar("runtime_bulk");
  expect(bru.hasVar("runtime_bulk")).to.be.false;

  bru.setCollectionVar("collection_bulk", "beta");
  expect(bru.getAllCollectionVars().collection_bulk).to.equal("beta");
  bru.deleteAllCollectionVars();
  expect(bru.getCollectionVar("collection_bulk")).to.be.undefined;

  bru.setVar("runtime_again", "gamma");
  bru.deleteAllVars();
  expect(bru.hasVar("runtime_again")).to.be.false;

  expect(bru.utils.minifyJson(' { "a": 1, "b": [ true ] } ')).to.equal('{"a":1,"b":[true]}');
  expect(JSON.parse(bru.utils.minifyJson({ a: 1 })).a).to.equal(1);
  expect(bru.utils.minifyJson("   ")).to.equal("");
  expect(function () { bru.utils.minifyJson(7); }).to.throw("minifyJson expects");
  expect(bru.utils.minifyXml("<root>\n  <item> ok </item>\n</root>")).to.equal("<root><item> ok </item></root>");
  expect(function () { bru.utils.minifyXml(7); }).to.throw("minifyXml expects");
});`, importers.JSStringLiteral(collection.Path), importers.JSStringLiteral(collection.Name))
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &server.URL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, envID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("metadata helper request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("metadata helper test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSetBodyFormURLEncodedParity(t *testing.T) {
	expectedBodies := map[string]string{
		"/object": "key=value%20with%20spaces&name=bruno&array=test&array=value",
		"/array":  "empty=&null=&undefined=&zero=0&false=false&=empty_key&key=value1&name=bruno&key=value2",
		"/header": "key=value&name=bruno",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		expected, ok := expectedBodies[r.URL.Path]
		if !ok {
			t.Fatalf("unexpected form test path: %s", r.URL.Path)
		}
		if got := string(bodyBytes); got != expected {
			t.Fatalf("unexpected %s body: %q", r.URL.Path, got)
		}
		if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/x-www-form-urlencoded") {
			t.Fatalf("unexpected %s content type: %q", r.URL.Path, contentType)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(bodyBytes)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodPost
	cases := []struct {
		name      string
		path      string
		body      RequestBody
		preScript string
		expected  string
	}{
		{
			name: "object body",
			path: "/object",
			body: RequestBody{Mode: "formUrlEncoded"},
			preScript: `req.setBody({
  "key": "value with spaces",
  "name": "bruno",
  "array": ["test", "value"],
});`,
			expected: expectedBodies["/object"],
		},
		{
			name: "array body",
			path: "/array",
			body: RequestBody{Mode: "formUrlEncoded"},
			preScript: `req.setBody([
  {name: "empty", value: ""},
  {name: "null", value: null},
  {name: "undefined", value: undefined},
  {name: "zero", value: 0},
  {name: "false", value: false},
  {name: "", value: "empty_key"},
  {name: "key", value: "value1"},
  {name: "name", value: "bruno"},
  {name: "key", value: "value2"},
]);`,
			expected: expectedBodies["/array"],
		},
		{
			name: "content type via setHeader",
			path: "/header",
			body: RequestBody{Mode: "none"},
			preScript: `req.setHeader("content-type", "application/x-www-form-urlencoded");
req.setBody([
  {name: "key", value: "value"},
  {name: "name", value: "bruno"},
]);`,
			expected: expectedBodies["/header"],
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			targetURL := server.URL + tc.path
			tests := fmt.Sprintf(`test("request body is encoded", function () {
  expect(req.getBody()).to.equal(%s);
  expect(req.getBody({ raw: true })).to.equal(%s);
});
test("response body is encoded", function () {
  expect(res.getBody()).to.equal(%s);
});`, importers.JSStringLiteral(tc.expected), importers.JSStringLiteral(tc.expected), importers.JSStringLiteral(tc.expected))
			if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
				Method:    &method,
				URL:       &targetURL,
				Body:      &tc.body,
				PreScript: &tc.preScript,
				Tests:     &tests,
			}); err != nil {
				t.Fatal(err)
			}
			state, err := app.SendRequest(collection.ID, item.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			updated, ok := findItemInState(state, collection.ID, item.ID)
			if !ok || updated.Response == nil || updated.Response.Status != http.StatusOK {
				t.Fatalf("form setBody request failed: %#v", updated.Response)
			}
			if len(updated.Response.TestResults) != 2 || !updated.Response.TestResults[0].Passed || !updated.Response.TestResults[1].Passed {
				t.Fatalf("form setBody tests did not pass: %#v", updated.Response.TestResults)
			}
		})
	}
}

func TestJavaScriptRuntimeCanMutateResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"before":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	postScript := `res.setBody({ after: true, count: 2 });`
	tests := `test("mutated response body", function () {
  expect(res.getBody()).to.deep.equal({ after: true, count: 2 });
  expect(res.json.after).to.equal(true);
  expect(res.body).to.equal('{"after":true,"count":2}');
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:        &server.URL,
		PostScript: &postScript,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("response mutation request failed: %#v", item.Response)
	}
	if item.Response.Body != `{"after":true,"count":2}` {
		t.Fatalf("response body was not persisted after mutation: %s", item.Response.Body)
	}
	if item.Response.Size != len(item.Response.Body) {
		t.Fatalf("response size was not updated: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("response mutation test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeExposesBrunoRequestHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/users/42" || r.URL.RawQuery != "active=true" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"name":"Ada","count":2}` {
			t.Fatalf("unexpected body: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	name := "Helper Request"
	method := http.MethodPost
	targetURL := server.URL + "/users/:userId?active=true"
	pathParams := []KeyValue{{Name: "userId", Value: "42", Enabled: true}}
	headers := []KeyValue{{Name: "X-Initial", Value: "one", Enabled: true}}
	body := RequestBody{Mode: "json", JSON: `{"name":"Ada","count":2}`}
	tags := []string{"smoke", "api"}
	tests := `test("bruno request helpers", function () {
  expect(req.name).to.equal("Helper Request");
  expect(req.tags).to.include("api");
  expect(req.timeout).to.equal(30000);
  expect(req.pathParams[0].name).to.equal("userId");
  expect(req.pathParams[0].value).to.equal("42");
  expect(req.getName()).to.equal("Helper Request");
  expect(req.getTags()).to.include("smoke");
  expect(req.getTimeout()).to.equal(30000);
  expect(req.getMethod()).to.equal("POST");
  expect(req.getHost()).to.include("127.0.0.1");
  expect(req.getPath()).to.equal("/users/42");
  expect(req.getQueryString()).to.equal("active=true");
  const pathParams = req.getPathParams();
  expect(pathParams).to.have.lengthOf(1);
  expect(pathParams[0].name).to.equal("userId");
  expect(pathParams[0].value).to.equal("42");
  expect(pathParams[0].type).to.equal("path");
  expect(req.getExecutionMode()).to.equal("single");
  expect(req.getBody()).to.deep.equal({ name: "Ada", count: 2 });
  expect(req.getBody({ raw: true })).to.equal('{"name":"Ada","count":2}');
  expect(req.getHeaders()).to.have.property("X-Initial", "one");
  req.setHeader("X-Script", "two");
  expect(req.headers["X-Script"]).to.equal("two");
  req.deleteHeader("X-Initial");
  expect(req.getHeaders()["X-Initial"]).to.not.exist;
  req.setHeaders({ "X-Replaced": "yes" });
  expect(req.getHeader("X-Replaced")).to.equal("yes");
  expect(req.headers["X-Replaced"]).to.equal("yes");
  req.deleteHeaders(["X-Replaced"]);
  expect(req.getHeader("X-Replaced")).to.equal("");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		Name:       &name,
		Method:     &method,
		URL:        &targetURL,
		PathParams: &pathParams,
		Headers:    &headers,
		Body:       &body,
		Tags:       &tags,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("request helper request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("request helper test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeCanControlRedirectsAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = w.Write([]byte("followed"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL + "/redirect"
	preScript := `req.setMaxRedirects(0);
req.setTimeout(1234);
bru.setVar("script_timeout", String(req.getTimeout()));`
	tests := `test("request execution controls", function () {
  expect(res.status).to.equal(302);
  expect(res.getHeader("Location")).to.equal("/final");
  expect(req.timeout).to.equal(1234);
  expect(req.getTimeout()).to.equal(1234);
  expect(bru.getVar("script_timeout")).to.equal("1234");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &targetURL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusFound {
		t.Fatalf("redirect control request failed: %#v", item.Response)
	}
	if item.Response.RequestedURL != targetURL {
		t.Fatalf("request should not have followed redirect: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("redirect control test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeCanDisableResponseJSONParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `req.disableParsingResponseJson();`
	tests := `test("disable response json parsing", function () {
  expect(res.getBody()).to.equal('{"ok":true}');
  expect(res.data).to.equal('{"ok":true}');
  expect(res.json).to.be.undefined;
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("disable json parsing request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("disable json parsing test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeSupportsMutableRequestHeaderList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Only") != "updated" || r.Header.Get("X-Second") != "two" {
			t.Fatalf("headerList mutations were not sent: %#v", r.Header)
		}
		if r.Header.Get("X-Added") != "" || r.Header.Get("X-Remove") != "" {
			t.Fatalf("pruned/removed headers were sent: %#v", r.Header)
		}
		w.Header().Set("X-Test", "ok")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	headers := []KeyValue{
		{Name: "X-Remove", Value: "gone", Enabled: true},
		{Name: "X-Disabled", Value: "off", Enabled: false},
	}
	preScript := `req.headerList.add({ key: "X-Added", value: "one" });
req.headerList.add("X-Line: line");
req.headerList.upsert("X-Upsert", "two");
req.headerList.upsert({ key: "x-upsert", value: "three" });
req.headerList.remove("X-Remove");
req.headerList.populate([{ key: "X-Added", value: "skip" }, { key: "X-Pop", value: "pop" }]);
req.headerList.populate("X-String: string\r\nX-Pop: skip");
bru.setVar("header_has_added", String(req.headerList.has("x-added", "one")));
bru.setVar("header_has_object", String(req.headerList.has({ key: "X-Line" })));
bru.setVar("header_index_object", String(req.headerList.indexOf({ key: "X-Line", value: "line" })));
bru.setVar("header_context", req.headerList.map(function (header) { return this.prefix + header.key; }, { prefix: "seen:" }).join("|"));
bru.setVar("header_disabled_present", String(req.headerList.toObject(false, true, false, false)["X-Disabled"] === "off"));
bru.setVar("header_disabled_excluded", String(req.headerList.toObject(true, true, false, false)["X-Disabled"] === undefined));
req.headerList.clear();
req.headerList.repopulate([{ key: "X-Only", value: "only" }]);
req.headerList.assimilate([{ key: "X-Only", value: "updated" }, { key: "X-Second", value: "two" }], true);
bru.setVar("header_object_lower", JSON.stringify(req.headerList.toObject(false, false, true, true)));`
	tests := `test("request headerList write parity", function () {
  expect(bru.getVar("header_has_added")).to.equal("true");
  expect(bru.getVar("header_has_object")).to.equal("true");
  expect(Number(bru.getVar("header_index_object"))).to.be.above(-1);
  expect(bru.getVar("header_context")).to.include("seen:X-Added");
  expect(bru.getVar("header_disabled_present")).to.equal("true");
  expect(bru.getVar("header_disabled_excluded")).to.equal("true");
  const lower = JSON.parse(bru.getVar("header_object_lower"));
  expect(lower["x-only"]).to.equal("updated");
  expect(req.headerList.count()).to.equal(2);
  expect(req.headerList.get("x-only")).to.equal("updated");
  expect(req.headerList.has({ key: "X-Second" })).to.be.true;
  expect(req.headerList.indexOf({ key: "x-second", value: "two" })).to.be.above(-1);
  expect(req.headerList.toString()).to.include("X-Only: updated");
  expect(req.headers["X-Only"]).to.equal("updated");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &server.URL,
		Headers:   &headers,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("headerList mutation request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("headerList mutation test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeResponseHeaderListIsReadOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("response headerList is read-only", function () {
  expect(res.headerList.get("x-test")).to.equal("ok");
  expect(res.headerList.has("X-Test", "ok")).to.be.true;
  expect(res.headerList.has({ key: "X-Test" })).to.be.true;
  expect(res.headerList.indexOf({ key: "X-Test", value: "ok" })).to.be.above(-1);
  expect(res.headerList.toObject(false, false)["x-test"]).to.equal("ok");
  expect(function () { res.headerList.add({ key: "X-New", value: "no" }); }).to.throw("read-only");
  expect(function () { res.headerList.upsert("X-New", "no"); }).to.throw("read-only");
  expect(function () { res.headerList.remove("X-Test"); }).to.throw("read-only");
  expect(function () { res.headerList.clear(); }).to.throw("read-only");
  expect(function () { res.headerList.populate([]); }).to.throw("read-only");
  expect(function () { res.headerList.repopulate([]); }).to.throw("read-only");
  expect(function () { res.headerList.assimilate([]); }).to.throw("read-only");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &server.URL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("response headerList request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("response headerList read-only test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeCanSkipRequest(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `console.log("before skip");
bru.runner.skipRequest();`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, PreScript: &preScript}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("server should not have been called, got %d calls", got)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatal("missing skipped response")
	}
	if item.Response.StatusText != "Skipped" || item.Response.Status != 0 {
		t.Fatalf("unexpected skipped response: %#v", item.Response)
	}
	if len(state.NetworkLog) != 0 {
		t.Fatalf("skipped request should not create network log entries: %#v", state.NetworkLog)
	}
	if len(item.Timeline) != 1 || item.Timeline[0].Kind != "script" {
		t.Fatalf("skipped request should create script timeline entry: %#v", item.Timeline)
	}
	if len(item.Response.ScriptLogs) != 1 || item.Response.ScriptLogs[0].Message != "before skip" {
		t.Fatalf("skip script log was not captured: %#v", item.Response.ScriptLogs)
	}
}

func TestRequestOnFailHandlerRunsForTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	targetURL := server.URL
	server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `req.onFail(function (err) {
  console.warn("onFail", err.message.length > 0);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, PreScript: &preScript}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatal("missing failed response")
	}
	if item.Response.Error == "" {
		t.Fatalf("transport error was not preserved: %#v", item.Response)
	}
	if len(item.Response.ScriptLogs) != 1 || item.Response.ScriptLogs[0].Level != "warn" || item.Response.ScriptLogs[0].Message != "onFail true" {
		t.Fatalf("onFail console log was not captured: %#v", item.Response.ScriptLogs)
	}
}

func TestRequestOnFailHandlerErrorAugmentsResponseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	targetURL := server.URL
	server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `req.onFail(function () {
  throw new Error("handler failed");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, PreScript: &preScript}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatal("missing failed response")
	}
	if !strings.Contains(item.Response.Error, "onFail:") || !strings.Contains(item.Response.Error, "handler failed") {
		t.Fatalf("onFail handler error was not appended: %s", item.Response.Error)
	}
}

func TestJavaScriptRuntimeCanReadRequestCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "session=abc123" {
			t.Fatalf("stored cookie was not sent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cookie":true}`))
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, err := app.SaveCookie(CookieInput{
		Name:     "session",
		Value:    "abc123",
		Domain:   parsedURL.Hostname(),
		Path:     "/",
		Session:  true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tests := `test("bru cookies direct access", function () {
  expect(bru.cookies.count()).to.equal(1);
  expect(bru.cookies.has("session")).to.equal(true);
  expect(bru.cookies.get("session")).to.equal("abc123");
  expect(bru.cookies.toObject().session).to.equal("abc123");
  expect(bru.cookies.toString()).to.equal("session=abc123");
  expect(bru.cookies.all()[0].key).to.equal("session");
  expect(bru.cookies.one("session").value).to.equal("abc123");
  expect(bru.cookies.idx(0).name).to.equal("session");
  expect(bru.cookies.indexOf({ name: "session", value: "abc123" })).to.equal(0);
  expect(bru.cookies.find(function (cookie) { return cookie.name === "session"; }).value).to.equal("abc123");
  expect(bru.cookies.filter(function (cookie) { return cookie.value === "abc123"; }).length).to.equal(1);
  expect(bru.cookies.map(function (cookie) { return cookie.name; })[0]).to.equal("session");
  expect(bru.cookies.reduce(function (acc, cookie) { return acc + cookie.value; }, "")).to.equal("abc123");
  var eachName = "";
  bru.cookies.each(function (cookie) { eachName = cookie.name; });
  expect(eachName).to.equal("session");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("cookie script request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("cookie script test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeCanWriteRequestCookiesBeforeSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Cookie")
		if strings.Contains(got, "old=1") {
			t.Fatalf("pre-request removed cookie was still sent: %q", got)
		}
		if !strings.Contains(got, "script=from-pre") {
			t.Fatalf("pre-request script cookie was not sent: %q", got)
		}
		http.SetCookie(w, &http.Cookie{Name: "response", Value: "after", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, err := app.SaveCookie(CookieInput{
		Name:     "old",
		Value:    "1",
		Domain:   parsedURL.Hostname(),
		Path:     "/",
		Session:  true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	preScript := `bru.cookies.remove("old");
bru.cookies.add({ name: "script", value: "from-pre" });`
	postScript := `if (!bru.cookies.has("response", "after")) {
  throw new Error("response cookie missing from script jar");
}`
	tests := `test("writable cookies", function () {
  expect(bru.cookies.has("old")).to.equal(false);
  expect(bru.cookies.has("script", "from-pre")).to.equal(true);
  expect(bru.cookies.has("response", "after")).to.equal(true);
  expect(bru.cookies.one("script").value).to.equal("from-pre");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:        &server.URL,
		PreScript:  &preScript,
		PostScript: &postScript,
		Tests:      &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("writable cookie request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("writable cookie test did not pass: %#v", item.Response.TestResults)
	}
	cookiesByName := map[string]CookieEntry{}
	for _, cookie := range state.Cookies {
		cookiesByName[cookie.Name] = cookie
	}
	if _, ok := cookiesByName["old"]; ok {
		t.Fatalf("removed cookie persisted: %#v", state.Cookies)
	}
	if cookiesByName["script"].Value != "from-pre" || cookiesByName["response"].Value != "after" {
		t.Fatalf("script/response cookies were not persisted: %#v", state.Cookies)
	}
}

func TestJavaScriptRuntimeCookieJarCanWriteCrossURLCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/target" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "jarred=ok" {
			t.Fatalf("pre-request jar cookie was not sent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	targetURL := "{{host}}/target"
	preScript := `const jar = bru.cookies.jar();
await jar.setCookie("{{host}}/target", "jarred", "ok");
if (!(await jar.hasCookie("{{host}}/target", "jarred"))) {
  throw new Error("jarred cookie missing before send");
}`
	tests := `test("cross url cookie jar", async function () {
  const jar = bru.cookies.jar();
  expect((await jar.getCookie("{{host}}/target", "jarred")).value).to.equal("ok");
  expect((await jar.getCookies("{{host}}/target")).length).to.equal(1);
  await jar.deleteCookie("{{host}}/target", "jarred");
  expect(await jar.hasCookie("{{host}}/target", "jarred")).to.equal(false);
  await jar.setCookies("{{host}}/target", [{ name: "multi", value: "one" }]);
  expect((await jar.getCookie("{{host}}/target", "multi")).value).to.equal("one");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &targetURL,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("cross-url jar request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("cross-url jar test did not pass: %#v", item.Response.TestResults)
	}
	cookiesByName := map[string]CookieEntry{}
	for _, cookie := range state.Cookies {
		cookiesByName[cookie.Name] = cookie
	}
	if _, ok := cookiesByName["jarred"]; ok {
		t.Fatalf("deleted jar cookie persisted: %#v", state.Cookies)
	}
	if cookiesByName["multi"].Value != "one" {
		t.Fatalf("setCookies value was not persisted: %#v", state.Cookies)
	}
}

func TestJavaScriptRuntimeCookieJarSupportsCallbacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	targetURL := "{{host}}/target"
	tests := `test("cookie jar callback APIs", async function () {
  const jar = bru.cookies.jar();
  let setCalled = false;
  await jar.setCookie("{{host}}/target", "callbacked", "yes", function (err) {
    expect(err).to.be.null;
    setCalled = true;
  });
  expect(setCalled).to.equal(true);

  let gotValue = "";
  await jar.getCookie("{{host}}/target", "callbacked", function (err, cookie) {
    expect(err).to.be.null;
    gotValue = cookie.value;
  });
  expect(gotValue).to.equal("yes");

  let hasValue = false;
  await jar.hasCookie("{{host}}/target", "callbacked", function (err, hasCookie) {
    expect(err).to.be.null;
    hasValue = hasCookie;
  });
  expect(hasValue).to.equal(true);

  let cookieCount = 0;
  await jar.getCookies("{{host}}/target", function (err, cookies) {
    expect(err).to.be.null;
    cookieCount = cookies.length;
  });
  expect(cookieCount).to.equal(1);

  let objectSet = false;
  await jar.setCookie("{{host}}/target", { name: "objected", value: "one" }, function (err) {
    expect(err).to.be.null;
    objectSet = true;
  });
  expect(objectSet).to.equal(true);
  expect((await jar.getCookie("{{host}}/target", "objected")).value).to.equal("one");

  await jar.setCookie("{{host}}/target", "emptyByCallback", function (err) {
    expect(err).to.be.null;
  });
  expect((await jar.getCookie("{{host}}/target", "emptyByCallback")).value).to.equal("");

  let manySet = false;
  await jar.setCookies("{{host}}/target", [{ name: "multi_callback", value: "two" }], function (err) {
    expect(err).to.be.null;
    manySet = true;
  });
  expect(manySet).to.equal(true);
  expect((await jar.getCookie("{{host}}/target", "multi_callback")).value).to.equal("two");

  let deleted = false;
  await jar.deleteCookie("{{host}}/target", "callbacked", function (err) {
    expect(err).to.be.null;
    deleted = true;
  });
  expect(deleted).to.equal(true);
  expect(await jar.hasCookie("{{host}}/target", "callbacked")).to.equal(false);

  let deletedAll = false;
  await jar.deleteCookies("{{host}}/target", function (err) {
    expect(err).to.be.null;
    deletedAll = true;
  });
  expect(deletedAll).to.equal(true);
  expect((await jar.getCookies("{{host}}/target")).length).to.equal(0);

  await jar.setCookie("{{host}}/target", "last", "3");
  let cleared = false;
  await jar.clear(function (err) {
    expect(err).to.be.null;
    cleared = true;
  });
  expect(cleared).to.equal(true);
  expect(await jar.hasCookie("{{host}}/target", "last")).to.equal(false);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &targetURL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("cookie callback request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("cookie callback test did not pass: %#v", item.Response.TestResults)
	}
}

func TestJavaScriptRuntimeCookieJarSupportsPromiseAPIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	targetURL := "{{host}}/target"
	tests := `test("cookie jar promise APIs", async function () {
  const jar = bru.cookies.jar();
  await jar.clear();

  await jar.setCookie("{{host}}/target", { key: "auth", value: "token", path: "/" });
  await jar.setCookie("{{host}}/target", "theme", "dark");
  await jar.setCookies("{{host}}/target", [{ name: "multi", value: "one" }]);

  const authCookie = await jar.getCookie("{{host}}/target", "auth");
  expect(authCookie.key).to.equal("auth");
  expect(authCookie.value).to.equal("token");
  expect(await jar.hasCookie("{{host}}/target", "theme")).to.equal(true);

  const cookies = await jar.getCookies("{{host}}/target");
  expect(cookies.map(function (cookie) { return cookie.name; })).to.include("multi");

  await jar.deleteCookie("{{host}}/target", "theme");
  expect(await jar.hasCookie("{{host}}/target", "theme")).to.equal(false);

  await jar.deleteCookies("{{host}}/target");
  expect((await jar.getCookies("{{host}}/target")).length).to.equal(0);
});

test("cookie jar callback forms are awaitable", async function () {
  const jar = bru.cookies.jar();
  await jar.clear();

  let setCalled = false;
  await jar.setCookie("{{host}}/target", "callback", "yes", function (err) {
    expect(err).to.be.null;
    setCalled = true;
  });
  expect(setCalled).to.equal(true);

  let callbackValue = "";
  await jar.getCookie("{{host}}/target", "callback", function (err, cookie) {
    expect(err).to.be.null;
    callbackValue = cookie.value;
  });
  expect(callbackValue).to.equal("yes");

  let rejected = false;
  try {
    await jar.setCookie("not a url", "bad", "value");
  } catch (err) {
    rejected = true;
    expect(err.message).to.include("current request URL");
  }
  expect(rejected).to.equal(true);

  let handledError = false;
  await jar.setCookie("not a url", "bad", "value", function (err) {
    handledError = !!err;
  });
  expect(handledError).to.equal(true);
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:   &targetURL,
		Tests: &tests,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("cookie promise request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 2 {
		t.Fatalf("unexpected cookie promise test count: %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("cookie promise test did not pass: %#v", item.Response.TestResults)
		}
	}
}

func TestCookieStoreSendsSecureCookiesToLoopbackHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "secure_local=ok" {
			t.Fatalf("secure loopback cookie was not sent: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, err := app.SaveCookie(CookieInput{
		Name:     "secure_local",
		Value:    "ok",
		Domain:   parsedURL.Hostname(),
		Path:     "/",
		Session:  true,
		Secure:   true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("secure loopback request failed: %#v", item.Response)
	}
}

func TestCookiePersistenceEncryptsValuesAndHydrates(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "test-cookie-persistence-key")
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.SaveCookie(CookieInput{
		Name:     "session",
		Value:    "plain-cookie-secret",
		Domain:   "example.com",
		Path:     "/",
		Session:  true,
		HTTPOnly: true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 1 || state.Cookies[0].Value != "plain-cookie-secret" {
		t.Fatalf("cookie should remain plaintext in runtime state: %#v", state.Cookies)
	}
	flushPersistForTest(t, app)
	stateData, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateData), "plain-cookie-secret") || !strings.Contains(string(stateData), "$01:") {
		t.Fatalf("state.json did not encrypt cookie value: %s", stateData)
	}
	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedState.Cookies) != 1 || reloadedState.Cookies[0].Value != "plain-cookie-secret" {
		t.Fatalf("encrypted cookie was not hydrated: %#v", reloadedState.Cookies)
	}
}

func TestCookiePrefixValidationMatchesBrunoJarRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Cookie")
		if strings.Contains(got, "__Host-BAD") || strings.Contains(got, "__Secure-BAD") {
			t.Fatalf("invalid prefixed script cookie was sent: %q", got)
		}
		if !strings.Contains(got, "__Host-OK=script") {
			t.Fatalf("valid __Host- script cookie was not sent: %q", got)
		}
		w.Header().Add("Set-Cookie", "__Secure-RESPBAD=bad; Path=/")
		w.Header().Add("Set-Cookie", "__Host-RESPBAD=bad; Path=/; Secure; Domain=127.0.0.1")
		w.Header().Add("Set-Cookie", "__Host-RESP=ok; Path=/; Secure")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"prefix":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	if _, err := app.SaveCookie(CookieInput{
		Name:     "__Secure-MANUAL",
		Value:    "bad",
		Domain:   "127.0.0.1",
		Path:     "/",
		Session:  true,
		HostOnly: true,
	}); err == nil {
		t.Fatal("expected invalid __Secure- manual cookie to be rejected")
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	targetURL := "{{host}}/prefix"
	preScript := `const jar = bru.cookies.jar();
await jar.setCookie("{{host}}/prefix", "__Host-BAD", "bad").catch(function () {});
await jar.setCookie("{{host}}/prefix", { key: "__Host-BADDOMAIN", value: "bad", path: "/", secure: true, domain: "127.0.0.1" }).catch(function () {});
await jar.setCookie("{{host}}/prefix", { key: "__Secure-BAD", value: "bad", path: "/" }).catch(function () {});
await jar.setCookie("{{host}}/prefix", { key: "__Host-OK", value: "script", path: "/", secure: true });`
	tests := `test("prefixed cookies", async function () {
  const jar = bru.cookies.jar();
  expect(await jar.hasCookie("{{host}}/prefix", "__Host-BAD")).to.equal(false);
  expect(await jar.hasCookie("{{host}}/prefix", "__Host-BADDOMAIN")).to.equal(false);
  expect(await jar.hasCookie("{{host}}/prefix", "__Secure-BAD")).to.equal(false);
  expect((await jar.getCookie("{{host}}/prefix", "__Host-OK")).value).to.equal("script");
  expect((await jar.getCookie("{{host}}/prefix", "__Host-RESP")).value).to.equal("ok");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, PreScript: &preScript, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("prefix validation request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("prefix validation test did not pass: %#v", item.Response.TestResults)
	}
	cookiesByName := map[string]CookieEntry{}
	for _, cookie := range state.Cookies {
		cookiesByName[cookie.Name] = cookie
	}
	for _, forbidden := range []string{"__Host-BAD", "__Host-BADDOMAIN", "__Secure-BAD", "__Secure-RESPBAD", "__Host-RESPBAD"} {
		if _, ok := cookiesByName[forbidden]; ok {
			t.Fatalf("invalid prefixed cookie persisted: %s in %#v", forbidden, state.Cookies)
		}
	}
	if cookiesByName["__Host-OK"].Value != "script" || cookiesByName["__Host-RESP"].Value != "ok" {
		t.Fatalf("valid prefixed cookies were not persisted: %#v", state.Cookies)
	}
}

func TestCookieRuntimeValidationRejectsForeignPublicSuffixAndNormalizesPaths(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.SaveCookie(CookieInput{
		Name:     "manual_public",
		Value:    "bad",
		Domain:   "com",
		Path:     "/",
		Session:  true,
		HostOnly: true,
	}); err == nil {
		t.Fatal("expected public suffix manual cookie to be rejected")
	}
	if _, err := app.SaveCookie(CookieInput{
		Name:     "manual_ip_domain",
		Value:    "bad",
		Domain:   "127.0.0.1",
		Path:     "/",
		Session:  true,
		HostOnly: false,
	}); err == nil {
		t.Fatal("expected non-host-only IP cookie to be rejected")
	}
	state, err := app.SaveCookie(CookieInput{
		Name:     "kill",
		Value:    "old",
		Domain:   "127.0.0.1",
		Path:     "/",
		Session:  true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 1 {
		t.Fatalf("expected seed cookie: %#v", state.Cookies)
	}
	state, err = app.AddCookieFromHeader(strings.Join([]string{
		"foreign=bad; Domain=evil.com; Path=/",
		"public=bad; Domain=com; Path=/",
		"ipdomain=bad; Domain=127.0.0.1; Path=/",
		"host=ok; Path=api",
		"kill=gone; Path=/; Max-Age=0",
	}, "\n"), "http://127.0.0.1/api/list")
	if err != nil {
		t.Fatal(err)
	}
	cookiesByName := map[string]CookieEntry{}
	for _, cookie := range state.Cookies {
		cookiesByName[cookie.Name] = cookie
	}
	for _, forbidden := range []string{"foreign", "public", "ipdomain", "kill"} {
		if _, ok := cookiesByName[forbidden]; ok {
			t.Fatalf("invalid or expired cookie persisted: %s in %#v", forbidden, state.Cookies)
		}
	}
	host := cookiesByName["host"]
	if host.Value != "ok" || host.Domain != "127.0.0.1" || host.Path != "/" || !host.HostOnly {
		t.Fatalf("host-only raw cookie was not normalized like tough-cookie: %#v", host)
	}
}

func TestJavaScriptRuntimeCookieJarRejectsInvalidDomainsAndNormalizesPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Cookie")
		if strings.Contains(got, "foreign=") || strings.Contains(got, "public=") || strings.Contains(got, "ipdomain=") {
			t.Fatalf("invalid script cookie was sent: %q", got)
		}
		if !strings.Contains(got, "scriptPath=ok") {
			t.Fatalf("normalized path script cookie was not sent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cookie":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	targetURL := "{{host}}/api/list"
	preScript := `const jar = bru.cookies.jar();
await jar.setCookie("{{host}}/api/list", { key: "foreign", value: "bad", domain: "evil.com" });
await jar.setCookie("{{host}}/api/list", { key: "public", value: "bad", domain: "com" });
await jar.setCookie("{{host}}/api/list", { key: "ipdomain", value: "bad", domain: "127.0.0.1" });
await jar.setCookie("{{host}}/api/list", { key: "scriptPath", value: "ok", path: "api" });`
	tests := `test("script cookie validation", async function () {
  const jar = bru.cookies.jar();
  expect(await jar.hasCookie("https://evil.com/", "foreign")).to.equal(false);
  expect(await jar.hasCookie("https://example.com/", "public")).to.equal(false);
  expect(await jar.hasCookie("http://127.0.0.1/", "ipdomain")).to.equal(false);
  const cookie = await jar.getCookie("{{host}}/anywhere", "scriptPath");
  expect(cookie.value).to.equal("ok");
  expect(cookie.path).to.equal("/");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, PreScript: &preScript, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("script cookie validation request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("script cookie validation test did not pass: %#v", item.Response.TestResults)
	}
	cookiesByName := map[string]CookieEntry{}
	for _, cookie := range state.Cookies {
		cookiesByName[cookie.Name] = cookie
	}
	for _, forbidden := range []string{"foreign", "public", "ipdomain"} {
		if _, ok := cookiesByName[forbidden]; ok {
			t.Fatalf("invalid script cookie persisted: %s in %#v", forbidden, state.Cookies)
		}
	}
	if cookiesByName["scriptPath"].Path != "/" {
		t.Fatalf("script cookie path was not normalized: %#v", state.Cookies)
	}
}

func TestCookieStoreCapturesSendsAndDeletesCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/", HttpOnly: true})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"set":true}`))
		case "/check":
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "session=abc123") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"cookie":false}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cookie":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	setURL := server.URL + "/set"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &setURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 1 || state.Cookies[0].Name != "session" || !state.Cookies[0].HTTPOnly {
		t.Fatalf("cookie was not captured: %#v", state.Cookies)
	}

	checkURL := server.URL + "/check"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &checkURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("stored cookie was not sent: %#v", item.Response)
	}

	state, err = app.DeleteCookie(state.Cookies[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 0 {
		t.Fatalf("cookie was not deleted: %#v", state.Cookies)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusUnauthorized {
		t.Fatalf("deleted cookie should not be sent: %#v", item.Response)
	}
}

func TestCookieStoreCapturesAndSendsRedirectCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "hop", Value: "one", Path: "/next"})
			http.Redirect(w, r, "/next", http.StatusFound)
		case "/next":
			if got := r.Header.Get("Cookie"); got != "hop=one" {
				t.Fatalf("redirect cookie was not sent to target: %q", got)
			}
			http.SetCookie(w, &http.Cookie{Name: "final", Value: "done", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"redirect":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	startURL := server.URL + "/start"
	tests := fmt.Sprintf(`test("redirect cookies", async function () {
  expect(await bru.cookies.jar().hasCookie("%s/next", "hop")).to.equal(true);
  expect(bru.cookies.has("final", "done")).to.equal(true);
});`, server.URL)
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &startURL, Tests: &tests}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("redirect request failed: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("redirect cookie test did not pass: %#v", item.Response.TestResults)
	}
	if len(item.Response.Cookies) != 2 {
		t.Fatalf("response did not include redirect and final cookies: %#v", item.Response.Cookies)
	}
	cookiesByName := map[string]CookieEntry{}
	for _, cookie := range state.Cookies {
		cookiesByName[cookie.Name] = cookie
	}
	if cookiesByName["hop"].Value != "one" || cookiesByName["hop"].Path != "/next" || cookiesByName["final"].Value != "done" {
		t.Fatalf("redirect cookies were not persisted correctly: %#v", state.Cookies)
	}
}

func TestCookieStoreCapturesRedirectCookieWhenRedirectsDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "redirect_only", Value: "saved", Path: "/"})
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	settings := item.Settings
	settings.FollowRedirects = false
	startURL := server.URL + "/start"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &startURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusFound {
		t.Fatalf("redirect response was not preserved: %#v", item.Response)
	}
	if len(state.Cookies) != 1 || state.Cookies[0].Name != "redirect_only" || state.Cookies[0].Value != "saved" {
		t.Fatalf("redirect cookie was not captured when redirects disabled: %#v", state.Cookies)
	}
}

func TestCookieStoreMergesManualCookieHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "manual=keep; session=abc123" {
			t.Fatalf("manual and stored cookies were not merged like Bruno: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newAppForTest(t)
	state, err := app.SaveCookie(CookieInput{
		Name:     "session",
		Value:    "abc123",
		Domain:   parsedURL.Hostname(),
		Path:     "/",
		Session:  true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	headers := []KeyValue{{Name: "Cookie", Value: "manual=keep; session=old", Enabled: true}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &server.URL, Headers: &headers}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("merged cookie request failed: %#v", item.Response)
	}
}

func TestCookieManagerManualRawAndDomainClear(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.SaveCookie(CookieInput{
		Name:     "manual",
		Value:    "one",
		Domain:   ".Example.COM",
		Path:     "api",
		Session:  true,
		HTTPOnly: true,
		HostOnly: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 1 {
		t.Fatalf("expected one cookie, got %#v", state.Cookies)
	}
	manual := state.Cookies[0]
	if manual.Domain != "example.com" || manual.Path != "/api" || manual.ID == "" || !manual.HTTPOnly || manual.HostOnly {
		t.Fatalf("manual cookie was not normalized: %#v", manual)
	}

	updatedExpiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	state, err = app.SaveCookie(CookieInput{
		ID:       manual.ID,
		Name:     "manual",
		Value:    "two",
		Domain:   "example.com",
		Path:     "/api",
		Expires:  updatedExpiry,
		Session:  false,
		SameSite: "Strict",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 1 || state.Cookies[0].Value != "two" || state.Cookies[0].SameSite != "strict" || state.Cookies[0].Session {
		t.Fatalf("manual cookie was not updated in place: %#v", state.Cookies)
	}

	state, err = app.AddCookieFromHeader("raw=ok; Path=/v1; Max-Age=3600; Secure; SameSite=Lax", "https://api.example.com/v1/list")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 2 {
		t.Fatalf("expected raw cookie to be imported: %#v", state.Cookies)
	}
	var raw CookieEntry
	for _, cookie := range state.Cookies {
		if cookie.Name == "raw" {
			raw = cookie
		}
	}
	if raw.Domain != "api.example.com" || raw.Path != "/v1" || !raw.Secure || raw.SameSite != "lax" || raw.Session || raw.Expires.IsZero() {
		t.Fatalf("raw Set-Cookie was not parsed correctly: %#v", raw)
	}

	state, err = app.ClearDomainCookies("api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cookies) != 1 || state.Cookies[0].Name != "manual" {
		t.Fatalf("domain clear removed the wrong cookies: %#v", state.Cookies)
	}
}

func TestNotificationsMarkReadAndClearPersist(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.SaveCookie(CookieInput{
		Name:     "notify",
		Value:    "one",
		Domain:   "api.example.com",
		Path:     "/",
		Session:  true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Notifications) != 1 {
		t.Fatalf("expected cookie save notification, got %#v", state.Notifications)
	}
	notification := state.Notifications[0]
	if notification.Read || notification.Title == "" || notification.Description == "" || notification.Type != "Success" {
		t.Fatalf("notification was not normalized for display: %#v", notification)
	}
	state, err = app.MarkNotificationRead(notification.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Notifications[0].Read {
		t.Fatalf("notification was not marked read: %#v", state.Notifications[0])
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedState.Notifications) != 1 || !reloadedState.Notifications[0].Read {
		t.Fatalf("read notification was not persisted: %#v", reloadedState.Notifications)
	}
	reloadedState, err = reloaded.SaveCookie(CookieInput{
		Name:     "notify-two",
		Value:    "two",
		Domain:   "api.example.com",
		Path:     "/",
		Session:  true,
		HostOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedState.Notifications) != 2 || reloadedState.Notifications[0].Read {
		t.Fatalf("expected a new unread notification: %#v", reloadedState.Notifications)
	}
	reloadedState, err = reloaded.MarkAllNotificationsRead()
	if err != nil {
		t.Fatal(err)
	}
	for _, notification := range reloadedState.Notifications {
		if !notification.Read {
			t.Fatalf("mark all read missed notification: %#v", reloadedState.Notifications)
		}
	}
	reloadedState, err = reloaded.ClearNotifications()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedState.Notifications) != 0 {
		t.Fatalf("notifications were not cleared: %#v", reloadedState.Notifications)
	}
}

func TestOpenRequestTabCreatesTabForExistingItem(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Second"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	second := collection.Items[1]
	state, err = app.OpenRequestTab(collection.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	active := state.OpenTabs[len(state.OpenTabs)-1]
	if state.ActiveTabID != collection.ID+":"+second.ID || active.ItemID != second.ID {
		t.Fatalf("request tab was not opened: %#v active=%s", state.OpenTabs, state.ActiveTabID)
	}
}

func TestOpenResponseExampleTabCreatesTabForSavedExample(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	example := ResponseExample{
		ID:   "example-open-tab",
		Name: "Opened Example",
		Request: ResponseExampleRequest{
			Method: http.MethodGet,
			URL:    "https://api.example.test/opened",
		},
		Response: ResponseExamplePayload{Status: http.StatusOK, StatusText: "OK"},
	}
	app.state.Workspaces[0].Collections[0].Items[0].Examples = []ResponseExample{example}
	initialTabCount := len(app.state.OpenTabs)

	state, err = app.OpenResponseExampleTab(collection.ID, item.ID, example.ID)
	if err != nil {
		t.Fatal(err)
	}
	active := state.OpenTabs[len(state.OpenTabs)-1]
	expectedTabID := collection.ID + ":" + item.ID + ":example:" + example.ID
	if state.ActiveTabID != expectedTabID || active.ID != expectedTabID || active.Kind != "response-example" || active.ItemID != item.ID || active.ExampleID != example.ID || active.ExampleName != example.Name || active.ResponseTab != "examples" {
		t.Fatalf("response example tab was not opened correctly: %#v active=%s", active, state.ActiveTabID)
	}
	if len(state.OpenTabs) != initialTabCount+1 {
		t.Fatalf("response example tab count mismatch: %#v", state.OpenTabs)
	}

	app.state.Workspaces[0].Collections[0].Items[0].Examples[0].Name = "Renamed Example Tab"
	state, err = app.OpenResponseExampleTab(collection.ID, item.ID, example.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.OpenTabs) != initialTabCount+1 || state.OpenTabs[len(state.OpenTabs)-1].ExampleName != "Renamed Example Tab" {
		t.Fatalf("opening the same response example should reuse/update its tab: %#v", state.OpenTabs)
	}
}

func TestUpdateOpenTabPanesPersistsPaneSelection(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	tabID := state.OpenTabs[0].ID
	if state.OpenTabs[0].CollectionID != collection.ID || state.OpenTabs[0].ItemID != item.ID {
		t.Fatalf("default open tab does not target the first request: %#v", state.OpenTabs[0])
	}
	state, err = app.UpdateOpenTabPanes(tabID, "body", "headers")
	if err != nil {
		t.Fatal(err)
	}
	tab, ok := findOpenTab(state.OpenTabs, tabID)
	if !ok || tab.RequestPaneTab != "body" || tab.ResponseTab != "headers" {
		t.Fatalf("tab pane selection was not updated: tab=%#v ok=%v", tab, ok)
	}
	state, err = app.UpdateOpenTabPanes(tabID, "", "metadata")
	if err != nil {
		t.Fatal(err)
	}
	tab, ok = findOpenTab(state.OpenTabs, tabID)
	if !ok || tab.ResponseTab != "metadata" {
		t.Fatalf("gRPC metadata pane selection was not updated: tab=%#v ok=%v", tab, ok)
	}
	state, err = app.UpdateOpenTabPanes(tabID, "", "trailers")
	if err != nil {
		t.Fatal(err)
	}
	tab, ok = findOpenTab(state.OpenTabs, tabID)
	if !ok || tab.ResponseTab != "trailers" {
		t.Fatalf("gRPC trailers pane selection was not updated: tab=%#v ok=%v", tab, ok)
	}
	if _, err := app.UpdateOpenTabPanes(tabID, "bogus", "headers"); err == nil {
		t.Fatalf("expected invalid request pane tab to fail")
	}
	if _, err := app.UpdateOpenTabPanes(tabID, "params", "bogus"); err == nil {
		t.Fatalf("expected invalid response pane tab to fail")
	}
	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, app.dataDir)
	state, err = reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	tab, ok = findOpenTab(state.OpenTabs, tabID)
	if !ok || tab.RequestPaneTab != "body" || tab.ResponseTab != "trailers" {
		t.Fatalf("tab pane selection was not persisted: tab=%#v ok=%v", tab, ok)
	}
}

func TestOpenTabManagementPersistsOrderAndActiveState(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Second"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateRequest(collection.ID, "http", "Third"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	firstTabID := state.OpenTabs[0].ID
	secondID := collection.Items[1].ID
	thirdID := collection.Items[2].ID
	secondTabID := collection.ID + ":" + secondID
	thirdTabID := collection.ID + ":" + thirdID
	if state, err = app.OpenRequestTab(collection.ID, secondID); err != nil {
		t.Fatal(err)
	}
	if state, err = app.OpenRequestTab(collection.ID, thirdID); err != nil {
		t.Fatal(err)
	}
	if got := []string{state.OpenTabs[0].ID, state.OpenTabs[1].ID, state.OpenTabs[2].ID}; !reflect.DeepEqual(got, []string{firstTabID, secondTabID, thirdTabID}) {
		t.Fatalf("unexpected initial tab order: %#v", state.OpenTabs)
	}
	state, err = app.MoveOpenTab(thirdTabID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{state.OpenTabs[0].ID, state.OpenTabs[1].ID, state.OpenTabs[2].ID}; !reflect.DeepEqual(got, []string{firstTabID, thirdTabID, secondTabID}) {
		t.Fatalf("moving active tab left failed: %#v", state.OpenTabs)
	}
	if state.ActiveTabID != thirdTabID {
		t.Fatalf("moving a tab should keep it active, active=%s", state.ActiveTabID)
	}
	state, err = app.MoveOpenTab(thirdTabID, -99)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{state.OpenTabs[0].ID, state.OpenTabs[1].ID, state.OpenTabs[2].ID}; !reflect.DeepEqual(got, []string{thirdTabID, firstTabID, secondTabID}) {
		t.Fatalf("moving active tab to start failed: %#v", state.OpenTabs)
	}
	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, app.dataDir)
	state, err = reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{state.OpenTabs[0].ID, state.OpenTabs[1].ID, state.OpenTabs[2].ID}; !reflect.DeepEqual(got, []string{thirdTabID, firstTabID, secondTabID}) {
		t.Fatalf("tab order was not persisted: %#v", state.OpenTabs)
	}
	state, err = reloaded.CloseTab(thirdTabID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != secondTabID || len(state.OpenTabs) != 2 {
		t.Fatalf("closing active tab should focus the last remaining tab: active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if len(state.ClosedTabs) != 1 || state.ClosedTabs[0].ID != thirdTabID {
		t.Fatalf("closed tab history was not recorded: %#v", state.ClosedTabs)
	}
	state, err = reloaded.ReopenLastClosedTab(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != thirdTabID || len(state.OpenTabs) != 3 || len(state.ClosedTabs) != 0 {
		t.Fatalf("reopening closed tab failed: active=%s tabs=%#v closed=%#v", state.ActiveTabID, state.OpenTabs, state.ClosedTabs)
	}
	if got := state.OpenTabs[len(state.OpenTabs)-1].ID; got != thirdTabID {
		t.Fatalf("reopened tab should be appended as the active tab, got %s tabs=%#v", got, state.OpenTabs)
	}
	if _, err := reloaded.CloseTab("missing-tab"); err == nil {
		t.Fatalf("closing a missing tab should fail")
	}
	if _, err := reloaded.MoveOpenTab("missing-tab", 1); err == nil {
		t.Fatalf("moving a missing tab should fail")
	}
	state, err = reloaded.CloseAllTabs()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.OpenTabs) != 0 || state.ActiveTabID != "" {
		t.Fatalf("close all tabs failed: active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if len(state.ClosedTabs) != 3 {
		t.Fatalf("close all should preserve reopen history for each tab: %#v", state.ClosedTabs)
	}
	state, err = reloaded.ReopenLastClosedTab("")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != thirdTabID || len(state.OpenTabs) != 1 || state.OpenTabs[0].ID != thirdTabID {
		t.Fatalf("reopen after close all restored the wrong tab: active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	reloaded.state.OpenTabs = nil
	reloaded.state.ActiveTabID = ""
	reloaded.state.ClosedTabs = []OpenTab{{ID: "missing-tab", CollectionID: "missing-collection", ItemID: "missing-item", Kind: "request"}}
	state, err = reloaded.ReopenLastClosedTab("")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.OpenTabs) != 0 || len(state.ClosedTabs) != 0 || state.ActiveTabID != "" {
		t.Fatalf("stale closed tab entries should be discarded: active=%s tabs=%#v closed=%#v", state.ActiveTabID, state.OpenTabs, state.ClosedTabs)
	}
	otherState, err := reloaded.CreateCollection(state.ActiveWorkspaceID, "Other API", "yml")
	if err != nil {
		t.Fatal(err)
	}
	otherCollection := otherState.Workspaces[0].Collections[len(otherState.Workspaces[0].Collections)-1]
	if _, err := reloaded.CreateRequest(otherCollection.ID, "http", "Other Request"); err != nil {
		t.Fatal(err)
	}
	otherState, err = reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	otherCollection = otherState.Workspaces[0].Collections[len(otherState.Workspaces[0].Collections)-1]
	if len(otherCollection.Items) == 0 {
		t.Fatalf("other collection request was not created: %#v", otherCollection)
	}
	otherItemID := otherCollection.Items[0].ID
	otherTabID := otherCollection.ID + ":" + otherItemID
	if state, err = reloaded.OpenRequestTab(collection.ID, secondID); err != nil {
		t.Fatal(err)
	}
	if state, err = reloaded.OpenRequestTab(otherCollection.ID, otherItemID); err != nil {
		t.Fatal(err)
	}
	if _, err = reloaded.CloseTab(secondTabID); err != nil {
		t.Fatal(err)
	}
	if _, err = reloaded.CloseTab(otherTabID); err != nil {
		t.Fatal(err)
	}
	state, err = reloaded.ReopenLastClosedTab(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != secondTabID {
		t.Fatalf("scoped reopen should restore the latest closed tab for the requested collection: active=%s closed=%#v", state.ActiveTabID, state.ClosedTabs)
	}
	state, err = reloaded.ReopenLastClosedTab("")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != otherTabID {
		t.Fatalf("global reopen should restore the latest remaining closed tab: active=%s closed=%#v", state.ActiveTabID, state.ClosedTabs)
	}
}

func findOpenTab(tabs []OpenTab, id string) (OpenTab, bool) {
	for _, tab := range tabs {
		if tab.ID == id {
			return tab, true
		}
	}
	return OpenTab{}, false
}

func TestRunCollectionCountsPassFailAndSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fail") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.UpdateCollectionVariables(collection.ID, []Variable{{ID: "host", Name: "host", Value: server.URL, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateRequest(collection.ID, "http", "Failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateRequest(collection.ID, "websocket", "Socket"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	fail := collection.Items[1]
	failURL := "{{host}}/fail"
	if _, err := app.UpdateRequest(collection.ID, fail.ID, RequestPatch{URL: &failURL}); err != nil {
		t.Fatal(err)
	}

	state, err = app.RunCollection(collection.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if state.Runner.Total != 3 || state.Runner.Passed != 1 || state.Runner.Failed != 1 || state.Runner.Skipped != 1 {
		t.Fatalf("unexpected runner counts: %#v", state.Runner)
	}
}

func TestRunCollectionWithOptionsSelectsRequestsAndAppliesDelay(t *testing.T) {
	var mu sync.Mutex
	paths := []string{}
	times := []time.Time{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		times = append(times, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Second"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateRequest(collection.ID, "http", "Third"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	first := collection.Items[0]
	second := collection.Items[1]
	third := collection.Items[2]
	for _, pair := range []struct {
		item RequestItem
		path string
	}{
		{first, "/first"},
		{second, "/second"},
		{third, "/third"},
	} {
		targetURL := server.URL + pair.path
		if _, err := app.UpdateRequest(collection.ID, pair.item.ID, RequestPatch{URL: &targetURL}); err != nil {
			t.Fatal(err)
		}
	}

	state, err = app.RunCollectionWithOptions(collection.ID, "", RunnerOptions{
		SelectedItemIDs: []string{second.ID, third.ID},
		DelayMs:         25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Runner.Total != 2 || state.Runner.Passed != 2 || state.Runner.Failed != 0 || state.Runner.Skipped != 0 {
		t.Fatalf("unexpected filtered runner counts: %#v", state.Runner)
	}
	if state.Runner.Results[0].ItemID != second.ID || state.Runner.Results[1].ItemID != third.ID {
		t.Fatalf("runner did not preserve selected request order: %#v", state.Runner.Results)
	}
	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	gotTimes := append([]time.Time(nil), times...)
	mu.Unlock()
	if !reflect.DeepEqual(gotPaths, []string{"/second", "/third"}) {
		t.Fatalf("runner executed wrong request paths: %#v", gotPaths)
	}
	if len(gotTimes) != 2 || gotTimes[1].Sub(gotTimes[0]) < 20*time.Millisecond {
		t.Fatalf("runner delay was not applied between selected requests: %#v", gotTimes)
	}
}

func TestRunCollectionSkipsPromptVariableRequests(t *testing.T) {
	var promptCalls int32
	var normalCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prompt":
			atomic.AddInt32(&promptCalls, 1)
		case "/ok":
			atomic.AddInt32(&normalCalls, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Normal"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	promptRequest := collection.Items[0]
	normalRequest := collection.Items[1]
	promptURL := server.URL + "/prompt?name={{runner_prompt}}"
	normalURL := server.URL + "/ok"
	body := RequestBody{Mode: "none"}
	vars := RequestVars{Req: []Variable{
		{ID: "runner-prompt", Name: "runner_prompt", Value: "{{?Runner Name}}", DataType: "string", Enabled: true},
	}}
	if _, err := app.UpdateRequest(collection.ID, promptRequest.ID, RequestPatch{URL: &promptURL, Body: &body, Vars: &vars}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, normalRequest.ID, RequestPatch{URL: &normalURL}); err != nil {
		t.Fatal(err)
	}

	state, err = app.RunCollection(collection.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&promptCalls); got != 0 {
		t.Fatalf("runner should skip prompt request before network execution, got %d calls", got)
	}
	if got := atomic.LoadInt32(&normalCalls); got != 1 {
		t.Fatalf("runner should continue after prompt skip, got %d normal calls", got)
	}
	if state.Runner.Total != 2 || state.Runner.Passed != 1 || state.Runner.Failed != 0 || state.Runner.Skipped != 1 {
		t.Fatalf("unexpected runner prompt skip counts: %#v", state.Runner)
	}
	if state.Runner.Results[0].Status != "skipped" || state.Runner.Results[0].Code != 0 || state.Runner.Results[0].DurationMs != 0 {
		t.Fatalf("prompt request was not recorded as skipped: %#v", state.Runner.Results[0])
	}
	if !strings.Contains(state.Runner.Results[0].Error, "Prompt variables detected") || !strings.Contains(state.Runner.Results[0].Error, "Runner Name") {
		t.Fatalf("prompt skip error did not name prompt variables: %#v", state.Runner.Results[0])
	}
	if state.Runner.Results[1].Status != "passed" || state.Runner.Results[1].Code != http.StatusOK {
		t.Fatalf("normal request did not pass after prompt skip: %#v", state.Runner.Results[1])
	}
}

func TestRunCollectionPersistsRuntimeVariablesAcrossRequests(t *testing.T) {
	var checkedCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/checked" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt32(&checkedCalls, 1)
		if got := r.Header.Get("X-Runner-Token"); got != "runner-secret" {
			t.Fatalf("runner did not interpolate persisted runtime variable into header: %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Uses Runtime"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	setter := collection.Items[0]
	consumer := collection.Items[1]
	setterURL := server.URL + "/set"
	setterPostScript := `bru.setVar("runner_token", "runner-secret");`
	if _, err := app.UpdateRequest(collection.ID, setter.ID, RequestPatch{URL: &setterURL, PostScript: &setterPostScript}); err != nil {
		t.Fatal(err)
	}
	consumerURL := server.URL + "/checked"
	consumerHeaders := []KeyValue{{Name: "X-Runner-Token", Value: "{{runner_token}}", Enabled: true}}
	consumerTests := `test("runner runtime variable visible", function () {
  expect(bru.getVar("runner_token")).to.equal("runner-secret");
});`
	if _, err := app.UpdateRequest(collection.ID, consumer.ID, RequestPatch{URL: &consumerURL, Headers: &consumerHeaders, Tests: &consumerTests}); err != nil {
		t.Fatal(err)
	}

	state, err = app.RunCollection(collection.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&checkedCalls); got != 1 {
		t.Fatalf("runner should execute runtime-variable consumer once, got %d", got)
	}
	if state.Runner.Total != 2 || state.Runner.Passed != 2 || state.Runner.Failed != 0 || state.Runner.Skipped != 0 {
		t.Fatalf("unexpected runner runtime-variable counts: %#v", state.Runner)
	}
	collection = state.Workspaces[0].Collections[0]
	runtimeVars := variablesByName(collection.RuntimeVariables)
	if runtimeVars["runner_token"].Value != "runner-secret" {
		t.Fatalf("runtime variable was not persisted to collection: %#v", collection.RuntimeVariables)
	}
	consumer, ok := findItemInState(state, collection.ID, consumer.ID)
	if !ok || consumer.Response == nil || len(consumer.Response.TestResults) != 1 || !consumer.Response.TestResults[0].Passed {
		t.Fatalf("consumer did not see persisted runtime variable in tests: %#v", consumer.Response)
	}
}

func TestRunCollectionHonorsStopExecution(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.CreateRequest(collection.ID, "http", "Second"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	first := collection.Items[0]
	second := collection.Items[1]
	firstURL := server.URL + "/first"
	secondURL := server.URL + "/second"
	postScript := `bru.runner.stopExecution();`
	if _, err := app.UpdateRequest(collection.ID, first.ID, RequestPatch{URL: &firstURL, PostScript: &postScript}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, second.ID, RequestPatch{URL: &secondURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.RunCollection(collection.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("runner should stop after first request, got %d calls", got)
	}
	if state.Runner.Total != 1 || state.Runner.Passed != 1 || state.Runner.Failed != 0 || state.Runner.Skipped != 0 {
		t.Fatalf("unexpected runner stop counts: %#v", state.Runner)
	}
	if state.Runner.Results[0].ItemID != first.ID {
		t.Fatalf("runner recorded wrong request: %#v", state.Runner.Results)
	}
}

func TestRunCollectionHonorsSetNextRequest(t *testing.T) {
	var secondCalls int32
	var fourthCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/second":
			atomic.AddInt32(&secondCalls, 1)
		case "/fourth":
			atomic.AddInt32(&fourthCalls, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	for _, name := range []string{"Second", "Third", "Fourth"} {
		if _, err := app.CreateRequest(collection.ID, "http", name); err != nil {
			t.Fatal(err)
		}
	}
	state, err = app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	first := collection.Items[0]
	second := collection.Items[1]
	third := collection.Items[2]
	fourth := collection.Items[3]
	firstURL := server.URL + "/first"
	secondURL := server.URL + "/second"
	thirdURL := server.URL + "/third"
	fourthURL := server.URL + "/fourth"
	firstPostScript := `bru.runner.setNextRequest("Third");`
	thirdPostScript := `bru.setNextRequest(null);`
	if _, err := app.UpdateRequest(collection.ID, first.ID, RequestPatch{URL: &firstURL, PostScript: &firstPostScript}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, second.ID, RequestPatch{URL: &secondURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, third.ID, RequestPatch{URL: &thirdURL, PostScript: &thirdPostScript}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateRequest(collection.ID, fourth.ID, RequestPatch{URL: &fourthURL}); err != nil {
		t.Fatal(err)
	}

	state, err = app.RunCollection(collection.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&secondCalls); got != 0 {
		t.Fatalf("second request should have been skipped by setNextRequest jump, got %d calls", got)
	}
	if got := atomic.LoadInt32(&fourthCalls); got != 0 {
		t.Fatalf("fourth request should have been skipped by setNextRequest(null), got %d calls", got)
	}
	if state.Runner.Total != 2 || state.Runner.Passed != 2 || state.Runner.Failed != 0 || state.Runner.Skipped != 0 {
		t.Fatalf("unexpected runner next-request counts: %#v", state.Runner)
	}
	if state.Runner.Results[0].ItemID != first.ID || state.Runner.Results[1].ItemID != third.ID {
		t.Fatalf("runner did not jump to third request: %#v", state.Runner.Results)
	}
}

func TestImportPostmanAndBruRoundTrip(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	postman := `{
		"info": {"name":"Postman Sample"},
		"auth":{"type":"apikey","apikey":[{"key":"key","value":"X-Collection-Key"},{"key":"value","value":"{{api_key}}"},{"key":"in","value":"header"}]},
		"variable":[
			{"key":"api key","value":"secret"},
			{"key":"enabled","value":true},
			{"key":"user","value":{"id":1}}
		],
		"event":[
			{"listen":"prerequest","script":{"exec":["pm.variables.set('collection_pre', 'yes');"]}},
			{"listen":"test","script":{"exec":["pm.variables.set('collection_post', pm.response.json().ok ? 'yes' : 'no');"]}}
		],
		"item": [{
			"name":"Get Echo",
			"request":{
				"method":"GET",
				"url":{
					"raw":"https://example.test/users/:id?active=true&skip=false",
					"query":[
						{"key":"active","value":"true"},
						{"key":"skip","value":"false","disabled":true}
					],
					"variable":[{"key":"id","value":"123"}]
				},
				"header":[{"key":"X-Test","value":"1"}],
				"auth":{"type":"bearer","bearer":[{"key":"token","value":"{{token}}"}]}
			},
			"event":[
				{"listen":"prerequest","script":{"exec":["pm.request.headers.upsert({key: 'X-Postman', value: pm.variables.get('api_key')});","pm.variables.set('request_pre', 'yes');"]}},
				{"listen":"test","script":{"exec":["pm.test('status ok', function () {","  pm.response.to.have.status(200);","  pm.expect(pm.response.code).to.equal(200);","  pm.expect(pm.response.json().ok).to.equal(true);","  pm.expect(pm.getResponseHeader('X-Test')).to.equal('ok');","});","pm.variables.set('request_post', 'yes');"]}}
			],
			"response":[{
				"name":"Success",
				"status":"OK",
				"code":200,
				"header":[
					{"key":"Content-Type","value":"application/json"},
					{"key":"x-powered-by","value":"Postman"}
				],
				"body":"{\"ok\":true}",
				"_postman_previewlanguage":"json"
			}]
		}, {
			"name":"Submit Form",
			"request":{
				"method":"POST",
				"url":{"raw":"https://example.test/form"},
				"auth":{"type":"basic","basic":[{"key":"username","value":"ada"},{"key":"password","value":"secret"}]},
				"body":{
					"mode":"urlencoded",
					"urlencoded":[
						{"key":"email","value":"ada@example.test"},
						{"key":"disabled","value":"ignored","disabled":true}
					]
				}
			}
		}, {
			"name":"Upload Avatar",
			"request":{
				"method":"POST",
				"url":{"raw":"https://example.test/upload"},
				"auth":{"type":"apikey","apikey":[{"key":"key","value":"api_key"},{"key":"value","value":"{{apiKey}}"},{"key":"in","value":"query"}]},
				"body":{
					"mode":"formdata",
					"formdata":[
						{"key":"caption","value":"hello"},
						{"key":"avatar","type":"file","src":"/tmp/avatar.png"}
					]
				}
			}
		}, {
			"name":"Graph Search",
			"request":{
				"method":"POST",
				"url":{"raw":"https://example.test/graphql"},
				"body":{
					"mode":"graphql",
					"graphql":{
						"query":"query Search($term: String!) { search(term: $term) { id } }",
						"variables":{"term":"ada"}
					}
				}
			}
		}, {
			"name":"Admin APIs",
			"auth":{"type":"basic","basic":[{"key":"username","value":"folder-user"},{"key":"password","value":"folder-pass"}]},
			"event":[{"listen":"test","script":{"exec":["pm.variables.set('folder_post', 'yes');"]}}],
			"item":[{
				"name":"Create User",
				"request":{
					"method":"POST",
					"url":{"raw":"https://example.test/users"},
					"header":[{"key":"Content-Type","value":"application/json"}],
					"body":{"mode":"raw","raw":"{\"name\":\"Ada\"}"}
				},
				"response":[{
					"name":"Created",
					"status":"Created",
					"code":201,
					"header":[{"key":"Content-Type","value":"application/json"}],
					"body":"{\"id\":1}",
					"_postman_previewlanguage":"json"
				}]
			}, {
				"name":"Public Ping",
				"request":{
					"method":"GET",
					"url":{"raw":"https://example.test/ping"},
					"auth":{"type":"noauth"}
				}
			}]
		}]
	}`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		// US-044 made translation opt-in. This test is ABOUT the translator,
		// so it asks for it explicitly; the default is now to keep pm.*
		// verbatim and run it on the native pm object.
		Kind: "postman", Content: postman, TranslatePostmanScripts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if imported.Name != "Postman Sample" || len(imported.Items) != 6 {
		t.Fatalf("unexpected import: %#v", imported)
	}
	vars := map[string]string{}
	for _, variable := range imported.Variables {
		vars[variable.Name] = fmt.Sprint(variable.Value)
	}
	if vars["api_key"] != "secret" || vars["enabled"] != "true" || vars["user"] != `{"id":1}` {
		t.Fatalf("postman collection variables not converted: %#v", imported.Variables)
	}
	if imported.Auth.Mode != "apikey" || imported.Auth.APIKey != "X-Collection-Key" || imported.Auth.APIValue != "{{api_key}}" || imported.Auth.APILocation != "header" {
		t.Fatalf("postman collection auth not converted: %#v", imported.Auth)
	}
	if !strings.Contains(imported.PreScript, "collection_pre") || !strings.Contains(imported.PostScript, "collection_post") {
		t.Fatalf("postman collection events not converted: pre=%q post=%q", imported.PreScript, imported.PostScript)
	}
	if imported.Items[0].URL != "https://example.test/users/:id?active=true&skip=false" || imported.Items[0].Headers[0].Name != "X-Test" {
		t.Fatalf("postman fields not converted: %#v", imported.Items[0])
	}
	if len(imported.Items[0].Params) != 2 || imported.Items[0].Params[0].Name != "active" || !imported.Items[0].Params[0].Enabled || imported.Items[0].Params[1].Name != "skip" || imported.Items[0].Params[1].Enabled {
		t.Fatalf("postman URL params not converted: %#v", imported.Items[0].Params)
	}
	if len(imported.Items[0].PathParams) != 1 || imported.Items[0].PathParams[0].Name != "id" || imported.Items[0].PathParams[0].Value != "123" {
		t.Fatalf("postman URL path params not converted: %#v", imported.Items[0].PathParams)
	}
	if imported.Items[0].Auth.Mode != "bearer" || imported.Items[0].Auth.Token != "{{token}}" {
		t.Fatalf("postman bearer auth not converted: %#v", imported.Items[0].Auth)
	}
	// US-044: pm.variables is deliberately NOT translated even with the flag
	// on, because bru has no equivalent — bru.getVar reads only the runtime
	// scope, whereas pm.variables reads the fully resolved chain. Translating
	// it was half of the scope-collapse bug this story fixes. Left alone it
	// runs on the native pm.variables from US-040, which is strictly more
	// correct than any rewrite. Everything else must still convert.
	untranslatedPre := strings.ReplaceAll(imported.Items[0].PreScript, "pm.variables.", "")
	untranslatedPost := strings.ReplaceAll(imported.Items[0].PostScript, "pm.variables.", "")
	if strings.Contains(untranslatedPre, "pm.") || strings.Contains(untranslatedPost, "pm.") || !strings.Contains(imported.Items[0].PreScript, "req.setHeader") || !strings.Contains(imported.Items[0].PostScript, "test(") || !strings.Contains(imported.Items[0].PostScript, "expect(res.status).to.equal(200)") || !strings.Contains(imported.Items[0].PostScript, "res.json.ok") || !strings.Contains(imported.Items[0].PostScript, "res.getHeader") {
		t.Fatalf("postman request events not converted: pre=%q post=%q", imported.Items[0].PreScript, imported.Items[0].PostScript)
	}
	scripts := mergedRuntimeScripts(imported, imported.Items[0])
	requestCopy := imported.Items[0]
	runtimeVars := buildVariableMap(nil, &imported, "", requestCopy)
	if err := runPreRequestScript(scripts.Pre, &requestCopy, runtimeVars, nil); err != nil {
		t.Fatalf("translated postman pre-request script did not execute: %v\n%s", err, scripts.Pre)
	}
	if getKeyValue(requestCopy.Headers, "X-Postman") != "secret" || runtimeVars["collection_pre"] != "yes" || runtimeVars["request_pre"] != "yes" {
		t.Fatalf("translated postman pre-request script did not mutate request/vars: headers=%#v vars=%#v", requestCopy.Headers, runtimeVars)
	}
	if err := runPostResponseScript(scripts.Post, requestCopy, Response{
		Status:  http.StatusOK,
		Body:    `{"ok":true}`,
		Headers: map[string]string{"X-Test": "ok"},
	}, runtimeVars, nil); err != nil {
		t.Fatalf("translated postman post-response script did not execute: %v\n%s", err, scripts.Post)
	}
	if runtimeVars["request_post"] != "yes" || runtimeVars["collection_post"] != "yes" {
		t.Fatalf("translated postman post-response script did not mutate vars: %#v", runtimeVars)
	}
	if len(imported.Items[0].Examples) != 1 {
		t.Fatalf("postman response examples not converted: %#v", imported.Items[0].Examples)
	}
	example := imported.Items[0].Examples[0]
	if example.Name != "Success" || example.Request.URL != "https://example.test/users/:id?active=true&skip=false" || example.Response.Status != http.StatusOK || example.Response.StatusText != "OK" || example.Response.BodyType != "json" || example.Response.Body != `{"ok":true}` {
		t.Fatalf("postman response example fields not converted: %#v", example)
	}
	if len(example.Response.Headers) != 2 || example.Response.Headers[0].Name != "Content-Type" || example.Response.Headers[1].Name != "x-powered-by" {
		t.Fatalf("postman response example headers not converted: %#v", example.Response.Headers)
	}
	form := imported.Items[1]
	if form.Body.Mode != "formUrlEncoded" || len(form.Body.FormURLEncoded) != 2 || form.Body.FormURLEncoded[0].Name != "email" || form.Body.FormURLEncoded[0].Value != "ada@example.test" || !form.Body.FormURLEncoded[0].Enabled || form.Body.FormURLEncoded[1].Enabled {
		t.Fatalf("postman urlencoded body not converted: %#v", form.Body)
	}
	if form.Auth.Mode != "basic" || form.Auth.Username != "ada" || form.Auth.Password != "secret" {
		t.Fatalf("postman basic auth not converted: %#v", form.Auth)
	}
	multipart := imported.Items[2]
	if multipart.Body.Mode != "multipartForm" || len(multipart.Body.Multipart) != 2 || multipart.Body.Multipart[0].Name != "caption" || multipart.Body.Multipart[0].Value != "hello" || multipart.Body.Multipart[1].Name != "avatar" || multipart.Body.Multipart[1].FilePath != "/tmp/avatar.png" {
		t.Fatalf("postman multipart body not converted: %#v", multipart.Body)
	}
	if multipart.Auth.Mode != "apikey" || multipart.Auth.APIKey != "api_key" || multipart.Auth.APIValue != "{{apiKey}}" || multipart.Auth.APILocation != "query" {
		t.Fatalf("postman api key auth not converted: %#v", multipart.Auth)
	}
	graphQL := imported.Items[3]
	if graphQL.Type != "graphql" || graphQL.Body.Mode != "graphql" || graphQL.Method != http.MethodPost || !strings.Contains(graphQL.Body.GraphQLQuery, "query Search") || !strings.Contains(graphQL.Body.GraphQLVariables, `"term": "ada"`) {
		t.Fatalf("postman graphql body not converted: %#v", graphQL)
	}
	if graphQL.Auth.Mode != "inherit" {
		t.Fatalf("postman request without auth should inherit: %#v", graphQL.Auth)
	}
	effectiveGraphQL := effectiveRequest(imported, graphQL)
	if effectiveGraphQL.Auth.Mode != "apikey" || effectiveGraphQL.Auth.APIKey != "X-Collection-Key" {
		t.Fatalf("postman collection auth was not inherited: %#v", effectiveGraphQL.Auth)
	}
	nested := imported.Items[4]
	if nested.Name != "Create User" || nested.FolderPath != "Admin APIs" || nested.Method != http.MethodPost || nested.URL != "https://example.test/users" || nested.Body.Mode != "json" || !strings.Contains(nested.Body.JSON, `"name":"Ada"`) {
		t.Fatalf("nested postman request not converted: %#v", nested)
	}
	if len(imported.Folders) != 1 || imported.Folders[0].Path != "Admin APIs" || imported.Folders[0].Auth.Mode != "basic" || imported.Folders[0].Auth.Username != "folder-user" || imported.Folders[0].Auth.Password != "folder-pass" || !strings.Contains(imported.Folders[0].PostScript, "folder_post") {
		t.Fatalf("postman folder auth not converted: %#v", imported.Folders)
	}
	effectiveNested := effectiveRequest(imported, nested)
	if effectiveNested.Auth.Mode != "basic" || effectiveNested.Auth.Username != "folder-user" || effectiveNested.Auth.Password != "folder-pass" {
		t.Fatalf("postman folder auth was not inherited: %#v", effectiveNested.Auth)
	}
	if len(nested.Examples) != 1 || nested.Examples[0].Response.Status != http.StatusCreated || nested.Examples[0].Response.Body != `{"id":1}` {
		t.Fatalf("nested postman response example not converted: %#v", nested.Examples)
	}
	noAuth := imported.Items[5]
	if noAuth.Name != "Public Ping" || noAuth.FolderPath != "Admin APIs" || noAuth.Auth.Mode != "none" {
		t.Fatalf("postman noauth request not converted: %#v", noAuth)
	}

	bru := stringifyBru(imported.Items[0])
	if !strings.Contains(bru, "params:path") || !strings.Contains(bru, "  id: 123") {
		t.Fatalf("bru export did not include path params:\n%s", bru)
	}
	parsed, err := parseBru(bru)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != imported.Items[0].Name || parsed.Method != imported.Items[0].Method || parsed.URL != imported.Items[0].URL {
		t.Fatalf("bru round-trip mismatch:\n%s\n%#v", bru, parsed)
	}
	if len(parsed.PathParams) != 1 || parsed.PathParams[0].Name != "id" || parsed.PathParams[0].Value != "123" {
		t.Fatalf("bru path params did not round-trip:\n%s\n%#v", bru, parsed.PathParams)
	}
}

func TestImportPostmanAdvancedAuth(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	postman := `{
		"info": {"name":"Postman Advanced Auth"},
		"auth":{"type":"oauth1","oauth1":[
			{"key":"consumerKey","value":"collection-consumer"},
			{"key":"consumerSecret","value":"collection-secret"},
			{"key":"token","value":"collection-token"},
			{"key":"tokenSecret","value":"collection-token-secret"},
			{"key":"signatureMethod","value":"HMAC-SHA1"},
			{"key":"addParamsToHeader","value":false},
			{"key":"includeBodyHash","value":true},
			{"key":"callback","value":"https://example.test/callback"},
			{"key":"verifier","value":"verifier"},
			{"key":"timestamp","value":"1234567890"},
			{"key":"nonce","value":"nonce-value"},
			{"key":"version","value":"1.0"},
			{"key":"realm","value":"example"}
		]},
		"item":[{
			"name":"AWS Folder",
			"auth":{"type":"awsv4","awsv4":[
				{"key":"accessKey","value":"AKIA_TEST"},
				{"key":"secretKey","value":"secret"},
				{"key":"sessionToken","value":"session"},
				{"key":"service","value":"execute-api"},
				{"key":"region","value":"us-east-1"}
			]},
			"item":[{
				"name":"Digest Request",
				"request":{
					"method":"GET",
					"url":{"raw":"https://example.test/digest"},
					"auth":{"type":"digest","digest":{"username":"digest-user","password":"digest-pass"}}
				}
			}, {
				"name":"AWS Inherit",
				"request":{
					"method":"GET",
					"url":{"raw":"https://example.test/aws"}
				}
			}]
		}, {
			"name":"OAuth2 Password",
			"request":{
				"method":"POST",
				"url":{"raw":"https://example.test/oauth2/password"},
				"auth":{"type":"oauth2","oauth2":{
					"grant_type":"password_credentials",
					"accessTokenUrl":"https://auth.example.test/token",
					"refreshTokenUrl":"https://auth.example.test/refresh",
					"username":"oauth-user",
					"password":"oauth-pass",
					"clientId":"client-id",
					"clientSecret":"client-secret",
					"scope":"read write",
					"state":"state-value",
					"addTokenTo":"header",
					"headerPrefix":"Token",
					"client_authentication":"body"
				}}
			}
		}, {
			"name":"OAuth2 PKCE",
			"request":{
				"method":"GET",
				"url":{"raw":"https://example.test/oauth2/pkce"},
				"auth":{"type":"oauth2","oauth2":[
					{"key":"grant_type","value":"authorization_code_with_pkce"},
					{"key":"authUrl","value":"https://auth.example.test/authorize"},
					{"key":"redirect_uri","value":"https://app.example.test/callback"},
					{"key":"accessTokenUrl","value":"https://auth.example.test/token"},
					{"key":"refreshTokenUrl","value":"https://auth.example.test/refresh"},
					{"key":"clientId","value":"pkce-client"},
					{"key":"clientSecret","value":"pkce-secret"},
					{"key":"scope","value":"openid"},
					{"key":"state","value":"pkce-state"},
					{"key":"addTokenTo","value":"query"},
					{"key":"client_authentication","value":"header"}
				]}
			}
		}]}`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		// US-044 made translation opt-in. This test is ABOUT the translator,
		// so it asks for it explicitly; the default is now to keep pm.*
		// verbatim and run it on the native pm object.
		Kind: "postman", Content: postman, TranslatePostmanScripts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if imported.Auth.Mode != "oauth1" || imported.Auth.OAuth1.ConsumerKey != "collection-consumer" || imported.Auth.OAuth1.AccessToken != "collection-token" || imported.Auth.OAuth1.Placement != "query" || !imported.Auth.OAuth1.IncludeBodyHash || imported.Auth.OAuth1.PrivateKeyType != "text" || imported.Auth.OAuth1.Realm != "example" {
		t.Fatalf("postman collection oauth1 auth not converted: %#v", imported.Auth)
	}
	if len(imported.Folders) != 1 || imported.Folders[0].Auth.Mode != "awsv4" || imported.Folders[0].Auth.AWSV4.AccessKeyID != "AKIA_TEST" || imported.Folders[0].Auth.AWSV4.SecretAccessKey != "secret" || imported.Folders[0].Auth.AWSV4.SessionToken != "session" || imported.Folders[0].Auth.AWSV4.Service != "execute-api" || imported.Folders[0].Auth.AWSV4.Region != "us-east-1" {
		t.Fatalf("postman folder awsv4 auth not converted: %#v", imported.Folders)
	}
	digest := imported.Items[0]
	if digest.Auth.Mode != "digest" || digest.Auth.Username != "digest-user" || digest.Auth.Password != "digest-pass" {
		t.Fatalf("postman digest auth not converted: %#v", digest.Auth)
	}
	awsInherit := effectiveRequest(imported, imported.Items[1])
	if awsInherit.Auth.Mode != "awsv4" || awsInherit.Auth.AWSV4.Region != "us-east-1" {
		t.Fatalf("postman folder awsv4 auth was not inherited: %#v", awsInherit.Auth)
	}
	oauthPassword := imported.Items[2].Auth
	if oauthPassword.Mode != "oauth2" || oauthPassword.OAuth2.GrantType != "password" || oauthPassword.OAuth2.Username != "oauth-user" || oauthPassword.OAuth2.Password != "oauth-pass" || oauthPassword.OAuth2.ClientID != "client-id" || oauthPassword.OAuth2.ClientSecret != "client-secret" || oauthPassword.OAuth2.TokenPlacement != "header" || oauthPassword.OAuth2.TokenHeaderPrefix != "Token" || oauthPassword.OAuth2.CredentialsPlacement != "body" {
		t.Fatalf("postman oauth2 password auth not converted: %#v", oauthPassword)
	}
	oauthPKCE := imported.Items[3].Auth
	if oauthPKCE.Mode != "oauth2" || oauthPKCE.OAuth2.GrantType != "authorization_code" || !oauthPKCE.OAuth2.PKCE || oauthPKCE.OAuth2.AuthorizationURL != "https://auth.example.test/authorize" || oauthPKCE.OAuth2.CallbackURL != "https://app.example.test/callback" || oauthPKCE.OAuth2.TokenPlacement != "url" || oauthPKCE.OAuth2.CredentialsPlacement != "basic_auth_header" || oauthPKCE.OAuth2.TokenQueryKey != "access_token" {
		t.Fatalf("postman oauth2 pkce auth not converted: %#v", oauthPKCE)
	}
}

func TestImportPostmanCookieScriptTranslation(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	postman := `{
		"info": {"name":"Postman Cookie Scripts"},
		"item":[{
			"name":"Cookie Check",
			"request":{
				"method":"GET",
				"url":{"raw":"https://example.test/cookies"}
			},
			"event":[{
				"listen":"test",
				"script":{"exec":[
					"pm.test('cookie access', function () {",
					"  pm.expect(pm.cookies.has('session')).to.equal(true);",
					"  pm.expect(pm.cookies.get('session')).to.equal('abc123');",
					"  pm.expect(pm.cookies.toObject().session).to.equal('abc123');",
					"  pm.expect(pm.cookies.toString()).to.include('session=abc123');",
					"  pm.expect(pm.cookies.count()).to.equal(1);",
					"  pm.expect(pm.cookies.all()[0].key).to.equal('session');",
					"});"
				]}
			}]
		}]}`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		// US-044 made translation opt-in. This test is ABOUT the translator,
		// so it asks for it explicitly; the default is now to keep pm.*
		// verbatim and run it on the native pm object.
		Kind: "postman", Content: postman, TranslatePostmanScripts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(imported.Items) != 1 {
		t.Fatalf("unexpected postman import: %#v", imported)
	}
	script := imported.Items[0].PostScript
	if strings.Contains(script, "pm.cookies") || !strings.Contains(script, "bru.cookies.get") || !strings.Contains(script, "bru.cookies.toObject") || !strings.Contains(script, "bru.cookies.all") {
		t.Fatalf("postman cookie script not translated: %s", script)
	}
	cookies := []CookieEntry{{Name: "session", Value: "abc123", Domain: "example.test", Path: "/", Session: true}}
	if err := runPostResponseScript(script, imported.Items[0], Response{Status: http.StatusOK, Body: `{}`}, map[string]string{}, cookies); err != nil {
		t.Fatalf("translated postman cookie script did not execute: %v\n%s", err, script)
	}
}

func TestImportPostmanHeaderListScriptTranslation(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	postman := `{
		"info": {"name":"Postman HeaderList Scripts"},
		"item":[{
			"name":"Header Check",
			"request":{
				"method":"GET",
				"url":{"raw":"https://example.test/headers"},
				"header":[{"key":"X-Req","value":"1"}]
			},
			"event":[{
				"listen":"test",
				"script":{"exec":[
					"pm.test('header lists', function () {",
					"  pm.expect(pm.response.headers.has('X-Test')).to.equal(true);",
					"  pm.expect(pm.response.headers.count()).to.equal(2);",
					"  pm.expect(pm.response.headers.toObject()['X-Test']).to.equal('ok');",
					"  pm.expect(pm.response.headers.all()[0].key).to.equal('Content-Type');",
					"  pm.expect(pm.response.headers.one('X-Test').value).to.equal('ok');",
					"  pm.expect(pm.response.headers.indexOf('X-Test')).to.equal(1);",
					"  pm.expect(pm.response.headers.filter(function (h) { return h.key === 'X-Test'; }).length).to.equal(1);",
					"  var seen = '';",
					"  pm.response.headers.each(function (h) { if (h.key === 'X-Test') { seen = h.key; } });",
					"  pm.expect(seen).to.equal('X-Test');",
					"  pm.expect(pm.response.headers.map(function (h) { return h.key; }).length).to.equal(2);",
					"  pm.expect(pm.response.headers.toString()).to.include('X-Test: ok');",
					"  pm.expect(pm.request.headers.has('X-Req')).to.equal(true);",
					"  pm.expect(pm.request.headers.count()).to.equal(1);",
					"  pm.expect(pm.request.headers.toObject()['X-Req']).to.equal('1');",
					"});"
				]}
			}]
		}]}`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		// US-044 made translation opt-in. This test is ABOUT the translator,
		// so it asks for it explicitly; the default is now to keep pm.*
		// verbatim and run it on the native pm object.
		Kind: "postman", Content: postman, TranslatePostmanScripts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	script := imported.Items[0].PostScript
	if strings.Contains(script, "pm.response.headers") || strings.Contains(script, "pm.request.headers.has") || !strings.Contains(script, "res.headerList.count") || !strings.Contains(script, "req.headerList.toObject") {
		t.Fatalf("postman header-list script not translated: %s", script)
	}
	if err := runPostResponseScript(script, imported.Items[0], Response{
		Status:  http.StatusOK,
		Body:    `{}`,
		Headers: map[string]string{"Content-Type": "application/json", "X-Test": "ok"},
	}, map[string]string{}, nil); err != nil {
		t.Fatalf("translated postman header-list script did not execute: %v\n%s", err, script)
	}
}

func TestImportInsomniaV4AndV5(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	insomniaV4 := `{
		"_type":"export",
		"__export_format":4,
		"resources":[{
			"_id":"wrk_1",
			"_type":"workspace",
			"name":"Insomnia V4"
		}, {
			"_id":"env_base",
			"_type":"environment",
			"parentId":"wrk_1",
			"name":"Base",
			"data":{"base_url":"https://api.example.test","nested":{"token":"base-token"}}
		}, {
			"_id":"env_local",
			"_type":"environment",
			"parentId":"env_base",
			"name":"Local",
			"data":{"nested":{"token":"local-token"},"debug":true}
		}, {
			"_id":"fld_admin",
			"_type":"request_group",
			"parentId":"wrk_1",
			"name":"Admin APIs"
		}, {
			"_id":"req_get",
			"_type":"request",
			"parentId":"fld_admin",
			"name":"Get User",
			"method":"GET",
			"url":"{{ _.base_url }}/users/{{ user_id }}",
			"headers":[{"name":"X-Token","value":"{{ _.nested.token }}"}],
			"parameters":[{"name":"active","value":true},{"name":"disabled","value":"no","disabled":true}],
			"pathParameters":[{"name":"user_id","value":123}],
			"authentication":{"type":"bearer","token":"{{ _.nested.token }}"}
		}, {
			"_id":"req_json",
			"_type":"request",
			"parentId":"wrk_1",
			"name":"Create User",
			"method":"POST",
			"url":"https://api.example.test/users",
			"authentication":{"type":"basic","username":"ada","password":"secret"},
			"body":{"mimeType":"application/json","text":"{\"name\":\"{{ user }}\"}"}
		}]
	}`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "insomnia", Content: insomniaV4})
	if err != nil {
		t.Fatal(err)
	}
	importedV4 := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if importedV4.Name != "Insomnia V4" || len(importedV4.Items) != 2 || len(importedV4.Folders) != 1 {
		t.Fatalf("unexpected Insomnia v4 import: %#v", importedV4)
	}
	getUser := importedV4.Items[0]
	if getUser.Name != "Get User" || getUser.FolderPath != "Admin APIs" || getUser.URL != "{{base_url}}/users/{{user_id}}" || getUser.Auth.Mode != "bearer" || getUser.Auth.Token != "{{nested.token}}" || getUser.Headers[0].Value != "{{nested.token}}" {
		t.Fatalf("Insomnia v4 request fields not converted: %#v", getUser)
	}
	if len(getUser.Params) != 2 || getUser.Params[0].Name != "active" || getUser.Params[0].Value != "true" || !getUser.Params[0].Enabled || getUser.Params[1].Enabled {
		t.Fatalf("Insomnia v4 params not converted: %#v", getUser.Params)
	}
	if len(getUser.PathParams) != 1 || getUser.PathParams[0].Name != "user_id" || getUser.PathParams[0].Value != "123" {
		t.Fatalf("Insomnia v4 path params not converted: %#v", getUser.PathParams)
	}
	createUser := importedV4.Items[1]
	if createUser.Body.Mode != "json" || createUser.Body.JSON != `{"name":"{{user}}"}` || createUser.Auth.Mode != "basic" || createUser.Auth.Username != "ada" {
		t.Fatalf("Insomnia v4 JSON/basic request not converted: %#v", createUser)
	}
	if len(importedV4.Environments) != 2 || importedV4.Environments[0].Name != "Base" || importedV4.Environments[1].Name != "Local" {
		t.Fatalf("Insomnia v4 environments not converted: %#v", importedV4.Environments)
	}
	localVars := map[string]string{}
	for _, variable := range importedV4.Environments[1].Variables {
		localVars[variable.Name] = fmt.Sprint(variable.Value)
	}
	if localVars["base_url"] != "https://api.example.test" || localVars["nested.token"] != "local-token" || localVars["debug"] != "true" {
		t.Fatalf("Insomnia v4 environment merge/flatten failed: %#v", importedV4.Environments[1].Variables)
	}

	insomniaV5 := `
type: collection.insomnia.rest/5.0
name: Insomnia V5
collection:
  - name: Forms
    children:
      - name: Submit Form
        method: POST
        url: https://api.example.test/form
        body:
          mimeType: application/x-www-form-urlencoded
          params:
            - name: email
              value: ada@example.test
            - name: disabled
              value: ignored
              disabled: true
      - name: Upload Avatar
        method: POST
        url: https://api.example.test/upload
        body:
          mimeType: multipart/form-data
          params:
            - name: caption
              value: hello
  - name: Graph Search
    method: POST
    url: https://api.example.test/graphql
    body:
      mimeType: application/graphql
      text: '{"query":"query Search($term: String!) { search(term: \"{{ _.term }}\") { id } }","variables":{"term":"ada"}}'
  - name: Raw Text
    method: POST
    url: https://api.example.test/raw
    body:
      mimeType: ""
      text: |-
        line1
        {{ _.base_url }}
environments:
  name: Imported Environment
  data:
    base_url: https://api.example.test
    nested:
      token: base-token
  subEnvironments:
    - name: Local
      data:
        nested:
          token: local-token
        debug: true
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "insomnia", Content: insomniaV5})
	if err != nil {
		t.Fatal(err)
	}
	importedV5 := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if importedV5.Name != "Insomnia V5" || len(importedV5.Items) != 4 || len(importedV5.Folders) != 1 {
		t.Fatalf("unexpected Insomnia v5 import: %#v", importedV5)
	}
	form := importedV5.Items[0]
	if form.FolderPath != "Forms" || form.Body.Mode != "formUrlEncoded" || len(form.Body.FormURLEncoded) != 2 || form.Body.FormURLEncoded[1].Enabled {
		t.Fatalf("Insomnia v5 form body not converted: %#v", form)
	}
	multipart := importedV5.Items[1]
	if multipart.Body.Mode != "multipartForm" || len(multipart.Body.Multipart) != 1 || multipart.Body.Multipart[0].Name != "caption" || multipart.Body.Multipart[0].Value != "hello" {
		t.Fatalf("Insomnia v5 multipart body not converted: %#v", multipart.Body)
	}
	graphQL := importedV5.Items[2]
	if graphQL.Type != "graphql" || graphQL.Body.Mode != "graphql" || !strings.Contains(graphQL.Body.GraphQLQuery, "{{term}}") || !strings.Contains(graphQL.Body.GraphQLVariables, `"term": "ada"`) {
		t.Fatalf("Insomnia v5 graphql body not converted: %#v", graphQL.Body)
	}
	rawText := importedV5.Items[3]
	if rawText.Body.Mode != "text" || !strings.Contains(rawText.Body.Text, "{{base_url}}") {
		t.Fatalf("Insomnia v5 raw text body not converted: %#v", rawText.Body)
	}
	if len(importedV5.Environments) != 2 || importedV5.Environments[0].Name != "Imported Environment" || importedV5.Environments[1].Name != "Local" {
		t.Fatalf("Insomnia v5 environments not converted: %#v", importedV5.Environments)
	}
}

func TestBruResponseExamplesRoundTrip(t *testing.T) {
	content := `meta {
	  name: Get User
	  type: http
  seq: 1
}

get {
  url: https://api.example.test/users/123
  body: none
  auth: none
}

example {
  name: Get User Example
  description: Existing saved response

  request: {
    url: https://api.example.test/users/123?expand=projects
    method: get
    mode: json
    headers: {
      X-Example: request
      ~X-Skip: no
    }
    params: {
      expand: projects
      ~skip: no
    }
    body: {
      type: json
      content: '''
        {"request":true}
      '''
    }
  }

  response: {
    status: {
      code: 200
      text: OK
    }

    headers: {
      Content-Type: application/json
    }

    body: {
      type: json
      content: '''
        {"id":123,"name":"Ada"}
      '''
    }
  }
}
`
	item, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Examples) != 1 {
		t.Fatalf("expected one example, got %#v", item.Examples)
	}
	example := item.Examples[0]
	if example.Name != "Get User Example" || example.Request.URL != "https://api.example.test/users/123?expand=projects" || example.Request.BodyMode != "json" || example.Response.Status != http.StatusOK || example.Response.BodyType != "json" {
		t.Fatalf("example metadata was not parsed: %#v", example)
	}
	if len(example.Request.Headers) != 2 || !example.Request.Headers[0].Enabled || example.Request.Headers[1].Enabled || len(example.Request.Params) != 2 || !example.Request.Params[0].Enabled || example.Request.Params[1].Enabled || !strings.Contains(example.Request.Body, `"request":true`) {
		t.Fatalf("example request snapshot was not parsed: %#v", example.Request)
	}
	if !strings.Contains(example.Response.Body, `"name":"Ada"`) {
		t.Fatalf("example body was not parsed: %q", example.Response.Body)
	}
	roundTrip, err := parseBru(stringifyBru(item))
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Examples) != 1 || roundTrip.Examples[0].Request.BodyMode != "json" || roundTrip.Examples[0].Request.Headers[1].Enabled || roundTrip.Examples[0].Request.Params[1].Enabled || !strings.Contains(roundTrip.Examples[0].Request.Body, `"request":true`) || !strings.Contains(roundTrip.Examples[0].Response.Body, `"id":123`) {
		t.Fatalf("example did not round-trip:\n%s\n%#v", stringifyBru(item), roundTrip.Examples)
	}
}

func TestBruResponseExampleFormURLEncodedRoundTrip(t *testing.T) {
	content := `meta {
  name: Submit Form
  type: http
  seq: 1
}

post {
  url: https://api.example.test/forms
  body: form-urlencoded
  auth: none
}

example {
  name: Submitted Form Example

  request: {
    url: https://api.example.test/forms
    method: post
    mode: formUrlEncoded
    body:form-urlencoded: {
      email: ada@example.test
      notes: hello there
      ~disabled: nope
    }
  }

  response: {
    status: {
      code: 200
      text: OK
    }
  }
}
`
	item, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Examples) != 1 {
		t.Fatalf("expected one example, got %#v", item.Examples)
	}
	example := item.Examples[0]
	if example.Request.BodyMode != "formUrlEncoded" || len(example.Request.FormURLEncoded) != 3 {
		t.Fatalf("form-url-encoded example body was not parsed: %#v", example.Request)
	}
	if example.Request.FormURLEncoded[0].Name != "email" || example.Request.FormURLEncoded[0].Value != "ada@example.test" || !example.Request.FormURLEncoded[0].Enabled {
		t.Fatalf("enabled form example row did not parse: %#v", example.Request.FormURLEncoded[0])
	}
	if example.Request.FormURLEncoded[2].Name != "disabled" || example.Request.FormURLEncoded[2].Enabled {
		t.Fatalf("disabled form example row did not parse: %#v", example.Request.FormURLEncoded[2])
	}
	written := stringifyBru(item)
	if !strings.Contains(written, "body:form-urlencoded: {") || !strings.Contains(written, "      email: ada@example.test") || !strings.Contains(written, "      ~disabled: nope") {
		t.Fatalf("form-url-encoded example body was not written:\n%s", written)
	}
	roundTrip, err := parseBru(written)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Examples) != 1 || len(roundTrip.Examples[0].Request.FormURLEncoded) != 3 || roundTrip.Examples[0].Request.FormURLEncoded[2].Enabled {
		t.Fatalf("form-url-encoded example body did not round-trip: %#v", roundTrip.Examples)
	}
}

func TestBruResponseExampleMultipartRoundTrip(t *testing.T) {
	content := `meta {
  name: Upload Form
  type: http
  seq: 1
}

post {
  url: https://api.example.test/upload
  body: multipart-form
  auth: none
}

example {
  name: Upload Example

  request: {
    url: https://api.example.test/upload
    method: post
    mode: multipartForm
    body:multipart-form: {
      document: @file(examples/sample.pdf) @contentType(application/pdf)
      title: Sample Document
      ~skip: nope
    }
  }

  response: {
    status: {
      code: 201
      text: Created
    }
  }
}
`
	item, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Examples) != 1 {
		t.Fatalf("expected one example, got %#v", item.Examples)
	}
	example := item.Examples[0]
	if example.Request.BodyMode != "multipartForm" || len(example.Request.MultipartForm) != 3 {
		t.Fatalf("multipart example body was not parsed: %#v", example.Request)
	}
	if example.Request.MultipartForm[0].Name != "document" || example.Request.MultipartForm[0].FilePath != "examples/sample.pdf" || example.Request.MultipartForm[0].ContentType != "application/pdf" || !example.Request.MultipartForm[0].Enabled {
		t.Fatalf("multipart file row did not parse: %#v", example.Request.MultipartForm[0])
	}
	if example.Request.MultipartForm[2].Name != "skip" || example.Request.MultipartForm[2].Enabled {
		t.Fatalf("disabled multipart row did not parse: %#v", example.Request.MultipartForm[2])
	}
	written := stringifyBru(item)
	if !strings.Contains(written, "body:multipart-form: {") || !strings.Contains(written, "      document: @file(examples/sample.pdf) @contentType(application/pdf)") || !strings.Contains(written, "      ~skip: nope") {
		t.Fatalf("multipart example body was not written:\n%s", written)
	}
	roundTrip, err := parseBru(written)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Examples) != 1 || len(roundTrip.Examples[0].Request.MultipartForm) != 3 || roundTrip.Examples[0].Request.MultipartForm[2].Enabled {
		t.Fatalf("multipart example body did not round-trip: %#v", roundTrip.Examples)
	}
}

func TestBruResponseExampleFileBodyRoundTrip(t *testing.T) {
	content := `meta {
  name: Upload File
  type: http
  seq: 1
}

post {
  url: https://api.example.test/upload
  body: file
  auth: none
}

example {
  name: File Upload Example

  request: {
    url: https://api.example.test/upload
    method: post
    mode: file
    body:file: {
      file: @file(examples/selected.bin) @contentType(application/octet-stream)
      ~file: @file(examples/backup.json) @contentType(application/json)
    }
  }

  response: {
    status: {
      code: 200
      text: OK
    }
  }
}
`
	item, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Examples) != 1 {
		t.Fatalf("expected one example, got %#v", item.Examples)
	}
	example := item.Examples[0]
	if example.Request.BodyMode != "file" || len(example.Request.File) != 2 {
		t.Fatalf("file example body was not parsed: %#v", example.Request)
	}
	if example.Request.File[0].FilePath != "examples/selected.bin" || example.Request.File[0].ContentType != "application/octet-stream" || !example.Request.File[0].Selected {
		t.Fatalf("selected file row did not parse: %#v", example.Request.File[0])
	}
	if example.Request.File[1].FilePath != "examples/backup.json" || example.Request.File[1].ContentType != "application/json" || example.Request.File[1].Selected {
		t.Fatalf("unselected file row did not parse: %#v", example.Request.File[1])
	}
	written := stringifyBru(item)
	if !strings.Contains(written, "body:file: {") || !strings.Contains(written, "      file: @file(examples/selected.bin) @contentType(application/octet-stream)") || !strings.Contains(written, "      ~file: @file(examples/backup.json) @contentType(application/json)") {
		t.Fatalf("file example body was not written:\n%s", written)
	}
	roundTrip, err := parseBru(written)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Examples) != 1 || len(roundTrip.Examples[0].Request.File) != 2 || !roundTrip.Examples[0].Request.File[0].Selected || roundTrip.Examples[0].Request.File[1].Selected {
		t.Fatalf("file example body did not round-trip: %#v", roundTrip.Examples)
	}
}

func TestBruFormURLEncodedBodyRoundTrip(t *testing.T) {
	item := RequestItem{
		Name:   "Submit Form",
		Type:   "http",
		Method: http.MethodPost,
		URL:    "https://api.example.test/forms",
		Auth:   AuthConfig{Mode: "none"},
		Body: RequestBody{
			Mode: "formUrlEncoded",
			FormURLEncoded: []KeyValue{
				{Name: "email", Value: "ada@example.test", Enabled: true},
				{Name: "notes", Value: "{{formNote}}", Enabled: true},
				{Name: "disabled", Value: "nope", Enabled: false},
			},
		},
		Settings: RequestSettings{TimeoutMs: 30000, FollowRedirects: true, MaxRedirects: 5, EncodeURL: true, StoreCookies: true, VerifyTLS: true},
	}
	content := stringifyBru(item)
	if !strings.Contains(content, "body:form-urlencoded {") || !strings.Contains(content, "  email: ada@example.test") || !strings.Contains(content, "  ~disabled: nope") {
		t.Fatalf("form-url-encoded body was not written:\n%s", content)
	}
	roundTrip, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Body.Mode != "formUrlEncoded" || len(roundTrip.Body.FormURLEncoded) != 3 {
		t.Fatalf("form-url-encoded body did not round-trip: %#v", roundTrip.Body)
	}
	if roundTrip.Body.FormURLEncoded[1].Name != "notes" || roundTrip.Body.FormURLEncoded[1].Value != "{{formNote}}" || !roundTrip.Body.FormURLEncoded[1].Enabled {
		t.Fatalf("enabled form row did not round-trip: %#v", roundTrip.Body.FormURLEncoded[1])
	}
	if roundTrip.Body.FormURLEncoded[2].Name != "disabled" || roundTrip.Body.FormURLEncoded[2].Enabled {
		t.Fatalf("disabled form row did not round-trip: %#v", roundTrip.Body.FormURLEncoded[2])
	}
}

func TestBruMultipartBodyRoundTrip(t *testing.T) {
	item := RequestItem{
		Name:   "Upload Form",
		Type:   "http",
		Method: http.MethodPost,
		URL:    "https://api.example.test/upload",
		Auth:   AuthConfig{Mode: "none"},
		Body: RequestBody{
			Mode: "multipartForm",
			Multipart: []FormPart{
				{Name: "title", Value: "{{uploadTitle}}", ContentType: "text/plain", Enabled: true},
				{Name: "asset", FilePath: "fixtures/image.png", ContentType: "image/png", Enabled: true},
				{Name: "disabled", Value: "nope", Enabled: false},
			},
		},
		Settings: RequestSettings{TimeoutMs: 30000, FollowRedirects: true, MaxRedirects: 5, EncodeURL: true, StoreCookies: true, VerifyTLS: true},
	}
	content := stringifyBru(item)
	if !strings.Contains(content, "body:multipart-form {") || !strings.Contains(content, "  title: {{uploadTitle}} @contentType(text/plain)") || !strings.Contains(content, "  asset: @file(fixtures/image.png) @contentType(image/png)") || !strings.Contains(content, "  ~disabled: nope") {
		t.Fatalf("multipart body was not written:\n%s", content)
	}
	roundTrip, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Body.Mode != "multipartForm" || len(roundTrip.Body.Multipart) != 3 {
		t.Fatalf("multipart body did not round-trip: %#v", roundTrip.Body)
	}
	if roundTrip.Body.Multipart[0].Name != "title" || roundTrip.Body.Multipart[0].Value != "{{uploadTitle}}" || roundTrip.Body.Multipart[0].ContentType != "text/plain" {
		t.Fatalf("text multipart row did not round-trip: %#v", roundTrip.Body.Multipart[0])
	}
	if roundTrip.Body.Multipart[1].Name != "asset" || roundTrip.Body.Multipart[1].FilePath != "fixtures/image.png" || roundTrip.Body.Multipart[1].ContentType != "image/png" {
		t.Fatalf("file multipart row did not round-trip: %#v", roundTrip.Body.Multipart[1])
	}
	if roundTrip.Body.Multipart[2].Name != "disabled" || roundTrip.Body.Multipart[2].Enabled {
		t.Fatalf("disabled multipart row did not round-trip: %#v", roundTrip.Body.Multipart[2])
	}
}

func TestMultipartBodyContentTypeAndFilePathInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "upload.json")
	if err := os.WriteFile(filePath, []byte(`{"file":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body := RequestBody{
		Mode: "multipartForm",
		Multipart: []FormPart{
			{Name: "payload", Value: "{{payload}}", ContentType: "application/json", Enabled: true},
			{Name: "asset", FilePath: "{{filePath}}", ContentType: "application/json", Enabled: true},
			{Name: "skip", Value: "nope", Enabled: false},
		},
	}
	reader, contentType, err := buildBody(body, map[string]string{"payload": `{"ok":true}`, "filePath": filePath})
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("unexpected multipart content type: %q", contentType)
	}
	multipartReader := multipart.NewReader(reader, params["boundary"])
	seen := map[string]struct {
		body        string
		contentType string
		fileName    string
	}{}
	for {
		part, err := multipartReader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		partBytes, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		seen[part.FormName()] = struct {
			body        string
			contentType string
			fileName    string
		}{body: string(partBytes), contentType: part.Header.Get("Content-Type"), fileName: part.FileName()}
	}
	if seen["payload"].body != `{"ok":true}` || seen["payload"].contentType != "application/json" || seen["payload"].fileName != "" {
		t.Fatalf("text multipart part mismatch: %#v", seen["payload"])
	}
	if seen["asset"].body != `{"file":true}` || seen["asset"].contentType != "application/json" || seen["asset"].fileName != "upload.json" {
		t.Fatalf("file multipart part mismatch: %#v", seen["asset"])
	}
	if _, ok := seen["skip"]; ok || len(seen) != 2 {
		t.Fatalf("disabled multipart part was included: %#v", seen)
	}
}

func TestBruFileBodyRoundTrip(t *testing.T) {
	item := RequestItem{
		Name:   "Upload Raw File",
		Type:   "http",
		Method: http.MethodPost,
		URL:    "https://api.example.test/upload",
		Auth:   AuthConfig{Mode: "none"},
		Body: RequestBody{
			Mode:            "file",
			FilePath:        "fixtures/payload.json",
			FileContentType: "application/json",
		},
		Settings: RequestSettings{TimeoutMs: 30000, FollowRedirects: true, MaxRedirects: 5, EncodeURL: true, StoreCookies: true, VerifyTLS: true},
	}
	content := stringifyBru(item)
	if !strings.Contains(content, "body:file {") || !strings.Contains(content, "  file: @file(fixtures/payload.json) @contentType(application/json)") {
		t.Fatalf("file body was not written:\n%s", content)
	}
	roundTrip, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Body.Mode != "file" || roundTrip.Body.FilePath != "fixtures/payload.json" || roundTrip.Body.FileContentType != "application/json" {
		t.Fatalf("file body did not round-trip: %#v", roundTrip.Body)
	}
	if len(roundTrip.Body.Files) != 1 || !roundTrip.Body.Files[0].Selected || roundTrip.Body.Files[0].FilePath != "fixtures/payload.json" || roundTrip.Body.Files[0].ContentType != "application/json" {
		t.Fatalf("file body rows did not round-trip: %#v", roundTrip.Body.Files)
	}
}

func TestBruMultiFileBodyRoundTrip(t *testing.T) {
	item := RequestItem{
		Name:   "Upload Raw File Variants",
		Type:   "http",
		Method: http.MethodPost,
		URL:    "https://api.example.test/upload",
		Auth:   AuthConfig{Mode: "none"},
		Body: RequestBody{
			Mode: "file",
			Files: []FileBodyEntry{
				{FilePath: "fixtures/selected.json", ContentType: "application/json", Selected: true},
				{FilePath: "fixtures/old.bin", ContentType: "application/octet-stream", Selected: false},
			},
		},
		Settings: RequestSettings{TimeoutMs: 30000, FollowRedirects: true, MaxRedirects: 5, EncodeURL: true, StoreCookies: true, VerifyTLS: true},
	}
	content := stringifyBru(item)
	if !strings.Contains(content, "  file: @file(fixtures/selected.json) @contentType(application/json)") || !strings.Contains(content, "  ~file: @file(fixtures/old.bin) @contentType(application/octet-stream)") {
		t.Fatalf("multi-file body was not written with selected and disabled rows:\n%s", content)
	}
	roundTrip, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Body.Mode != "file" || roundTrip.Body.FilePath != "fixtures/selected.json" || roundTrip.Body.FileContentType != "application/json" {
		t.Fatalf("selected file body fields did not round-trip: %#v", roundTrip.Body)
	}
	if len(roundTrip.Body.Files) != 2 || !roundTrip.Body.Files[0].Selected || roundTrip.Body.Files[1].Selected || roundTrip.Body.Files[1].FilePath != "fixtures/old.bin" {
		t.Fatalf("multi-file body rows did not round-trip: %#v", roundTrip.Body.Files)
	}
}

func TestYAMLFileBodyRoundTrip(t *testing.T) {
	item := RequestItem{
		Name:   "Upload Raw File YAML",
		Type:   "http",
		Method: http.MethodPost,
		URL:    "https://api.example.test/upload",
		Auth:   AuthConfig{Mode: "none"},
		Body: RequestBody{
			Mode: "file",
			Files: []FileBodyEntry{
				{FilePath: "fixtures/selected.json", ContentType: "application/json", Selected: true},
				{FilePath: "fixtures/old.bin", ContentType: "application/octet-stream", Selected: false},
			},
		},
		Settings: RequestSettings{TimeoutMs: 30000, FollowRedirects: true, MaxRedirects: 5, EncodeURL: true, StoreCookies: true, VerifyTLS: true},
	}
	content, err := stringifyYAMLRequest(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "type: file") || !strings.Contains(content, "filePath: fixtures/selected.json") || !strings.Contains(content, "selected: true") || !strings.Contains(content, "selected: false") {
		t.Fatalf("YAML file body rows were not written:\n%s", content)
	}
	roundTrip, err := parseYAMLRequest(content)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Body.Mode != "file" || roundTrip.Body.FilePath != "fixtures/selected.json" || roundTrip.Body.FileContentType != "application/json" {
		t.Fatalf("selected YAML file body fields did not round-trip: %#v", roundTrip.Body)
	}
	if len(roundTrip.Body.Files) != 2 || !roundTrip.Body.Files[0].Selected || roundTrip.Body.Files[1].Selected || roundTrip.Body.Files[1].ContentType != "application/octet-stream" {
		t.Fatalf("YAML file body rows did not round-trip: %#v", roundTrip.Body.Files)
	}
}

func TestFileBodyContentTypeAndPathInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "payload.json")
	if err := os.WriteFile(filePath, []byte(`{"raw":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body := RequestBody{Mode: "file", FilePath: "{{filePath}}", FileContentType: "{{contentType}}"}
	reader, contentType, err := buildBody(body, map[string]string{"filePath": filePath, "contentType": "application/json"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closer, ok := reader.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(bodyBytes) != `{"raw":true}` || contentType != "application/json" {
		t.Fatalf("unexpected file body build: contentType=%q body=%q", contentType, string(bodyBytes))
	}
}

func TestFileBodyUsesSelectedRowAndCollectionRelativePath(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "skip.txt"), []byte("skip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "selected.json"), []byte(`{"selected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body := RequestBody{
		Mode: "file",
		Files: []FileBodyEntry{
			{FilePath: "skip.txt", ContentType: "text/plain", Selected: false},
			{FilePath: "selected.json", Selected: true},
		},
	}
	reader, contentType, err := buildBody(body, nil, tempDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closer, ok := reader.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(bodyBytes) != `{"selected":true}` || contentType != "application/json" {
		t.Fatalf("selected file row was not used: contentType=%q body=%q", contentType, string(bodyBytes))
	}
}

func TestFileBodyWithoutSelectedRowSendsNoBody(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "skip.txt"), []byte("skip"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := RequestBody{
		Mode: "file",
		Files: []FileBodyEntry{
			{FilePath: "skip.txt", ContentType: "text/plain", Selected: false},
		},
	}
	reader, contentType, err := buildBody(body, nil, tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if reader != nil || contentType != "application/octet-stream" {
		t.Fatalf("unselected file rows should not produce a body: reader=%#v contentType=%q", reader, contentType)
	}
}

func TestGrpcBruRoundTripPreservesMetadataProtoPathMethodTypeAndMessages(t *testing.T) {
	content := `meta {
  name: Say Hello
  type: grpc
  seq: 3
}

grpc {
  url: grpc://localhost:50051
  method: helloworld.Greeter/SayHello
  body: grpc
  auth: bearer
  methodType: unary
  protoPath: protos/greeter.proto
}

metadata {
  authorization: Bearer {{token}}
  ~x-disabled: off
}

body:grpc {
  name: hello
  content: '''
    {"name":"Ada"}
  '''
}

body:grpc {
  name: second
  content: {"name":"Grace"}
}
`
	item, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if item.Type != "grpc" || item.Method != "helloworld.Greeter/SayHello" || item.Body.Mode != "grpc" {
		t.Fatalf("gRPC request core fields were not parsed: %#v", item)
	}
	if item.GrpcMethodType != "unary" || item.ProtoPath != "protos/greeter.proto" {
		t.Fatalf("gRPC method type/proto path were not parsed: %#v", item)
	}
	if len(item.Headers) != 2 || item.Headers[1].Name != "x-disabled" || item.Headers[1].Enabled {
		t.Fatalf("gRPC metadata was not parsed: %#v", item.Headers)
	}
	if len(item.GrpcMessages) != 2 || item.GrpcMessages[0].Name != "hello" || !strings.Contains(item.GrpcMessages[0].Content, `"Ada"`) {
		t.Fatalf("gRPC messages were not parsed: %#v", item.GrpcMessages)
	}

	roundTrip := stringifyBru(item)
	for _, expected := range []string{"grpc {", "methodType: unary", "protoPath: protos/greeter.proto", "metadata {", "~x-disabled: off", "body:grpc {", "name: second"} {
		if !strings.Contains(roundTrip, expected) {
			t.Fatalf("gRPC .bru did not preserve %q:\n%s", expected, roundTrip)
		}
	}
	if strings.Contains(roundTrip, "\nheaders {") {
		t.Fatalf("gRPC .bru should write metadata instead of headers:\n%s", roundTrip)
	}
	parsed, err := parseBru(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ProtoPath != item.ProtoPath || parsed.GrpcMethodType != item.GrpcMethodType || len(parsed.GrpcMessages) != 2 {
		t.Fatalf("gRPC .bru did not round-trip:\n%s\n%#v", roundTrip, parsed)
	}
}

func TestRequestDocsRoundTripForWebSocketAndGRPCBru(t *testing.T) {
	socket := types.NewRequestItem("Socket Docs", "websocket", 1)
	socket.URL = "ws://example.test/socket"
	socket.WSMessages = []WSMessage{{Name: "hello", Type: "text", Content: "ping", Selected: true}}

	grpcItem := types.NewRequestItem("Grpc Docs", "grpc", 2)
	grpcItem.URL = "grpc://localhost:50051"
	grpcItem.Method = "helloworld.Greeter/SayHello"
	grpcItem.GrpcMessages = []GrpcMessage{{Name: "hello", Content: `{"name":"Ada"}`}}

	for _, tc := range []struct {
		name string
		item RequestItem
	}{
		{name: "websocket", item: socket},
		{name: "grpc", item: grpcItem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := tc.item
			item.Docs = "Request docs survive storage."
			item.Vars.Req = []Variable{{Name: "token", Value: "abc", DataType: "string", Enabled: true}}
			item.Vars.Res = []Variable{{Name: "saved", Value: "ok", DataType: "string", Enabled: true}}
			item.PreScript = `bru.setVar("kind", req.getMethod());`
			item.PostScript = `bru.setVar("done", true);`
			item.Tests = `expect status equals 200`

			content := stringifyBru(item)
			for _, expected := range []string{"vars:pre-request {", "vars:post-response {", "script:pre-request {", "script:post-response {", "tests {", "docs {", item.Docs} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s .bru missing %q:\n%s", tc.name, expected, content)
				}
			}

			parsed, err := parseBru(content)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Docs != item.Docs ||
				strings.TrimSpace(parsed.PreScript) != item.PreScript ||
				strings.TrimSpace(parsed.PostScript) != item.PostScript ||
				strings.TrimSpace(parsed.Tests) != item.Tests {
				t.Fatalf("%s common blocks did not round-trip:\n%s\n%#v", tc.name, content, parsed)
			}
			if len(parsed.Vars.Req) != 1 || parsed.Vars.Req[0].Name != "token" || parsed.Vars.Req[0].Value != "abc" {
				t.Fatalf("%s pre-request variables did not round-trip: %#v", tc.name, parsed.Vars.Req)
			}
			if len(parsed.Vars.Res) != 1 || parsed.Vars.Res[0].Name != "saved" || parsed.Vars.Res[0].Value != "ok" {
				t.Fatalf("%s post-response variables did not round-trip: %#v", tc.name, parsed.Vars.Res)
			}
		})
	}
}

func TestOpenGrpcBruCollectionAndSavePreservesGrpcBlocks(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Grpc Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Grpc Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "greeter.bru"), []byte(`meta {
  name: Greeter
  type: grpc
  seq: 1
}

grpc {
  url: grpc://localhost:50051
  method: helloworld.Greeter/SayHello
  body: grpc
  auth: none
  methodType: unary
  protoPath: protos/greeter.proto
}

metadata {
  x-request-id: {{requestId}}
}

body:grpc {
  name: message 1
  content: {"name":"Ada"}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if opened.Name != "Grpc Collection" || len(opened.Items) != 1 {
		t.Fatalf("unexpected opened gRPC collection: %#v", opened)
	}
	item := opened.Items[0]
	if item.Type != "grpc" || item.ProtoPath != "protos/greeter.proto" || len(item.GrpcMessages) != 1 {
		t.Fatalf("gRPC request was not parsed from disk: %#v", item)
	}

	newURL := "grpc://api.example.test:443"
	newProto := "proto/v2/greeter.proto"
	newMethodType := "server-streaming"
	messages := []GrpcMessage{{Name: "stream", Content: `{"name":"Lin"}`}}
	if _, err := app.UpdateRequest(opened.ID, item.ID, RequestPatch{URL: &newURL, ProtoPath: &newProto, GrpcMethodType: &newMethodType, GrpcMessages: &messages}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveRequest(opened.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(collectionPath, "greeter.bru"))
	if err != nil {
		t.Fatal(err)
	}
	savedText := string(saved)
	for _, expected := range []string{"grpc {", "url: grpc://api.example.test:443", "methodType: server-streaming", "protoPath: proto/v2/greeter.proto", "metadata {", "body:grpc {", `{"name":"Lin"}`} {
		if !strings.Contains(savedText, expected) {
			t.Fatalf("saved gRPC .bru did not preserve %q:\n%s", expected, savedText)
		}
	}
	if strings.Contains(savedText, "\ncall {") || strings.Contains(savedText, "\nheaders {") {
		t.Fatalf("saved gRPC .bru degraded into generic request blocks:\n%s", savedText)
	}
}

func TestGrpcYAMLRoundTrip(t *testing.T) {
	item := types.NewRequestItem("YAML Greeter", "grpc", 9)
	item.URL = "grpc://localhost:50051"
	item.Method = "helloworld.Greeter/SayHello"
	item.GrpcMethodType = "client-streaming"
	item.ProtoPath = "protos/greeter.proto"
	item.Headers = []KeyValue{
		{Name: "authorization", Value: "Bearer {{token}}", Enabled: true, Description: "token"},
		{Name: "x-disabled", Value: "off", Enabled: false},
	}
	item.GrpcMessages = []GrpcMessage{{Name: "hello", Content: `{"name":"Ada"}`}}

	content, err := stringifyYAMLRequest(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"grpc:", "methodType: client-streaming", "protoFilePath: protos/greeter.proto", "metadata:", "disabled: true", "message:"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("gRPC YAML did not preserve %q:\n%s", expected, content)
		}
	}
	parsed, err := parseYAMLRequest(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != "grpc" || parsed.Method != item.Method || parsed.GrpcMethodType != item.GrpcMethodType || parsed.ProtoPath != item.ProtoPath {
		t.Fatalf("gRPC YAML core fields did not round-trip:\n%s\n%#v", content, parsed)
	}
	if len(parsed.Headers) != 2 || parsed.Headers[1].Enabled {
		t.Fatalf("gRPC YAML metadata did not round-trip: %#v", parsed.Headers)
	}
	if len(parsed.GrpcMessages) != 1 || parsed.GrpcMessages[0].Name != "hello" || !strings.Contains(parsed.GrpcMessages[0].Content, "Ada") {
		t.Fatalf("gRPC YAML messages did not round-trip: %#v", parsed.GrpcMessages)
	}
}

func TestGRPCUnaryRequestExecutesWithProtoAndMetadata(t *testing.T) {
	root := t.TempDir()
	protoPath := filepath.Join(root, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(`syntax = "proto3";
package helloworld;

service Greeter {
  rpc SayHello (HelloRequest) returns (HelloReply);
}

message HelloRequest {
  string name = 1;
}

message HelloReply {
  string message = 1;
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	gotMetadata := map[string]string{}
	address, stop := startDynamicGreeterServer(t, protoPath, gotMetadata)
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Say Hello")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "helloworld.Greeter/SayHello"
	headers := []KeyValue{{Name: "x-request-id", Value: "abc-123", Enabled: true}}
	messages := []GrpcMessage{{Name: "hello", Content: `{"name":"Ada"}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		Headers:      &headers,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("gRPC response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("gRPC unary call failed: %#v", updated.Response)
	}
	if !strings.Contains(updated.Response.Body, "hello Ada") || updated.Response.Headers["grpc-status"] != "0" {
		t.Fatalf("gRPC response body/headers were not captured: %#v", updated.Response)
	}
	if gotMetadata["x-request-id"] != "abc-123" {
		t.Fatalf("gRPC metadata was not sent: %#v", gotMetadata)
	}
}

func TestGRPCUnaryRequestExecutesOverUnixSocket(t *testing.T) {
	root := t.TempDir()
	protoPath := filepath.Join(root, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(`syntax = "proto3";
package helloworld;

service Greeter {
  rpc SayHello (HelloRequest) returns (HelloReply);
}

message HelloRequest {
  string name = 1;
}

message HelloReply {
  string message = 1;
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("liteapi-grpc-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(socketPath) }()
	gotMetadata := map[string]string{}
	stop := startDynamicGreeterServerOnListener(t, protoPath, listener, gotMetadata)
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Unix Say Hello")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := (&url.URL{Scheme: "unix", Path: socketPath}).String()
	method := "helloworld.Greeter/SayHello"
	headers := []KeyValue{{Name: "x-request-id", Value: "unix-123", Enabled: true}}
	messages := []GrpcMessage{{Name: "unix", Content: `{"name":"Unix"}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		Headers:      &headers,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("gRPC Unix-socket response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("gRPC Unix-socket call failed: %#v", updated.Response)
	}
	if !strings.Contains(updated.Response.Body, "hello Unix") || updated.Response.Headers["grpc-status"] != "0" {
		t.Fatalf("gRPC Unix-socket response body/headers were not captured: %#v", updated.Response)
	}
	if gotMetadata["x-request-id"] != "unix-123" {
		t.Fatalf("gRPC Unix-socket metadata was not sent: %#v", gotMetadata)
	}
	if _, err := grpcDialTarget("unix://relative.sock"); err == nil {
		t.Fatal("relative Unix socket paths should be rejected")
	}
}

func TestGRPCUnaryRequestCanUseServerReflection(t *testing.T) {
	gotMetadata := map[string]string{}
	address, stop := startReflectedTestService(t, gotMetadata)
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Reflected Unary")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/UnaryCall"
	protoPath := ""
	headers := []KeyValue{
		{Name: "x-reflected", Value: "yes", Enabled: true},
		{Name: "x-reflected-bin", Value: base64.StdEncoding.EncodeToString([]byte("binary-reflected")), Enabled: true},
	}
	messages := []GrpcMessage{{Name: "reflection", Content: `{"fillUsername": true}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		Headers:      &headers,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("reflected gRPC response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("reflected gRPC unary call failed: %#v", updated.Response)
	}
	if !strings.Contains(updated.Response.Body, `"reflected"`) || updated.Response.Headers["grpc-method"] != "/grpc.testing.TestService/UnaryCall" {
		t.Fatalf("reflected gRPC response body/headers were not captured: %#v", updated.Response)
	}
	if gotMetadata["x-reflected"] != "yes" {
		t.Fatalf("reflected gRPC metadata was not sent: %#v", gotMetadata)
	}
	if gotMetadata["x-reflected-bin"] != "binary-reflected" {
		t.Fatalf("reflected gRPC binary metadata was not decoded before send: %#v", gotMetadata)
	}
	if getKeyValue(updated.Response.Metadata, "x-grpc-initial") != "unary-header" || getKeyValue(updated.Response.Trailers, "x-grpc-trailer") != "unary-trailer" {
		t.Fatalf("reflected gRPC metadata/trailers were not separated: metadata=%#v trailers=%#v headers=%#v", updated.Response.Metadata, updated.Response.Trailers, updated.Response.Headers)
	}
}

func TestGRPCUnaryRequestUsesUserAgentHeaderAsDialUserAgent(t *testing.T) {
	gotMetadata := map[string]string{}
	address, stop := startReflectedTestService(t, gotMetadata)
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Reflected User Agent")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/UnaryCall"
	protoPath := ""
	headers := []KeyValue{
		{Name: "User-Agent", Value: "LiteAPI-UA-Test/1.2", Enabled: true},
		{Name: "x-user-agent-sidecar", Value: "metadata-ok", Enabled: true},
		{Name: "user-agent", Value: "ignored-disabled", Enabled: false},
	}
	messages := []GrpcMessage{{Name: "reflection", Content: `{"fillUsername": true}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		Headers:      &headers,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("reflected gRPC response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("reflected gRPC unary call failed: %#v", updated.Response)
	}
	if got := gotMetadata["user-agent"]; !strings.Contains(got, "LiteAPI-UA-Test/1.2") {
		t.Fatalf("gRPC User-Agent header was not applied as the dial user agent: %#v", gotMetadata)
	}
	if gotMetadata["x-user-agent-sidecar"] != "metadata-ok" {
		t.Fatalf("ordinary gRPC metadata was not sent alongside User-Agent: %#v", gotMetadata)
	}
}

func TestGRPCUnaryRequestAppliesOAuth2AndWSSEMetadata(t *testing.T) {
	gotMetadata := map[string]string{}
	address, stop := startReflectedTestService(t, gotMetadata)
	defer stop()

	tokenCalls := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected OAuth2 token method: %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Fatalf("unexpected OAuth2 grant_type: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"grpc-oauth-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Reflected OAuth2")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/UnaryCall"
	protoPath := ""
	messages := []GrpcMessage{{Name: "reflection", Content: `{"fillUsername": true}`}}
	oauthAuth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:         "client_credentials",
		AccessTokenURL:    tokenServer.URL,
		ClientID:          "grpc-client",
		ClientSecret:      "grpc-secret",
		TokenHeaderPrefix: "Token",
	}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		GrpcMessages: &messages,
		Auth:         &oauthAuth,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil || updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("OAuth2 gRPC request failed: %#v", updated.Response)
	}
	if gotMetadata["authorization"] != "Token grpc-oauth-token" || tokenCalls != 1 {
		t.Fatalf("OAuth2 token was not applied as gRPC metadata: metadata=%#v tokenCalls=%d", gotMetadata, tokenCalls)
	}

	gotMetadata = map[string]string{}
	address, stop = startReflectedTestService(t, gotMetadata)
	defer stop()
	state, err = app.CreateRequest(collection.ID, "grpc", "Reflected WSSE")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item = collection.Items[len(collection.Items)-1]
	targetURL = "grpc://" + address
	wsseAuth := AuthConfig{Mode: "wsse", Username: "grpc-user", Password: "grpc-pass"}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		GrpcMessages: &messages,
		Auth:         &wsseAuth,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil || updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("WSSE gRPC request failed: %#v", updated.Response)
	}
	wsse := gotMetadata["x-wsse"]
	for _, expected := range []string{`UsernameToken`, `Username="grpc-user"`, `PasswordDigest="`, `Nonce="`, `Created="`} {
		if !strings.Contains(wsse, expected) {
			t.Fatalf("WSSE metadata missing %q: %q metadata=%#v", expected, wsse, gotMetadata)
		}
	}
}

func TestGRPCUnaryRequestUsesCollectionClientCertificate(t *testing.T) {
	certPEM, keyPEM, _, _ := testClientCertificate(t)
	gotMetadata := map[string]string{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCert := testServerTLSCertificate(t)
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
	})))
	grpc_testing.RegisterTestServiceServer(server, &reflectedTestService{gotMetadata: gotMetadata})
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	certDir := filepath.Join(collection.Path, "certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "client.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "client.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if _, err := app.UpdateCollectionClientCertificates(collection.ID, []ClientCertificateConfig{{
		Domain:       "grpcs://" + address,
		Type:         "cert",
		CertFilePath: "certs/client.pem",
		KeyFilePath:  "certs/client.key",
	}}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "grpc", "mTLS Reflected Unary")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpcs://" + address
	method := "grpc.testing.TestService/UnaryCall"
	protoPath := ""
	messages := []GrpcMessage{{Name: "reflection", Content: `{"fillUsername": true}`}}
	settings := item.Settings
	settings.VerifyTLS = false
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		GrpcMessages: &messages,
		Settings:     &settings,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("mTLS gRPC response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" {
		t.Fatalf("mTLS gRPC unary call failed: %#v", updated.Response)
	}
	if !strings.Contains(updated.Response.Body, `"reflected"`) || updated.Response.Headers["grpc-method"] != "/grpc.testing.TestService/UnaryCall" {
		t.Fatalf("mTLS gRPC response body/headers were not captured: %#v", updated.Response)
	}
	if gotMetadata["peer-cert-cn"] != "liteapi-client" {
		t.Fatalf("gRPC server did not receive collection client certificate: %#v", gotMetadata)
	}
}

func TestGenerateGrpcurlCommandIncludesMetadataProtoAndBody(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	protoDir := filepath.Join(collection.Path, "protos")
	if err := os.MkdirAll(protoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protoDir, "greeter.proto"), []byte(`syntax = "proto3";`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "grpc", "grpcurl unary")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://api.example.test:50051/prefix"
	method := "helloworld.Greeter/SayHello"
	protoPath := "protos/greeter.proto"
	headers := []KeyValue{{Name: "x-request-id", Value: "abc-123", Enabled: true}, {Name: "x-disabled", Value: "no", Enabled: false}}
	messages := []GrpcMessage{{Name: "hello", Content: `{"name":"Ada"}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		Headers:      &headers,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}

	command, err := app.GenerateGrpcurlCommand(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"grpcurl -plaintext",
		"-H 'x-request-id: abc-123'",
		"-import-path '" + protoDir + "'",
		"-proto 'greeter.proto'",
		"-d '{\"name\":\"Ada\"}'",
		"api.example.test:50051 prefix/helloworld.Greeter/SayHello",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("grpcurl command missing %q:\n%s", expected, command)
		}
	}
	if strings.Contains(command, "x-disabled") {
		t.Fatalf("grpcurl command included disabled metadata:\n%s", command)
	}
}

func TestGenerateGrpcurlCommandUsesCollectionClientCertificate(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.UpdateCollectionClientCertificates(collection.ID, []ClientCertificateConfig{{
		Domain:       "grpcs://secure.example.test:443",
		Type:         "cert",
		CertFilePath: "certs/client.pem",
		KeyFilePath:  "certs/client.key",
	}}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "grpc", "grpcurl mtls")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpcs://secure.example.test:443"
	method := "grpc.testing.TestService/UnaryCall"
	settings := item.Settings
	settings.VerifyTLS = false
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Method: &method, Settings: &settings}); err != nil {
		t.Fatal(err)
	}

	command, err := app.GenerateGrpcurlCommand(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(collection.Path, "certs", "client.pem")
	keyPath := filepath.Join(collection.Path, "certs", "client.key")
	for _, expected := range []string{
		"grpcurl -insecure",
		"-cert '" + certPath + "'",
		"-key '" + keyPath + "'",
		"secure.example.test:443 grpc.testing.TestService/UnaryCall",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("grpcurl command missing %q:\n%s", expected, command)
		}
	}
	if strings.Contains(command, "-plaintext") {
		t.Fatalf("secure grpcurl command should not use plaintext:\n%s", command)
	}
}

func TestGenerateGrpcurlCommandUsesHeredocForClientStreaming(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "grpcurl stream")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://127.0.0.1:50051"
	method := "grpc.testing.TestService/StreamingInputCall"
	methodType := "client-streaming"
	messages := []GrpcMessage{
		{Name: "one", Content: `{"payload":{"body":"YQ=="}}`},
		{Name: "two", Content: `{"payload":{"body":"YmM="}}`},
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:            &targetURL,
		Method:         &method,
		GrpcMethodType: &methodType,
		GrpcMessages:   &messages,
	}); err != nil {
		t.Fatal(err)
	}

	command, err := app.GenerateGrpcurlCommand(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"grpcurl -plaintext -d @ 127.0.0.1:50051 grpc.testing.TestService/StreamingInputCall",
		"<< EOF\n{\"payload\":{\"body\":\"YQ==\"}}\n{\"payload\":{\"body\":\"YmM=\"}}\nEOF",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("grpcurl command missing %q:\n%s", expected, command)
		}
	}
}

func TestGRPCServerStreamingRequestReturnsJSONMessages(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	method := "grpc.testing.TestService/StreamingOutputCall"
	res := sendReflectedGRPCRequest(t, address, method, []GrpcMessage{{Name: "stream", Content: `{}`}})
	if res.Status != http.StatusOK || res.Error != "" {
		t.Fatalf("server-streaming gRPC call failed: %#v", res)
	}
	if res.Headers["grpc-stream"] != "server" || res.Headers["grpc-request-count"] != "1" || res.Headers["grpc-response-count"] != "2" || !strings.Contains(res.Body, "c2VydmVyLW9uZQ==") || !strings.Contains(res.Body, "c2VydmVyLXR3bw==") {
		t.Fatalf("server-streaming gRPC response was not captured as an array: %#v", res)
	}
}

func TestGRPCServerStreamingErrorPreservesMetadataTrailersAndPartialResponses(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Stream Error Metadata")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/StreamingOutputCall"
	protoPath := ""
	messages := []GrpcMessage{{Name: "stream", Content: `{"payload":{"body":"ZXJyb3ItYWZ0ZXItb25l"}}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &protoPath,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("gRPC response was not recorded: %#v", updated)
	}
	res := updated.Response
	if res.Status != int(codes.ResourceExhausted) || res.StatusText != codes.ResourceExhausted.String() || res.Error != "stream quota" {
		t.Fatalf("server-streaming error status was not captured: %#v", res)
	}
	if res.Headers["grpc-status"] != strconv.Itoa(int(codes.ResourceExhausted)) ||
		res.Headers["grpc-method"] != "/grpc.testing.TestService/StreamingOutputCall" ||
		res.Headers["grpc-stream"] != "server" ||
		res.Headers["grpc-request-count"] != "1" ||
		res.Headers["grpc-response-count"] != "1" {
		t.Fatalf("server-streaming error headers missing method/status/counts: %#v", res.Headers)
	}
	if !strings.Contains(res.Body, "cGFydGlhbA==") {
		t.Fatalf("server-streaming partial response body was not preserved: %s", res.Body)
	}
	if getKeyValue(res.Metadata, "x-error-initial") != "one, two" ||
		getKeyValue(res.Metadata, "x-error-bin") != base64.StdEncoding.EncodeToString([]byte("binary-initial")) {
		t.Fatalf("server-streaming error metadata was not captured/displayed: %#v", res.Metadata)
	}
	if getKeyValue(res.Trailers, "x-error-trailer") != "trail-one, trail-two" ||
		getKeyValue(res.Trailers, "x-error-trailer-bin") != base64.StdEncoding.EncodeToString([]byte("binary-trailer")) {
		t.Fatalf("server-streaming error trailers were not captured/displayed: %#v", res.Trailers)
	}
	eventTypes := map[string]int{}
	for _, row := range updated.Timeline {
		if row.Source == "grpc" {
			eventTypes[row.EventType]++
			if row.EventType == "status" && getKeyValue(row.Trailers, "x-error-trailer") != "trail-one, trail-two" {
				t.Fatalf("status timeline row missing trailers: %#v", row)
			}
			if row.EventType != "error" && row.Error != "" {
				t.Fatalf("non-error gRPC timeline row should not carry terminal error text: %#v", row)
			}
			if row.EventType == "response" && !strings.Contains(row.Payload, "cGFydGlhbA==") {
				t.Fatalf("response timeline row missing partial payload: %#v", row)
			}
			if row.EventType == "error" {
				if !strings.Contains(row.Payload, "stream quota") || row.Error != "stream quota" {
					t.Fatalf("error timeline row missing status payload: %#v", row)
				}
				if getKeyValue(row.Trailers, "x-error-trailer") != "trail-one, trail-two" {
					t.Fatalf("error timeline row missing trailers: %#v", row)
				}
			}
		}
	}
	for eventType, expectedCount := range map[string]int{"request": 1, "response": 1, "metadata": 1, "status": 1, "error": 1} {
		if eventTypes[eventType] != expectedCount {
			t.Fatalf("gRPC error timeline event type %q count = %d, want %d: %#v", eventType, eventTypes[eventType], expectedCount, updated.Timeline)
		}
	}
	if eventTypes["message"] != 0 || eventTypes["end"] != 0 {
		t.Fatalf("server-streaming error timeline should not include outbound message/end rows: %#v", updated.Timeline)
	}
}

func TestGRPCClientStreamingRequestSendsAllMessages(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	method := "grpc.testing.TestService/StreamingInputCall"
	res := sendReflectedGRPCRequest(t, address, method, []GrpcMessage{
		{Name: "one", Content: `{"payload":{"body":"YQ=="}}`},
		{Name: "two", Content: `{"payload":{"body":"YmM="}}`},
	})
	if res.Status != http.StatusOK || res.Error != "" {
		t.Fatalf("client-streaming gRPC call failed: %#v", res)
	}
	if res.Headers["grpc-stream"] != "client" || res.Headers["grpc-request-count"] != "2" || res.Headers["grpc-response-count"] != "1" || !strings.Contains(res.Body, "aggregatedPayloadSize") || !strings.Contains(res.Body, "3") {
		t.Fatalf("client-streaming gRPC response was not captured: %#v", res)
	}
}

func TestGRPCBidiStreamingRequestSendsAndReceivesMessages(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	method := "grpc.testing.TestService/FullDuplexCall"
	res := sendReflectedGRPCRequest(t, address, method, []GrpcMessage{
		{Name: "one", Content: `{"payload":{"body":"b25l"}}`},
		{Name: "two", Content: `{"payload":{"body":"dHdv"}}`},
	})
	if res.Status != http.StatusOK || res.Error != "" {
		t.Fatalf("bidi-streaming gRPC call failed: %#v", res)
	}
	if res.Headers["grpc-stream"] != "bidi" || res.Headers["grpc-request-count"] != "2" || res.Headers["grpc-response-count"] != "2" || !strings.Contains(res.Body, "b25l") || !strings.Contains(res.Body, "dHdv") {
		t.Fatalf("bidi-streaming gRPC response was not captured: %#v", res)
	}
}

func TestGRPCStreamingTimelineCapturesMethodAndCounts(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Bidi Timeline")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/FullDuplexCall"
	messages := []GrpcMessage{
		{Name: "one", Content: `{"payload":{"body":"b25l"}}`},
		{Name: "two", Content: `{"payload":{"body":"dHdv"}}`},
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("gRPC response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Headers["grpc-stream"] != "bidi" {
		t.Fatalf("bidi gRPC response failed: %#v", updated.Response)
	}
	var row *TimelineItem
	eventTypes := map[string]int{}
	responsePayloads := []string{}
	metadataRows := 0
	trailerRows := 0
	for index := range updated.Timeline {
		entry := &updated.Timeline[index]
		if entry.Source == "main" {
			row = entry
		}
		if entry.Source == "grpc" {
			eventTypes[entry.EventType]++
			if entry.EventType == "response" {
				responsePayloads = append(responsePayloads, entry.Payload)
			}
			if entry.EventType == "metadata" && getKeyValue(entry.Metadata, "x-grpc-initial") == "bidi-header" {
				metadataRows++
			}
			if entry.EventType == "status" && getKeyValue(entry.Trailers, "x-grpc-trailer") == "bidi-trailer" {
				trailerRows++
			}
		}
	}
	if row == nil || row.Kind != "request" || row.Source != "main" || row.Method != "CALL" || row.URL != targetURL || row.Status != http.StatusOK {
		t.Fatalf("bad gRPC timeline identity: %#v", updated.Timeline)
	}
	for _, expected := range []string{"FullDuplexCall", "bidi stream", "sent 2", "received 2"} {
		if !strings.Contains(row.Message, expected) {
			t.Fatalf("gRPC timeline message missing %q: %#v", expected, row)
		}
	}
	for eventType, expectedCount := range map[string]int{"request": 1, "message": 2, "response": 2, "metadata": 1, "status": 1, "end": 1} {
		if eventTypes[eventType] != expectedCount {
			t.Fatalf("gRPC timeline event type %q count = %d, want %d: %#v", eventType, eventTypes[eventType], expectedCount, updated.Timeline)
		}
	}
	if len(responsePayloads) != 2 || !strings.Contains(responsePayloads[0], "b25l") || !strings.Contains(responsePayloads[1], "dHdv") {
		t.Fatalf("gRPC response timeline payloads missing echoed messages: %#v", responsePayloads)
	}
	if metadataRows != 1 || trailerRows != 1 {
		t.Fatalf("gRPC metadata/trailer timeline rows missing detail: metadataRows=%d trailerRows=%d timeline=%#v", metadataRows, trailerRows, updated.Timeline)
	}
}

func TestGRPCLiveBidiStreamSendEndRecordsEvents(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Live Bidi")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/FullDuplexCall"
	methodType := "bidi-streaming"
	messages := []GrpcMessage{
		{Name: "one", Content: `{"payload":{"body":"b25l"}}`},
		{Name: "two", Content: `{"payload":{"body":"dHdv"}}`},
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:            &targetURL,
		Method:         &method,
		GrpcMethodType: &methodType,
		GrpcMessages:   &messages,
	}); err != nil {
		t.Fatal(err)
	}

	state, err = app.ConnectGRPCStream(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("live gRPC connect did not record response: %#v", updated)
	}
	if updated.Response.Headers["x-grpc-stream-connected"] != "true" || updated.Response.Headers["grpc-request-count"] != "0" || updated.Response.Headers["grpc-response-count"] != "0" {
		t.Fatalf("unexpected live gRPC connected response: %#v", updated.Response)
	}

	state, err = app.SendGRPCStreamMessage(collection.ID, item.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SendGRPCStreamMessage(collection.ID, item.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("live gRPC send did not record response: %#v", updated)
	}
	if updated.Response.Headers["x-grpc-stream-connected"] != "true" || updated.Response.Headers["grpc-request-count"] != "2" || updated.Response.Headers["grpc-response-count"] != "2" {
		t.Fatalf("unexpected live gRPC send response: %#v", updated.Response)
	}
	for _, expected := range []string{`"direction": "sent"`, `"direction": "received"`, `"name": "one"`, `"name": "two"`, `b25l`, `dHdv`} {
		if !strings.Contains(updated.Response.Body, expected) {
			t.Fatalf("live gRPC send history missing %q:\n%s", expected, updated.Response.Body)
		}
	}

	state, err = app.EndGRPCStream(collection.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("live gRPC end did not record response: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" ||
		updated.Response.Headers["x-grpc-stream-connected"] != "false" ||
		updated.Response.Headers["x-grpc-stream-ended"] != "true" ||
		updated.Response.Headers["grpc-status"] != "0" ||
		updated.Response.Headers["grpc-request-count"] != "2" ||
		updated.Response.Headers["grpc-response-count"] != "2" {
		t.Fatalf("unexpected live gRPC end response: %#v", updated.Response)
	}
	for _, expected := range []string{`"direction": "received"`, `"type": "end"`, `b25l`, `dHdv`} {
		if !strings.Contains(updated.Response.Body, expected) {
			t.Fatalf("live gRPC end history missing %q:\n%s", expected, updated.Response.Body)
		}
	}
	if getKeyValue(updated.Response.Metadata, "x-grpc-initial") != "bidi-header" || getKeyValue(updated.Response.Trailers, "x-grpc-trailer") != "bidi-trailer" {
		t.Fatalf("live gRPC metadata/trailers were not separated: metadata=%#v trailers=%#v headers=%#v", updated.Response.Metadata, updated.Response.Trailers, updated.Response.Headers)
	}
	eventTypes := map[string]int{}
	responsePayloads := []string{}
	for _, row := range updated.Timeline {
		eventTypes[row.EventType]++
		if row.EventType == "response" {
			responsePayloads = append(responsePayloads, row.Payload)
		}
	}
	for eventType, expectedCount := range map[string]int{"request": 1, "message": 2, "response": 2, "metadata": 1, "status": 1, "end": 1} {
		if eventTypes[eventType] != expectedCount {
			t.Fatalf("live gRPC timeline event type %q count = %d, want %d: %#v", eventType, eventTypes[eventType], expectedCount, updated.Timeline)
		}
	}
	if len(responsePayloads) != 2 || !strings.Contains(responsePayloads[0], "b25l") || !strings.Contains(responsePayloads[1], "dHdv") {
		t.Fatalf("live gRPC response timeline payloads missing echoed messages: %#v", responsePayloads)
	}
}

func TestGRPCLiveStreamCancelMarksSessionCancelled(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Cancel Bidi")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/FullDuplexCall"
	methodType := "bidi-streaming"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:            &targetURL,
		Method:         &method,
		GrpcMethodType: &methodType,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ConnectGRPCStream(collection.ID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	state, err = app.CancelGRPCStream(collection.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("live gRPC cancel did not record response: %#v", updated)
	}
	if updated.Response.Status != 1 ||
		updated.Response.StatusText != "CANCELLED" ||
		updated.Response.Headers["x-grpc-stream-connected"] != "false" ||
		updated.Response.Headers["x-grpc-stream-close-reason"] != "cancelled" ||
		updated.Response.Error != "cancelled" ||
		!strings.Contains(updated.Response.Body, `"type": "cancel"`) {
		t.Fatalf("unexpected live gRPC cancel response: %#v\n%s", updated.Response, updated.Response.Body)
	}
}

func TestGRPCMethodsFromProtoGenerateSampleMessage(t *testing.T) {
	root := t.TempDir()
	protoPath := filepath.Join(root, "sample.proto")
	if err := os.WriteFile(protoPath, []byte(`syntax = "proto3";
package sample;

service SampleService {
  rpc Create (SampleRequest) returns (SampleReply);
  rpc Watch (SampleRequest) returns (stream SampleReply);
}

message SampleRequest {
  string name = 1;
  int32 count = 2;
  repeated string tags = 3;
  Nested nested = 4;
  map<string, int32> scores = 5;
  enum Mood {
    MOOD_UNSPECIFIED = 0;
    HAPPY = 1;
  }
  Mood mood = 6;
}

message Nested {
  bool active = 1;
}

message SampleReply {
  string id = 1;
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Sample")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	method := "sample.SampleService/Create"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, ProtoPath: &protoPath}); err != nil {
		t.Fatal(err)
	}
	methods, err := app.ListGRPCMethods(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 {
		t.Fatalf("expected two proto methods, got %#v", methods)
	}
	var createMethod GRPCMethodInfo
	var watchMethod GRPCMethodInfo
	for _, candidate := range methods {
		if candidate.Path == "sample.SampleService/Create" {
			createMethod = candidate
		}
		if candidate.Path == "sample.SampleService/Watch" {
			watchMethod = candidate
		}
	}
	if createMethod.Type != "unary" || watchMethod.Type != "server-streaming" || !strings.Contains(createMethod.Template, `"nested"`) {
		t.Fatalf("method metadata/templates were not generated: %#v %#v", createMethod, watchMethod)
	}
	sample, err := app.GenerateGRPCMessage(collection.ID, item.ID, "", "sample.SampleService/Create")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"name": "string"`, `"count": 0`, `"tags"`, `"scores"`, `"mood": "MOOD_UNSPECIFIED"`} {
		if !strings.Contains(sample, expected) {
			t.Fatalf("sample message missing %q:\n%s", expected, sample)
		}
	}
}

func TestCollectionProtobufFallbackLoadsImportedMethodsAndSamples(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	protoRoot := filepath.Join(collection.Path, "protos")
	if err := os.MkdirAll(filepath.Join(protoRoot, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(protoRoot, "messages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protoRoot, "messages", "hello.proto"), []byte(`syntax = "proto3";
package helloworld.messages;

message HelloRequest {
  string name = 1;
}

message HelloReply {
  string message = 1;
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protoRoot, "services", "greeter.proto"), []byte(`syntax = "proto3";
package helloworld;

import "messages/hello.proto";

service Greeter {
  rpc SayHello (helloworld.messages.HelloRequest) returns (helloworld.messages.HelloReply);
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = app.UpdateCollectionProtobuf(collection.ID, CollectionProtobufConfig{
		ProtoFiles: []CollectionProtoFile{{Path: "protos/services/greeter.proto", Type: "file"}},
		ImportPaths: []CollectionProtoImportPath{
			{Path: "protos", Enabled: true},
			{Path: "disabled-protos", Enabled: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Collection Proto Methods")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	method := "helloworld.Greeter/SayHello"
	emptyProtoPath := ""
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, ProtoPath: &emptyProtoPath}); err != nil {
		t.Fatal(err)
	}
	methods, err := app.ListGRPCMethods(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || methods[0].Path != "helloworld.Greeter/SayHello" || methods[0].Type != "unary" {
		t.Fatalf("collection proto methods were not discovered: %#v", methods)
	}
	sample, err := app.GenerateGRPCMessage(collection.ID, item.ID, "", "helloworld.Greeter/SayHello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sample, `"name": "string"`) {
		t.Fatalf("collection proto sample was not generated:\n%s", sample)
	}
}

func TestGRPCUnaryRequestUsesCollectionProtobufConfig(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	protoDir := filepath.Join(collection.Path, "protos")
	if err := os.MkdirAll(protoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	protoPath := filepath.Join(protoDir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(`syntax = "proto3";
package helloworld;

service Greeter {
  rpc SayHello (HelloRequest) returns (HelloReply);
}

message HelloRequest {
  string name = 1;
}

message HelloReply {
  string message = 1;
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	gotMetadata := map[string]string{}
	address, stop := startDynamicGreeterServer(t, protoPath, gotMetadata)
	defer stop()

	state, err = app.UpdateCollectionProtobuf(collection.ID, CollectionProtobufConfig{
		ProtoFiles:  []CollectionProtoFile{{Path: "protos/greeter.proto", Type: "file"}},
		ImportPaths: []CollectionProtoImportPath{{Path: "protos", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Collection Say Hello")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "helloworld.Greeter/SayHello"
	emptyProtoPath := ""
	headers := []KeyValue{{Name: "x-request-id", Value: "collection-proto", Enabled: true}}
	messages := []GrpcMessage{{Name: "hello", Content: `{"name":"Collection"}`}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:          &targetURL,
		Method:       &method,
		ProtoPath:    &emptyProtoPath,
		Headers:      &headers,
		GrpcMessages: &messages,
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || updated.Response == nil {
		t.Fatalf("gRPC response was not recorded: %#v", updated)
	}
	if updated.Response.Status != http.StatusOK || updated.Response.Error != "" || !strings.Contains(updated.Response.Body, "hello Collection") {
		t.Fatalf("collection protobuf gRPC call failed: %#v", updated.Response)
	}
	if gotMetadata["x-request-id"] != "collection-proto" {
		t.Fatalf("gRPC metadata was not sent: %#v", gotMetadata)
	}
}

func TestGRPCMethodsFromReflectionGenerateSampleMessage(t *testing.T) {
	address, stop := startReflectedTestService(t, map[string]string{})
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Reflect Methods")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/UnaryCall"
	protoPath := ""
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Method: &method, ProtoPath: &protoPath}); err != nil {
		t.Fatal(err)
	}
	methods, err := app.ListGRPCMethods(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, method := range methods {
		seen[method.Path] = method.Type
	}
	if seen["grpc.testing.TestService/UnaryCall"] != "unary" || seen["grpc.testing.TestService/StreamingOutputCall"] != "server-streaming" || seen["grpc.testing.TestService/StreamingInputCall"] != "client-streaming" || seen["grpc.testing.TestService/FullDuplexCall"] != "bidi-streaming" {
		t.Fatalf("reflected method types were not listed: %#v", seen)
	}
	sample, err := app.GenerateGRPCMessage(collection.ID, item.ID, "", "grpc.testing.TestService/UnaryCall")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sample, "responseType") || !strings.Contains(sample, "fillUsername") {
		t.Fatalf("reflected sample message was not generated:\n%s", sample)
	}
}

func TestGRPCReflectionUsesRequestMetadataAndAuth(t *testing.T) {
	tokenCalls := int32(0)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Fatalf("unexpected OAuth2 grant type: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"reflection-oauth-token","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	address, reflectionHits, stop := startAuthenticatedReflectedTestService(t, map[string]string{
		"x-reflection-token": "open-sesame",
		"authorization":      "Token reflection-oauth-token",
	})
	defer stop()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "grpc", "Authenticated Reflection")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "grpc://" + address
	method := "grpc.testing.TestService/UnaryCall"
	protoPath := ""
	headers := []KeyValue{{Name: "x-reflection-token", Value: "open-sesame", Enabled: true}}
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:         "client_credentials",
		AccessTokenURL:    tokenServer.URL,
		ClientID:          "reflection-client",
		ClientSecret:      "reflection-secret",
		TokenHeaderPrefix: "Token",
	}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &targetURL,
		Method:    &method,
		ProtoPath: &protoPath,
		Headers:   &headers,
		Auth:      &auth,
	}); err != nil {
		t.Fatal(err)
	}

	methods, err := app.ListGRPCMethods(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, method := range methods {
		seen[method.Path] = method.Type
	}
	if seen["grpc.testing.TestService/UnaryCall"] != "unary" {
		t.Fatalf("authenticated reflection methods were not listed: %#v", seen)
	}
	sample, err := app.GenerateGRPCMessage(collection.ID, item.ID, "", "grpc.testing.TestService/UnaryCall")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sample, "fillUsername") {
		t.Fatalf("authenticated reflection sample message was not generated:\n%s", sample)
	}
	if got := atomic.LoadInt32(reflectionHits); got < 3 {
		t.Fatalf("expected reflection to be exercised for list and sample, got %d hit(s)", got)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Fatalf("expected one cached OAuth2 token fetch across reflection calls, got %d", got)
	}
}

func TestWebSocketRequestSendsAndReadsMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read ws: %v", err)
		}
		if string(payload) != "hello Ada" {
			t.Fatalf("unexpected ws payload: %s", payload)
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("echo: "+string(payload))); err != nil {
			t.Fatalf("write ws: %v", err)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Echo socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	messages := []WSMessage{{Name: "message 1", Type: "text", Content: "hello {{name}}", Selected: true}}
	vars := RequestVars{Req: []Variable{{ID: "name", Name: "name", Value: "Ada", DataType: "string", Enabled: true}}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages, Vars: &vars}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing websocket response")
	}
	if item.Response.Status != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d: %s", item.Response.Status, item.Response.Error)
	}
	if item.Response.Body != "echo: hello Ada" {
		t.Fatalf("unexpected websocket body: %q", item.Response.Body)
	}
	if item.Response.PreviewMode != "websocket" {
		t.Fatalf("expected websocket preview mode, got %q", item.Response.PreviewMode)
	}
}

func TestWebSocketLiveConnectionUsesCollectionManualProxy(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade target ws: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read target ws: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("proxied live: "+string(payload))); err != nil {
			t.Fatalf("write target ws: %v", err)
		}
	}))
	defer target.Close()

	var proxyHits int32
	var proxiedURL string
	var proxyAuth string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHits, 1)
		proxiedURL = r.URL.String()
		proxyAuth = r.Header.Get("Proxy-Authorization")
		if r.Method != http.MethodConnect {
			t.Fatalf("expected CONNECT proxy request, got %s %q", r.Method, r.URL.String())
		}
		targetAddress := firstNonEmpty(r.Host, r.URL.Host)
		targetConn, err := net.DialTimeout("tcp", targetAddress, time.Second)
		if err != nil {
			t.Fatalf("dial proxy target: %v", err)
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("proxy response writer cannot hijack")
		}
		clientConn, clientRW, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("hijack proxy connection: %v", err)
		}
		if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			t.Fatalf("write proxy tunnel response: %v", err)
		}
		go func() {
			defer func() { _ = targetConn.Close() }()
			defer func() { _ = clientConn.Close() }()
			_, _ = io.Copy(targetConn, clientRW)
		}()
		go func() {
			defer func() { _ = targetConn.Close() }()
			defer func() { _ = clientConn.Close() }()
			_, _ = io.Copy(clientConn, targetConn)
		}()
	}))
	defer proxy.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	proxyHost, proxyPort := splitTestServerHostPort(t, proxy.URL)
	if _, err := app.UpdateCollectionProxy(collection.ID, ProxyConfig{
		Inherit:  false,
		Protocol: "http",
		Hostname: proxyHost,
		Port:     proxyPort,
		Auth:     ProxyAuthConfig{Username: "proxy-user", Password: "proxy-pass"},
	}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "websocket", "Proxy socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(target.URL, "http") + "/socket?via=proxy"
	messages := []WSMessage{{Name: "proxied", Type: "text", Content: "hello proxy", Selected: true}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages}); err != nil {
		t.Fatal(err)
	}
	state, err = app.ConnectWebSocket(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SendWebSocketMessage(collection.ID, item.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing proxied websocket response")
	}
	if item.Response.Status != http.StatusSwitchingProtocols ||
		!strings.Contains(item.Response.Body, "proxied live: hello proxy") {
		t.Fatalf("expected proxied WebSocket success, got %#v", item.Response)
	}
	if atomic.LoadInt32(&proxyHits) != 1 || atomic.LoadInt32(&targetHits) != 1 {
		t.Fatalf("expected one proxy and target hit, proxy=%d target=%d", proxyHits, targetHits)
	}
	targetHost := strings.TrimPrefix(strings.TrimPrefix(targetURL, "ws://"), "wss://")
	targetHost, _, _ = strings.Cut(targetHost, "/")
	if proxiedURL != "//"+targetHost {
		t.Fatalf("proxy saw URL %q, want //%s", proxiedURL, targetHost)
	}
	if proxyAuth != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-pass")) {
		t.Fatalf("proxy auth header mismatch: %q", proxyAuth)
	}
}

func TestWebSocketRequestUsesCollectionClientCertificate(t *testing.T) {
	certPEM, keyPEM, _, _ := testClientCertificate(t)
	upgrader := websocket.Upgrader{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade mtls ws: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read mtls ws: %v", err)
		}
		reply := fmt.Sprintf("mtls %s: %s", r.TLS.PeerCertificates[0].Subject.CommonName, payload)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(reply)); err != nil {
			t.Fatalf("write mtls ws: %v", err)
		}
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	certDir := filepath.Join(collection.Path, "certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "client.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "client.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	targetURL := "wss" + strings.TrimPrefix(server.URL, "https")
	parsedTarget, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateCollectionClientCertificates(collection.ID, []ClientCertificateConfig{{
		Domain:       parsedTarget.Host,
		Type:         "cert",
		CertFilePath: "certs/client.pem",
		KeyFilePath:  "certs/client.key",
	}}); err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "websocket", "mTLS socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	settings := item.Settings
	settings.VerifyTLS = false
	messages := []WSMessage{{Name: "secure", Type: "text", Content: "hello cert", Selected: true}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing mtls websocket response")
	}
	if item.Response.Status != http.StatusSwitchingProtocols ||
		!strings.Contains(item.Response.Body, "mtls liteapi-client: hello cert") {
		t.Fatalf("expected mtls WebSocket success, got %#v", item.Response)
	}
}

func TestWebSocketRequestSendsMultipleSelectedMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer func() { _ = conn.Close() }()
		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("read ws %d: %v", i, err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte("reply: "+string(payload))); err != nil {
				t.Fatalf("write ws %d: %v", i, err)
			}
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Queue socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	messages := []WSMessage{
		{Name: "first", Type: "text", Content: "first {{name}}", Selected: true},
		{Name: "second", Type: "json", Content: `{"message":"second {{name}}"}`, Selected: true},
		{Name: "draft", Type: "text", Content: "not sent", Selected: false},
	}
	vars := RequestVars{Req: []Variable{{ID: "name", Name: "name", Value: "Ada", DataType: "string", Enabled: true}}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages, Vars: &vars}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing websocket response")
	}
	if item.Response.Status != http.StatusSwitchingProtocols || item.Response.Headers["x-websocket-messages-sent"] != "2" {
		t.Fatalf("unexpected websocket status/headers: %#v", item.Response)
	}
	if !strings.Contains(item.Response.Body, `"name": "first"`) ||
		!strings.Contains(item.Response.Body, `reply: first Ada`) ||
		!strings.Contains(item.Response.Body, `reply: {\"message\":\"second Ada\"}`) ||
		strings.Contains(item.Response.Body, "not sent") {
		t.Fatalf("unexpected websocket event body:\n%s", item.Response.Body)
	}
}

func TestWebSocketPersistentConnectionSendsMessagesWithoutReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connections int32
	received := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&connections, 1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer func() { _ = conn.Close() }()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			received <- string(payload)
			if err := conn.WriteMessage(websocket.TextMessage, []byte("ack: "+string(payload))); err != nil {
				t.Fatalf("write ws: %v", err)
			}
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Live socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	messages := []WSMessage{
		{Name: "first", Type: "text", Content: "first {{name}}", Selected: true},
		{Name: "second", Type: "json", Content: `{"message":"second {{name}}"}`, Selected: true},
	}
	vars := RequestVars{Req: []Variable{{ID: "name", Name: "name", Value: "Ada", DataType: "string", Enabled: true}}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages, Vars: &vars}); err != nil {
		t.Fatal(err)
	}

	state, err = app.ConnectWebSocket(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing connected websocket response")
	}
	if item.Response.Status != http.StatusSwitchingProtocols || item.Response.Headers["x-websocket-connected"] != "true" {
		t.Fatalf("expected connected websocket response, got %#v", item.Response)
	}

	state, err = app.SendWebSocketMessage(collection.ID, item.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SendWebSocketMessage(collection.ID, item.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&connections); got != 1 {
		t.Fatalf("expected one persistent websocket connection, got %d", got)
	}
	for _, expected := range []string{"first Ada", `{"message":"second Ada"}`} {
		select {
		case got := <-received:
			if got != expected {
				t.Fatalf("unexpected websocket payload: got %q want %q", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for websocket payload %q", expected)
		}
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing websocket message response")
	}
	for _, expected := range []string{
		`"direction": "sent"`,
		`"direction": "received"`,
		`ack: first Ada`,
		`ack: {\"message\":\"second Ada\"}`,
	} {
		if !strings.Contains(fmt.Sprintf("%#v\n%s", item.Response.Headers, item.Response.Body), expected) {
			t.Fatalf("websocket history missing %q:\nheaders=%#v\nbody=%s", expected, item.Response.Headers, item.Response.Body)
		}
	}
	if item.Response.Headers["x-websocket-events"] != "4" {
		t.Fatalf("expected four websocket events, got headers=%#v", item.Response.Headers)
	}

	state, err = app.DisconnectWebSocket(collection.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil || item.Response.Headers["x-websocket-connected"] != "false" {
		t.Fatalf("expected disconnected websocket response, got %#v", item.Response)
	}
}

func TestWebSocketKeepAliveIntervalSendsPingFrames(t *testing.T) {
	upgrader := websocket.Upgrader{}
	pings := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer func() { _ = conn.Close() }()
		conn.SetPingHandler(func(appData string) error {
			pings <- appData
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Keep alive socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	settings := item.Settings
	settings.KeepAliveInterval = 20
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, Settings: &settings}); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ConnectWebSocket(collection.ID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pings:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket keep-alive ping")
	}

	state, err = app.DisconnectWebSocket(collection.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing websocket disconnect response")
	}
	if item.Response.Headers["x-websocket-keep-alive-interval"] != "20" ||
		!strings.Contains(item.Response.Body, `"type": "ping"`) ||
		!strings.Contains(item.Response.Body, `"data": "keep-alive"`) {
		t.Fatalf("keep-alive response did not record ping: headers=%#v body=%s", item.Response.Headers, item.Response.Body)
	}
}

func TestWebSocketBinaryResponsePreservesBase64AndHex(t *testing.T) {
	upgrader := websocket.Upgrader{}
	binaryReply := []byte{0x00, 0x01, 0xfe, 0xff, 'O', 'K'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer func() { _ = conn.Close() }()
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read ws: %v", err)
		}
		if messageType != websocket.BinaryMessage || string(payload) != "ping" {
			t.Fatalf("unexpected ws binary request: type=%d payload=%q", messageType, payload)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, binaryReply); err != nil {
			t.Fatalf("write ws: %v", err)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Binary socket")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	messages := []WSMessage{{Name: "binary", Type: "binary", Content: base64.StdEncoding.EncodeToString([]byte("ping")), Selected: true}}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing websocket binary response")
	}
	if item.Response.Status != http.StatusSwitchingProtocols || item.Response.Headers["x-websocket-message-type"] != "binary" {
		t.Fatalf("unexpected websocket binary status/headers: %#v", item.Response)
	}
	if !bytes.Equal([]byte(item.Response.Body), binaryReply) {
		t.Fatalf("binary websocket body changed: %x", []byte(item.Response.Body))
	}
	if item.Response.BodyBase64 != base64.StdEncoding.EncodeToString(binaryReply) ||
		item.Response.Headers["x-websocket-message-base64"] != base64.StdEncoding.EncodeToString(binaryReply) ||
		item.Response.Headers["x-websocket-message-hex"] != hex.EncodeToString(binaryReply) {
		t.Fatalf("binary websocket base64/hex metadata missing: %#v", item.Response)
	}
}

func TestWebSocketBinaryEventArrayIncludesBase64AndHex(t *testing.T) {
	upgrader := websocket.Upgrader{}
	binaryReply := []byte{0x00, 0x01, 0xfe, 0xff}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, _, err = conn.ReadMessage()
		if err != nil {
			t.Fatalf("read first ws: %v", err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, binaryReply); err != nil {
			t.Fatalf("write first ws: %v", err)
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read second ws: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("echo: "+string(payload))); err != nil {
			t.Fatalf("write second ws: %v", err)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateRequest(collection.ID, "websocket", "Binary queue")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[len(collection.Items)-1]
	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	messages := []WSMessage{
		{Name: "binary", Type: "binary", Content: base64.StdEncoding.EncodeToString([]byte("one")), Selected: true},
		{Name: "text", Type: "text", Content: "two", Selected: true},
	}
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{URL: &targetURL, WSMessages: &messages}); err != nil {
		t.Fatal(err)
	}

	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing websocket binary event response")
	}
	for _, expected := range []string{
		`"name": "binary"`,
		`"type": "binary"`,
		`"dataBase64": "` + base64.StdEncoding.EncodeToString(binaryReply) + `"`,
		`"dataHex": "` + hex.EncodeToString(binaryReply) + `"`,
		`echo: two`,
	} {
		if !strings.Contains(item.Response.Body, expected) {
			t.Fatalf("websocket event body missing %q:\n%s", expected, item.Response.Body)
		}
	}
}

func TestWebSocketMessagesBruAndYAMLRoundTrip(t *testing.T) {
	item := types.NewRequestItem("Socket", "websocket", 1)
	item.URL = "ws://example.test/socket"
	item.Headers = []KeyValue{{Name: "X-Socket", Value: "1", Enabled: true}}
	item.WSMessages = []WSMessage{
		{Name: "hello", Type: "json", Content: `{"hello":"world"}`, Selected: true},
		{Name: "xml", Type: "xml", Content: "<hello />", Selected: false},
	}

	content := stringifyBru(item)
	for _, expected := range []string{"ws {", "body: ws", "body:ws {", "name: hello", "type: json", "selected: true", `{"hello":"world"}`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("websocket .bru missing %q:\n%s", expected, content)
		}
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != "websocket" || parsed.Body.Mode != "ws" || len(parsed.WSMessages) != 2 ||
		parsed.WSMessages[0].Name != "hello" ||
		parsed.WSMessages[0].Type != "json" ||
		parsed.WSMessages[0].Content != `{"hello":"world"}` ||
		!parsed.WSMessages[0].Selected ||
		parsed.WSMessages[1].Type != "xml" {
		t.Fatalf("websocket .bru did not round-trip: %#v", parsed)
	}

	yamlContent, err := stringifyYAMLRequest(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"websocket:", "message:", "title: hello", "selected: true", "type: json"} {
		if !strings.Contains(yamlContent, expected) {
			t.Fatalf("websocket YAML missing %q:\n%s", expected, yamlContent)
		}
	}
	parsedYAML, err := parseYAMLRequest(yamlContent)
	if err != nil {
		t.Fatal(err)
	}
	if parsedYAML.Type != "websocket" || parsedYAML.Body.Mode != "ws" || len(parsedYAML.WSMessages) != 2 ||
		parsedYAML.WSMessages[0].Name != "hello" ||
		parsedYAML.WSMessages[0].Type != "json" ||
		!parsedYAML.WSMessages[0].Selected ||
		parsedYAML.WSMessages[1].Content != "<hello />" {
		t.Fatalf("websocket YAML did not round-trip: %#v", parsedYAML)
	}
}

func TestSSEPreviewModeIsDetected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: hello\n\n"))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	method := http.MethodGet
	targetURL := server.URL
	body := item.Body
	body.Mode = "none"
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, item.ID)
	if !ok || item.Response == nil {
		t.Fatalf("missing response")
	}
	if item.Response.PreviewMode != "sse" {
		t.Fatalf("expected sse preview mode, got %q", item.Response.PreviewMode)
	}
	if !strings.Contains(item.Response.Body, "data: hello") {
		t.Fatalf("unexpected sse body: %q", item.Response.Body)
	}
}

func TestOpenCollectionFromDiskAndSaveRequest(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Disk Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Disk Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}

headers {
  X-Disk: yes
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if opened.Name != "Disk Collection" || len(opened.Items) != 1 {
		t.Fatalf("unexpected opened collection: %#v", opened)
	}
	if opened.Items[0].Name != "Ping" || opened.Items[0].Headers[0].Name != "X-Disk" {
		t.Fatalf("request not parsed from disk: %#v", opened.Items[0])
	}

	newURL := "https://example.test/changed"
	if _, err := app.UpdateRequest(opened.ID, opened.Items[0].ID, RequestPatch{URL: &newURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveRequest(opened.ID, opened.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(collectionPath, "ping.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), newURL) {
		t.Fatalf("saved bru did not contain updated URL:\n%s", content)
	}
	if exactFileExists(t, collectionPath, "Ping.bru") {
		t.Fatalf("save should preserve original filename instead of creating Ping.bru")
	}
}

func TestRefreshChangedCollectionsReloadsExternalDiskChanges(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Watcher Collection")
	requestPath := filepath.Join(collectionPath, "ping.bru")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Watcher Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, requestPath, "Ping", "https://example.test/ping", 1)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	activeTabID := state.ActiveTabID
	result, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("freshly opened collection should not refresh immediately: %#v", result)
	}

	writeWatcherRequestFile(t, requestPath, "Pong", "https://example.test/pong", 1)
	result, err = app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || strings.Join(result.Refreshed, ",") != "Watcher Collection" {
		t.Fatalf("expected watcher refresh, got %#v", result)
	}
	refreshed := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if refreshed.Items[0].Name != "Pong" || refreshed.Items[0].URL != "https://example.test/pong" {
		t.Fatalf("request was not reloaded from disk: %#v", refreshed.Items[0])
	}
	if result.State.ActiveTabID != activeTabID {
		t.Fatalf("watcher refresh should preserve active tab, got %q want %q", result.State.ActiveTabID, activeTabID)
	}

	extraPath := filepath.Join(collectionPath, "extra.bru")
	writeWatcherRequestFile(t, extraPath, "Extra", "https://example.test/extra", 2)
	result, err = app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	refreshed = result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(refreshed.Items) != 2 {
		t.Fatalf("expected added request to appear, got %#v", refreshed.Items)
	}

	if err := os.Remove(requestPath); err != nil {
		t.Fatal(err)
	}
	result, err = app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	refreshed = result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(refreshed.Items) != 1 || refreshed.Items[0].Name != "Extra" {
		t.Fatalf("expected deleted request to disappear, got %#v", refreshed.Items)
	}
	for _, tab := range result.State.OpenTabs {
		if tab.CollectionID == opened.ID && tab.ItemID == opened.Items[0].ID {
			t.Fatalf("deleted request tab should have been closed: %#v", tab)
		}
	}
}

func TestInternalSaveKeepsRequestAndTabIdentityAcrossWatcherAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	app := newAppInDirForTest(t, dataDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	echo := collection.Items[0]

	state, err = app.CreateRequest(collection.ID, "http", "Recovery Probe")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	var probe RequestItem
	for _, candidate := range collection.Items {
		if candidate.Name == "Recovery Probe" {
			probe = candidate
			break
		}
	}
	if probe.ID == "" || probe.FilePath == "" {
		t.Fatalf("new file-backed request did not receive stable identity: %#v", probe)
	}
	if want := deterministicID("request", filepath.Clean(probe.FilePath)); probe.ID != want {
		t.Fatalf("new request ID = %q, want path-derived %q", probe.ID, want)
	}
	if state.ActiveTabID != collection.ID+":"+probe.ID {
		t.Fatalf("new request tab is not canonical: active=%q collection=%q item=%q", state.ActiveTabID, collection.ID, probe.ID)
	}

	savedURL := "https://example.test/recovery-probe"
	if _, err := app.UpdateRequest(collection.ID, probe.ID, RequestPatch{URL: &savedURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveRequest(collection.ID, probe.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || len(result.Refreshed) != 0 || len(result.SkippedDirty) != 0 {
		t.Fatalf("internal save was misclassified as an external edit: %#v", result)
	}
	if item, ok := findItemInState(result.State, collection.ID, probe.ID); !ok || item.URL != savedURL || item.Draft {
		t.Fatalf("saved request identity/content changed after watcher poll: ok=%v item=%#v", ok, item)
	}
	if result.State.ActiveTabID != collection.ID+":"+probe.ID {
		t.Fatalf("watcher poll changed the active request tab: %q", result.State.ActiveTabID)
	}

	flushPersistForTest(t, app)
	restarted := newAppInDirForTest(t, dataDir)
	restartedState, err := restarted.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if item, ok := findItemInState(restartedState, collection.ID, probe.ID); !ok || item.URL != savedURL {
		t.Fatalf("request identity/content did not survive restart: ok=%v item=%#v", ok, item)
	}
	result, err = restarted.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("first watcher observation after restart must establish a baseline: %#v", result)
	}

	draftURL := "https://example.test/recovery-probe-draft"
	updated, err := restarted.UpdateRequest(collection.ID, probe.ID, RequestPatch{URL: &draftURL})
	if err != nil {
		t.Fatal(err)
	}
	updatedProbe, ok := findItemInState(updated, collection.ID, probe.ID)
	if !ok || updatedProbe.URL != draftURL || !updatedProbe.Draft {
		t.Fatalf("probe edit was not applied to its own request: ok=%v item=%#v", ok, updatedProbe)
	}
	updatedEcho, ok := findItemInState(updated, collection.ID, echo.ID)
	if !ok || updatedEcho.URL != echo.URL || updatedEcho.Draft {
		t.Fatalf("probe edit leaked into Echo JSON: ok=%v got=%#v wantURL=%q", ok, updatedEcho, echo.URL)
	}
}

func TestRefreshChangedCollectionsSkipsDirtyDrafts(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Dirty Watcher")
	requestPath := filepath.Join(collectionPath, "ping.bru")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Dirty Watcher","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, requestPath, "Ping", "https://example.test/ping", 1)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	draftURL := "https://example.test/local-draft"
	if _, err := app.UpdateRequest(opened.ID, opened.Items[0].ID, RequestPatch{URL: &draftURL}); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, requestPath, "Disk Pong", "https://example.test/disk-pong", 1)

	result, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("dirty collection should not be refreshed by watcher: %#v", result)
	}
	if strings.Join(result.SkippedDirty, ",") != "Dirty Watcher" {
		t.Fatalf("expected dirty skip, got %#v", result)
	}
	item, ok := findItemInState(result.State, opened.ID, opened.Items[0].ID)
	if !ok {
		t.Fatalf("missing dirty item")
	}
	if item.URL != draftURL || !item.Draft {
		t.Fatalf("dirty draft was overwritten: %#v", item)
	}
}

func TestOpenCollectionHonorsBrunoConfigIgnore(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Ignored Collection")
	if err := os.MkdirAll(filepath.Join(collectionPath, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(collectionPath, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{
  "version": "1",
  "name": "Ignored Collection",
  "type": "collection",
  "ignore": ["ignored", "skip-file.bru", "environments/ignored.bru"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, filepath.Join(collectionPath, "keep.bru"), "Keep", "https://example.test/keep", 1)
	writeWatcherRequestFile(t, filepath.Join(collectionPath, "skip-file.bru"), "Skip File", "https://example.test/skip-file", 2)
	writeWatcherRequestFile(t, filepath.Join(collectionPath, "ignored", "hidden.bru"), "Hidden", "https://example.test/hidden", 3)
	if err := os.WriteFile(filepath.Join(collectionPath, "environments", "prod.bru"), []byte(`vars {
  host: https://prod.example
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "environments", "ignored.bru"), []byte(`vars {
  host: https://ignored.example
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(opened.Items) != 1 || opened.Items[0].Name != "Keep" {
		t.Fatalf("ignored request files should not be loaded: %#v", opened.Items)
	}
	if len(opened.Environments) != 1 || opened.Environments[0].Name != "prod" {
		t.Fatalf("ignored environment files should not be loaded: %#v", opened.Environments)
	}
}

func TestOpenCollectionHonorsOpenCollectionBrunoIgnore(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Ignored YAML Collection")
	if err := os.MkdirAll(filepath.Join(collectionPath, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0
info:
  name: Ignored YAML Collection
  version: "1"
extensions:
  bruno:
    ignore:
      - ignored
`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, filepath.Join(collectionPath, "keep.bru"), "Keep YAML", "https://example.test/keep-yaml", 1)
	writeWatcherRequestFile(t, filepath.Join(collectionPath, "ignored", "hidden.bru"), "Hidden YAML", "https://example.test/hidden-yaml", 2)

	collection, err := readCollectionFromDisk(collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if collection.Format != "yml" {
		t.Fatalf("expected yml collection, got %q", collection.Format)
	}
	if len(collection.Items) != 1 || collection.Items[0].Name != "Keep YAML" {
		t.Fatalf("OpenCollection Bruno ignore should hide ignored request files: %#v", collection.Items)
	}
}

func TestRefreshChangedCollectionsHonorsBrunoConfigIgnore(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Watcher Ignore")
	ignoredPath := filepath.Join(collectionPath, "ignored")
	if err := os.MkdirAll(ignoredPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Watcher Ignore","type":"collection","ignore":["ignored"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	keepPath := filepath.Join(collectionPath, "keep.bru")
	writeWatcherRequestFile(t, keepPath, "Keep", "https://example.test/keep", 1)

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	writeWatcherRequestFile(t, filepath.Join(ignoredPath, "hidden.bru"), "Hidden", "https://example.test/hidden", 2)
	result, err := app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("ignored request addition should not trigger watcher refresh: %#v", result)
	}

	writeWatcherRequestFile(t, keepPath, "Keep Changed", "https://example.test/keep-changed", 1)
	result, err = app.RefreshChangedCollections()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatalf("non-ignored request edit should trigger watcher refresh")
	}
	refreshed := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if len(refreshed.Items) != 1 || refreshed.Items[0].Name != "Keep Changed" {
		t.Fatalf("unexpected refreshed items after ignored addition: %#v", refreshed.Items)
	}
}

func writeWatcherRequestFile(t *testing.T, path, name, targetURL string, seq int) {
	t.Helper()
	content := fmt.Sprintf(`meta {
  name: %s
  type: http
  seq: %d
}

get {
  url: %s
  body: none
  auth: none
}
`, name, seq, targetURL)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionFileCacheCachesInvalidatesAndClears(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Cache Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Cache Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "ping.bru")
	if err := os.WriteFile(requestPath, []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	app := newAppInDirForTest(t, dataDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.Cache.File.Enabled = true
	state, err = app.UpdatePreferences(preferences)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if opened.Items[0].Name != "Ping" {
		t.Fatalf("expected disk parse before cache mutation, got %#v", opened.Items[0])
	}
	size, err := app.GetFileCacheSize()
	if err != nil {
		t.Fatal(err)
	}
	if size <= 0 {
		t.Fatalf("expected collection file cache to be written, got %d", size)
	}

	cachePath := filepath.Join(dataDir, "cache", "collections.json")
	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var store collectionFileCacheStore
	if err := json.Unmarshal(cacheData, &store); err != nil {
		t.Fatal(err)
	}
	entry := store.Collections[filepath.Clean(collectionPath)]
	entry.Collection.Items[0].Name = "Cached Ping"
	store.Collections[filepath.Clean(collectionPath)] = entry
	cacheData, err = json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, cacheData, 0o600); err != nil {
		t.Fatal(err)
	}

	state, err = app.RefreshCollection(opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if refreshed.Items[0].Name != "Cached Ping" {
		t.Fatalf("expected cache hit to reuse parsed collection, got %#v", refreshed.Items[0])
	}

	if err := os.WriteFile(requestPath, []byte(`meta {
  name: Pong
  type: http
  seq: 1
}

get {
  url: https://example.test/pong
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = app.RefreshCollection(opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	refreshed = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if refreshed.Items[0].Name != "Pong" {
		t.Fatalf("expected file cache invalidation after disk edit, got %#v", refreshed.Items[0])
	}

	size, err = app.ClearFileCache()
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Fatalf("expected clear file cache to report zero size, got %d", size)
	}
	size, err = app.GetFileCacheSize()
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Fatalf("expected cleared file cache size to be zero, got %d", size)
	}
}

func TestGenerateCollectionDocsBuildsBrunoStyleHTMLAndFiltersEnvironments(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Name = "GenerateDocsOrder"
	collection.Version = "1"
	collection.Docs = "# Collection docs"
	collection.Folders = []FolderConfig{
		{Path: "Zoo", DisplayPath: "Zoo", Name: "Zoo", Seq: 1, Docs: "Zoo docs"},
		{Path: "Aviary", DisplayPath: "Aviary", Name: "Aviary", Seq: 2},
	}
	collection.Items = []RequestItem{
		docsTestRequest("ReqAlpha", "", 2),
		docsTestRequest("ReqBeta", "", 1),
		docsTestRequest("Bear", "Zoo", 2),
		docsTestRequest("Lion", "Zoo", 1),
		docsTestRequest("Parrot", "Aviary", 1),
	}
	collection.Environments = []Environment{
		{ID: "env-prod", Name: "Production", Color: "#22c55e", Variables: []Variable{{Name: "host", Value: "https://prod.example", Enabled: true}}},
		{ID: "env-dev", Name: "Development", Color: "#3b82f6", Variables: []Variable{{Name: "host", Value: "https://dev.example", Enabled: true}}},
		{ID: "env-stage", Name: "Staging", Color: "#f59e0b", Variables: []Variable{{Name: "host", Value: "https://stage.example", Enabled: true}}},
	}
	app.mu.Unlock()

	result, err := app.GenerateCollectionDocs(collectionID, GenerateCollectionDocsOptions{EnvironmentIDs: []string{"env-prod", "env-stage"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.FileName != "GenerateDocsOrder-documentation.html" {
		t.Fatalf("unexpected filename %q", result.FileName)
	}
	if result.Version != "v1.0.0" || result.FolderCount != 2 || result.RequestCount != 5 {
		t.Fatalf("unexpected result summary: %#v", result)
	}
	if !strings.Contains(result.HTML, `https://cdn.opencollection.com/docs.css`) || !strings.Contains(result.HTML, `const collectionData = "`) {
		t.Fatalf("generated HTML missing Bruno docs shell:\n%s", result.HTML)
	}

	openCollection := parseGeneratedDocsYAML(t, result.HTML)
	info := openCollection["info"].(map[string]interface{})
	if info["name"] != "GenerateDocsOrder" || info["version"] != "1" {
		t.Fatalf("unexpected generated info: %#v", info)
	}
	items := openCollection["items"].([]interface{})
	if got := generatedDocsItemNames(items); strings.Join(got, ",") != "Zoo,Aviary,ReqBeta,ReqAlpha" {
		t.Fatalf("unexpected root order: %v", got)
	}
	zoo := items[0].(map[string]interface{})
	if got := generatedDocsItemNames(zoo["items"].([]interface{})); strings.Join(got, ",") != "Lion,Bear" {
		t.Fatalf("unexpected nested order: %v", got)
	}
	config := openCollection["config"].(map[string]interface{})
	envs := config["environments"].([]interface{})
	envNames := make([]string, 0, len(envs))
	for _, env := range envs {
		envNames = append(envNames, env.(map[string]interface{})["name"].(string))
	}
	if strings.Join(envNames, ",") != "Production,Staging" {
		t.Fatalf("unexpected generated environments: %v", envNames)
	}
}

func TestShareCollectionExportsYamlZipPostmanAndSaves(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Name = "Share Me"
	collection.Version = "2"
	collection.Format = "yml"
	collection.Docs = "# Share docs"
	collection.Folders = []FolderConfig{{Path: "Zoo", DisplayPath: "Zoo", Name: "Zoo", Seq: 1}}
	httpItem := docsTestRequest("Root", "", 1)
	httpItem.Body = RequestBody{Mode: "json", JSON: `{"ok":true}`}
	wsItem := types.NewRequestItem("Socket", "websocket", 2)
	wsItem.FolderPath = "Zoo"
	wsItem.WSMessages = []WSMessage{{Name: "hello", Type: "text", Content: "ping", Selected: true}}
	grpcItem := types.NewRequestItem("Greeter", "grpc", 3)
	grpcItem.Method = "helloworld.Greeter/SayHello"
	transient := docsTestRequest("Scratch", "", 4)
	transient.Transient = true
	collection.Items = []RequestItem{httpItem, wsItem, grpcItem, transient}
	collection.Environments = []Environment{
		{ID: "env-prod", Name: "Production", Variables: []Variable{
			{Name: "host", Value: "https://prod.example", Enabled: true},
			{Name: "token", Value: "secret-token", Enabled: true, Secret: true},
		}},
	}
	app.mu.Unlock()

	yamlExport, err := app.ExportCollectionWithOptions(collectionID, CollectionExportOptions{Format: "yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if yamlExport.Format != "yaml" || yamlExport.Filename != "Share Me.yml" || yamlExport.FolderCount != 1 || yamlExport.RequestCount != 3 || yamlExport.EnvironmentCount != 1 {
		t.Fatalf("unexpected YAML export summary: %#v", yamlExport)
	}
	if strings.Contains(yamlExport.Content, "secret-token") || strings.Contains(yamlExport.Content, "Scratch docs") {
		t.Fatalf("YAML export leaked secret or transient data:\n%s", yamlExport.Content)
	}
	var yamlPayload map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlExport.Content), &yamlPayload); err != nil {
		t.Fatal(err)
	}
	if yamlPayload["bundled"] != true {
		t.Fatalf("YAML export should be bundled OpenCollection: %#v", yamlPayload["bundled"])
	}

	zipExport, err := app.ExportCollectionWithOptions(collectionID, CollectionExportOptions{Format: "zip"})
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := base64.StdEncoding.DecodeString(zipExport.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	zipFiles := map[string]string{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		zipFiles[file.Name] = string(data)
	}
	for _, name := range []string{"opencollection.yml", "Zoo/folder.yml", "Root.yml", "Zoo/Socket.yml", "Greeter.yml"} {
		if _, ok := zipFiles[name]; !ok {
			t.Fatalf("ZIP export missing %s; files=%v", name, reflect.ValueOf(zipFiles).MapKeys())
		}
	}
	for name, content := range zipFiles {
		if strings.Contains(name, ".git") || strings.Contains(name, "node_modules") || strings.Contains(content, "secret-token") || strings.Contains(content, "Scratch") {
			t.Fatalf("ZIP export leaked excluded content in %s:\n%s", name, content)
		}
	}

	targetPath := filepath.Join(t.TempDir(), "share.yml")
	saveResult, err := app.SaveCollectionExport(collectionID, CollectionExportOptions{Format: "yaml"}, targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if saveResult.Path != targetPath {
		t.Fatalf("unexpected save path: %#v", saveResult)
	}
	saved, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, []byte(yamlExport.Content)) {
		t.Fatalf("saved YAML did not match export")
	}

	postmanExport, err := app.ExportCollectionWithOptions(collectionID, CollectionExportOptions{Format: "postman"})
	if err != nil {
		t.Fatal(err)
	}
	if postmanExport.RequestCount != 1 || strings.Join(postmanExport.SkippedTypes, ",") != "WebSocket,gRPC" {
		t.Fatalf("unexpected Postman export summary: %#v", postmanExport)
	}
	if !strings.Contains(postmanExport.Warning, "WebSocket, gRPC") || strings.Contains(postmanExport.Content, "Socket") || strings.Contains(postmanExport.Content, "Greeter") {
		t.Fatalf("Postman export did not warn/skip unsupported types: %#v\n%s", postmanExport, postmanExport.Content)
	}
}

func docsTestRequest(name, folderPath string, seq int) RequestItem {
	item := types.NewRequestItem(name, "http", seq)
	item.ID = "request-" + strings.ToLower(name)
	item.Method = http.MethodGet
	item.URL = "https://example.test/" + strings.ToLower(name)
	item.FolderPath = folderPath
	item.Docs = name + " docs"
	return item
}

func parseGeneratedDocsYAML(t *testing.T, htmlContent string) map[string]interface{} {
	t.Helper()
	match := regexp.MustCompile(`const collectionData = ("(?:\\.|[^"\\])*");`).FindStringSubmatch(htmlContent)
	if len(match) != 2 {
		t.Fatalf("could not find embedded collectionData in:\n%s", htmlContent)
	}
	var yamlContent string
	if err := json.Unmarshal([]byte(match[1]), &yamlContent); err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func generatedDocsItemNames(items []interface{}) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		itemMap := item.(map[string]interface{})
		info := itemMap["info"].(map[string]interface{})
		names = append(names, info["name"].(string))
	}
	return names
}

func TestSaveAllTabsWritesOpenRequestTabs(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Save All Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Save All Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(collectionPath, "first.bru")
	secondPath := filepath.Join(collectionPath, "second.bru")
	if err := os.WriteFile(firstPath, []byte(`meta {
  name: First
  type: http
  seq: 1
}

get {
  url: https://example.test/first
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(`meta {
  name: Second
  type: http
  seq: 2
}

get {
  url: https://example.test/second
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(opened.Items) != 2 {
		t.Fatalf("expected two requests, got %#v", opened.Items)
	}
	first := opened.Items[0]
	second := opened.Items[1]
	state, err = app.OpenRequestTab(opened.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstURL := "https://example.test/first-saved-by-all"
	secondURL := "https://example.test/second-saved-by-all"
	if state, err = app.UpdateRequest(opened.ID, first.ID, RequestPatch{URL: &firstURL}); err != nil {
		t.Fatal(err)
	}
	if state, err = app.UpdateRequest(opened.ID, second.ID, RequestPatch{URL: &secondURL}); err != nil {
		t.Fatal(err)
	}
	otherState, err := app.CreateCollection(state.ActiveWorkspaceID, "Other Save All Collection", "yml")
	if err != nil {
		t.Fatal(err)
	}
	otherCollection := otherState.Workspaces[0].Collections[len(otherState.Workspaces[0].Collections)-1]
	if otherState, err = app.CreateRequest(otherCollection.ID, "http", "Other Request"); err != nil {
		t.Fatal(err)
	}
	otherCollection = otherState.Workspaces[0].Collections[len(otherState.Workspaces[0].Collections)-1]
	otherItem := otherCollection.Items[0]
	otherURL := "https://example.test/other-should-stay-draft"
	if state, err = app.UpdateRequest(otherCollection.ID, otherItem.ID, RequestPatch{URL: &otherURL}); err != nil {
		t.Fatal(err)
	}
	for _, item := range state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1].Items {
		if !item.Draft {
			t.Fatalf("updated request should be draft before save all: %#v", item)
		}
	}
	state, err = app.SaveAllTabs(opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	var savedCollection Collection
	var savedOtherCollection Collection
	for _, collection := range state.Workspaces[0].Collections {
		if collection.ID == opened.ID {
			savedCollection = collection
		}
		if collection.ID == otherCollection.ID {
			savedOtherCollection = collection
		}
	}
	for _, item := range savedCollection.Items {
		if item.Draft {
			t.Fatalf("save all should clear draft flag: %#v", item)
		}
	}
	if len(savedOtherCollection.Items) == 0 || !savedOtherCollection.Items[0].Draft {
		t.Fatalf("save all should not save requests outside the active collection: %#v", savedOtherCollection.Items)
	}
	firstContent, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstContent), firstURL) || !strings.Contains(string(secondContent), secondURL) {
		t.Fatalf("save all did not write both open request files:\nfirst:\n%s\nsecond:\n%s", firstContent, secondContent)
	}
}

func TestSaveAllTabsAssignsUniquePathsForDuplicateRequestNames(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if state, err = app.CreateRequest(collection.ID, "http", "Duplicate"); err != nil {
		t.Fatal(err)
	}
	if state, err = app.CreateRequest(collection.ID, "http", "Duplicate"); err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	if len(collection.Items) < 3 {
		t.Fatalf("expected duplicate requests to be created: %#v", collection.Items)
	}
	first := collection.Items[1]
	second := collection.Items[2]
	firstURL := "https://example.test/duplicate-one"
	secondURL := "https://example.test/duplicate-two"
	if state, err = app.UpdateRequest(collection.ID, first.ID, RequestPatch{URL: &firstURL}); err != nil {
		t.Fatal(err)
	}
	if state, err = app.UpdateRequest(collection.ID, second.ID, RequestPatch{URL: &secondURL}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveAllTabs(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	first = collection.Items[1]
	second = collection.Items[2]
	if first.FilePath == "" || second.FilePath == "" || first.FilePath == second.FilePath {
		t.Fatalf("duplicate request names should get unique file paths: first=%#v second=%#v", first, second)
	}
	firstContent, err := os.ReadFile(first.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := os.ReadFile(second.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstContent), firstURL) || !strings.Contains(string(secondContent), secondURL) {
		t.Fatalf("duplicate request files were not written correctly:\nfirst:\n%s\nsecond:\n%s", firstContent, secondContent)
	}
}

func TestSaveResponseExampleWritesInlineBruExample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"saved":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Example Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Example Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "save-me.bru"), []byte(fmt.Sprintf(`meta {
  name: Save Me
  type: http
  seq: 1
}

get {
  url: %s/example
  body: none
  auth: none
}
`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.SendRequest(opened.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveResponseExample(opened.ID, item.ID, "Created Example")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("example was not stored in state: %#v", updated.Examples)
	}
	content, err := os.ReadFile(filepath.Join(collectionPath, "save-me.bru"))
	if err != nil {
		t.Fatal(err)
	}
	file := string(content)
	for _, expected := range []string{"example {", "name: Created Example", "code: 201", `{"saved":true}`} {
		if !strings.Contains(file, expected) {
			t.Fatalf("saved bru missing %q:\n%s", expected, file)
		}
	}
	reopened, err := parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || reopened.Examples[0].Response.Status != http.StatusCreated {
		t.Fatalf("saved example did not parse back: %#v", reopened.Examples)
	}
}

func TestCreateResponseExampleWritesInlineBruExampleAndOpensTab(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Manual Example Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Manual Example Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "manual.bru")
	if err := os.WriteFile(requestPath, []byte(`meta {
  name: Manual
  type: http
  seq: 1
}

post {
  url: https://api.example.test/manual
  body: json
  auth: none
}

body:json {
  {"manual":true}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.CreateResponseExample(opened.ID, item.ID, "Manual Example", "Created by hand")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("manual example was not stored in state: %#v", updated.Examples)
	}
	example := updated.Examples[0]
	if example.Name != "Manual Example" || example.Description != "Created by hand" || example.Request.Method != http.MethodPost || example.Request.URL != "https://api.example.test/manual" || example.Request.BodyMode != "json" || !strings.Contains(example.Request.Body, `{"manual":true}`) || example.Response.Status != http.StatusOK || example.Response.StatusText != "OK" || example.Response.BodyType != "text" || example.Response.Body != "" {
		t.Fatalf("manual example defaults/snapshot mismatch: %#v", example)
	}
	tabID := responseExampleTabID(opened.ID, item.ID, example.ID)
	if state.ActiveTabID != tabID {
		t.Fatalf("creating an example should open its response-example tab, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if tab, ok := findOpenTab(state.OpenTabs, tabID); !ok || tab.ExampleName != "Manual Example" || tab.Kind != "response-example" {
		t.Fatalf("manual example tab mismatch: tab=%#v ok=%v", tab, ok)
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := string(content)
	for _, expected := range []string{"example {", "name: Manual Example", "description: Created by hand", "method: post", "url: https://api.example.test/manual", "mode: json", `{"manual":true}`, "code: 200", "text: OK"} {
		if !strings.Contains(file, expected) {
			t.Fatalf("manual example file missing %q:\n%s", expected, file)
		}
	}
	reopened, err := parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || reopened.Examples[0].Name != "Manual Example" || reopened.Examples[0].Description != "Created by hand" || reopened.Examples[0].Response.Status != http.StatusOK {
		t.Fatalf("manual example did not parse back: %#v", reopened.Examples)
	}
}

func TestSaveResponseExamplePreservesFormURLEncodedRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"form":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Example Form Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Example Form Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "form-example.bru")
	if err := os.WriteFile(requestPath, []byte(fmt.Sprintf(`meta {
  name: Save Form Example
  type: http
  seq: 1
}

post {
  url: %s/form
  body: form-urlencoded
  auth: none
}

headers {
  Content-Type: application/x-www-form-urlencoded
}

body:form-urlencoded {
  email: ada@example.test
  notes: hello there
  ~disabled: nope
}
`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.SendRequest(opened.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveResponseExample(opened.ID, item.ID, "Form Example")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("example was not stored in state: %#v", updated.Examples)
	}
	example := updated.Examples[0]
	if example.Request.BodyMode != "formUrlEncoded" || len(example.Request.FormURLEncoded) != 3 || example.Request.FormURLEncoded[2].Enabled {
		t.Fatalf("saved example did not preserve form rows: %#v", example.Request)
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := string(content)
	if !strings.Contains(file, "body:form-urlencoded: {") || !strings.Contains(file, "      email: ada@example.test") || !strings.Contains(file, "      ~disabled: nope") {
		t.Fatalf("saved example did not write form rows:\n%s", file)
	}
	reopened, err := parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || len(reopened.Examples[0].Request.FormURLEncoded) != 3 || reopened.Examples[0].Request.FormURLEncoded[2].Enabled {
		t.Fatalf("saved form example did not parse back: %#v", reopened.Examples)
	}
}

func TestSaveResponseExamplePreservesMultipartRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"multipart":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Example Multipart Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Example Multipart Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(collectionPath, "examples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "examples", "sample.pdf"), []byte("sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "multipart-example.bru")
	if err := os.WriteFile(requestPath, []byte(fmt.Sprintf(`meta {
  name: Save Multipart Example
  type: http
  seq: 1
}

post {
  url: %s/upload
  body: multipart-form
  auth: none
}

body:multipart-form {
  title: Sample Document
  document: @file(examples/sample.pdf) @contentType(application/pdf)
  ~skip: nope
}
`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.SendRequest(opened.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveResponseExample(opened.ID, item.ID, "Multipart Example")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("example was not stored in state: %#v", updated.Examples)
	}
	example := updated.Examples[0]
	if example.Request.BodyMode != "multipartForm" || len(example.Request.MultipartForm) != 3 || example.Request.MultipartForm[2].Enabled || example.Request.MultipartForm[1].FilePath != "examples/sample.pdf" || example.Request.MultipartForm[1].ContentType != "application/pdf" {
		t.Fatalf("saved example did not preserve multipart rows: %#v", example.Request)
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := string(content)
	if !strings.Contains(file, "body:multipart-form: {") || !strings.Contains(file, "      document: @file(examples/sample.pdf) @contentType(application/pdf)") || !strings.Contains(file, "      ~skip: nope") {
		t.Fatalf("saved example did not write multipart rows:\n%s", file)
	}
	reopened, err := parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || len(reopened.Examples[0].Request.MultipartForm) != 3 || reopened.Examples[0].Request.MultipartForm[2].Enabled {
		t.Fatalf("saved multipart example did not parse back: %#v", reopened.Examples)
	}
}

func TestSaveResponseExamplePreservesFileBodyRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"file":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Example File Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Example File Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(collectionPath, "examples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "examples", "selected.bin"), []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "examples", "backup.json"), []byte(`{"backup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "file-example.bru")
	if err := os.WriteFile(requestPath, []byte(fmt.Sprintf(`meta {
  name: Save File Example
  type: http
  seq: 1
}

post {
  url: %s/upload
  body: file
  auth: none
}

body:file {
  file: @file(examples/selected.bin) @contentType(application/octet-stream)
  ~file: @file(examples/backup.json) @contentType(application/json)
}
`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.SendRequest(opened.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveResponseExample(opened.ID, item.ID, "File Example")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("example was not stored in state: %#v", updated.Examples)
	}
	example := updated.Examples[0]
	if example.Request.BodyMode != "file" || len(example.Request.File) != 2 || !example.Request.File[0].Selected || example.Request.File[1].Selected || example.Request.File[1].ContentType != "application/json" {
		t.Fatalf("saved example did not preserve file rows: %#v", example.Request)
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := string(content)
	if !strings.Contains(file, "body:file: {") || !strings.Contains(file, "      file: @file(examples/selected.bin) @contentType(application/octet-stream)") || !strings.Contains(file, "      ~file: @file(examples/backup.json) @contentType(application/json)") {
		t.Fatalf("saved example did not write file rows:\n%s", file)
	}
	reopened, err := parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || len(reopened.Examples[0].Request.File) != 2 || !reopened.Examples[0].Request.File[0].Selected || reopened.Examples[0].Request.File[1].Selected {
		t.Fatalf("saved file example did not parse back: %#v", reopened.Examples)
	}
}

func TestResponseExampleRenameCloneDeleteWritesInlineBruExamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"example":"crud"}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Example CRUD Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Example CRUD Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "crud.bru")
	if err := os.WriteFile(requestPath, []byte(fmt.Sprintf(`meta {
  name: CRUD
  type: http
  seq: 1
}

get {
  url: %s/example
  body: none
  auth: none
}
`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.SendRequest(opened.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveResponseExample(opened.ID, item.ID, "Created Example")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("example was not stored in state: %#v", updated.Examples)
	}
	exampleID := updated.Examples[0].ID
	createdTabID := responseExampleTabID(opened.ID, item.ID, exampleID)
	if state.ActiveTabID != createdTabID {
		t.Fatalf("saving an example should open its response-example tab, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if tab, ok := findOpenTab(state.OpenTabs, createdTabID); !ok || tab.Kind != "response-example" || tab.ExampleName != "Created Example" || tab.ResponseTab != "examples" {
		t.Fatalf("created example tab mismatch: tab=%#v ok=%v", tab, ok)
	}

	state, err = app.RenameResponseExample(opened.ID, item.ID, exampleID, "Renamed Example")
	if err != nil {
		t.Fatal(err)
	}
	renamed, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(renamed.Examples) != 1 || renamed.Examples[0].Name != "Renamed Example" {
		t.Fatalf("example was not renamed in state: %#v", renamed.Examples)
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(content); !strings.Contains(file, "name: Renamed Example") || strings.Contains(file, "name: Created Example") {
		t.Fatalf("renamed example was not persisted:\n%s", file)
	}
	if tab, ok := findOpenTab(state.OpenTabs, createdTabID); !ok || tab.ExampleName != "Renamed Example" {
		t.Fatalf("renamed example tab was not synchronized: tab=%#v ok=%v", tab, ok)
	}

	state, err = app.CloneResponseExample(opened.ID, item.ID, exampleID)
	if err != nil {
		t.Fatal(err)
	}
	cloned, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(cloned.Examples) != 2 {
		t.Fatalf("example was not cloned in state: %#v", cloned.Examples)
	}
	cloneID := cloned.Examples[1].ID
	if cloneID == "" || cloneID == exampleID || cloned.Examples[1].Name != "Renamed Example (Copy)" {
		t.Fatalf("clone metadata mismatch: %#v", cloned.Examples[1])
	}
	if cloned.Examples[1].Response.Status != http.StatusCreated || !strings.Contains(cloned.Examples[1].Response.Body, `"crud"`) {
		t.Fatalf("clone did not preserve response snapshot: %#v", cloned.Examples[1])
	}
	cloneTabID := responseExampleTabID(opened.ID, item.ID, cloneID)
	if state.ActiveTabID != cloneTabID {
		t.Fatalf("cloning an example should open the cloned example tab, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	if tab, ok := findOpenTab(state.OpenTabs, cloneTabID); !ok || tab.ExampleName != "Renamed Example (Copy)" || tab.Kind != "response-example" {
		t.Fatalf("cloned example tab mismatch: tab=%#v ok=%v", tab, ok)
	}
	content, err = os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(content); !strings.Contains(file, "name: Renamed Example (Copy)") {
		t.Fatalf("cloned example was not persisted:\n%s", file)
	}

	state, err = app.DeleteResponseExample(opened.ID, item.ID, cloneID)
	if err != nil {
		t.Fatal(err)
	}
	deleted, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(deleted.Examples) != 1 || deleted.Examples[0].Name != "Renamed Example" {
		t.Fatalf("clone was not deleted from state: %#v", deleted.Examples)
	}
	if _, ok := findOpenTab(state.OpenTabs, cloneTabID); ok || state.ActiveTabID != createdTabID {
		t.Fatalf("deleting an open cloned example should close its tab and focus the previous example tab, active=%s tabs=%#v", state.ActiveTabID, state.OpenTabs)
	}
	content, err = os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if file := string(content); strings.Contains(file, "Renamed Example (Copy)") || !strings.Contains(file, "name: Renamed Example") {
		t.Fatalf("deleted example was not removed from disk:\n%s", file)
	}
}

func TestUpdateResponseExampleEditsInlineBruExample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"example":"edit"}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Example Edit Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Example Edit Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(collectionPath, "edit.bru")
	if err := os.WriteFile(requestPath, []byte(fmt.Sprintf(`meta {
  name: Edit Example
  type: http
  seq: 1
}

get {
  url: %s/example
  body: none
  auth: none
}
`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	state, err = app.SendRequest(opened.ID, item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.SaveResponseExample(opened.ID, item.ID, "Editable Example")
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(updated.Examples) != 1 {
		t.Fatalf("example was not stored in state: %#v", updated.Examples)
	}
	example := updated.Examples[0]
	example.Description = "Edited request and response example"
	example.Request.Method = "post"
	example.Request.URL = server.URL + "/edited?from=ui"
	example.Request.BodyMode = "json"
	example.Request.Body = `{"request":true}`
	example.Request.Headers = []KeyValue{
		{Name: "X-Request-Edited", Value: "yes", Enabled: true},
		{Name: "X-Request-Skip", Value: "no", Enabled: false},
	}
	example.Request.Params = []KeyValue{
		{Name: "from", Value: "ui", Enabled: true},
		{Name: "ignored", Value: "no", Enabled: false},
	}
	example.Response.Status = http.StatusAccepted
	example.Response.StatusText = "Accepted"
	example.Response.BodyType = "json"
	example.Response.Body = `{"edited":true}`
	example.Response.Headers = []KeyValue{
		{Name: "X-Edited", Value: "yes", Enabled: true},
		{Name: "X-Skip", Value: "no", Enabled: false},
	}
	state, err = app.UpdateResponseExample(opened.ID, item.ID, example.ID, example)
	if err != nil {
		t.Fatal(err)
	}
	edited, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(edited.Examples) != 1 {
		t.Fatalf("edited example missing from state: %#v", edited.Examples)
	}
	if edited.Examples[0].Description != "Edited request and response example" || edited.Examples[0].Request.Method != http.MethodPost || edited.Examples[0].Request.URL != server.URL+"/edited?from=ui" || edited.Examples[0].Request.BodyMode != "json" || edited.Examples[0].Request.Body != `{"request":true}` || len(edited.Examples[0].Request.Headers) != 2 || len(edited.Examples[0].Request.Params) != 2 || edited.Examples[0].Response.Status != http.StatusAccepted || edited.Examples[0].Response.Size != len(`{"edited":true}`) || len(edited.Examples[0].Response.Headers) != 2 {
		t.Fatalf("edited example was not normalized in state: %#v", edited.Examples[0])
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := string(content)
	for _, expected := range []string{"description: Edited request and response example", "method: post", "url: " + server.URL + "/edited?from=ui", "mode: json", "X-Request-Edited: yes", "~X-Request-Skip: no", "from: ui", "~ignored: no", `{"request":true}`, "code: 202", "text: Accepted", "X-Edited: yes", "~X-Skip: no", `{"edited":true}`} {
		if !strings.Contains(file, expected) {
			t.Fatalf("edited example file missing %q:\n%s", expected, file)
		}
	}
	reopened, err := parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || reopened.Examples[0].Description != "Edited request and response example" || reopened.Examples[0].Request.Method != http.MethodPost || reopened.Examples[0].Request.URL != server.URL+"/edited?from=ui" || reopened.Examples[0].Request.BodyMode != "json" || reopened.Examples[0].Request.Body != `{"request":true}` || reopened.Examples[0].Response.Status != http.StatusAccepted || reopened.Examples[0].Response.Body != `{"edited":true}` || !reopened.Examples[0].Request.Headers[0].Enabled || reopened.Examples[0].Request.Headers[1].Enabled || !reopened.Examples[0].Request.Params[0].Enabled || reopened.Examples[0].Request.Params[1].Enabled || !reopened.Examples[0].Response.Headers[0].Enabled || reopened.Examples[0].Response.Headers[1].Enabled {
		t.Fatalf("edited example did not parse back: %#v", reopened.Examples)
	}

	formExample := edited.Examples[0]
	formExample.Request.BodyMode = "formUrlEncoded"
	formExample.Request.Body = ""
	formExample.Request.FormURLEncoded = []KeyValue{
		{Name: "email", Value: "ada@example.test", Enabled: true},
		{Name: "disabled", Value: "nope", Enabled: false},
	}
	state, err = app.UpdateResponseExample(opened.ID, item.ID, formExample.ID, formExample)
	if err != nil {
		t.Fatal(err)
	}
	formEdited, ok := findItemInState(state, opened.ID, item.ID)
	if !ok || len(formEdited.Examples) != 1 || len(formEdited.Examples[0].Request.FormURLEncoded) != 2 || formEdited.Examples[0].Request.FormURLEncoded[1].Enabled {
		t.Fatalf("form example update missing rows in state: %#v", formEdited.Examples)
	}
	content, err = os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	file = string(content)
	if !strings.Contains(file, "mode: formUrlEncoded") || !strings.Contains(file, "body:form-urlencoded: {") || !strings.Contains(file, "      email: ada@example.test") || !strings.Contains(file, "      ~disabled: nope") {
		t.Fatalf("form example update was not persisted:\n%s", file)
	}
	reopened, err = parseBru(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Examples) != 1 || reopened.Examples[0].Request.BodyMode != "formUrlEncoded" || len(reopened.Examples[0].Request.FormURLEncoded) != 2 || reopened.Examples[0].Request.FormURLEncoded[1].Enabled {
		t.Fatalf("form example update did not parse back: %#v", reopened.Examples)
	}
}

func TestGenerateResponseExampleCodeUsesRequestSnapshot(t *testing.T) {
	example := ResponseExample{
		Name: "Generated Code Example",
		Request: ResponseExampleRequest{
			Method:   http.MethodPost,
			URL:      "https://api.example.test/users?existing=1",
			BodyMode: "json",
			Body:     `{"name":"Ada"}`,
			Params: []KeyValue{
				{Name: "expand", Value: "projects", Enabled: true},
				{Name: "skip", Value: "no", Enabled: false},
			},
			Headers: []KeyValue{
				{Name: "X-Trace", Value: "abc123", Enabled: true},
				{Name: "X-Skip", Value: "no", Enabled: false},
			},
		},
	}

	curl, err := codegen.GenerateResponseExampleCode(example, "curl")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"curl --request 'POST' 'https://api.example.test/users?existing=1&expand=projects'",
		"--header 'X-Trace: abc123'",
		"--header 'Content-Type: application/json'",
		`--data-raw '{"name":"Ada"}'`,
	} {
		if !strings.Contains(curl, expected) {
			t.Fatalf("curl snippet missing %q:\n%s", expected, curl)
		}
	}
	if strings.Contains(curl, "X-Skip") || strings.Contains(curl, "skip=no") {
		t.Fatalf("curl snippet included disabled rows:\n%s", curl)
	}

	fetch, err := codegen.GenerateResponseExampleCode(example, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`fetch("https://api.example.test/users?existing=1&expand=projects"`,
		`method: "POST"`,
		`"X-Trace": "abc123"`,
		`"Content-Type": "application/json"`,
		`const body = JSON.stringify({"name":"Ada"});`,
	} {
		if !strings.Contains(fetch, expected) {
			t.Fatalf("fetch snippet missing %q:\n%s", expected, fetch)
		}
	}
	if strings.Contains(fetch, "X-Skip") || strings.Contains(fetch, "skip") {
		t.Fatalf("fetch snippet included disabled rows:\n%s", fetch)
	}
}

func TestGenerateResponseExampleCodeBodyModes(t *testing.T) {
	form := ResponseExample{
		Request: ResponseExampleRequest{
			Method:   http.MethodPost,
			URL:      "https://api.example.test/forms",
			BodyMode: "formUrlEncoded",
			FormURLEncoded: []KeyValue{
				{Name: "email", Value: "ada@example.test", Enabled: true},
				{Name: "notes", Value: "hello there", Enabled: true},
				{Name: "disabled", Value: "nope", Enabled: false},
			},
		},
	}
	curl, err := codegen.GenerateResponseExampleCode(form, "curl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(curl, "--header 'Content-Type: application/x-www-form-urlencoded'") || !strings.Contains(curl, "--data-raw 'email=ada%40example.test&notes=hello+there'") || strings.Contains(curl, "disabled") {
		t.Fatalf("form cURL snippet mismatch:\n%s", curl)
	}
	fetch, err := codegen.GenerateResponseExampleCode(form, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`const body = new URLSearchParams();`, `body.append("email", "ada@example.test");`, `body.append("notes", "hello there");`, `body: body`} {
		if !strings.Contains(fetch, expected) {
			t.Fatalf("form fetch snippet missing %q:\n%s", expected, fetch)
		}
	}
	if strings.Contains(fetch, "disabled") {
		t.Fatalf("form fetch snippet included disabled row:\n%s", fetch)
	}

	multipartExample := ResponseExample{
		Request: ResponseExampleRequest{
			Method:   http.MethodPost,
			URL:      "https://api.example.test/upload",
			BodyMode: "multipartForm",
			MultipartForm: []FormPart{
				{Name: "title", Value: "Sample Document", Enabled: true},
				{Name: "document", FilePath: "examples/sample.pdf", ContentType: "application/pdf", Enabled: true},
				{Name: "skip", Value: "nope", Enabled: false},
			},
		},
	}
	curl, err = codegen.GenerateResponseExampleCode(multipartExample, "curl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(curl, "--form 'title=Sample Document'") || !strings.Contains(curl, "--form 'document=@examples/sample.pdf;type=application/pdf'") || strings.Contains(curl, "skip") {
		t.Fatalf("multipart cURL snippet mismatch:\n%s", curl)
	}
	fetch, err = codegen.GenerateResponseExampleCode(multipartExample, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`const body = new FormData();`, `body.append("title", "Sample Document");`, `body.append("document", new Blob([]), "sample.pdf"); // examples/sample.pdf (application/pdf)`} {
		if !strings.Contains(fetch, expected) {
			t.Fatalf("multipart fetch snippet missing %q:\n%s", expected, fetch)
		}
	}
	if strings.Contains(fetch, "Content-Type") || strings.Contains(fetch, "skip") {
		t.Fatalf("multipart fetch snippet should not set multipart content-type or disabled rows:\n%s", fetch)
	}

	fileExample := ResponseExample{
		Request: ResponseExampleRequest{
			Method:   http.MethodPut,
			URL:      "https://api.example.test/upload",
			BodyMode: "file",
			File: []FileBodyEntry{
				{FilePath: "examples/ignored.bin", ContentType: "application/octet-stream", Selected: false},
				{FilePath: "examples/backup.json", ContentType: "application/json", Selected: true},
			},
		},
	}
	curl, err = codegen.GenerateResponseExampleCode(fileExample, "curl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(curl, "--request 'PUT'") || !strings.Contains(curl, "--header 'Content-Type: application/json'") || !strings.Contains(curl, "--data-binary '@examples/backup.json'") || strings.Contains(curl, "ignored.bin") {
		t.Fatalf("file cURL snippet mismatch:\n%s", curl)
	}
	fetch, err = codegen.GenerateResponseExampleCode(fileExample, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fetch, `const body = new Blob([]); // examples/backup.json (application/json)`) || !strings.Contains(fetch, `"Content-Type": "application/json"`) || strings.Contains(fetch, "ignored.bin") {
		t.Fatalf("file fetch snippet mismatch:\n%s", fetch)
	}
}

func TestGenerateResponseExampleCodeFindsSavedBruExample(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Code Example Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Code Example Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "code.bru"), []byte(`meta {
  name: Code Request
  type: http
  seq: 1
}

post {
  url: https://api.example.test/live
  body: none
  auth: none
}

example {
  name: Snapshot Code

  request: {
    url: https://api.example.test/snapshot
    method: post
    mode: json
    headers: {
      X-Example: yes
    }
    body: {
      type: json
      content: '''
        {"snapshot":true}
      '''
    }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	item := opened.Items[0]
	if len(item.Examples) != 1 {
		t.Fatalf("expected loaded example: %#v", item.Examples)
	}
	code, err := app.GenerateResponseExampleCode(opened.ID, item.ID, item.Examples[0].ID, "curl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "https://api.example.test/snapshot") || strings.Contains(code, "https://api.example.test/live") || !strings.Contains(code, "--header 'X-Example: yes'") || !strings.Contains(code, `{"snapshot":true}`) {
		t.Fatalf("generated snippet did not use saved example snapshot:\n%s", code)
	}
}

func TestGenerateRequestCodeUsesCurrentRequestAndVariables(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	state, err = app.CreateEnvironment(collection.ID, "Code Env")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	env := collection.Environments[len(collection.Environments)-1]
	state, err = app.UpdateCollectionVariables(collection.ID, []Variable{
		{Name: "collection_header", Value: "base-header", DataType: "string", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.UpdateEnvironmentVariables(collection.ID, env.ID, []Variable{
		{Name: "host", Value: "api.example.test", DataType: "string", Enabled: true},
		{Name: "user_id", Value: "42", DataType: "string", Enabled: true},
		{Name: "expand", Value: "projects", DataType: "string", Enabled: true},
		{Name: "trace_id", Value: "abc123", DataType: "string", Enabled: true},
		{Name: "name", Value: "Ada", DataType: "string", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.UpdateCollectionHeaders(collection.ID, []KeyValue{{Name: "X-Collection", Value: "{{collection_header}}", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	method := http.MethodPost
	targetURL := "https://{{host}}/users/:userId"
	params := []KeyValue{
		{Name: "expand", Value: "{{expand}}", Enabled: true},
		{Name: "skip", Value: "no", Enabled: false},
	}
	pathParams := []KeyValue{{Name: "userId", Value: "{{user_id}}", Enabled: true}}
	headers := []KeyValue{
		{Name: "X-Trace", Value: "{{trace_id}}", Enabled: true},
		{Name: "X-Skip", Value: "no", Enabled: false},
	}
	body := RequestBody{Mode: "json", JSON: `{"name":"{{name}}"}`}
	vars := RequestVars{Req: []Variable{{Name: "name", Value: "Ada", DataType: "string", Enabled: true}}}
	state, err = app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		Method:     &method,
		URL:        &targetURL,
		Params:     &params,
		PathParams: &pathParams,
		Headers:    &headers,
		Body:       &body,
		Vars:       &vars,
	})
	if err != nil {
		t.Fatal(err)
	}

	curl, err := app.GenerateRequestCode(collection.ID, item.ID, env.ID, "curl")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"curl --request 'POST' 'https://api.example.test/users/42?expand=projects'",
		"--header 'X-Collection: base-header'",
		"--header 'X-Trace: abc123'",
		"--header 'Content-Type: application/json'",
		`--data-raw '{"name":"Ada"}'`,
	} {
		if !strings.Contains(curl, expected) {
			t.Fatalf("request cURL snippet missing %q:\n%s", expected, curl)
		}
	}
	if strings.Contains(curl, "X-Skip") || strings.Contains(curl, "skip=no") || strings.Contains(curl, "{{") {
		t.Fatalf("request cURL snippet included disabled or uninterpolated content:\n%s", curl)
	}

	fetch, err := app.GenerateRequestCode(collection.ID, item.ID, env.ID, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`fetch("https://api.example.test/users/42?expand=projects"`,
		`method: "POST"`,
		`"X-Collection": "base-header"`,
		`"X-Trace": "abc123"`,
		`const body = JSON.stringify({"name":"Ada"});`,
	} {
		if !strings.Contains(fetch, expected) {
			t.Fatalf("request fetch snippet missing %q:\n%s", expected, fetch)
		}
	}
}

func TestGenerateRequestCodeSupportsGraphQLAndRejectsInvalidTargets(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.CreateEnvironment(collection.ID, "GraphQL Code Env")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	env := collection.Environments[len(collection.Environments)-1]
	state, err = app.UpdateEnvironmentVariables(collection.ID, env.ID, []Variable{
		{Name: "host", Value: "api.example.test", DataType: "string", Enabled: true},
		{Name: "user_id", Value: "42", DataType: "string", Enabled: true},
		{Name: "team", Value: "platform", DataType: "string", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "graphql", "GraphQL Code")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	graphQL := collection.Items[len(collection.Items)-1]
	targetURL := "https://{{host}}/graphql"
	body := graphQL.Body
	body.GraphQLQuery = `query GetUser { user(id: "{{user_id}}") { name } }`
	body.GraphQLVariables = `{"team":"{{team}}"}`
	if _, err := app.UpdateRequest(collection.ID, graphQL.ID, RequestPatch{URL: &targetURL, Body: &body}); err != nil {
		t.Fatal(err)
	}
	fetch, err := app.GenerateRequestCode(collection.ID, graphQL.ID, env.ID, "fetch")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`fetch("https://api.example.test/graphql"`,
		`"Content-Type": "application/json"`,
		`const body = JSON.stringify({"query":"query GetUser { user(id: \"42\") { name } }","variables":{"team":"platform"}});`,
	} {
		if !strings.Contains(fetch, expected) {
			t.Fatalf("GraphQL fetch snippet missing %q:\n%s", expected, fetch)
		}
	}

	blankURL := ""
	if _, err := app.UpdateRequest(collection.ID, graphQL.ID, RequestPatch{URL: &blankURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GenerateRequestCode(collection.ID, graphQL.ID, env.ID, "curl"); err == nil || !strings.Contains(err.Error(), "URL is required") {
		t.Fatalf("expected blank URL rejection, got %v", err)
	}

	state, err = app.CreateRequest(collection.ID, "grpc", "gRPC Code")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	grpcItem := collection.Items[len(collection.Items)-1]
	if _, err := app.GenerateRequestCode(collection.ID, grpcItem.ID, env.ID, "curl"); err == nil || !strings.Contains(err.Error(), "HTTP and GraphQL") {
		t.Fatalf("expected unsupported request type rejection, got %v", err)
	}
}

func TestGitRemoteMetadataManagedIgnoreAndGhostRows(t *testing.T) {
	dataDir := t.TempDir()
	app := newAppInDirForTest(t, dataDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	state, err = app.CreateCollection(workspace.ID, "Git Backed", "bru")
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if _, err := app.ConnectCollectionGitRemote(collection.ID, "https://user:token@example.com/org/repo.git"); err == nil {
		t.Fatal("expected embedded credentials to be rejected")
	}
	state, err = app.ConnectCollectionGitRemote(collection.ID, "git@example.com:org/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if collection.Remote != "git@example.com:org/repo.git" || collection.NotFoundLocally {
		t.Fatalf("remote metadata was not stored: %#v", collection)
	}
	ignorePath := filepath.Join(workspace.Path, ".gitignore")
	ignore, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ignore), "/Git Backed") || !strings.Contains(string(ignore), "LiteAPI managed Git-backed collections") {
		t.Fatalf("managed .gitignore entry missing:\n%s", ignore)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dataDir)
	if err := os.RemoveAll(collection.Path); err != nil {
		t.Fatal(err)
	}
	state, err = reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	ghost := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if !ghost.NotFoundLocally || ghost.Remote == "" || len(ghost.Items) != 0 {
		t.Fatalf("missing remote collection was not marked as ghost: %#v", ghost)
	}
	state, err = reloaded.DisconnectCollectionGitRemote(ghost.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range state.Workspaces[0].Collections {
		if collection.ID == ghost.ID {
			t.Fatalf("ghost collection should be removed after disconnect: %#v", state.Workspaces[0].Collections)
		}
	}
	if data, err := os.ReadFile(ignorePath); err == nil && strings.Contains(string(data), "/Git Backed") {
		t.Fatalf("managed .gitignore entry was not removed:\n%s", data)
	}
}

func TestGitCloneScanAndOpenSelectedCollections(t *testing.T) {
	if _, err := gitVersion(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	collectionPath := filepath.Join(source, "Api")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Git API","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

get {
  url: https://example.test/ping
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "init")
	runGit(t, source, "add", ".")
	runGit(t, source, "-c", "user.name=LiteAPI Test", "-c", "user.email=liteapi@example.test", "commit", "-m", "initial")

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	remote := (&url.URL{Scheme: "file", Path: source}).String()
	result, err := app.CloneGitRepository(remote, filepath.Join(root, "clones"), "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == "" || result.TargetPath == "" || len(result.Candidates) != 1 {
		t.Fatalf("unexpected clone result: %#v", result)
	}
	if result.Candidates[0].Name != "Git API" || result.Candidates[0].RequestCount != 1 {
		t.Fatalf("unexpected scanned candidate: %#v", result.Candidates[0])
	}
	state, err = app.OpenGitCollections(state.Workspaces[0].ID, []string{result.Candidates[0].Path}, remote)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if opened.Name != "Git API" || opened.Remote != remote || opened.NotFoundLocally || len(opened.Items) != 1 {
		t.Fatalf("git collection was not opened: %#v", opened)
	}
}

func TestGitVersionReportsMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := gitVersion(); err == nil || !strings.Contains(err.Error(), "git is not installed") {
		t.Fatalf("expected missing git error, got %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestOpenLegacyYMLCollectionFromDiskAndSaveRequest(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Sample API Collection")
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "opencollection.yml"), []byte(`opencollection: 1.0.0

info:
  name: Sample API Collection

request:
  headers:
    - name: X-Collection
      value: yes
      enabled: true
  variables:
    - name: host
      value: https://jsonplaceholder.typicode.com

config:
  environments:
    - name: Local
      variables:
        - name: host
          value: https://example.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "Get Users.yml"), []byte(`info:
  name: Get Users
  type: http
  seq: 1

http:
  method: GET
  url: '{{host}}/users/:userId'
  headers:
    - name: Accept
      value: application/json
  params:
    - name: active
      value: 'true'
      type: query
    - name: userId
      value: '123'
      type: path

settings:
  encodeUrl: true
  timeout: 0
  followRedirects: true
  maxRedirects: 5

docs: This request retrieves a list of users.
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if opened.Name != "Sample API Collection" || opened.Format != "yml" || len(opened.Items) != 1 {
		t.Fatalf("unexpected opened yml collection: %#v", opened)
	}
	if len(opened.Headers) != 1 || opened.Headers[0].Name != "X-Collection" {
		t.Fatalf("collection headers were not hydrated: %#v", opened.Headers)
	}
	if len(opened.Variables) != 1 || opened.Variables[0].Name != "host" {
		t.Fatalf("collection variables were not hydrated: %#v", opened.Variables)
	}
	if len(opened.Environments) != 1 || opened.Environments[0].Name != "Local" {
		t.Fatalf("collection environments were not hydrated: %#v", opened.Environments)
	}
	item := opened.Items[0]
	if item.Name != "Get Users" || item.Method != http.MethodGet || item.URL != "{{host}}/users/:userId" {
		t.Fatalf("request not parsed from legacy yml: %#v", item)
	}
	if len(item.Headers) != 1 || item.Headers[0].Name != "Accept" {
		t.Fatalf("request headers were not parsed: %#v", item.Headers)
	}
	if len(item.Params) != 1 || item.Params[0].Name != "active" {
		t.Fatalf("request params were not parsed: %#v", item.Params)
	}
	if len(item.PathParams) != 1 || item.PathParams[0].Name != "userId" || item.PathParams[0].Value != "123" {
		t.Fatalf("request path params were not parsed: %#v", item.PathParams)
	}
	if !item.Settings.EncodeURL {
		t.Fatalf("request encodeUrl setting was not parsed: %#v", item.Settings)
	}
	if !strings.Contains(item.Docs, "retrieves") {
		t.Fatalf("request docs were not parsed: %q", item.Docs)
	}

	newURL := "{{host}}/users/1"
	if _, err := app.UpdateRequest(opened.ID, item.ID, RequestPatch{URL: &newURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveRequest(opened.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(collectionPath, "Get Users.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), newURL) {
		t.Fatalf("saved yml did not contain updated URL:\n%s", content)
	}
	if !strings.Contains(string(content), "type: path") || !strings.Contains(string(content), "userId") {
		t.Fatalf("saved yml did not preserve path params:\n%s", content)
	}
	if !strings.Contains(string(content), "encodeUrl: true") {
		t.Fatalf("saved yml did not preserve encodeUrl:\n%s", content)
	}
	config, err := os.ReadFile(filepath.Join(collectionPath, "opencollection.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "X-Collection") {
		t.Fatalf("saved opencollection did not contain collection header:\n%s", config)
	}
}

func TestOpenNestedFoldersAndPreserveOriginalRequestPath(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Nested Collection")
	requestDir := filepath.Join(collectionPath, "users", "v1")
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Nested Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "users", "folder.bru"), []byte(`meta {
  name: Users
  seq: 1
}

headers {
  X-Folder: users
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "folder.yml"), []byte(`info:
  name: v1
  seq: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	originalRequestPath := filepath.Join(requestDir, "get-users.original.bru")
	if err := os.WriteFile(originalRequestPath, []byte(`meta {
  name: List Users
  type: http
  seq: 2
}

get {
  url: https://example.test/users
  body: none
  auth: none
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(opened.Items) != 1 {
		t.Fatalf("expected nested request, got %#v", opened.Items)
	}
	item := opened.Items[0]
	if item.FolderPath != "Users/v1" {
		t.Fatalf("expected folder display path from metadata, got %q", item.FolderPath)
	}
	if item.FilePath != originalRequestPath {
		t.Fatalf("expected original file path %q, got %q", originalRequestPath, item.FilePath)
	}

	newURL := "https://example.test/users?page=2"
	if _, err := app.UpdateRequest(opened.ID, item.ID, RequestPatch{URL: &newURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveRequest(opened.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(originalRequestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), newURL) {
		t.Fatalf("saved nested request did not update original file:\n%s", content)
	}
	if exactFileExists(t, requestDir, "List Users.bru") {
		t.Fatalf("save should not create a sanitized duplicate inside the folder")
	}
}

func TestFolderHeadersVariablesAndAuthAreInherited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Root"); got != "root" {
			t.Fatalf("missing root header: %q", got)
		}
		if got := r.Header.Get("X-Folder"); got != "users" {
			t.Fatalf("missing folder header: %q", got)
		}
		if got := r.Header.Get("X-Override"); got != "folder" {
			t.Fatalf("folder header should override root header, got %q", got)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "folder-user" || pass != "folder-pass" {
			t.Fatalf("folder auth should override inherited collection auth: %q %q %v", user, pass, ok)
		}
		if got := r.URL.Query().Get("folder"); got != "child" {
			t.Fatalf("folder variable was not interpolated: %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Inheritance")
	requestDir := filepath.Join(collectionPath, "users")
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Folder Inheritance","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	collectionBru := fmt.Sprintf(`headers {
  X-Root: root
  X-Override: root
}

auth {
  mode: basic
}

auth:basic {
  username: root-user
  password: root-pass
}

vars:pre-request {
  host: %s
  folderVar: root
}
`, server.URL)
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(collectionBru), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "folder.bru"), []byte(`meta {
  name: Users
}

headers {
  X-Folder: users
  X-Override: folder
}

auth {
  mode: basic
}

auth:basic {
  username: folder-user
  password: folder-pass
}

vars:pre-request {
  folderVar: child
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "inspect.bru"), []byte(`meta {
  name: Inspect
  type: http
  seq: 1
}

get {
  url: {{host}}/inspect?folder={{folderVar}}
  body: none
  auth: inherit
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(opened.Folders) != 1 || opened.Folders[0].DisplayPath != "Users" {
		t.Fatalf("folder metadata was not stored: %#v", opened.Folders)
	}
	state, err = app.SendRequest(opened.ID, opened.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, opened.ID, opened.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("expected successful folder-inherited request: %#v", item.Response)
	}
}

func TestFolderResponseVariablesRunBeforePostScriptsAndTests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"collection":   "collection-token",
			"folder":       "folder-token",
			"folderShared": "folder-shared",
			"local":        "local-token",
			"request":      "request-token",
			"shared":       "request-shared",
		})
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Response Vars")
	requestDir := filepath.Join(collectionPath, "users")
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Folder Response Vars","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	collectionBru := fmt.Sprintf(`vars:pre-request {
  host: %s
}

vars:post-response {
  collection_token: $res.body.collection
  shared: "collection-shared"
}

script:post-response {
  bru.setVar("collection_post_seen", bru.getVar("request_post_seen") + ":" + bru.getVar("collection_token"));
}

tests {
  test("collection can read response vars and previous post scripts", function () {
    expect(bru.getVar("collection_post_seen")).to.equal("folder-token:request-shared:collection-token");
  });
}
`, server.URL)
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(collectionBru), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "folder.bru"), []byte(`meta {
  name: Users
}

vars:post-response {
  folder_token: $res.body.folder
  shared: $res.body.folderShared
  ~disabled_folder: $res.body.folder
  @local_folder: $res.body.local
}

script:post-response {
  bru.setVar("folder_post_seen", bru.getVar("folder_token"));
}

tests {
  test("folder can read response vars", function () {
    expect(bru.getVar("folder_post_seen")).to.equal("folder-token");
  });
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "capture.bru"), []byte(`meta {
  name: Capture
  type: http
  seq: 1
}

get {
  url: {{host}}/capture
  body: none
  auth: none
}

vars:post-response {
  request_token: $res.body.request
  shared: $res.body.shared
}

script:post-response {
  bru.setVar("request_post_seen", bru.getVar("folder_token") + ":" + bru.getVar("shared"));
}

tests {
  test("request sees inherited response vars", function () {
    expect(bru.getVar("collection_token")).to.equal("collection-token");
    expect(bru.getVar("folder_token")).to.equal("folder-token");
    expect(bru.getVar("request_token")).to.equal("request-token");
    expect(bru.getVar("shared")).to.equal("request-shared");
    expect(bru.getVar("local_folder")).to.equal("local-token");
    expect(bru.getVar("disabled_folder")).to.equal(undefined);
  });
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(opened.Folders) != 1 || len(opened.Folders[0].ResVariables) != 4 || len(opened.Items) != 1 || len(opened.Items[0].Vars.Res) != 2 {
		t.Fatalf("response variables were not parsed from folder/request files: folders=%#v item=%#v", opened.Folders, opened.Items)
	}
	if opened.Folders[0].ResVariables[3].Name != "local_folder" {
		t.Fatalf("local response variable marker was not normalized: %#v", opened.Folders[0].ResVariables)
	}
	state, err = app.SendRequest(opened.ID, opened.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, opened.ID, opened.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("expected successful response-var request: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 3 {
		t.Fatalf("expected request/folder/collection tests, got %#v", item.Response.TestResults)
	}
	for _, result := range item.Response.TestResults {
		if !result.Passed {
			t.Fatalf("response-var lifecycle test failed: %#v", item.Response.TestResults)
		}
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	runtimeVars := variablesByName(collection.RuntimeVariables)
	for name, want := range map[string]string{
		"collection_token":     "collection-token",
		"folder_token":         "folder-token",
		"local_folder":         "local-token",
		"request_token":        "request-token",
		"shared":               "request-shared",
		"request_post_seen":    "folder-token:request-shared",
		"folder_post_seen":     "folder-token",
		"collection_post_seen": "folder-token:request-shared:collection-token",
	} {
		if got := fmt.Sprint(runtimeVars[name].Value); got != want {
			t.Fatalf("runtime variable %s = %q, want %q; all=%#v", name, got, want, collection.RuntimeVariables)
		}
	}
	if _, ok := runtimeVars["disabled_folder"]; ok {
		t.Fatalf("disabled response variable should not be persisted: %#v", collection.RuntimeVariables)
	}
}

func TestUpdateFolderSettingsWritesFolderBruAndAffectsRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Folder-Setting") != "enabled" {
			http.Error(w, "missing folder header", http.StatusFailedDependency)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "folder-user" || password != "folder-pass" {
			http.Error(w, "missing folder auth", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("marker") != "folder-value" {
			http.Error(w, "missing folder variable", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "folder-token"})
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Folder Settings")
	requestDir := filepath.Join(collectionPath, "users")
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Folder Settings","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "folder.bru"), []byte(`meta {
  name: Users
  type: folder
  seq: 1
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, "inspect.bru"), []byte(`meta {
  name: Inspect
  type: http
  seq: 1
}

get {
  url: {{host}}/inspect?marker={{folder_marker}}
  body: none
  auth: inherit
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.Folders) != 1 {
		t.Fatalf("expected one folder, got %#v", collection.Folders)
	}

	state, err = app.UpdateFolderSettings(collection.ID, collection.Folders[0].Path, FolderConfig{
		Name: "Users",
		Headers: []KeyValue{{
			Name:    "X-Folder-Setting",
			Value:   "enabled",
			Enabled: true,
		}},
		Variables: []Variable{
			{Name: "host", Value: server.URL, Enabled: true, Type: "string", DataType: "string"},
			{Name: "folder_marker", Value: "folder-value", Enabled: true, Type: "string", DataType: "string"},
		},
		ResVariables: []Variable{{Name: "folder_token", Value: "$res.body.token", Enabled: true, Type: "string", DataType: "string"}},
		Auth:         AuthConfig{Mode: "basic", Username: "folder-user", Password: "folder-pass"},
		PreScript:    `bru.setVar("folder_pre_seen", "ran");`,
		PostScript:   `bru.setVar("folder_post_seen", bru.getVar("folder_token"));`,
		Tests: `test("folder settings are inherited", function () {
  expect(bru.getVar("folder_pre_seen")).to.equal("ran");
  expect(bru.getVar("folder_post_seen")).to.equal("folder-token");
});`,
		Docs: "Folder documentation from the settings editor.",
	})
	if err != nil {
		t.Fatal(err)
	}

	saved, err := os.ReadFile(filepath.Join(requestDir, "folder.bru"))
	if err != nil {
		t.Fatal(err)
	}
	savedText := string(saved)
	for _, want := range []string{
		"headers {",
		"X-Folder-Setting: enabled",
		"vars:pre-request {",
		"folder_marker: folder-value",
		"vars:post-response {",
		"folder_token: $res.body.token",
		"auth:basic {",
		"script:pre-request {",
		"script:post-response {",
		"tests {",
		"docs {",
	} {
		if !strings.Contains(savedText, want) {
			t.Fatalf("folder.bru missing %q:\n%s", want, savedText)
		}
	}
	parsed := readFolderConfig(requestDir)
	if parsed.Auth.Mode != "basic" || parsed.Auth.Username != "folder-user" || parsed.Auth.Password != "folder-pass" {
		t.Fatalf("folder auth did not round trip: %#v", parsed.Auth)
	}
	if variablesByName(parsed.Variables)["folder_marker"].Value != "folder-value" {
		t.Fatalf("folder variables did not round trip: %#v", parsed.Variables)
	}
	if variablesByName(parsed.ResVariables)["folder_token"].Value != "$res.body.token" {
		t.Fatalf("folder response variables did not round trip: %#v", parsed.ResVariables)
	}
	if !strings.Contains(parsed.PreScript, "folder_pre_seen") || !strings.Contains(parsed.PostScript, "folder_post_seen") || !strings.Contains(parsed.Tests, "folder settings are inherited") || parsed.Docs == "" {
		t.Fatalf("folder scripts/tests/docs did not round trip: %#v", parsed)
	}

	updatedCollection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	state, err = app.SendRequest(updatedCollection.ID, updatedCollection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, updatedCollection.ID, updatedCollection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("expected successful folder-settings request: %#v", item.Response)
	}
	if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
		t.Fatalf("expected inherited folder test to pass: %#v", item.Response.TestResults)
	}
}

func TestProcessEnvInterpolationAndFolderEnvPrecedence(t *testing.T) {
	t.Setenv("LITEAPI_PROC_VALUE", "process-value")
	t.Setenv("LITEAPI_PROC_HEADER", "process-header")
	t.Setenv("LITEAPI_PROC_SCRIPT", "process-script")
	t.Setenv("LITEAPI_PROC_TEST", "process-test")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("value"); got != "folder" {
			t.Fatalf("folder variables should override active environment variables, got %q", got)
		}
		if got := r.URL.Query().Get("nested"); got != "process-value" {
			t.Fatalf("nested process env interpolation failed: %q", got)
		}
		if got := r.Header.Get("X-Process"); got != "process-header" {
			t.Fatalf("process env header interpolation failed: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"value":  r.URL.Query().Get("value"),
			"nested": r.URL.Query().Get("nested"),
			"header": r.Header.Get("X-Process"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	itemID := state.Workspaces[0].Collections[0].Items[0].ID
	envID := state.Workspaces[0].Collections[0].Environments[0].ID

	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Variables = append(collection.Variables,
		Variable{ID: "collision-collection", Name: "collision", Value: "collection", DataType: "string", Enabled: true},
		Variable{ID: "nested-process", Name: "nested_proc", Value: "{{process.env.LITEAPI_PROC_VALUE}}", DataType: "string", Enabled: true},
	)
	collection.Environments[0].Variables = append(collection.Environments[0].Variables,
		Variable{ID: "collision-env", Name: "collision", Value: "environment", DataType: "string", Enabled: true},
	)
	collection.Folders = []FolderConfig{{
		Path:        "Folder",
		DisplayPath: "Folder",
		Name:        "Folder",
		Variables: []Variable{
			{ID: "collision-folder", Name: "collision", Value: "folder", DataType: "string", Enabled: true},
		},
	}}
	item := &collection.Items[0]
	item.FolderPath = "Folder"
	item.Method = http.MethodGet
	item.URL = server.URL + "/check?value={{collision}}&nested={{nested_proc}}"
	item.Headers = []KeyValue{{Name: "X-Process", Value: "{{process.env.LITEAPI_PROC_HEADER}}", Enabled: true}}
	item.PreScript = `if (bru.interpolate("{{process.env.LITEAPI_PROC_SCRIPT}}") !== "process-script") {
  throw new Error("process env was not available to bru.interpolate");
}
if (bru.getCollectionVar("nested_proc") !== "process-value") {
  throw new Error("process env was not available inside scoped variable values");
}`
	item.Tests = `test("process env interpolation and precedence", function () {
  expect(res.json.value).to.equal("folder");
  expect(res.json.nested).to.equal("process-value");
  expect(res.json.header).to.equal("process-header");
  expect(bru.interpolate("{{process.env.LITEAPI_PROC_TEST}}")).to.equal("process-test");
});`
	app.mu.Unlock()

	state, err = app.SendRequest(collectionID, itemID, envID)
	if err != nil {
		t.Fatal(err)
	}
	itemResult, ok := findItemInState(state, collectionID, itemID)
	if !ok || itemResult.Response == nil || itemResult.Response.Status != http.StatusOK {
		t.Fatalf("process env precedence request failed: %#v", itemResult.Response)
	}
	if len(itemResult.Response.TestResults) != 1 || !itemResult.Response.TestResults[0].Passed {
		t.Fatalf("process env precedence test did not pass: %#v", itemResult.Response.TestResults)
	}
}

func TestDotEnvProcessEnvPrecedenceAcrossCollectionWorkspaceAndOS(t *testing.T) {
	t.Setenv("LITEAPI_DOTENV_PRIORITY", "os")
	t.Setenv("LITEAPI_DOTENV_OS_ONLY", "os-only")
	t.Setenv("LITEAPI_DOTENV_SCRIPT_ONLY", "os-script")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if got := query.Get("priority"); got != "collection" {
			t.Fatalf("collection .env should override workspace .env and OS env, got %q", got)
		}
		if got := query.Get("workspace"); got != "workspace-only" {
			t.Fatalf("workspace .env should override OS env, got %q", got)
		}
		if got := query.Get("collection"); got != "collection-only" {
			t.Fatalf("collection .env variable missing: %q", got)
		}
		if got := query.Get("os"); got != "os-only" {
			t.Fatalf("OS env fallback missing: %q", got)
		}
		if got := query.Get("nested"); got != "collection" {
			t.Fatalf("selected env variable did not resolve process env: %q", got)
		}
		if got := r.Header.Get("X-Quoted"); got != "workspace # quoted" {
			t.Fatalf("quoted workspace .env value not parsed: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"priority":   query.Get("priority"),
			"workspace":  query.Get("workspace"),
			"collection": query.Get("collection"),
			"os":         query.Get("os"),
			"nested":     query.Get("nested"),
			"quoted":     r.Header.Get("X-Quoted"),
		})
	}))
	defer server.Close()

	appDir := t.TempDir()
	app := newAppInDirForTest(t, appDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := state.Workspaces[0].Path
	collectionPath := state.Workspaces[0].Collections[0].Path
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(collectionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, ".env"), []byte(`LITEAPI_DOTENV_PRIORITY=workspace
LITEAPI_DOTENV_WORKSPACE_ONLY=workspace-only
LITEAPI_DOTENV_QUOTED="workspace # quoted"
LITEAPI_DOTENV_SCRIPT_ONLY=workspace-script
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, ".env"), []byte(`LITEAPI_DOTENV_PRIORITY=collection
LITEAPI_DOTENV_COLLECTION_ONLY=collection-only
LITEAPI_DOTENV_SCRIPT_ONLY=collection-script
`), 0o600); err != nil {
		t.Fatal(err)
	}

	collectionID := state.Workspaces[0].Collections[0].ID
	itemID := state.Workspaces[0].Collections[0].Items[0].ID
	envID := state.Workspaces[0].Collections[0].Environments[0].ID
	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Environments[0].Variables = append(collection.Environments[0].Variables,
		Variable{ID: "dotenv-nested", Name: "dotenv_nested", Value: "{{process.env.LITEAPI_DOTENV_PRIORITY}}", DataType: "string", Enabled: true},
	)
	item := &collection.Items[0]
	item.Method = http.MethodGet
	item.URL = server.URL + "/dotenv?priority={{process.env.LITEAPI_DOTENV_PRIORITY}}&workspace={{process.env.LITEAPI_DOTENV_WORKSPACE_ONLY}}&collection={{process.env.LITEAPI_DOTENV_COLLECTION_ONLY}}&os={{process.env.LITEAPI_DOTENV_OS_ONLY}}&nested={{dotenv_nested}}"
	item.Headers = []KeyValue{{Name: "X-Quoted", Value: "{{process.env.LITEAPI_DOTENV_QUOTED}}", Enabled: true}}
	item.PreScript = `if (bru.getProcessEnv("LITEAPI_DOTENV_SCRIPT_ONLY") !== "collection-script") {
  throw new Error("bru.getProcessEnv did not use collection .env");
}
if (bru.getProcessEnv("LITEAPI_DOTENV_WORKSPACE_ONLY") !== "workspace-only") {
  throw new Error("bru.getProcessEnv did not include workspace .env");
}
if (bru.interpolate("{{process.env.LITEAPI_DOTENV_PRIORITY}}") !== "collection") {
  throw new Error("bru.interpolate did not use .env precedence");
}`
	item.Tests = `test("dotenv process env precedence", function () {
  expect(res.json.priority).to.equal("collection");
  expect(res.json.workspace).to.equal("workspace-only");
  expect(res.json.collection).to.equal("collection-only");
  expect(res.json.os).to.equal("os-only");
  expect(res.json.nested).to.equal("collection");
  expect(res.json.quoted).to.equal("workspace # quoted");
});`
	app.mu.Unlock()

	state, err = app.SendRequest(collectionID, itemID, envID)
	if err != nil {
		t.Fatal(err)
	}
	itemResult, ok := findItemInState(state, collectionID, itemID)
	if !ok || itemResult.Response == nil || itemResult.Response.Status != http.StatusOK {
		t.Fatalf("dotenv request failed: %#v", itemResult.Response)
	}
	if len(itemResult.Response.TestResults) != 1 || !itemResult.Response.TestResults[0].Passed {
		t.Fatalf("dotenv precedence test did not pass: %#v", itemResult.Response.TestResults)
	}
}

func TestDotEnvFileManagerListsSavesDeletesAndRuntimeExactEnvOnly(t *testing.T) {
	t.Setenv("LITEAPI_DOTENV_UI", "os")
	t.Setenv("LITEAPI_DOTENV_UI_OS_ONLY", "os-only")

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	collection := workspace.Collections[0]
	item := collection.Items[0]
	envID := collection.Environments[0].ID

	_, err = app.SaveDotEnvFile(workspace.ID, collection.ID, "workspace", ".env", "LITEAPI_DOTENV_UI=workspace\nLITEAPI_DOTENV_UI_WORKSPACE_ONLY=workspace-only\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.SaveDotEnvFile(workspace.ID, collection.ID, "collection", ".env.local", "LITEAPI_DOTENV_UI=local\nLITEAPI_DOTENV_UI_LOCAL_ONLY=local-only\n")
	if err != nil {
		t.Fatal(err)
	}
	files, err := app.SaveDotEnvFile(workspace.ID, collection.ID, "collection", ".env", "LITEAPI_DOTENV_UI=collection\n")
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]DotEnvFile{}
	for _, file := range files {
		byKey[file.Scope+":"+file.Name] = file
	}
	if got := byKey["workspace:.env"]; got.Name != ".env" || !got.Runtime || !strings.Contains(got.Content, "workspace-only") {
		t.Fatalf("workspace .env not listed with content/runtime metadata: %#v", got)
	}
	if got := byKey["collection:.env"]; got.Name != ".env" || !got.Runtime || !strings.Contains(got.Content, "collection") {
		t.Fatalf("collection .env not listed with content/runtime metadata: %#v", got)
	}
	if got := byKey["collection:.env.local"]; got.Name != ".env.local" || got.Runtime || !strings.Contains(got.Content, "local-only") {
		t.Fatalf("collection .env.local not listed as non-runtime editable file: %#v", got)
	}
	if _, err := app.SaveDotEnvFile(workspace.ID, collection.ID, "collection", "../.env", "bad"); err == nil {
		t.Fatal("path traversal .env filename was accepted")
	}
	if _, err := app.SaveDotEnvFile(workspace.ID, collection.ID, "collection", "env", "bad"); err == nil {
		t.Fatal("non-.env filename was accepted")
	}
	if _, err := app.SaveDotEnvFile(workspace.ID, collection.ID, "collection", ".env bad", "bad"); err == nil {
		t.Fatal("unsafe .env suffix was accepted")
	}

	_, _, vars, err := app.effectiveRequestContextForExecution(collection.ID, item.ID, envID)
	if err != nil {
		t.Fatal(err)
	}
	if got := vars["process.env.LITEAPI_DOTENV_UI"]; got != "collection" {
		t.Fatalf("runtime did not use exact collection .env over workspace/OS: %q", got)
	}
	if got := vars["process.env.LITEAPI_DOTENV_UI_WORKSPACE_ONLY"]; got != "workspace-only" {
		t.Fatalf("runtime did not include exact workspace .env: %q", got)
	}
	if got := vars["process.env.LITEAPI_DOTENV_UI_OS_ONLY"]; got != "os-only" {
		t.Fatalf("runtime did not include OS fallback: %q", got)
	}
	if _, ok := vars["process.env.LITEAPI_DOTENV_UI_LOCAL_ONLY"]; ok {
		t.Fatalf(".env.local participated in runtime vars: %#v", vars)
	}

	files, err = app.DeleteDotEnvFile(workspace.ID, collection.ID, "collection", ".env.local")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Scope == "collection" && file.Name == ".env.local" {
			t.Fatalf("deleted .env.local still listed: %#v", files)
		}
	}
	if exactFileExists(t, collection.Path, ".env.local") {
		t.Fatal(".env.local still exists after delete")
	}
}

func TestResolveProcessEnvValuesUsesRuntimePrecedence(t *testing.T) {
	t.Setenv("LITEAPI_TOOLTIP_ENV_PRIORITY", "os")
	t.Setenv("LITEAPI_TOOLTIP_ENV_OS_ONLY", "os-only")

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	collection := workspace.Collections[0]

	if _, err := app.SaveDotEnvFile(workspace.ID, collection.ID, "workspace", ".env", "LITEAPI_TOOLTIP_ENV_PRIORITY=workspace\nLITEAPI_TOOLTIP_ENV_WORKSPACE_ONLY=workspace-only\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveDotEnvFile(workspace.ID, collection.ID, "collection", ".env", "LITEAPI_TOOLTIP_ENV_PRIORITY=collection\nLITEAPI_TOOLTIP_ENV_COLLECTION_ONLY=collection-only\n"); err != nil {
		t.Fatal(err)
	}

	values, err := app.ResolveProcessEnvValues(collection.ID, []string{
		"process.env.LITEAPI_TOOLTIP_ENV_PRIORITY",
		"process.env.LITEAPI_TOOLTIP_ENV_COLLECTION_ONLY",
		"process.env.LITEAPI_TOOLTIP_ENV_WORKSPACE_ONLY",
		"process.env.LITEAPI_TOOLTIP_ENV_OS_ONLY",
		"process.env.LITEAPI_TOOLTIP_ENV_MISSING",
		"not-process-env",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := values["process.env.LITEAPI_TOOLTIP_ENV_PRIORITY"]; got != "collection" {
		t.Fatalf("collection .env should win in tooltip process-env resolver, got %q", got)
	}
	if got := values["process.env.LITEAPI_TOOLTIP_ENV_COLLECTION_ONLY"]; got != "collection-only" {
		t.Fatalf("collection .env value missing from tooltip resolver: %q", got)
	}
	if got := values["process.env.LITEAPI_TOOLTIP_ENV_WORKSPACE_ONLY"]; got != "workspace-only" {
		t.Fatalf("workspace .env value missing from tooltip resolver: %q", got)
	}
	if got := values["process.env.LITEAPI_TOOLTIP_ENV_OS_ONLY"]; got != "os-only" {
		t.Fatalf("OS fallback missing from tooltip resolver: %q", got)
	}
	if got, ok := values["process.env.LITEAPI_TOOLTIP_ENV_MISSING"]; !ok || got != "" {
		t.Fatalf("missing process env should be present as an empty value, got %q present=%v", got, ok)
	}
	if _, ok := values["not-process-env"]; ok {
		t.Fatalf("non-process-env key should not be returned: %#v", values)
	}
}

func TestPromptVariableInterpolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "Ada" {
			t.Fatalf("prompt URL interpolation failed: %q", got)
		}
		if got := r.URL.Query().Get("nested"); got != "nested-value" {
			t.Fatalf("nested prompt interpolation failed: %q", got)
		}
		if got := r.Header.Get("X-Prompt"); got != "header-value" {
			t.Fatalf("prompt header interpolation failed: %q", got)
		}
		if got := r.Header.Get("X-Request-Var"); got != "request-var-value" {
			t.Fatalf("prompt request variable interpolation failed: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"name":       r.URL.Query().Get("name"),
			"nested":     r.URL.Query().Get("nested"),
			"header":     r.Header.Get("X-Prompt"),
			"requestVar": r.Header.Get("X-Request-Var"),
		})
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID
	itemID := state.Workspaces[0].Collections[0].Items[0].ID

	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Variables = append(collection.Variables,
		Variable{ID: "nested-prompt", Name: "nested_prompt", Value: "{{?Nested Prompt}}", DataType: "string", Enabled: true},
	)
	item := &collection.Items[0]
	item.Method = http.MethodGet
	item.URL = server.URL + "/check?name={{?User Name}}&nested={{nested_prompt}}"
	item.Headers = []KeyValue{
		{Name: "X-Prompt", Value: "{{?Header Prompt}}", Enabled: true},
		{Name: "X-Request-Var", Value: "{{request_prompt}}", Enabled: true},
	}
	item.Vars.Req = []Variable{
		{ID: "request-prompt", Name: "request_prompt", Value: "{{?Request Prompt}}", DataType: "string", Enabled: true},
		{ID: "script-prompt", Name: "script_prompt", Value: "{{?Script Prompt}}", DataType: "string", Enabled: true},
	}
	item.PreScript = `if (bru.interpolate("{{script_prompt}}") !== "script-value") {
  throw new Error("prompt variable was not available to pre-request scripts");
}`
	item.Tests = `test("prompt variables interpolate", function () {
  expect(res.json.name).to.equal("Ada");
  expect(res.json.nested).to.equal("nested-value");
  expect(res.json.header).to.equal("header-value");
  expect(res.json.requestVar).to.equal("request-var-value");
  expect(bru.interpolate("{{?Script Prompt}}")).to.equal("script-value");
});`
	app.mu.Unlock()

	state, err = app.SendRequestWithPromptValues(collectionID, itemID, "", map[string]string{
		"User Name":      "Ada",
		"Nested Prompt":  "nested-value",
		"Header Prompt":  "header-value",
		"Request Prompt": "request-var-value",
		"Script Prompt":  "script-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	itemResult, ok := findItemInState(state, collectionID, itemID)
	if !ok || itemResult.Response == nil || itemResult.Response.Status != http.StatusOK {
		t.Fatalf("prompt request failed: %#v", itemResult.Response)
	}
	if len(itemResult.Response.TestResults) != 1 || !itemResult.Response.TestResults[0].Passed {
		t.Fatalf("prompt variable test did not pass: %#v", itemResult.Response.TestResults)
	}
}

func TestOpenBruCollectionMetadataAndEnvironmentFromDisk(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Bru Collection")
	if err := os.MkdirAll(filepath.Join(collectionPath, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Bru Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "collection.bru"), []byte(`headers {
  X-Collection: yes
}

auth {
  mode: basic
}

auth:basic {
  username: alice
  password: secret
}

vars:pre-request {
  host: https://example.test
  @number
  retryCount: 3
}

script:pre-request {
  bru.setVar("fromCollection", "ok");
}

tests {
  expect status equals 200
}

docs {
  # Bru Collection
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "environments", "Local.bru"), []byte(`vars {
  host: http://localhost:8080
  @boolean
  featureFlag: true
}
vars:secret [
  apiToken
]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "ping.bru"), []byte(`meta {
  name: Ping
  type: http
  seq: 1
}

post {
  url: {{host}}/ping
  body: json
  auth: inherit
}

body:json {
  {
    "ok": true
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(opened.Headers) != 1 || opened.Headers[0].Name != "X-Collection" {
		t.Fatalf("collection headers were not parsed: %#v", opened.Headers)
	}
	if opened.Auth.Mode != "basic" || opened.Auth.Username != "alice" || opened.Auth.Password != "secret" {
		t.Fatalf("collection auth was not parsed: %#v", opened.Auth)
	}
	if len(opened.Variables) != 2 || opened.Variables[1].DataType != "number" {
		t.Fatalf("collection vars were not parsed with data types: %#v", opened.Variables)
	}
	if !strings.Contains(opened.PreScript, "fromCollection") || !strings.Contains(opened.Tests, "expect status") || !strings.Contains(opened.Docs, "Bru Collection") {
		t.Fatalf("collection script/tests/docs were not parsed: %#v", opened)
	}
	if len(opened.Environments) != 1 || opened.Environments[0].Name != "Local" {
		t.Fatalf("environment was not parsed: %#v", opened.Environments)
	}
	if len(opened.Environments[0].Variables) != 3 || !opened.Environments[0].Variables[2].Secret {
		t.Fatalf("environment variables/secrets were not parsed: %#v", opened.Environments[0].Variables)
	}
	if len(opened.Items) != 1 || opened.Items[0].Body.Mode != "json" || !strings.Contains(opened.Items[0].Body.JSON, `"ok": true`) {
		t.Fatalf("request body was not parsed from bru: %#v", opened.Items)
	}

	if _, err := app.SaveRequest(opened.ID, opened.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	collectionFile, err := os.ReadFile(filepath.Join(collectionPath, "collection.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(collectionFile), "auth:basic") || !strings.Contains(string(collectionFile), "script:pre-request") {
		t.Fatalf("collection.bru did not preserve auth/script:\n%s", collectionFile)
	}
	envFile, err := os.ReadFile(filepath.Join(collectionPath, "environments", "Local.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envFile), "vars:secret") || !strings.Contains(string(envFile), "apiToken") {
		t.Fatalf("environment bru did not preserve secret marker:\n%s", envFile)
	}
}

func TestCollectionEnvironmentSecretsStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Secret Collection")
	if err := os.MkdirAll(filepath.Join(collectionPath, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Secret Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "environments", "Local.bru"), []byte(`vars {
  host: https://example.test
}

vars:secret [
  apiToken
]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "secret.bru"), []byte(`meta {
  name: Secret
  type: http
  seq: 1
}

get {
  url: {{host}}/secret
  body: none
  auth: none
}

headers {
  X-Token: {{apiToken}}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	appDir := t.TempDir()
	app := newAppInDirForTest(t, appDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	env := opened.Environments[0]
	vars := []Variable{
		{ID: "host", Name: "host", Value: "https://example.test", DataType: "string", Type: "string", Enabled: true},
		{ID: "apiToken", Name: "apiToken", Value: "super-secret-token", DataType: "string", Type: "string", Enabled: true, Secret: true},
	}
	if _, err := app.UpdateEnvironmentVariables(opened.ID, env.ID, vars); err != nil {
		t.Fatal(err)
	}

	envFile, err := os.ReadFile(filepath.Join(collectionPath, "environments", "Local.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(envFile), "super-secret-token") || !strings.Contains(string(envFile), "vars:secret") || !strings.Contains(string(envFile), "apiToken") {
		t.Fatalf("environment file leaked or lost secret marker:\n%s", envFile)
	}
	flushPersistForTest(t, app)
	stateFile, err := os.ReadFile(filepath.Join(appDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateFile), "super-secret-token") {
		t.Fatalf("state.json leaked environment secret:\n%s", stateFile)
	}
	secretsFile, err := os.ReadFile(filepath.Join(appDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretsFile), "super-secret-token") {
		t.Fatalf("secrets.json stored plaintext secret:\n%s", secretsFile)
	}
	var store environmentSecretsFile
	if err := json.Unmarshal(secretsFile, &store); err != nil {
		t.Fatal(err)
	}
	storedValue := ""
	for _, collection := range store.Collections {
		if collection.Path != normalizedEnvironmentSecretPath(collectionPath) {
			continue
		}
		for _, storedEnv := range collection.Environments {
			if storedEnv.Name != "Local" {
				continue
			}
			for _, secret := range storedEnv.Secrets {
				if secret.Name == "apiToken" {
					storedValue = secret.Value
				}
			}
		}
	}
	if !strings.HasPrefix(storedValue, "$01:") {
		t.Fatalf("secret store did not encrypt apiToken with Bruno AES fallback format: %#v", store)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, appDir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	reloadedCollection := reloadedState.Workspaces[0].Collections[len(reloadedState.Workspaces[0].Collections)-1]
	reloadedVars := variablesByName(reloadedCollection.Environments[0].Variables)
	if reloadedVars["apiToken"].Value != "super-secret-token" {
		t.Fatalf("secret was not hydrated from store after restart: %#v", reloadedVars["apiToken"])
	}
}

func TestCollectionEnvironmentSecretsHydrateBrunoEncryptedFallbacks(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "legacy-key")
	root := t.TempDir()
	collectionPath := filepath.Join(root, "Imported Secret Collection")
	if err := os.MkdirAll(filepath.Join(collectionPath, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Imported Secret Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "environments", "Local.bru"), []byte(`vars:secret [
  legacyToken
  safeToken
]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	appDir := t.TempDir()
	app := newAppInDirForTest(t, appDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenCollection(state.Workspaces[0].ID, collectionPath); err != nil {
		t.Fatal(err)
	}
	store := environmentSecretsFile{
		Collections: []environmentSecretCollection{
			{
				Path: normalizedEnvironmentSecretPath(collectionPath),
				Environments: []environmentSecretEnvironment{
					{
						Name: "Local",
						Secrets: []environmentSecretVariable{
							{Name: "legacyToken", Value: "$01:69b2ce3315570265db41263bc2e6a640"},
							{Name: "safeToken", Value: "$00:not-available-outside-electron"},
						},
					},
				},
			},
		},
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Flush before planting the fixture: a later flush would overwrite the
	// hand-written store with the app's own view of it.
	flushPersistForTest(t, app)
	if err := os.WriteFile(filepath.Join(appDir, "secrets.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded := newAppInDirForTest(t, appDir)
	reloadedState, err := reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	reloadedCollection := reloadedState.Workspaces[0].Collections[len(reloadedState.Workspaces[0].Collections)-1]
	vars := variablesByName(reloadedCollection.Environments[0].Variables)
	if vars["legacyToken"].Value != "legacy secret" {
		t.Fatalf("Bruno legacy AES secret was not hydrated: %#v", vars["legacyToken"])
	}
	if vars["safeToken"].Value != "" {
		t.Fatalf("Electron safeStorage secret should hydrate empty outside safeStorage context: %#v", vars["safeToken"])
	}
}

func TestCollectionEnvironmentSecretHydratesForRequestExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Token"); got != "super-secret-token" {
			t.Fatalf("secret header was not interpolated: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	root := t.TempDir()
	collectionPath := filepath.Join(root, "Secret Send Collection")
	if err := os.MkdirAll(filepath.Join(collectionPath, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "bruno.json"), []byte(`{"version":"1","name":"Secret Send Collection","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	envContent := fmt.Sprintf(`vars {
  host: %s
}

vars:secret [
  apiToken
]
`, server.URL)
	if err := os.WriteFile(filepath.Join(collectionPath, "environments", "Local.bru"), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "secret.bru"), []byte(`meta {
  name: Secret
  type: http
  seq: 1
}

get {
  url: {{host}}/secret
  body: none
  auth: none
}

headers {
  X-Token: {{apiToken}}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	appDir := t.TempDir()
	app := newAppInDirForTest(t, appDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenCollection(state.Workspaces[0].ID, collectionPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	env := opened.Environments[0]
	vars := []Variable{
		{ID: "host", Name: "host", Value: server.URL, DataType: "string", Type: "string", Enabled: true},
		{ID: "apiToken", Name: "apiToken", Value: "super-secret-token", DataType: "string", Type: "string", Enabled: true, Secret: true},
	}
	if _, err := app.UpdateEnvironmentVariables(opened.ID, env.ID, vars); err != nil {
		t.Fatal(err)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, appDir)
	state, err = reloaded.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	env = collection.Environments[0]
	state, err = reloaded.SendRequest(collection.ID, collection.Items[0].ID, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("request with hydrated secret failed: %#v", item.Response)
	}
}

func TestCollectionHeadersAndInheritedAuthAreApplied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Collection"); got != "yes" {
			t.Fatalf("missing collection header: %q", got)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			t.Fatalf("missing inherited basic auth: %q %q %v", user, pass, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if _, err := app.UpdateCollectionHeaders(collection.ID, []KeyValue{{Name: "X-Collection", Value: "yes", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdateCollectionAuth(collection.ID, AuthConfig{Mode: "basic", Username: "alice", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	method := http.MethodGet
	targetURL := server.URL
	body := collection.Items[0].Body
	body.Mode = "none"
	auth := AuthConfig{Mode: "inherit"}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("request failed: %#v", item.Response)
	}
}

func TestDigestAuthChallengeRetrySucceeds(t *testing.T) {
	const (
		realm    = "liteapi"
		nonce    = "nonce-value"
		opaque   = "opaque-value"
		username = "digest-user"
		password = "digest-pass"
	)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="`+realm+`", nonce="`+nonce+`", qop="auth", opaque="`+opaque+`"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		values := parseDigestChallenge(authHeader)
		if values["username"] != username || values["realm"] != realm || values["nonce"] != nonce || values["uri"] != r.URL.RequestURI() || values["opaque"] != opaque {
			t.Fatalf("unexpected digest values: %#v", values)
		}
		ha1 := md5Hex(username + ":" + realm + ":" + password)
		ha2 := md5Hex(r.Method + ":" + r.URL.RequestURI())
		expected := md5Hex(ha1 + ":" + nonce + ":" + values["nc"] + ":" + values["cnonce"] + ":" + values["qop"] + ":" + ha2)
		if values["response"] != expected {
			t.Fatalf("bad digest response: got %s expected %s values=%#v", values["response"], expected, values)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authenticated":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	targetURL := server.URL + "/digest"
	auth := AuthConfig{Mode: "digest", Username: username, Password: password}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("digest request failed: %#v", item.Response)
	}
	if attempts != 2 {
		t.Fatalf("expected challenge plus retry, got %d attempts", attempts)
	}
}

func TestDigestAuthBruRoundTrip(t *testing.T) {
	item := types.NewRequestItem("Digest", "http", 1)
	item.Auth = AuthConfig{Mode: "digest", Username: "alice", Password: "secret"}
	content := stringifyBru(item)
	if !strings.Contains(content, "auth:digest") {
		t.Fatalf("digest auth was not serialized:\n%s", content)
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Mode != "digest" || parsed.Auth.Username != "alice" || parsed.Auth.Password != "secret" {
		t.Fatalf("digest auth did not round-trip: %#v", parsed.Auth)
	}
}

func TestNTLMAuthChallengeFlowSucceeds(t *testing.T) {
	var negotiateSeen, authenticateSeen bool
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(body); got != `{"hello":"ntlm"}` {
			t.Fatalf("unexpected NTLM body on call %d: %q", callCount, got)
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("WWW-Authenticate", "NTLM")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(authHeader, "NTLM ") {
			t.Fatalf("unexpected NTLM authorization header: %s", authHeader)
		}
		messageType := ntlmMessageType(t, strings.TrimPrefix(authHeader, "NTLM "))
		switch messageType {
		case 1:
			negotiateSeen = true
			w.Header().Set("WWW-Authenticate", "NTLM "+testNTLMChallenge(t))
			w.WriteHeader(http.StatusUnauthorized)
		case 3:
			authenticateSeen = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ntlm":true}`))
		default:
			t.Fatalf("unexpected NTLM message type %d", messageType)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodPost
	targetURL := server.URL + "/ntlm"
	body := RequestBody{Mode: "json", JSON: `{"hello":"ntlm"}`}
	auth := AuthConfig{Mode: "ntlm", Username: "{{ntlm_user}}", Password: "{{ntlm_pass}}", Domain: "{{ntlm_domain}}"}
	vars := RequestVars{Req: []Variable{
		{ID: "var-user", Name: "ntlm_user", Value: "alice", DataType: "string", Enabled: true},
		{ID: "var-pass", Name: "ntlm_pass", Value: "secret", DataType: "string", Enabled: true},
		{ID: "var-domain", Name: "ntlm_domain", Value: "DOMAIN", DataType: "string", Enabled: true},
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body, Auth: &auth, Vars: &vars}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("NTLM request failed: %#v", item.Response)
	}
	if !negotiateSeen || !authenticateSeen || callCount != 3 {
		t.Fatalf("NTLM handshake did not complete: negotiate=%v authenticate=%v calls=%d", negotiateSeen, authenticateSeen, callCount)
	}
}

func TestNTLMAuthBruRoundTrip(t *testing.T) {
	item := types.NewRequestItem("NTLM", "http", 1)
	item.Auth = AuthConfig{Mode: "ntlm", Username: "alice", Password: "secret", Domain: "DOMAIN"}
	content := stringifyBru(item)
	for _, expected := range []string{"auth:ntlm", "username: alice", "password: secret", "domain: DOMAIN"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("NTLM auth was not serialized with %q:\n%s", expected, content)
		}
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Mode != "ntlm" || parsed.Auth.Username != "alice" || parsed.Auth.Password != "secret" || parsed.Auth.Domain != "DOMAIN" {
		t.Fatalf("NTLM auth did not round-trip: %#v", parsed.Auth)
	}
}

func TestOAuth2ClientCredentialsFetchesAndAppliesHeader(t *testing.T) {
	const (
		clientID     = "client-id"
		clientSecret = "client-secret"
		accessToken  = "fetched-access-token"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			user, pass, ok := r.BasicAuth()
			if !ok || user != clientID || pass != clientSecret {
				t.Fatalf("missing OAuth2 basic client credentials: %q %q %v", user, pass, ok)
			}
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Fatalf("unexpected token accept header: %q", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "read write" {
				t.Fatalf("unexpected token request form: %s", r.Form.Encode())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","token_type":"Bearer","expires_in":3600}`))
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing fetched OAuth2 token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"oauth2":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             clientID,
		ClientSecret:         clientSecret,
		Scope:                "read write",
		CredentialsPlacement: "basic_auth_header",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 client credentials request failed: %#v", item.Response)
	}
}

func TestTimelineCapturesOAuth2TokenRequest(t *testing.T) {
	const accessToken = "timeline-access-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "client-id" {
				t.Fatalf("unexpected OAuth2 token form: %s", r.Form.Encode())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","expires_in":3600}`))
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing OAuth2 bearer token: %q", got)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"timeline":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		CredentialsID:        "timeline-test",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusAccepted {
		t.Fatalf("OAuth2 timeline request failed: %#v", item.Response)
	}
	if len(item.Timeline) < 2 {
		t.Fatalf("expected OAuth2 token + main timeline rows, got %#v", item.Timeline)
	}
	var oauthRow, mainRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		switch row.Kind {
		case "oauth2":
			oauthRow = row
		case "request":
			mainRow = row
		}
	}
	if oauthRow == nil {
		t.Fatalf("missing OAuth2 timeline row: %#v", item.Timeline)
	}
	if oauthRow.ID == "" || oauthRow.RequestID != item.ID || oauthRow.Source != "oauth2.0" {
		t.Fatalf("bad OAuth2 timeline identity: %#v", oauthRow)
	}
	if oauthRow.Method != http.MethodPost || oauthRow.URL != server.URL+"/token" || oauthRow.Status != http.StatusOK || oauthRow.StatusText != "OK" {
		t.Fatalf("bad OAuth2 timeline request metadata: %#v", oauthRow)
	}
	if oauthRow.SourceFile == "" || !strings.Contains(oauthRow.Message, "-> 200") || oauthRow.At.IsZero() {
		t.Fatalf("incomplete OAuth2 timeline row: %#v", oauthRow)
	}
	if mainRow == nil {
		t.Fatalf("missing main timeline row: %#v", item.Timeline)
	}
	if mainRow.RequestID != item.ID || mainRow.Source != "main" || mainRow.Method != http.MethodPost || mainRow.URL != targetURL || mainRow.Status != http.StatusAccepted {
		t.Fatalf("bad main timeline row: %#v", mainRow)
	}
}

func TestOAuth2AuthorizationCodeFetchesTokenWithLoopbackCallback(t *testing.T) {
	const accessToken = "auth-code-token"
	var redirectValue atomic.Value
	var challengeValue atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "code" {
				t.Fatalf("bad OAuth2 authorization response_type: %q", got)
			}
			if got := query.Get("client_id"); got != "client-id" {
				t.Fatalf("bad OAuth2 authorization client_id: %q", got)
			}
			redirectURI := query.Get("redirect_uri")
			if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") || !strings.Contains(redirectURI, "/oauth/callback") {
				t.Fatalf("bad OAuth2 redirect_uri: %q", redirectURI)
			}
			if got := query.Get("scope"); got != "read write" {
				t.Fatalf("bad OAuth2 scope: %q", got)
			}
			if got := query.Get("state"); got != "state-123" {
				t.Fatalf("bad OAuth2 state: %q", got)
			}
			if got := query.Get("prompt"); got != "consent" {
				t.Fatalf("missing OAuth2 authorization additional param: %q", got)
			}
			if got := query.Get("code_challenge_method"); got != "S256" {
				t.Fatalf("bad OAuth2 code_challenge_method: %q", got)
			}
			challenge := query.Get("code_challenge")
			if challenge == "" {
				t.Fatalf("missing OAuth2 code_challenge: raw=%s", r.URL.RawQuery)
			}
			redirectValue.Store(redirectURI)
			challengeValue.Store(challenge)
			http.Redirect(w, r, redirectURI+"?code=auth-code&state=state-123", http.StatusFound)
		case "/token":
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Fatalf("body credential placement should not set Authorization: %q", auth)
			}
			if got := r.URL.Query().Get("token_query"); got != "query-extra" {
				t.Fatalf("missing OAuth2 token query param: %q raw=%s", got, r.URL.RawQuery)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"grant_type":    "authorization_code",
				"code":          "auth-code",
				"redirect_uri":  redirectValue.Load().(string),
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"token_body":    "token-extra",
			}
			for key, value := range expected {
				if got := r.Form.Get(key); got != value {
					t.Fatalf("bad OAuth2 authorization-code form %s: got %q form=%s", key, got, r.Form.Encode())
				}
			}
			verifier := r.Form.Get("code_verifier")
			if verifier == "" {
				t.Fatalf("missing OAuth2 PKCE code_verifier: %s", r.Form.Encode())
			}
			if got := oauth2CodeChallenge(verifier); got != challengeValue.Load().(string) {
				t.Fatalf("PKCE verifier did not match challenge: got %q want %q", got, challengeValue.Load())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","refresh_token":"refresh-code-token","expires_in":3600}`))
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing OAuth2 authorization-code bearer token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"authorizationCode":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	app.oauth2CallbackTimeout = 5 * time.Second
	app.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		go func() {
			resp, err := http.Get(authorizeURL)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "authorization_code",
		CallbackURL:          "http://127.0.0.1:0/oauth/callback",
		AuthorizationURL:     server.URL + "/authorize",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "client-id",
		ClientSecret:         "client-secret",
		Scope:                "read write",
		State:                "state-123",
		PKCE:                 true,
		CredentialsPlacement: "body",
		CredentialsID:        "auth-code-test",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
		AuthorizationAdditionalParams: []OAuth2AdditionalParam{
			{Name: "prompt", Value: "consent", SendIn: "queryparams", Enabled: true},
			{Name: "ignored_header", Value: "ignored", SendIn: "headers", Enabled: true},
		},
		TokenAdditionalParams: []OAuth2AdditionalParam{
			{Name: "token_query", Value: "query-extra", SendIn: "queryparams", Enabled: true},
			{Name: "token_body", Value: "token-extra", SendIn: "body", Enabled: true},
		},
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 authorization-code request failed: %#v", item.Response)
	}
	if len(item.Timeline) < 3 {
		t.Fatalf("expected callback + token + main timeline rows, got %#v", item.Timeline)
	}
	var callbackRow, tokenRow, mainRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		if row.Kind == "oauth2" && row.Method == http.MethodGet && strings.Contains(row.URL, "/oauth/callback") {
			callbackRow = row
		}
		if row.Kind == "oauth2" && row.Method == http.MethodPost && strings.Contains(row.URL, "/token") {
			tokenRow = row
		}
		if row.Kind == "request" {
			mainRow = row
		}
	}
	if callbackRow == nil || callbackRow.Source != "oauth2.0" || callbackRow.Status != http.StatusOK || !strings.Contains(callbackRow.Message, "-> 200") {
		t.Fatalf("bad OAuth2 callback timeline row: %#v", callbackRow)
	}
	if tokenRow == nil || tokenRow.Source != "oauth2.0" || tokenRow.Status != http.StatusOK || !strings.Contains(tokenRow.Message, "-> 200") {
		t.Fatalf("bad OAuth2 token timeline row: %#v", tokenRow)
	}
	if mainRow == nil || mainRow.Source != "main" || mainRow.Status != http.StatusOK {
		t.Fatalf("bad main timeline row: %#v", mainRow)
	}
}

func TestOAuth2AuthorizationBrowserPreferenceSelectsInAppOrSystemOpener(t *testing.T) {
	var authorizeCalls int32
	var tokenCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			atomic.AddInt32(&authorizeCalls, 1)
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "code" {
				t.Fatalf("bad OAuth2 browser response_type: %q", got)
			}
			redirectURI := query.Get("redirect_uri")
			if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") || !strings.Contains(redirectURI, "/browser/callback") {
				t.Fatalf("bad OAuth2 browser redirect_uri: %q", redirectURI)
			}
			http.Redirect(w, r, redirectURI+"?code=browser-code", http.StatusFound)
		case "/token":
			call := atomic.AddInt32(&tokenCalls, 1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("grant_type"); got != "authorization_code" {
				t.Fatalf("bad OAuth2 browser grant_type: %q", got)
			}
			if got := r.Form.Get("code"); got != "browser-code" {
				t.Fatalf("bad OAuth2 browser code: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"browser-token-%d","expires_in":3600}`, call)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := OAuth2Auth{
		GrantType:            "authorization_code",
		CallbackURL:          "http://127.0.0.1:0/browser/callback",
		AuthorizationURL:     server.URL + "/authorize",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "browser-client",
		CredentialsPlacement: "body",
		CredentialsID:        "browser-pref",
	}

	inApp := newAppForTest(t)
	inApp.ctx = context.Background()
	inApp.oauth2CallbackTimeout = 5 * time.Second
	var inAppCalls int32
	var unexpectedSystemCalls int32
	inApp.oauth2OpenInAppURL = func(ctx context.Context, request oauth2AuthorizationBrowserRequest) error {
		atomic.AddInt32(&inAppCalls, 1)
		if request.GrantType != "authorization_code" {
			t.Fatalf("bad in-app OAuth2 grant type: %#v", request)
		}
		authorizeURL, err := url.Parse(request.AuthorizeURL)
		if err != nil {
			t.Fatal(err)
		}
		if got := authorizeURL.Query().Get("redirect_uri"); got != request.CallbackURL {
			t.Fatalf("in-app opener callback mismatch: auth url %q request %#v", got, request)
		}
		go func() {
			resp, err := http.Get(request.AuthorizeURL)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	inApp.oauth2OpenURL = func(context.Context, string) error {
		atomic.AddInt32(&unexpectedSystemCalls, 1)
		return errors.New("system opener should not run for default OAuth2 preference")
	}
	response, _, err := inApp.requestOAuth2AuthorizationCodeTokenWithTimeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if response.AccessToken != "browser-token-1" || atomic.LoadInt32(&inAppCalls) != 1 || atomic.LoadInt32(&unexpectedSystemCalls) != 0 {
		t.Fatalf("in-app OAuth2 opener was not selected: response=%#v inApp=%d system=%d", response, inAppCalls, unexpectedSystemCalls)
	}

	system := newAppForTest(t)
	system.ctx = context.Background()
	system.oauth2CallbackTimeout = 5 * time.Second
	state, err := system.GetState()
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.Preferences
	preferences.OAuth2UseSystemBrowser = true
	if _, err := system.UpdatePreferences(preferences); err != nil {
		t.Fatal(err)
	}
	var systemCalls int32
	var unexpectedInAppCalls int32
	system.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		atomic.AddInt32(&systemCalls, 1)
		go func() {
			resp, err := http.Get(authorizeURL)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	system.oauth2OpenInAppURL = func(context.Context, oauth2AuthorizationBrowserRequest) error {
		atomic.AddInt32(&unexpectedInAppCalls, 1)
		return errors.New("in-app opener should not run when OAuth2 system browser preference is enabled")
	}
	response, _, err = system.requestOAuth2AuthorizationCodeTokenWithTimeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if response.AccessToken != "browser-token-2" || atomic.LoadInt32(&systemCalls) != 1 || atomic.LoadInt32(&unexpectedInAppCalls) != 0 {
		t.Fatalf("system OAuth2 opener was not selected: response=%#v system=%d inApp=%d", response, systemCalls, unexpectedInAppCalls)
	}
	if atomic.LoadInt32(&authorizeCalls) != 2 || atomic.LoadInt32(&tokenCalls) != 2 {
		t.Fatalf("unexpected OAuth2 browser flow counts: authorize=%d token=%d", authorizeCalls, tokenCalls)
	}
}

func TestOAuth2AuthorizationCodeSupportsHostedCallbackBridge(t *testing.T) {
	const (
		hostedCallback = "https://oauth.usebruno.com/callback"
		accessToken    = "hosted-auth-code-token"
	)
	var redirectValue atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "code" {
				t.Fatalf("bad hosted OAuth2 authorization response_type: %q", got)
			}
			if got := query.Get("redirect_uri"); got != hostedCallback {
				t.Fatalf("bad hosted OAuth2 redirect_uri: %q", got)
			}
			redirectValue.Store(query.Get("redirect_uri"))
			http.Redirect(w, r, hostedCallback+"?code=hosted-code&state=hosted-state", http.StatusFound)
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"grant_type":   "authorization_code",
				"code":         "hosted-code",
				"redirect_uri": hostedCallback,
				"client_id":    "hosted-client",
			}
			for key, value := range expected {
				if got := r.Form.Get(key); got != value {
					t.Fatalf("bad hosted OAuth2 token form %s: got %q form=%s", key, got, r.Form.Encode())
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","expires_in":3600}`))
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing hosted OAuth2 bearer token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"hosted":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	app.oauth2CallbackTimeout = 5 * time.Second
	app.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		go func() {
			client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			resp, err := client.Get(authorizeURL)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			if location := resp.Header.Get("Location"); location != "" {
				_, _ = app.CompleteOAuth2Callback(location)
			}
		}()
		return nil
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "authorization_code",
		CallbackURL:          hostedCallback,
		AuthorizationURL:     server.URL + "/authorize",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "hosted-client",
		State:                "hosted-state",
		CredentialsPlacement: "body",
		CredentialsID:        "hosted-code",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("hosted OAuth2 authorization-code request failed: %#v", item.Response)
	}
	if redirectValue.Load() != hostedCallback {
		t.Fatalf("authorization endpoint did not receive hosted callback: %v", redirectValue.Load())
	}
	var callbackRow, tokenRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		if row.Kind == "oauth2" && row.Method == http.MethodGet && strings.HasPrefix(row.URL, hostedCallback) {
			callbackRow = row
		}
		if row.Kind == "oauth2" && row.Method == http.MethodPost && strings.Contains(row.URL, "/token") {
			tokenRow = row
		}
	}
	if callbackRow == nil || callbackRow.Status != http.StatusOK || !strings.Contains(callbackRow.Message, "-> 200") {
		t.Fatalf("bad hosted OAuth2 callback timeline row: %#v", callbackRow)
	}
	if tokenRow == nil || tokenRow.Status != http.StatusOK || !strings.Contains(tokenRow.Message, "-> 200") {
		t.Fatalf("bad hosted OAuth2 token timeline row: %#v", tokenRow)
	}
}

func TestOAuth2AuthorizationCodeUsesHostedDefaultCallbackAndProtocolHandoff(t *testing.T) {
	const accessToken = "default-hosted-auth-code-token"
	var redirectValue atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "code" {
				t.Fatalf("bad default OAuth2 authorization response_type: %q", got)
			}
			if got := query.Get("redirect_uri"); got != brunoOAuth2DefaultCallbackURL {
				t.Fatalf("bad default OAuth2 redirect_uri: %q", got)
			}
			redirectValue.Store(query.Get("redirect_uri"))
			http.Redirect(w, r, "bruno://app/oauth2/callback?code=default-code&state=default-state", http.StatusFound)
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"grant_type":   "authorization_code",
				"code":         "default-code",
				"redirect_uri": brunoOAuth2DefaultCallbackURL,
				"client_id":    "default-client",
			}
			for key, value := range expected {
				if got := r.Form.Get(key); got != value {
					t.Fatalf("bad default OAuth2 token form %s: got %q form=%s", key, got, r.Form.Encode())
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","expires_in":3600}`))
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing default OAuth2 bearer token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"defaultHosted":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	app.oauth2CallbackTimeout = 5 * time.Second
	app.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get(authorizeURL)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		location := resp.Header.Get("Location")
		if location == "" {
			return errors.New("missing OAuth2 authorization redirect")
		}
		app.handleSecondInstanceArgs([]string{"/Applications/LiteAPI.app/Contents/MacOS/LiteAPI", location})
		return nil
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "authorization_code",
		AuthorizationURL:     server.URL + "/authorize",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "default-client",
		State:                "default-state",
		CredentialsPlacement: "body",
		CredentialsID:        "default-hosted-code",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("default hosted OAuth2 authorization-code request failed: %#v", item.Response)
	}
	if redirectValue.Load() != brunoOAuth2DefaultCallbackURL {
		t.Fatalf("authorization endpoint did not receive Bruno default callback: %v", redirectValue.Load())
	}
	var callbackRow, tokenRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		if row.Kind == "oauth2" && row.Method == http.MethodGet && strings.HasPrefix(row.URL, "bruno://app/oauth2/callback") {
			callbackRow = row
		}
		if row.Kind == "oauth2" && row.Method == http.MethodPost && strings.Contains(row.URL, "/token") {
			tokenRow = row
		}
	}
	if callbackRow == nil || callbackRow.Status != http.StatusOK || !strings.Contains(callbackRow.Message, "-> 200") || !strings.Contains(callbackRow.URL, "code=default-code") {
		t.Fatalf("bad default hosted OAuth2 protocol callback timeline row: %#v", callbackRow)
	}
	if tokenRow == nil || tokenRow.Status != http.StatusOK || !strings.Contains(tokenRow.Message, "-> 200") {
		t.Fatalf("bad default hosted OAuth2 token timeline row: %#v", tokenRow)
	}
}

func TestOAuth2ImplicitFetchesTokenWithLoopbackFragmentCallback(t *testing.T) {
	const accessToken = "implicit-access-token"
	var redirectValue atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "token" {
				t.Fatalf("bad OAuth2 implicit response_type: %q", got)
			}
			if got := query.Get("client_id"); got != "implicit-client" {
				t.Fatalf("bad OAuth2 implicit client_id: %q", got)
			}
			redirectURI := query.Get("redirect_uri")
			if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") || !strings.Contains(redirectURI, "/implicit/callback") {
				t.Fatalf("bad OAuth2 implicit redirect_uri: %q", redirectURI)
			}
			if got := query.Get("scope"); got != "openid profile" {
				t.Fatalf("bad OAuth2 implicit scope: %q", got)
			}
			if got := query.Get("state"); got != "implicit-state" {
				t.Fatalf("bad OAuth2 implicit state: %q", got)
			}
			if got := query.Get("prompt"); got != "none" {
				t.Fatalf("missing OAuth2 implicit authorization additional param: %q", got)
			}
			if got := query.Get("code_challenge"); got != "" {
				t.Fatalf("implicit flow should not send PKCE challenge: %q", got)
			}
			redirectValue.Store(redirectURI)
			fragment := url.Values{}
			fragment.Set("access_token", accessToken)
			fragment.Set("token_type", "Bearer")
			fragment.Set("expires_in", "3600")
			fragment.Set("state", "implicit-state")
			fragment.Set("scope", "openid profile")
			http.Redirect(w, r, redirectURI+"#"+fragment.Encode(), http.StatusFound)
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing OAuth2 implicit bearer token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"implicit":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	app.oauth2CallbackTimeout = 5 * time.Second
	app.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		if ctx == nil {
			ctx = context.Background()
		}
		go func() {
			client := http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			location := resp.Header.Get("Location")
			if location == "" {
				return
			}
			redirectURL, err := url.Parse(location)
			if err != nil {
				return
			}
			fragment := redirectURL.Fragment
			redirectURL.Fragment = ""
			landingResp, err := http.Get(redirectURL.String())
			if err == nil {
				_ = landingResp.Body.Close()
			}
			fragmentCallbackURL := oauth2ImplicitFragmentCallbackURL(redirectURL.String())
			fragmentResp, err := http.Post(fragmentCallbackURL, "application/x-www-form-urlencoded", strings.NewReader(fragment))
			if err == nil {
				_ = fragmentResp.Body.Close()
			}
		}()
		return nil
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:         "implicit",
		CallbackURL:       "http://127.0.0.1:0/implicit/callback",
		AuthorizationURL:  server.URL + "/authorize",
		ClientID:          "implicit-client",
		Scope:             "openid profile",
		State:             "implicit-state",
		CredentialsID:     "implicit-test",
		TokenPlacement:    "header",
		TokenHeaderPrefix: "Bearer",
		AuthorizationAdditionalParams: []OAuth2AdditionalParam{
			{Name: "prompt", Value: "none", SendIn: "queryparams", Enabled: true},
			{Name: "ignored_header", Value: "ignored", SendIn: "headers", Enabled: true},
		},
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 implicit request failed: %#v", item.Response)
	}
	if redirectValue.Load() == nil {
		t.Fatal("authorization endpoint was not opened")
	}
	if len(item.Timeline) < 3 {
		t.Fatalf("expected callback + fragment + main timeline rows, got %#v", item.Timeline)
	}
	var landingRow, fragmentRow, mainRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		if row.Kind == "oauth2" && row.Method == http.MethodGet && strings.Contains(row.URL, "/implicit/callback") {
			landingRow = row
		}
		if row.Kind == "oauth2" && row.Method == http.MethodPost && strings.Contains(row.URL, "__liteapi_oauth2_fragment") {
			fragmentRow = row
		}
		if row.Kind == "request" {
			mainRow = row
		}
	}
	if landingRow == nil || landingRow.Source != "oauth2.0" || landingRow.Status != http.StatusOK || !strings.Contains(landingRow.Message, "-> 200") {
		t.Fatalf("bad OAuth2 implicit landing timeline row: %#v", landingRow)
	}
	if strings.Contains(landingRow.URL, accessToken) {
		t.Fatalf("implicit landing row leaked access token: %#v", landingRow)
	}
	if fragmentRow == nil || fragmentRow.Source != "oauth2.0" || fragmentRow.Status != http.StatusOK || !strings.Contains(fragmentRow.Message, "-> 200") {
		t.Fatalf("bad OAuth2 implicit fragment timeline row: %#v", fragmentRow)
	}
	if strings.Contains(fragmentRow.URL, accessToken) {
		t.Fatalf("implicit fragment row leaked access token: %#v", fragmentRow)
	}
	if mainRow == nil || mainRow.Source != "main" || mainRow.Status != http.StatusOK {
		t.Fatalf("bad main timeline row: %#v", mainRow)
	}
}

func TestOAuth2ImplicitSupportsHostedCallbackBridge(t *testing.T) {
	const (
		hostedCallback = "https://oauth.usebruno.com/callback"
		accessToken    = "hosted-implicit-token"
	)
	var redirectValue atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "token" {
				t.Fatalf("bad hosted implicit response_type: %q", got)
			}
			if got := query.Get("redirect_uri"); got != hostedCallback {
				t.Fatalf("bad hosted implicit redirect_uri: %q", got)
			}
			redirectValue.Store(query.Get("redirect_uri"))
			fragment := url.Values{}
			fragment.Set("access_token", accessToken)
			fragment.Set("token_type", "Bearer")
			fragment.Set("expires_in", "3600")
			fragment.Set("state", "hosted-implicit-state")
			http.Redirect(w, r, hostedCallback+"#"+fragment.Encode(), http.StatusFound)
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing hosted implicit bearer token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"hostedImplicit":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	app.oauth2CallbackTimeout = 5 * time.Second
	app.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		go func() {
			client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			resp, err := client.Get(authorizeURL)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			if location := resp.Header.Get("Location"); location != "" {
				_, _ = app.CompleteOAuth2Callback(location)
			}
		}()
		return nil
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:         "implicit",
		CallbackURL:       hostedCallback,
		AuthorizationURL:  server.URL + "/authorize",
		ClientID:          "hosted-implicit-client",
		State:             "hosted-implicit-state",
		CredentialsID:     "hosted-implicit",
		TokenPlacement:    "header",
		TokenHeaderPrefix: "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("hosted OAuth2 implicit request failed: %#v", item.Response)
	}
	if redirectValue.Load() != hostedCallback {
		t.Fatalf("authorization endpoint did not receive hosted implicit callback: %v", redirectValue.Load())
	}
	var callbackRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		if row.Kind == "oauth2" && row.Method == http.MethodGet && strings.HasPrefix(row.URL, hostedCallback) {
			callbackRow = row
		}
	}
	if callbackRow == nil || callbackRow.Status != http.StatusOK || !strings.Contains(callbackRow.Message, "-> 200") {
		t.Fatalf("bad hosted implicit callback timeline row: %#v", callbackRow)
	}
	if strings.Contains(callbackRow.URL, accessToken) {
		t.Fatalf("hosted implicit callback row leaked access token: %#v", callbackRow)
	}
}

func TestOAuth2ImplicitUsesHostedDefaultCallbackAndProtocolHandoff(t *testing.T) {
	const accessToken = "default-hosted-implicit-token"
	var redirectValue atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			query := r.URL.Query()
			if got := query.Get("response_type"); got != "token" {
				t.Fatalf("bad default implicit response_type: %q", got)
			}
			if got := query.Get("redirect_uri"); got != brunoOAuth2DefaultCallbackURL {
				t.Fatalf("bad default implicit redirect_uri: %q", got)
			}
			redirectValue.Store(query.Get("redirect_uri"))
			fragment := url.Values{}
			fragment.Set("access_token", accessToken)
			fragment.Set("token_type", "Bearer")
			fragment.Set("expires_in", "3600")
			fragment.Set("state", "default-implicit-state")
			http.Redirect(w, r, "liteapi://app/oauth2/callback#"+fragment.Encode(), http.StatusFound)
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing default implicit bearer token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"defaultImplicit":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	app.oauth2CallbackTimeout = 5 * time.Second
	app.oauth2OpenURL = func(ctx context.Context, authorizeURL string) error {
		client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get(authorizeURL)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		location := resp.Header.Get("Location")
		if location == "" {
			return errors.New("missing OAuth2 implicit redirect")
		}
		app.handleOpenURL(location)
		return nil
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:         "implicit",
		AuthorizationURL:  server.URL + "/authorize",
		ClientID:          "default-implicit-client",
		State:             "default-implicit-state",
		CredentialsID:     "default-hosted-implicit",
		TokenPlacement:    "header",
		TokenHeaderPrefix: "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("default hosted OAuth2 implicit request failed: %#v", item.Response)
	}
	if redirectValue.Load() != brunoOAuth2DefaultCallbackURL {
		t.Fatalf("authorization endpoint did not receive Bruno default implicit callback: %v", redirectValue.Load())
	}
	var callbackRow *TimelineItem
	for i := range item.Timeline {
		row := &item.Timeline[i]
		if row.Kind == "oauth2" && row.Method == http.MethodGet && strings.HasPrefix(row.URL, "liteapi://app/oauth2/callback") {
			callbackRow = row
		}
	}
	if callbackRow == nil || callbackRow.Status != http.StatusOK || !strings.Contains(callbackRow.Message, "-> 200") {
		t.Fatalf("bad default hosted implicit protocol callback timeline row: %#v", callbackRow)
	}
	if strings.Contains(callbackRow.URL, accessToken) {
		t.Fatalf("default hosted implicit protocol callback row leaked access token: %#v", callbackRow)
	}
}

func TestOAuth2PasswordGrantFetchesIDTokenIntoURL(t *testing.T) {
	const idToken = "id-token-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Fatalf("body credential placement should not set Authorization: %q", auth)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"grant_type":    "password",
				"username":      "alice",
				"password":      "secret",
				"client_id":     "client-id",
				"client_secret": "client-secret",
			}
			for key, value := range expected {
				if got := r.Form.Get(key); got != value {
					t.Fatalf("bad OAuth2 password form %s: got %q form=%s", key, got, r.Form.Encode())
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"unused","id_token":"` + idToken + `"}`))
		case "/resource":
			if got := r.URL.Query().Get("custom_token"); got != idToken {
				t.Fatalf("missing OAuth2 URL token: %q raw=%s", got, r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"oauth2":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "password",
		AccessTokenURL:       server.URL + "/token",
		Username:             "alice",
		Password:             "secret",
		ClientID:             "client-id",
		ClientSecret:         "client-secret",
		CredentialsPlacement: "body",
		TokenSource:          "id_token",
		TokenPlacement:       "url",
		TokenQueryKey:        "custom_token",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 password request failed: %#v", item.Response)
	}
}

func TestOAuth2TokenCacheReusesValidToken(t *testing.T) {
	var tokenCalls, resourceCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"cached-token","expires_in":3600}`))
		case "/resource":
			resourceCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer cached-token" {
				t.Fatalf("missing cached OAuth2 token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		CredentialsID:        "cache-test",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
		if err != nil {
			t.Fatal(err)
		}
		item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
		if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
			t.Fatalf("OAuth2 cached request %d failed: %#v", i+1, item.Response)
		}
	}
	if tokenCalls != 1 || resourceCalls != 2 {
		t.Fatalf("unexpected OAuth2 cache counts: token=%d resource=%d", tokenCalls, resourceCalls)
	}
}

func TestOAuth2CredentialStoreEncryptsAndHydrates(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "test-oauth2-credential-store-key")
	const (
		accessToken  = "persistent-oauth2-token"
		refreshToken = "persistent-refresh-token"
	)
	var tokenCalls, resourceCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "client-id" {
				t.Fatalf("unexpected OAuth2 persistence form: %s", r.Form.Encode())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","refresh_token":"` + refreshToken + `","expires_in":3600,"token_type":"Bearer"}`))
		case "/resource":
			resourceCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing persistent OAuth2 token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"persistent":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		CredentialsID:        "persisted",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 persistence request failed: %#v", item.Response)
	}
	flushPersistForTest(t, app)
	storeData, err := os.ReadFile(filepath.Join(dir, "oauth2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storeData), accessToken) || strings.Contains(string(storeData), refreshToken) || !strings.Contains(string(storeData), "$01:") {
		t.Fatalf("oauth2.json did not encrypt OAuth2 credentials: %s", storeData)
	}

	flushPersistForTest(t, app)
	reloaded := newAppInDirForTest(t, dir)
	vars := reloaded.oauth2CredentialVariablesSnapshot()
	if vars["$oauth2.persisted.access_token"] != accessToken || vars["$oauth2.persisted.refresh_token"] != refreshToken {
		t.Fatalf("OAuth2 credential variables were not hydrated: %#v", vars)
	}
	reloadedState, err := reloaded.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok = findItemInState(reloadedState, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 hydrated request failed: %#v", item.Response)
	}
	if tokenCalls != 1 {
		t.Fatalf("expected hydrated OAuth2 token cache to skip token request, got %d token calls", tokenCalls)
	}
	if resourceCalls != 2 {
		t.Fatalf("expected two resource requests, got %d", resourceCalls)
	}
}

func TestJavaScriptRuntimeSupportsOAuth2CredentialVars(t *testing.T) {
	var tokenCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "client-id" {
				t.Fatalf("unexpected OAuth2 form: %s", r.Form.Encode())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","id_token":"id-%d","refresh_token":"refresh-%d","expires_in":3600,"token_type":"Bearer","scope":"{{scopeName}}"}`, tokenCalls, tokenCalls, tokenCalls)
		case "/resource":
			auth := r.Header.Get("Authorization")
			if want := fmt.Sprintf("Bearer access-%d", tokenCalls); auth != want {
				t.Fatalf("missing OAuth2 bearer token: got %q want %q", auth, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"authorization":%s}`, importers.JSStringLiteral(auth))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	state, err = app.UpdateCollectionVariables(collection.ID, []Variable{
		{ID: "scopeName", Name: "scopeName", Value: "read write", DataType: "string", Enabled: true},
		{ID: "accessToken", Name: "access_token", Value: "collection-token", DataType: "string", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[0]
	item := collection.Items[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
	}}
	preScript := `if (bru.getOauth2CredentialVar("$oauth2.credentials.access_token") !== undefined) {
  throw new Error("reset OAuth2 credentials leaked into pre-request");
}
if (bru.getOauth2CredentialVar("access_token") !== undefined) {
  throw new Error("plain collection variables leaked into OAuth2 credentials");
}`
	tests := `test("oauth2 credential vars", function () {
  const access = res.json.authorization.replace("Bearer ", "");
  const suffix = access.split("-")[1];
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.access_token")).to.equal(access);
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.id_token")).to.equal("id-" + suffix);
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.refresh_token")).to.equal("refresh-" + suffix);
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.expires_in")).to.equal(3600);
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.token_type")).to.equal("Bearer");
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.scope")).to.equal("read write");
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.created_at")).to.be.above(0);
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.expires_at")).to.be.above(bru.getOauth2CredentialVar("$oauth2.credentials.created_at"));
  expect(bru.getOauth2CredentialVar("access_token")).to.be.undefined;
  bru.resetOauth2Credential("credentials");
  expect(bru.getOauth2CredentialVar("$oauth2.credentials.access_token")).to.be.undefined;
  expect(function () { bru.resetOauth2Credential(""); }).to.throw("credentialId");
});`
	if _, err := app.UpdateRequest(collection.ID, item.ID, RequestPatch{
		URL:       &targetURL,
		Auth:      &auth,
		PreScript: &preScript,
		Tests:     &tests,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		state, err = app.SendRequest(collection.ID, item.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		item, ok := findItemInState(state, collection.ID, item.ID)
		if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
			t.Fatalf("OAuth2 credential-var request %d failed: %#v", i+1, item.Response)
		}
		if len(item.Response.TestResults) != 1 || !item.Response.TestResults[0].Passed {
			t.Fatalf("OAuth2 credential-var test %d did not pass: %#v", i+1, item.Response.TestResults)
		}
	}
	if tokenCalls != 2 {
		t.Fatalf("resetOauth2Credential did not clear cached OAuth2 token: token calls=%d", tokenCalls)
	}
}

func TestOAuth2TokenCacheRefreshesExpiredToken(t *testing.T) {
	var resourceCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"expired-token","refresh_token":"refresh-token","expires_in":0}`))
		case "/refresh":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-token" {
				t.Fatalf("unexpected refresh form: %s", r.Form.Encode())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		case "/resource":
			resourceCalls++
			want := "Bearer expired-token"
			if resourceCalls == 2 {
				want = "Bearer refreshed-token"
			}
			if got := r.Header.Get("Authorization"); got != want {
				t.Fatalf("bad OAuth2 token on resource call %d: got %q want %q", resourceCalls, got, want)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		RefreshTokenURL:      server.URL + "/refresh",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		CredentialsID:        "refresh-test",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
		AutoRefreshToken:     true,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
		if err != nil {
			t.Fatal(err)
		}
		item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
		if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
			t.Fatalf("OAuth2 refresh request %d failed: %#v", i+1, item.Response)
		}
	}
}

func TestOAuth2AdditionalParamsApplyToTokenRequest(t *testing.T) {
	const accessToken = "token-with-extras"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if got := r.Header.Get("X-Token-Header"); got != "header-value" {
				t.Fatalf("missing OAuth2 token header param: %q", got)
			}
			if got := r.Header.Get("X-Disabled"); got != "" {
				t.Fatalf("disabled OAuth2 token header was sent: %q", got)
			}
			if got := r.URL.Query().Get("token_query"); got != "query-value" {
				t.Fatalf("missing OAuth2 token query param: %q raw=%s", got, r.URL.RawQuery)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"grant_type":  "client_credentials",
				"client_id":   "client-id",
				"token_body":  "body-value",
				"legacy_body": "legacy-value",
			}
			for key, value := range expected {
				if got := r.Form.Get(key); got != value {
					t.Fatalf("bad OAuth2 token form %s: got %q form=%s", key, got, r.Form.Encode())
				}
			}
			if got := r.Form.Get("disabled_body"); got != "" {
				t.Fatalf("disabled OAuth2 token body was sent: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","expires_in":3600}`))
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("missing fetched OAuth2 token: %q", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
		TokenAdditionalParams: []OAuth2AdditionalParam{
			{Name: "X-Token-Header", Value: "header-value", SendIn: "headers", Enabled: true},
			{Name: "X-Disabled", Value: "disabled", SendIn: "headers", Enabled: false},
			{Name: "token_query", Value: "query-value", SendIn: "queryparams", Enabled: true},
			{Name: "token_body", Value: "body-value", SendIn: "body", Enabled: true},
			{Name: "disabled_body", Value: "disabled", SendIn: "body", Enabled: false},
		},
		AdditionalParams: []KeyValue{{Name: "legacy_body", Value: "legacy-value", Enabled: true}},
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth2 additional-parameter request failed: %#v", item.Response)
	}
}

func TestOAuth2AdditionalParamsApplyToRefreshRequest(t *testing.T) {
	var resourceCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"expired-token","refresh_token":"refresh-token","expires_in":0}`))
		case "/refresh":
			if got := r.Header.Get("X-Refresh-Header"); got != "refresh-header" {
				t.Fatalf("missing OAuth2 refresh header param: %q", got)
			}
			if got := r.URL.Query().Get("refresh_query"); got != "refresh-query" {
				t.Fatalf("missing OAuth2 refresh query param: %q raw=%s", got, r.URL.RawQuery)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"grant_type":    "refresh_token",
				"refresh_token": "refresh-token",
				"client_id":     "client-id",
				"refresh_body":  "refresh-body",
			}
			for key, value := range expected {
				if got := r.Form.Get(key); got != value {
					t.Fatalf("bad OAuth2 refresh form %s: got %q form=%s", key, got, r.Form.Encode())
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		case "/resource":
			resourceCalls++
			want := "Bearer expired-token"
			if resourceCalls == 2 {
				want = "Bearer refreshed-token"
			}
			if got := r.Header.Get("Authorization"); got != want {
				t.Fatalf("bad OAuth2 resource token on call %d: got %q want %q", resourceCalls, got, want)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	targetURL := server.URL + "/resource"
	auth := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "client_credentials",
		AccessTokenURL:       server.URL + "/token",
		RefreshTokenURL:      server.URL + "/refresh",
		ClientID:             "client-id",
		CredentialsPlacement: "body",
		CredentialsID:        "refresh-extra-test",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
		AutoRefreshToken:     true,
		RefreshAdditionalParams: []OAuth2AdditionalParam{
			{Name: "X-Refresh-Header", Value: "refresh-header", SendIn: "headers", Enabled: true},
			{Name: "refresh_query", Value: "refresh-query", SendIn: "queryparams", Enabled: true},
			{Name: "refresh_body", Value: "refresh-body", SendIn: "body", Enabled: true},
		},
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
		if err != nil {
			t.Fatal(err)
		}
		item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
		if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
			t.Fatalf("OAuth2 refresh extra request %d failed: %#v", i+1, item.Response)
		}
	}
}

func TestOAuth2AuthBruRoundTrip(t *testing.T) {
	item := types.NewRequestItem("OAuth2", "http", 1)
	item.Auth = AuthConfig{Mode: "oauth2", Token: "static-token", OAuth2: OAuth2Auth{
		GrantType:            "password",
		AccessTokenURL:       "https://auth.example.test/token",
		RefreshTokenURL:      "https://auth.example.test/refresh",
		Username:             "alice",
		Password:             "secret",
		ClientID:             "client-id",
		ClientSecret:         "client-secret",
		Scope:                "read",
		CredentialsPlacement: "body",
		CredentialsID:        "creds",
		TokenSource:          "id_token",
		TokenPlacement:       "url",
		TokenHeaderPrefix:    "Bearer",
		TokenQueryKey:        "custom_token",
		AutoFetchToken:       true,
		AutoRefreshToken:     true,
		AuthorizationAdditionalParams: []OAuth2AdditionalParam{
			{Name: "prompt", Value: "consent", SendIn: "queryparams", Enabled: true},
		},
		TokenAdditionalParams: []OAuth2AdditionalParam{
			{Name: "X-Token-Header", Value: "token-header", SendIn: "headers", Enabled: true},
			{Name: "token_query", Value: "token-query", SendIn: "queryparams", Enabled: true},
			{Name: "token_body", Value: "token-body", SendIn: "body", Enabled: true},
			{Name: "disabled_body", Value: "disabled", SendIn: "body", Enabled: false},
		},
		RefreshAdditionalParams: []OAuth2AdditionalParam{
			{Name: "refresh_body", Value: "refresh-body", SendIn: "body", Enabled: true},
		},
	}}
	content := stringifyBru(item)
	for _, expected := range []string{"auth:oauth2", "grant_type: password", "access_token_url: https://auth.example.test/token", "client_id: client-id", "credentials_placement: body", "token_source: id_token", "token_placement: url", "token_query_key: custom_token", "access_token: static-token", "auth:oauth2:additional_params:access_token_req:headers", "X-Token-Header: token-header", "auth:oauth2:additional_params:access_token_req:queryparams", "token_query: token-query", "auth:oauth2:additional_params:access_token_req:body", "~disabled_body: disabled", "auth:oauth2:additional_params:refresh_token_req:body", "refresh_body: refresh-body"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("OAuth2 auth was not serialized with %q:\n%s", expected, content)
		}
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Mode != "oauth2" ||
		parsed.Auth.Token != "static-token" ||
		parsed.Auth.OAuth2.GrantType != "password" ||
		parsed.Auth.OAuth2.AccessTokenURL != "https://auth.example.test/token" ||
		parsed.Auth.OAuth2.RefreshTokenURL != "https://auth.example.test/refresh" ||
		parsed.Auth.OAuth2.Username != "alice" ||
		parsed.Auth.OAuth2.Password != "secret" ||
		parsed.Auth.OAuth2.ClientID != "client-id" ||
		parsed.Auth.OAuth2.ClientSecret != "client-secret" ||
		parsed.Auth.OAuth2.CredentialsPlacement != "body" ||
		parsed.Auth.OAuth2.TokenSource != "id_token" ||
		parsed.Auth.OAuth2.TokenPlacement != "url" ||
		parsed.Auth.OAuth2.TokenQueryKey != "custom_token" ||
		!parsed.Auth.OAuth2.AutoFetchToken ||
		!parsed.Auth.OAuth2.AutoRefreshToken {
		t.Fatalf("OAuth2 auth did not round-trip: %#v", parsed.Auth)
	}
	if param, ok := findOAuth2Param(parsed.Auth.OAuth2.AuthorizationAdditionalParams, "prompt"); !ok || param.SendIn != "queryparams" || !param.Enabled {
		t.Fatalf("OAuth2 authorization additional params did not round-trip: %#v", parsed.Auth.OAuth2.AuthorizationAdditionalParams)
	}
	if param, ok := findOAuth2Param(parsed.Auth.OAuth2.TokenAdditionalParams, "X-Token-Header"); !ok || param.SendIn != "headers" || !param.Enabled {
		t.Fatalf("OAuth2 token header additional params did not round-trip: %#v", parsed.Auth.OAuth2.TokenAdditionalParams)
	}
	if param, ok := findOAuth2Param(parsed.Auth.OAuth2.TokenAdditionalParams, "disabled_body"); !ok || param.SendIn != "body" || param.Enabled {
		t.Fatalf("OAuth2 disabled token body additional param did not round-trip: %#v", parsed.Auth.OAuth2.TokenAdditionalParams)
	}
	if param, ok := findOAuth2Param(parsed.Auth.OAuth2.RefreshAdditionalParams, "refresh_body"); !ok || param.SendIn != "body" || !param.Enabled {
		t.Fatalf("OAuth2 refresh additional params did not round-trip: %#v", parsed.Auth.OAuth2.RefreshAdditionalParams)
	}
}

func TestOAuth2BrowserGrantFieldsRoundTrip(t *testing.T) {
	item := types.NewRequestItem("OAuth2 Browser", "http", 1)
	item.Auth = AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
		GrantType:            "authorization_code",
		CallbackURL:          "http://127.0.0.1:3000/callback",
		AuthorizationURL:     "https://auth.example.test/authorize",
		AccessTokenURL:       "https://auth.example.test/token",
		RefreshTokenURL:      "https://auth.example.test/refresh",
		ClientID:             "client-id",
		ClientSecret:         "client-secret",
		Scope:                "openid profile",
		State:                "csrf-state",
		PKCE:                 true,
		CredentialsPlacement: "basic_auth_header",
		TokenSource:          "access_token",
		TokenPlacement:       "header",
		TokenHeaderPrefix:    "Bearer",
		AutoFetchToken:       true,
		AutoRefreshToken:     true,
	}}

	content := stringifyBru(item)
	for _, expected := range []string{"grant_type: authorization_code", "callback_url: http://127.0.0.1:3000/callback", "authorization_url: https://auth.example.test/authorize", "state: csrf-state", "pkce: true"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("OAuth2 browser grant field was not serialized with %q:\n%s", expected, content)
		}
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	assertOAuth2BrowserGrantFields(t, parsed.Auth.OAuth2)

	yamlContent, err := stringifyYAMLRequest(item)
	if err != nil {
		t.Fatal(err)
	}
	parsedYAML, err := parseYAMLRequest(yamlContent)
	if err != nil {
		t.Fatal(err)
	}
	assertOAuth2BrowserGrantFields(t, parsedYAML.Auth.OAuth2)
}

func assertOAuth2BrowserGrantFields(t *testing.T, auth OAuth2Auth) {
	t.Helper()
	if auth.GrantType != "authorization_code" ||
		auth.CallbackURL != "http://127.0.0.1:3000/callback" ||
		auth.AuthorizationURL != "https://auth.example.test/authorize" ||
		auth.AccessTokenURL != "https://auth.example.test/token" ||
		auth.RefreshTokenURL != "https://auth.example.test/refresh" ||
		auth.ClientID != "client-id" ||
		auth.ClientSecret != "client-secret" ||
		auth.Scope != "openid profile" ||
		auth.State != "csrf-state" ||
		!auth.PKCE ||
		auth.CredentialsPlacement != "basic_auth_header" ||
		auth.TokenSource != "access_token" ||
		auth.TokenPlacement != "header" ||
		auth.TokenHeaderPrefix != "Bearer" ||
		!auth.AutoFetchToken ||
		!auth.AutoRefreshToken {
		t.Fatalf("OAuth2 browser grant fields did not round-trip: %#v", auth)
	}
}

func findOAuth2Param(params []OAuth2AdditionalParam, name string) (OAuth2AdditionalParam, bool) {
	for _, param := range params {
		if param.Name == name {
			return param, true
		}
	}
	return OAuth2AdditionalParam{}, false
}

func TestAWSV4AuthSignsHTTPRequest(t *testing.T) {
	const (
		accessKeyID     = "AKIDEXAMPLE"
		secretAccessKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		sessionToken    = "session-token"
		region          = "us-east-1"
		service         = "execute-api"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(body); got != `{"hello":"aws"}` {
			t.Fatalf("unexpected body: %s", got)
		}
		if got := r.Header.Get("X-Amz-Content-Sha256"); got != awsv4.PayloadSHA256(string(body)) {
			t.Fatalf("bad payload hash: %s", got)
		}
		if got := r.Header.Get("X-Amz-Security-Token"); got != sessionToken {
			t.Fatalf("missing session token: %q", got)
		}
		if got := r.Header.Get("X-Amz-Meta-Test"); got != "yes" {
			t.Fatalf("missing interpolated signed header: %q", got)
		}
		assertAWSV4Signature(t, r, accessKeyID, secretAccessKey, region, service)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"signed":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodPost
	targetURL := server.URL + "/prod/resource?z=last&a=first"
	body := RequestBody{Mode: "json", JSON: `{"hello":"aws"}`}
	headers := []KeyValue{{Name: "X-Amz-Meta-Test", Value: "{{meta}}", Enabled: true}}
	vars := RequestVars{Req: []Variable{
		{ID: "var-access", Name: "aws_access", Value: accessKeyID, DataType: "string", Enabled: true},
		{ID: "var-secret", Name: "aws_secret", Value: secretAccessKey, DataType: "string", Enabled: true},
		{ID: "var-token", Name: "aws_token", Value: sessionToken, DataType: "string", Enabled: true},
		{ID: "var-meta", Name: "meta", Value: "yes", DataType: "string", Enabled: true},
	}}
	auth := AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		AccessKeyID:     "{{aws_access}}",
		SecretAccessKey: "{{aws_secret}}",
		SessionToken:    "{{aws_token}}",
		Service:         service,
		Region:          region,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body, Headers: &headers, Vars: &vars, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("AWS SigV4 request failed: %#v", item.Response)
	}
}

func TestAWSV4AuthLoadsProfileCredentials(t *testing.T) {
	const (
		accessKeyID     = "PROFILEAKID"
		secretAccessKey = "PROFILESECRET"
		sessionToken    = "profile-token"
		region          = "us-west-2"
		service         = "execute-api"
	)
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials")
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(credentialsPath, []byte(`[dev]
aws_access_key_id = `+accessKeyID+`
aws_secret_access_key = `+secretAccessKey+`
aws_session_token = `+sessionToken+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`[profile dev]
region = us-east-2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", configPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Amz-Security-Token"); got != sessionToken {
			t.Fatalf("missing profile session token: %q", got)
		}
		assertAWSV4Signature(t, r, accessKeyID, secretAccessKey, region, service)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"signed":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	targetURL := server.URL + "/prod/profile"
	auth := AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		ProfileName: "dev",
		Service:     service,
		Region:      region,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("AWS SigV4 profile request failed: %#v", item.Response)
	}
}

func TestAWSV4AuthLoadsAssumeRoleProfileCredentials(t *testing.T) {
	const (
		sourceAccessKeyID     = "SOURCEAKID"
		sourceSecretAccessKey = "SOURCESECRET"
		sourceSessionToken    = "source-session"
		assumedAccessKeyID    = "ASSUMEDAKID"
		assumedSecretKey      = "ASSUMEDSECRET"
		assumedSessionToken   = "assumed-session"
		region                = "us-east-1"
		service               = "execute-api"
		roleARN               = "arn:aws:iam::123456789012:role/LiteAPITest"
	)
	var stsCalls int32
	stsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&stsCalls, 1)
		if got := r.Header.Get("X-Amz-Security-Token"); got != sourceSessionToken {
			t.Fatalf("STS AssumeRole should use source session token: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		expected := map[string]string{
			"Action":          "AssumeRole",
			"Version":         "2011-06-15",
			"RoleArn":         roleARN,
			"RoleSessionName": "liteapi-test",
			"ExternalId":      "external-test",
			"DurationSeconds": "900",
		}
		for key, value := range expected {
			if got := r.Form.Get(key); got != value {
				t.Fatalf("bad STS AssumeRole form %s: got %q form=%s", key, got, r.Form.Encode())
			}
		}
		assertAWSV4Signature(t, r, sourceAccessKeyID, sourceSecretAccessKey, region, "sts")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>` + assumedAccessKeyID + `</AccessKeyId>
      <SecretAccessKey>` + assumedSecretKey + `</SecretAccessKey>
      <SessionToken>` + assumedSessionToken + `</SessionToken>
      <Expiration>2030-01-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`))
	}))
	defer stsServer.Close()

	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials")
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(credentialsPath, []byte(`[source]
aws_access_key_id = `+sourceAccessKeyID+`
aws_secret_access_key = `+sourceSecretAccessKey+`
aws_session_token = `+sourceSessionToken+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`[profile assumed]
role_arn = `+roleARN+`
source_profile = source
role_session_name = liteapi-test
external_id = external-test
duration_seconds = 900
region = `+region+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_STS_ENDPOINT_URL", stsServer.URL)

	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Amz-Security-Token"); got != assumedSessionToken {
			t.Fatalf("missing assumed role session token: %q", got)
		}
		assertAWSV4Signature(t, r, assumedAccessKeyID, assumedSecretKey, region, service)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"assumed":true}`))
	}))
	defer resourceServer.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	targetURL := resourceServer.URL + "/prod/assumed"
	auth := AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		ProfileName: "assumed",
		Service:     service,
		Region:      region,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("AWS SigV4 assumed-role profile request failed: %#v", item.Response)
	}
	if got := atomic.LoadInt32(&stsCalls); got != 1 {
		t.Fatalf("expected one STS AssumeRole call, got %d", got)
	}
}

func TestAWSV4AuthLoadsAssumeRoleProfileCredentialsWithMFA(t *testing.T) {
	const (
		sourceAccessKeyID     = "MFASOURCEAKID"
		sourceSecretAccessKey = "MFASOURCESECRET"
		assumedAccessKeyID    = "MFAASSUMEDAKID"
		assumedSecretKey      = "MFAASSUMEDSECRET"
		assumedSessionToken   = "mfa-assumed-session"
		region                = "us-east-1"
		service               = "execute-api"
		roleARN               = "arn:aws:iam::123456789012:role/MFARole"
		mfaSerial             = "arn:aws:iam::123456789012:mfa/alice"
		mfaTokenCode          = "654321"
	)
	var stsCalls int32
	stsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&stsCalls, 1)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		expected := map[string]string{
			"Action":          "AssumeRole",
			"Version":         "2011-06-15",
			"RoleArn":         roleARN,
			"RoleSessionName": "mfa-session",
			"SerialNumber":    mfaSerial,
			"TokenCode":       mfaTokenCode,
		}
		for key, value := range expected {
			if got := r.Form.Get(key); got != value {
				t.Fatalf("bad MFA STS AssumeRole form %s: got %q form=%s", key, got, r.Form.Encode())
			}
		}
		assertAWSV4Signature(t, r, sourceAccessKeyID, sourceSecretAccessKey, region, "sts")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<AssumeRoleResponse>
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>` + assumedAccessKeyID + `</AccessKeyId>
      <SecretAccessKey>` + assumedSecretKey + `</SecretAccessKey>
      <SessionToken>` + assumedSessionToken + `</SessionToken>
    </Credentials>
  </AssumeRoleResult>
</AssumeRoleResponse>`))
	}))
	defer stsServer.Close()

	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "credentials")
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(credentialsPath, []byte(`[source]
aws_access_key_id = `+sourceAccessKeyID+`
aws_secret_access_key = `+sourceSecretAccessKey+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`[profile assumed-mfa]
role_arn = `+roleARN+`
source_profile = source
role_session_name = mfa-session
mfa_serial = `+mfaSerial+`
region = `+region+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_STS_ENDPOINT_URL", stsServer.URL)
	t.Setenv("AWS_MFA_TOKEN_CODE", "000000")
	t.Setenv("AWS_MFA_TOKEN_CODE_ASSUMED_MFA", mfaTokenCode)

	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Amz-Security-Token"); got != assumedSessionToken {
			t.Fatalf("missing MFA assumed role session token: %q", got)
		}
		assertAWSV4Signature(t, r, assumedAccessKeyID, assumedSecretKey, region, service)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"mfaAssumed":true}`))
	}))
	defer resourceServer.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	targetURL := resourceServer.URL + "/prod/mfa-assumed"
	auth := AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		ProfileName: "assumed-mfa",
		Service:     service,
		Region:      region,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("AWS SigV4 MFA assumed-role profile request failed: %#v", item.Response)
	}
	if got := atomic.LoadInt32(&stsCalls); got != 1 {
		t.Fatalf("expected one MFA STS AssumeRole call, got %d", got)
	}
}

func TestAWSV4AuthLoadsWebIdentityProfileCredentials(t *testing.T) {
	const (
		webIdentityToken     = "header.payload.signature"
		assumedAccessKeyID   = "WEBIDENTITYAKID"
		assumedSecretKey     = "WEBIDENTITYSECRET"
		assumedSessionToken  = "web-identity-session"
		region               = "us-east-2"
		service              = "execute-api"
		roleARN              = "arn:aws:iam::123456789012:role/WebIdentityRole"
		roleSessionName      = "web-identity-session-name"
		webIdentityProvider  = "graph.facebook.com"
		webIdentityDuration  = "1200"
		unexpectedAuthHeader = "web identity STS request should not be SigV4 signed"
	)
	var stsCalls int32
	stsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&stsCalls, 1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("%s: %q", unexpectedAuthHeader, got)
		}
		if got := r.Header.Get("X-Amz-Security-Token"); got != "" {
			t.Fatalf("web identity STS request should not send source session token: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		expected := map[string]string{
			"Action":           "AssumeRoleWithWebIdentity",
			"Version":          "2011-06-15",
			"RoleArn":          roleARN,
			"RoleSessionName":  roleSessionName,
			"WebIdentityToken": webIdentityToken,
			"DurationSeconds":  webIdentityDuration,
			"ProviderId":       webIdentityProvider,
		}
		for key, value := range expected {
			if got := r.Form.Get(key); got != value {
				t.Fatalf("bad STS web-identity form %s: got %q form=%s", key, got, r.Form.Encode())
			}
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleWithWebIdentityResult>
    <Credentials>
      <AccessKeyId>` + assumedAccessKeyID + `</AccessKeyId>
      <SecretAccessKey>` + assumedSecretKey + `</SecretAccessKey>
      <SessionToken>` + assumedSessionToken + `</SessionToken>
      <Expiration>2030-01-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleWithWebIdentityResult>
</AssumeRoleWithWebIdentityResponse>`))
	}))
	defer stsServer.Close()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "web-identity-token.jwt")
	if err := os.WriteFile(tokenPath, []byte(webIdentityToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(`[profile webid]
role_arn = `+roleARN+`
web_identity_token_file = `+tokenPath+`
role_session_name = `+roleSessionName+`
duration_seconds = `+webIdentityDuration+`
provider_id = `+webIdentityProvider+`
region = `+region+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_STS_ENDPOINT_URL", stsServer.URL)

	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Amz-Security-Token"); got != assumedSessionToken {
			t.Fatalf("missing web-identity role session token: %q", got)
		}
		assertAWSV4Signature(t, r, assumedAccessKeyID, assumedSecretKey, region, service)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"webIdentity":true}`))
	}))
	defer resourceServer.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	targetURL := resourceServer.URL + "/prod/web-identity"
	auth := AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		ProfileName: "webid",
		Service:     service,
		Region:      region,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("AWS SigV4 web-identity profile request failed: %#v", item.Response)
	}
	if got := atomic.LoadInt32(&stsCalls); got != 1 {
		t.Fatalf("expected one STS AssumeRoleWithWebIdentity call, got %d", got)
	}
}

func TestAWSV4AuthLoadsLegacySSOProfileCredentials(t *testing.T) {
	const (
		startURL        = "https://example.awsapps.com/start"
		ssoAccessToken  = "sso-access-token"
		ssoAccessKeyID  = "SSOAKID"
		ssoSecretKey    = "SSOSECRET"
		ssoSessionToken = "sso-session-token"
		accountID       = "123456789012"
		roleName        = "Developer"
		region          = "us-west-2"
		service         = "execute-api"
	)
	var ssoCalls int32
	ssoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ssoCalls, 1)
		if r.URL.Path != "/federation/credentials" {
			t.Fatalf("bad SSO credentials path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("account_id"); got != accountID {
			t.Fatalf("bad SSO account_id: %q", got)
		}
		if got := r.URL.Query().Get("role_name"); got != roleName {
			t.Fatalf("bad SSO role_name: %q", got)
		}
		if got := r.Header.Get("x-amz-sso_bearer_token"); got != ssoAccessToken {
			t.Fatalf("bad SSO bearer token: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"roleCredentials":{"accessKeyId":"` + ssoAccessKeyID + `","secretAccessKey":"` + ssoSecretKey + `","sessionToken":"` + ssoSessionToken + `","expiration":1893456000000}}`))
	}))
	defer ssoServer.Close()

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(cacheDir, awsv4.SSOCacheFilename(startURL)), []byte(`{"startUrl":"`+startURL+`","region":"`+region+`","accessToken":"`+ssoAccessToken+`","expiresAt":"`+expiresAt+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(`[profile ssolegacy]
sso_start_url = `+startURL+`
sso_region = `+region+`
sso_account_id = `+accountID+`
sso_role_name = `+roleName+`
region = `+region+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "missing-credentials"))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SSO_CACHE_DIR", cacheDir)
	t.Setenv("AWS_SSO_ENDPOINT_URL", ssoServer.URL)

	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Amz-Security-Token"); got != ssoSessionToken {
			t.Fatalf("missing SSO role session token: %q", got)
		}
		assertAWSV4Signature(t, r, ssoAccessKeyID, ssoSecretKey, region, service)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sso":true}`))
	}))
	defer resourceServer.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodGet
	targetURL := resourceServer.URL + "/prod/sso"
	auth := AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		ProfileName: "ssolegacy",
		Service:     service,
		Region:      region,
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("AWS SigV4 SSO profile request failed: %#v", item.Response)
	}
	if got := atomic.LoadInt32(&ssoCalls); got != 1 {
		t.Fatalf("expected one SSO GetRoleCredentials call, got %d", got)
	}
}

func TestAWSV4AuthBruRoundTrip(t *testing.T) {
	item := types.NewRequestItem("AWS", "http", 1)
	item.Auth = AuthConfig{Mode: "awsv4", AWSV4: AWSV4Auth{
		AccessKeyID:     "AKID",
		SecretAccessKey: "SECRET",
		SessionToken:    "TOKEN",
		Service:         "execute-api",
		Region:          "us-west-2",
		ProfileName:     "dev",
	}}
	content := stringifyBru(item)
	for _, expected := range []string{"auth:awsv4", "accessKeyId: AKID", "secretAccessKey: SECRET", "sessionToken: TOKEN", "service: execute-api", "region: us-west-2", "profileName: dev"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("AWS SigV4 auth was not serialized with %q:\n%s", expected, content)
		}
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Mode != "awsv4" ||
		parsed.Auth.AWSV4.AccessKeyID != "AKID" ||
		parsed.Auth.AWSV4.SecretAccessKey != "SECRET" ||
		parsed.Auth.AWSV4.SessionToken != "TOKEN" ||
		parsed.Auth.AWSV4.Service != "execute-api" ||
		parsed.Auth.AWSV4.Region != "us-west-2" ||
		parsed.Auth.AWSV4.ProfileName != "dev" {
		t.Fatalf("AWS SigV4 auth did not round-trip: %#v", parsed.Auth.AWSV4)
	}
}

func TestWSSEAuthHeaderSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := parseWSSEHeader(r.Header.Get("X-WSSE"))
		if values["Username"] != "wsse-user" {
			t.Fatalf("unexpected WSSE username: %#v", values)
		}
		if len(values["Nonce"]) != 32 {
			t.Fatalf("unexpected WSSE nonce: %#v", values)
		}
		if !strings.HasSuffix(values["Created"], "Z") {
			t.Fatalf("unexpected WSSE created timestamp: %#v", values)
		}
		expected := wsse.PasswordDigest(values["Nonce"], values["Created"], "wsse-pass")
		if values["PasswordDigest"] != expected {
			t.Fatalf("bad WSSE digest: got %s expected %s values=%#v", values["PasswordDigest"], expected, values)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"wsse":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodPost
	targetURL := server.URL + "/protected"
	body := RequestBody{Mode: "none"}
	vars := RequestVars{Req: []Variable{
		{ID: "var-user", Name: "wsse_user", Value: "wsse-user", DataType: "string", Enabled: true},
		{ID: "var-pass", Name: "wsse_pass", Value: "wsse-pass", DataType: "string", Enabled: true},
	}}
	auth := AuthConfig{Mode: "wsse", Username: "{{wsse_user}}", Password: "{{wsse_pass}}"}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body, Vars: &vars, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("WSSE request failed: %#v", item.Response)
	}
}

func TestWSSEAuthBruRoundTrip(t *testing.T) {
	item := types.NewRequestItem("WSSE", "http", 1)
	item.Auth = AuthConfig{Mode: "wsse", Username: "john", Password: "secret"}
	content := stringifyBru(item)
	if !strings.Contains(content, "auth:wsse") || !strings.Contains(content, "username: john") || !strings.Contains(content, "password: secret") {
		t.Fatalf("WSSE auth was not serialized:\n%s", content)
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Mode != "wsse" || parsed.Auth.Username != "john" || parsed.Auth.Password != "secret" {
		t.Fatalf("WSSE auth did not round-trip: %#v", parsed.Auth)
	}
}

func TestOAuth1AuthHeaderSignsHTTPRequest(t *testing.T) {
	const (
		consumerKey    = "consumer_key"
		consumerSecret = "consumer_secret"
		accessToken    = "access_token"
		tokenSecret    = "token_secret"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(body); got != "foo=bar&z=last" {
			t.Fatalf("unexpected form body: %q", got)
		}
		values := parseOAuth1Header(r.Header.Get("Authorization"))
		if values["realm"] != "example" {
			t.Fatalf("missing OAuth1 realm: %#v", values)
		}
		if values["oauth_consumer_key"] != consumerKey ||
			values["oauth_token"] != accessToken ||
			values["oauth_nonce"] != "testnonce" ||
			values["oauth_timestamp"] != "1234567890" ||
			values["oauth_signature_method"] != "HMAC-SHA1" ||
			values["oauth_version"] != "1.0" {
			t.Fatalf("unexpected OAuth1 params: %#v", values)
		}
		assertOAuth1Signature(t, r, values, consumerSecret, tokenSecret, [][2]string{{"foo", "bar"}, {"z", "last"}})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"oauth1":true}`))
	}))
	defer server.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	method := http.MethodPost
	targetURL := server.URL + "/resource?b=two&a=one"
	body := RequestBody{Mode: "text", Text: "foo=bar&z=last"}
	headers := []KeyValue{{Name: "Content-Type", Value: "application/x-www-form-urlencoded; charset=UTF-8", Enabled: true}}
	auth := AuthConfig{Mode: "oauth1", OAuth1: OAuth1Auth{
		ConsumerKey:       consumerKey,
		ConsumerSecret:    consumerSecret,
		AccessToken:       accessToken,
		AccessTokenSecret: tokenSecret,
		SignatureMethod:   "HMAC-SHA1",
		Timestamp:         "1234567890",
		Nonce:             "testnonce",
		Version:           "1.0",
		Realm:             "example",
		Placement:         "header",
	}}
	if _, err := app.UpdateRequest(collection.ID, collection.Items[0].ID, RequestPatch{Method: &method, URL: &targetURL, Body: &body, Headers: &headers, Auth: &auth}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SendRequest(collection.ID, collection.Items[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItemInState(state, collection.ID, collection.Items[0].ID)
	if !ok || item.Response == nil || item.Response.Status != http.StatusOK {
		t.Fatalf("OAuth1 request failed: %#v", item.Response)
	}
}

func TestOAuth1RSASignsWithInlineAndFilePrivateKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}))

	t.Run("inline private key", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://api.example.test/resource?existing=yes", nil)
		if err != nil {
			t.Fatal(err)
		}
		item := types.NewRequestItem("OAuth1 RSA", "http", 1)
		item.Auth = AuthConfig{Mode: "oauth1", OAuth1: OAuth1Auth{
			ConsumerKey:     "ck",
			AccessToken:     "at",
			SignatureMethod: "RSA-SHA256",
			Timestamp:       "1",
			Nonce:           "n",
			Version:         "1.0",
			PrivateKey:      privateKeyPEM,
			PrivateKeyType:  "text",
		}}
		if err := applyAuth(req, &item, nil); err != nil {
			t.Fatal(err)
		}
		values := parseOAuth1Header(req.Header.Get("Authorization"))
		if values["oauth_signature_method"] != "RSA-SHA256" {
			t.Fatalf("unexpected OAuth1 params: %#v", values)
		}
		assertOAuth1RSASignature(t, req, values, &privateKey.PublicKey, nil)
	})

	t.Run("file private key resolves from collection root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "collection.bru"), []byte("auth {\n  mode: none\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "keys"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "keys", "private.pem"), []byte(privateKeyPEM), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "folder"), 0o755); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, "https://api.example.test/resource", strings.NewReader("a=one"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		item := types.NewRequestItem("OAuth1 RSA", "http", 1)
		item.FilePath = filepath.Join(root, "folder", "request.bru")
		item.Auth = AuthConfig{Mode: "oauth1", OAuth1: OAuth1Auth{
			ConsumerKey:     "ck",
			SignatureMethod: "RSA-SHA512",
			Timestamp:       "1",
			Nonce:           "n",
			PrivateKey:      "keys/private.pem",
			PrivateKeyType:  "file",
		}}
		if err := applyAuth(req, &item, nil); err != nil {
			t.Fatal(err)
		}
		values := parseOAuth1Header(req.Header.Get("Authorization"))
		if values["oauth_signature_method"] != "RSA-SHA512" {
			t.Fatalf("unexpected OAuth1 params: %#v", values)
		}
		assertOAuth1RSASignature(t, req, values, &privateKey.PublicKey, [][2]string{{"a", "one"}})
	})
}

func TestOAuth1QueryBodyAndBodyHashPlacement(t *testing.T) {
	t.Run("query placement", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://api.example.test/resource?existing=yes", nil)
		if err != nil {
			t.Fatal(err)
		}
		item := types.NewRequestItem("OAuth1", "http", 1)
		item.Auth = AuthConfig{Mode: "oauth1", OAuth1: OAuth1Auth{ConsumerKey: "ck", ConsumerSecret: "cs", SignatureMethod: "PLAINTEXT", Timestamp: "1", Nonce: "n", Placement: "query"}}
		if err := applyAuth(req, &item, nil); err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("Authorization") != "" {
			t.Fatalf("query placement should not set Authorization")
		}
		q := req.URL.Query()
		if q.Get("oauth_consumer_key") != "ck" || q.Get("oauth_signature_method") != "PLAINTEXT" || q.Get("oauth_signature") != "cs&" || q.Get("existing") != "yes" {
			t.Fatalf("unexpected OAuth1 query params: %s", req.URL.RawQuery)
		}
	})

	t.Run("body placement with body hash", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "https://api.example.test/resource", strings.NewReader(`{"hello":"world"}`))
		if err != nil {
			t.Fatal(err)
		}
		item := types.NewRequestItem("OAuth1", "http", 1)
		item.Auth = AuthConfig{Mode: "oauth1", OAuth1: OAuth1Auth{ConsumerKey: "ck", ConsumerSecret: "cs", SignatureMethod: "HMAC-SHA256", Timestamp: "1", Nonce: "n", Placement: "body", IncludeBodyHash: true}}
		if err := applyAuth(req, &item, nil); err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("expected form content type, got %q", got)
		}
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		values, err := url.ParseQuery(string(bodyBytes))
		if err != nil {
			t.Fatal(err)
		}
		if values.Get("oauth_body_hash") != oauth1.BodyHash(`{"hello":"world"}`, "HMAC-SHA256") || values.Get("oauth_signature") == "" {
			t.Fatalf("unexpected OAuth1 body params: %s", string(bodyBytes))
		}
	})
}

func TestOAuth1AuthBruRoundTrip(t *testing.T) {
	item := types.NewRequestItem("OAuth1", "http", 1)
	item.Auth = AuthConfig{Mode: "oauth1", OAuth1: OAuth1Auth{
		ConsumerKey:       "ck",
		ConsumerSecret:    "cs",
		AccessToken:       "at",
		AccessTokenSecret: "ts",
		CallbackURL:       "https://example.test/callback",
		Verifier:          "verifier",
		SignatureMethod:   "HMAC-SHA256",
		PrivateKey:        "keys/private.pem",
		PrivateKeyType:    "file",
		Timestamp:         "123",
		Nonce:             "nonce",
		Version:           "1.0",
		Realm:             "realm",
		Placement:         "query",
		IncludeBodyHash:   true,
	}}
	content := stringifyBru(item)
	for _, expected := range []string{"auth:oauth1", "consumer_key: ck", "consumer_secret: cs", "access_token: at", "token_secret: ts", "callback_url: https://example.test/callback", "signature_method: HMAC-SHA256", "private_key: @file(keys/private.pem)", "placement: query", "include_body_hash: true"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("OAuth1 auth was not serialized with %q:\n%s", expected, content)
		}
	}
	parsed, err := parseBru(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Mode != "oauth1" ||
		parsed.Auth.OAuth1.ConsumerKey != "ck" ||
		parsed.Auth.OAuth1.ConsumerSecret != "cs" ||
		parsed.Auth.OAuth1.AccessToken != "at" ||
		parsed.Auth.OAuth1.AccessTokenSecret != "ts" ||
		parsed.Auth.OAuth1.CallbackURL != "https://example.test/callback" ||
		parsed.Auth.OAuth1.SignatureMethod != "HMAC-SHA256" ||
		parsed.Auth.OAuth1.PrivateKey != "keys/private.pem" ||
		parsed.Auth.OAuth1.PrivateKeyType != "file" ||
		parsed.Auth.OAuth1.Placement != "query" ||
		!parsed.Auth.OAuth1.IncludeBodyHash {
		t.Fatalf("OAuth1 auth did not round-trip: %#v", parsed.Auth.OAuth1)
	}
}

func parseAWSV4Authorization(header string) map[string]string {
	out := map[string]string{}
	header = strings.TrimSpace(strings.TrimPrefix(header, "AWS4-HMAC-SHA256"))
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

func testNTLMChallenge(t *testing.T) string {
	t.Helper()
	const defaultNTLMFlags = (1 << 23) | (1 << 31) | (1 << 29) | (1 << 0) | (1 << 19) | (1 << 9) | (1 << 15)
	var buf bytes.Buffer
	buf.Write([]byte{'N', 'T', 'L', 'M', 'S', 'S', 'P', 0x00})
	if err := binary.Write(&buf, binary.LittleEndian, uint32(2)); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, 8))
	if err := binary.Write(&buf, binary.LittleEndian, uint32(defaultNTLMFlags)); err != nil {
		t.Fatal(err)
	}
	buf.Write([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xef, 0xcd})
	buf.Write(make([]byte, 8))
	buf.Write(make([]byte, 8))
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func ntlmMessageType(t *testing.T, encoded string) uint32 {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("NTLM message was not base64: %v", err)
	}
	if len(data) < 12 || string(data[:8]) != "NTLMSSP\x00" {
		t.Fatalf("invalid NTLM message: %x", data)
	}
	return binary.LittleEndian.Uint32(data[8:12])
}

func parseOAuth1Header(header string) map[string]string {
	out := map[string]string{}
	header = strings.TrimSpace(strings.TrimPrefix(header, "OAuth"))
	for _, part := range splitDigestParts(header) {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			decoded = value
		}
		out[key] = decoded
	}
	return out
}

func assertOAuth1Signature(t *testing.T, r *http.Request, oauthValues map[string]string, consumerSecret, tokenSecret string, bodyPairs [][2]string) {
	t.Helper()
	baseString := oauth1TestSignatureBaseString(r, oauthValues, bodyPairs)
	expected, err := oauth1.Signature(baseString, consumerSecret, tokenSecret, oauthValues["oauth_signature_method"], "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if oauthValues["oauth_signature"] != expected {
		t.Fatalf("bad OAuth1 signature: got %s expected %s\nbase string:\n%s", oauthValues["oauth_signature"], expected, baseString)
	}
}

func assertOAuth1RSASignature(t *testing.T, r *http.Request, oauthValues map[string]string, publicKey *rsa.PublicKey, bodyPairs [][2]string) {
	t.Helper()
	signature, err := base64.StdEncoding.DecodeString(oauthValues["oauth_signature"])
	if err != nil {
		t.Fatalf("OAuth1 RSA signature was not base64: %v", err)
	}
	baseString := oauth1TestSignatureBaseString(r, oauthValues, bodyPairs)
	hashType, digest, err := oauth1.RSADigest(baseString, oauthValues["oauth_signature_method"])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(publicKey, hashType, digest, signature); err != nil {
		t.Fatalf("bad OAuth1 RSA signature: %v\nbase string:\n%s", err, baseString)
	}
}

func oauth1TestSignatureBaseString(r *http.Request, oauthValues map[string]string, bodyPairs [][2]string) string {
	oauthParams := map[string]string{}
	for key, value := range oauthValues {
		if strings.HasPrefix(key, "oauth_") && key != "oauth_signature" {
			oauthParams[key] = value
		}
	}
	extraPairs := append(oauth1.QueryPairs(r.URL.RawQuery), bodyPairs...)
	parameterString := oauth1.ParameterString(oauthParams, extraPairs)
	signedURL := *r.URL
	if signedURL.Scheme == "" {
		if r.TLS != nil {
			signedURL.Scheme = "https"
		} else {
			signedURL.Scheme = "http"
		}
	}
	if signedURL.Host == "" {
		signedURL.Host = r.Host
	}
	baseString := strings.ToUpper(r.Method) + "&" + oauth1.Encode(oauth1.BaseURL(&signedURL)) + "&" + oauth1.Encode(parameterString)
	return baseString
}

func parseWSSEHeader(header string) map[string]string {
	out := map[string]string{}
	header = strings.TrimSpace(strings.TrimPrefix(header, "UsernameToken"))
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[key] = strings.Trim(value, `"`)
	}
	return out
}

func TestImportOpenAPIGeneratesRequests(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Pet Store
servers:
  - url: https://api.example.test
security:
  - ApiKeyAuth: []
components:
  securitySchemes:
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
    BearerAuth:
      type: http
      scheme: bearer
    DigestAuth:
      type: http
      scheme: digest
  schemas:
    PetInput:
      type: object
      properties:
        name:
          type: string
        age:
          type: integer
        tags:
          type: array
          items:
            type: string
        status:
          type: string
          enum: [available, pending]
  headers:
    RequestId:
      description: Request correlation ID
      schema:
        type: string
        example: req-123
  requestBodies:
    CreatePet:
      content:
        application/json:
          examples:
            happy:
              summary: Happy request
              value:
                name: Lin
                age: 4
                tags: [friendly]
                status: available
          schema:
            $ref: '#/components/schemas/PetInput'
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      tags: [Pets]
      parameters:
        - name: limit
          in: query
          required: true
          schema:
            type: integer
            default: 25
    post:
      summary: Create pet
      tags: [Pets]
      security:
        - BearerAuth: []
      parameters:
        - name: X-Trace
          in: header
      requestBody:
        $ref: '#/components/requestBodies/CreatePet'
      responses:
        "201":
          description: Created pet
          headers:
            Location:
              description: Created resource URL
              example: /pets/123
              schema:
                type: string
            RateLimit-Remaining:
              description: Remaining requests
              schema:
                type: integer
                default: 99
            X-Request-ID:
              $ref: '#/components/headers/RequestId'
          content:
            application/json:
              examples:
                happy:
                  summary: Happy response
                  description: Created pet response
                  value:
                    id: 123
                    name: Lin
          links:
            GetCreatedPet:
              operationId: getPetById
              parameters:
                id: "$response.body#/id"
                location: "$response.header.Location"
  /pets/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: integer
          minimum: 1
    get:
      operationId: getPetById
      tags: [Pet Detail]
      security:
        - DigestAuth: []
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if imported.Name != "Pet Store" || len(imported.Items) != 3 {
		t.Fatalf("unexpected openapi import: %#v", imported)
	}
	if imported.Variables[0].Name != "baseUrl" || imported.Variables[0].Value != "https://api.example.test" {
		t.Fatalf("server was not imported as host variable: %#v", imported.Variables)
	}
	getReq := imported.Items[0]
	if getReq.Name != "List pets" || getReq.Method != http.MethodGet || getReq.Params[0].Name != "limit" || getReq.Params[0].Value != "25" || getReq.Auth.Mode != "apikey" || getReq.Auth.APIKey != "X-API-Key" || getReq.Headers[0].Name != "X-API-Key" || getReq.FolderPath != "Pets" {
		t.Fatalf("GET operation not imported: %#v", getReq)
	}
	postReq := imported.Items[1]
	if postReq.Method != http.MethodPost || postReq.Headers[0].Name != "X-Trace" || postReq.Auth.Mode != "bearer" || postReq.Body.Mode != "json" {
		t.Fatalf("POST operation not imported: %#v", postReq)
	}
	for _, expected := range []string{`"age": 4`, `"name": "Lin"`, `"status": "available"`, `"tags"`} {
		if !strings.Contains(postReq.Body.JSON, expected) {
			t.Fatalf("POST schema body missing %q:\n%s", expected, postReq.Body.JSON)
		}
	}
	if len(postReq.Examples) != 1 {
		t.Fatalf("expected imported response example, got %#v", postReq.Examples)
	}
	example := postReq.Examples[0]
	if example.Name != "Happy response" || example.Response.Status != http.StatusCreated || example.Response.BodyType != "json" || !strings.Contains(example.Response.Body, `"id": 123`) || !strings.Contains(example.Request.Body, `"name": "Lin"`) {
		t.Fatalf("OpenAPI response example not imported: %#v", example)
	}
	if len(example.Response.Headers) != 4 || example.Response.Headers[0].Name != "Content-Type" || getKeyValue(example.Response.Headers, "Location") != "/pets/123" || getKeyValue(example.Response.Headers, "RateLimit-Remaining") != "99" || getKeyValue(example.Response.Headers, "X-Request-ID") != "req-123" {
		t.Fatalf("OpenAPI response example headers not imported: %#v", example.Response.Headers)
	}
	for _, expected := range []string{
		`if (res.status === 201)`,
		`bru.setVar("getPetById_id", res.json["id"]);`,
		`bru.setVar("getPetById_location", res.getHeader("Location"));`,
	} {
		if !strings.Contains(postReq.PostScript, expected) {
			t.Fatalf("OpenAPI response link script missing %q:\n%s", expected, postReq.PostScript)
		}
	}
	linkVars := map[string]string{}
	if err := runPostResponseScript(postReq.PostScript, postReq, Response{
		Status:  http.StatusCreated,
		Body:    `{"id":123,"name":"Lin"}`,
		Headers: map[string]string{"Location": "/pets/123"},
	}, linkVars, nil); err != nil {
		t.Fatalf("OpenAPI response link script did not execute: %v", err)
	}
	if linkVars["getPetById_id"] != "123" || linkVars["getPetById_location"] != "/pets/123" {
		t.Fatalf("OpenAPI response link script did not capture vars: %#v", linkVars)
	}
	pathReq := imported.Items[2]
	if pathReq.URL != "{{baseUrl}}/pets/:id" || len(pathReq.PathParams) != 1 || pathReq.PathParams[0].Name != "id" || pathReq.PathParams[0].Value != "1" || pathReq.FolderPath != "Pet Detail" || pathReq.Auth.Mode != "digest" {
		t.Fatalf("path parameter operation not imported: %#v", pathReq)
	}

	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec, GroupBy: "path"})
	if err != nil {
		t.Fatal(err)
	}
	pathGrouped := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if pathGrouped.Items[0].FolderPath != "pets" || pathGrouped.Items[1].FolderPath != "pets" || pathGrouped.Items[2].FolderPath != "pets/{id}" {
		t.Fatalf("path grouping did not preserve OpenAPI path segments: %#v", pathGrouped.Items)
	}
}

func TestImportOpenAPITraceAndBrunoVariants(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Variant API
servers:
  - url: https://api.example.test
paths:
  /diagnostics:
    trace:
      summary: Trace diagnostics
      tags: [Diagnostics]
      responses:
        "204":
          description: No content
  /variants:
    post:
      summary: Create normal
      tags: [Variants]
      requestBody:
        content:
          application/json:
            example:
              mode: normal
      responses:
        "200":
          description: OK
          content:
            text/plain:
              example: normal ok
      x-bruno-variants:
        - summary: Create variant
          tags: [Variants]
          requestBody:
            content:
              application/json:
                example:
                  mode: variant
          responses:
            "202":
              description: Accepted
              content:
                text/plain:
                  example: variant ok
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(imported.Items) != 3 {
		t.Fatalf("expected TRACE plus base request plus variant, got %#v", imported.Items)
	}
	traceReq := imported.Items[0]
	if traceReq.Method != http.MethodTrace || traceReq.Name != "Trace diagnostics" || traceReq.FolderPath != "Diagnostics" {
		t.Fatalf("TRACE operation was not imported: %#v", traceReq)
	}
	traceRoundTrip, err := parseBru(stringifyBru(traceReq))
	if err != nil {
		t.Fatal(err)
	}
	if traceRoundTrip.Method != http.MethodTrace || traceRoundTrip.URL != traceReq.URL {
		t.Fatalf("TRACE .bru round-trip failed:\n%s\n%#v", stringifyBru(traceReq), traceRoundTrip)
	}
	baseReq := imported.Items[1]
	if baseReq.Name != "Create normal" || baseReq.Method != http.MethodPost || !strings.Contains(baseReq.Body.JSON, `"mode": "normal"`) || len(baseReq.Examples) != 1 || baseReq.Examples[0].Response.Status != http.StatusOK {
		t.Fatalf("base OpenAPI operation was not imported: %#v", baseReq)
	}
	variantReq := imported.Items[2]
	if variantReq.Name != "Create variant" || variantReq.Method != http.MethodPost || variantReq.URL != "{{baseUrl}}/variants" || !strings.Contains(variantReq.Body.JSON, `"mode": "variant"`) {
		t.Fatalf("x-bruno-variants operation was not imported: %#v", variantReq)
	}
	if len(variantReq.Examples) != 1 || variantReq.Examples[0].Response.Status != http.StatusAccepted || variantReq.Examples[0].Response.Body != "variant ok" {
		t.Fatalf("x-bruno-variants response example was not imported: %#v", variantReq.Examples)
	}
}

func TestImportOpenAPIWebhooksAndCallbacks(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.1.0
info:
  title: Events API
servers:
  - url: https://api.example.test
paths:
  /subscriptions:
    post:
      summary: Create subscription
      tags: [Subscriptions]
      requestBody:
        content:
          application/json:
            example:
              callbackUrl: https://client.example.test/hooks/orders
      responses:
        "201":
          description: Created
          content:
            application/json:
              example:
                id: sub_123
      callbacks:
        subscriptionEvents:
          '{$request.body#/callbackUrl}':
            post:
              summary: Subscription event callback
              tags: [Events]
              requestBody:
                content:
                  application/json:
                    example:
                      event: order.shipped
              responses:
                "204":
                  description: Accepted
webhooks:
  orderCreated:
    post:
      summary: Order created webhook
      tags: [Events]
      requestBody:
        content:
          application/json:
            example:
              orderId: ord_123
      responses:
        "202":
          description: Accepted
          content:
            application/json:
              example:
                accepted: true
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(imported.Items) != 3 {
		t.Fatalf("expected operation, callback, and webhook, got %#v", imported.Items)
	}
	baseReq := imported.Items[0]
	if baseReq.Name != "Create subscription" || baseReq.Method != http.MethodPost || baseReq.URL != "{{baseUrl}}/subscriptions" || !strings.Contains(baseReq.Body.JSON, "callbackUrl") {
		t.Fatalf("base operation was not imported: %#v", baseReq)
	}
	callbackReq := imported.Items[1]
	if callbackReq.Name != "Subscription event callback" || callbackReq.URL != "{{callbackUrl}}" || callbackReq.FolderPath != "Subscriptions/Callbacks/subscriptionEvents/Events" {
		t.Fatalf("callback operation was not imported as a request template: %#v", callbackReq)
	}
	callbackVars := map[string]string{}
	for _, variable := range callbackReq.Vars.Req {
		callbackVars[variable.Name] = fmt.Sprint(variable.Value)
	}
	if callbackVars["callbackUrl"] != "{$request.body#/callbackUrl}" || !strings.Contains(callbackReq.Body.JSON, "order.shipped") {
		t.Fatalf("callback URL variable/body were not imported: vars=%#v body=%s", callbackReq.Vars.Req, callbackReq.Body.JSON)
	}
	webhookReq := imported.Items[2]
	if webhookReq.Name != "Order created webhook" || webhookReq.URL != "{{baseUrl}}/webhooks/orderCreated" || webhookReq.FolderPath != "Webhooks/orderCreated/Events" {
		t.Fatalf("webhook operation was not imported as a request template: %#v", webhookReq)
	}
	if !strings.Contains(webhookReq.Body.JSON, "ord_123") || len(webhookReq.Examples) != 1 || webhookReq.Examples[0].Response.Status != http.StatusAccepted {
		t.Fatalf("webhook body/response example not imported: body=%s examples=%#v", webhookReq.Body.JSON, webhookReq.Examples)
	}
}

func TestImportOpenAPIServerOverrides(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Server API
servers:
  - url: https://{stage}.example.test/{version}/
    variables:
      stage:
        default: api
      version:
        enum: [v1, v2]
paths:
  /root:
    get:
      summary: Root server
      responses:
        "200":
          description: OK
  /local:
    servers:
      - url: https://local.example.test/{region}
        variables:
          region:
            default: us
    get:
      summary: Path server
      responses:
        "200":
          description: OK
  /op:
    get:
      summary: Operation server
      servers:
        - url: https://op.example.test/{tenant}
          variables:
            tenant:
              enum: [acme]
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec})
	if err != nil {
		t.Fatal(err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	varMap := func(values []Variable) map[string]string {
		out := map[string]string{}
		for _, value := range values {
			out[value.Name] = fmt.Sprint(value.Value)
		}
		return out
	}
	collectionVars := varMap(imported.Variables)
	if collectionVars["baseUrl"] != "https://{{stage}}.example.test/{{version}}" || collectionVars["stage"] != "api" || collectionVars["version"] != "v1" {
		t.Fatalf("collection server variables were not imported: %#v", imported.Variables)
	}
	itemsByName := map[string]RequestItem{}
	for _, item := range imported.Items {
		itemsByName[item.Name] = item
	}
	if rootVars := varMap(itemsByName["Root server"].Vars.Req); rootVars["baseUrl"] != "" {
		t.Fatalf("root server request should inherit collection baseUrl, got %#v", rootVars)
	}
	pathVars := varMap(itemsByName["Path server"].Vars.Req)
	if pathVars["baseUrl"] != "https://local.example.test/{{region}}" || pathVars["region"] != "us" {
		t.Fatalf("path server override variables were not imported: %#v", itemsByName["Path server"].Vars.Req)
	}
	operationVars := varMap(itemsByName["Operation server"].Vars.Req)
	if operationVars["baseUrl"] != "https://op.example.test/{{tenant}}" || operationVars["tenant"] != "acme" {
		t.Fatalf("operation server override variables were not imported: %#v", itemsByName["Operation server"].Vars.Req)
	}
}

func TestOpenAPISyncApplyPreservesUserValuesAndScripts(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Sync API
  version: 1.0.0
servers:
  - url: https://api.example.test
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
paths:
  /pets:
    post:
      summary: Create pet
      tags: [Pets]
      security:
        - BearerAuth: []
      parameters:
        - name: q
          in: query
          schema:
            type: string
            default: spec-q
        - name: old
          in: query
          schema:
            type: string
            default: remove-me
        - name: X-Trace
          in: header
          schema:
            type: string
            default: spec-trace
      requestBody:
        content:
          application/json:
            example:
              id: 0
              name: ""
              old: remove
              tags:
                - id: 0
                  value: ""
      responses:
        "200":
          description: OK
  /legacy:
    get:
      summary: Legacy request
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: initialSpec, SourceURL: "openapi.yml", OpenAPISync: true})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	var createPet RequestItem
	for _, item := range collection.Items {
		if item.Name == "Create pet" {
			createPet = item
			break
		}
	}
	if createPet.ID == "" {
		t.Fatalf("Create pet request not imported: %#v", collection.Items)
	}
	body := RequestBody{Mode: "json", JSON: `{"id": {{petId}}, "name": "Milo", "old": "keep", "tags": [{"id": 7, "value": "kept"}]}`}
	params := []KeyValue{{Name: "q", Value: "user-q", Enabled: false}, {Name: "old", Value: "user-old", Enabled: true}}
	headers := []KeyValue{{Name: "X-Trace", Value: "user-trace", Enabled: false}}
	auth := AuthConfig{Mode: "bearer", Token: "user-token"}
	preScript := `bru.setVar("kept", "pre")`
	postScript := `bru.setVar("kept", "post")`
	tests := `test("kept", () => expect(true).to.equal(true));`
	assertions := []Assertion{{Expression: "res.status", Operator: "equals", Value: "200", Enabled: true}}
	state, err = app.UpdateRequest(collection.ID, createPet.ID, RequestPatch{
		Params:     &params,
		Headers:    &headers,
		Body:       &body,
		Auth:       &auth,
		PreScript:  &preScript,
		PostScript: &postScript,
		Tests:      &tests,
		Assertions: &assertions,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	nextSpec := `
openapi: 3.0.3
info:
  title: Sync API
  version: 1.1.0
servers:
  - url: https://api.changed.test
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
paths:
  /pets:
    post:
      summary: Create pet v2
      tags: [Pets]
      security:
        - BearerAuth: []
      parameters:
        - name: q
          in: query
          schema:
            type: string
            default: spec-q2
        - name: page
          in: query
          schema:
            type: integer
            default: 1
        - name: X-Trace
          in: header
          schema:
            type: string
            default: spec-trace-2
        - name: X-New
          in: header
          schema:
            type: string
            default: spec-new
      requestBody:
        content:
          application/json:
            example:
              id: 0
              name: ""
              extra: true
              tags:
                - id: 0
                  label: ""
      responses:
        "200":
          description: OK
  /owners:
    get:
      summary: List owners
      tags: [Owners]
      responses:
        "200":
          description: OK
`
	preview, err := app.CheckOpenAPISync(collection.ID, OpenAPISyncOptions{Content: nextSpec, GroupBy: "tag", PreserveValues: true})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Added != 1 || preview.Updated != 1 || preview.Removed != 1 || !preview.HasChanges || preview.Version != "1.1.0" {
		t.Fatalf("unexpected sync preview: %#v", preview)
	}
	state, err = app.ApplyOpenAPISync(collection.ID, OpenAPISyncOptions{Content: nextSpec, SourceURL: "openapi.yml", GroupBy: "tag", PreserveValues: true})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	updated, err := findItem(&collection, createPet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != createPet.ID || updated.PreScript != preScript || updated.PostScript != postScript || updated.Tests != tests || !reflect.DeepEqual(updated.Assertions, assertions) {
		t.Fatalf("sync did not preserve request identity/scripts/tests/assertions: %#v", updated)
	}
	if updated.Params[0].Name != "q" || updated.Params[0].Value != "user-q" || updated.Params[0].Enabled || updated.Params[1].Name != "page" || updated.Params[1].Value != "1" {
		t.Fatalf("query params were not merged Bruno-style: %#v", updated.Params)
	}
	if updated.Headers[0].Name != "X-Trace" || updated.Headers[0].Value != "user-trace" || updated.Headers[0].Enabled || updated.Headers[1].Name != "X-New" || updated.Headers[1].Value != "spec-new" {
		t.Fatalf("headers were not merged Bruno-style: %#v", updated.Headers)
	}
	for _, expected := range []string{`"id": {{petId}}`, `"name": "Milo"`, `"extra": true`, `"label": ""`} {
		if !strings.Contains(updated.Body.JSON, expected) {
			t.Fatalf("merged JSON body missing %q:\n%s", expected, updated.Body.JSON)
		}
	}
	if strings.Contains(updated.Body.JSON, `"old"`) || strings.Contains(updated.Body.JSON, `"value"`) {
		t.Fatalf("merged JSON body kept spec-removed keys:\n%s", updated.Body.JSON)
	}
	if updated.Auth.Mode != "bearer" || updated.Auth.Token != "user-token" {
		t.Fatalf("auth credentials were not preserved: %#v", updated.Auth)
	}
	if len(collection.OpenAPI) != 1 || collection.OpenAPI[0].SourceURL != "openapi.yml" || collection.OpenAPI[0].GroupBy != "tag" || collection.OpenAPI[0].SpecHash == "" || collection.OpenAPI[0].LastSyncDate == "" {
		t.Fatalf("sync config was not stored: %#v", collection.OpenAPI)
	}
	var ownersFound bool
	for _, item := range collection.Items {
		if item.Name == "List owners" {
			ownersFound = true
		}
	}
	if !ownersFound {
		t.Fatalf("sync did not add new endpoint: %#v", collection.Items)
	}
	for _, item := range collection.Items {
		if item.Name == "Legacy request" {
			t.Fatalf("sync should delete spec-removed endpoints by default: %#v", collection.Items)
		}
	}
	configData, err := os.ReadFile(filepath.Join(collection.Path, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"openapi"`) || !strings.Contains(string(configData), `"sourceUrl": "openapi.yml"`) {
		t.Fatalf("bruno.json did not persist openapi config:\n%s", string(configData))
	}
}

func TestOpenAPISyncApplyHonorsEndpointDecisions(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Decision Sync API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: Get pets
      responses:
        "200":
          description: OK
  /legacy:
    get:
      summary: Legacy request
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: initialSpec, OpenAPISync: true})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	nextSpec := `
openapi: 3.0.3
info:
  title: Decision Sync API
  version: 1.1.0
paths:
  /pets:
    get:
      summary: Get pets v2
      parameters:
        - name: page
          in: query
          schema:
            type: integer
            default: 1
      responses:
        "200":
          description: OK
  /owners:
    get:
      summary: List owners
      responses:
        "200":
          description: OK
`
	preview, err := app.CheckOpenAPISync(collection.ID, OpenAPISyncOptions{Content: nextSpec, PreserveValues: true})
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]string{}
	for _, change := range preview.Changes {
		defaults[change.ID] = change.DefaultDecision
	}
	if defaults["GET:/pets"] != "accept-incoming" || defaults["GET:/owners"] != "accept-incoming" || defaults["GET:/legacy"] != "accept-incoming" {
		t.Fatalf("unexpected endpoint decision defaults: %#v", preview.Changes)
	}
	state, err = app.ApplyOpenAPISync(collection.ID, OpenAPISyncOptions{
		Content:        nextSpec,
		PreserveValues: true,
		EndpointDecisions: map[string]string{
			"GET:/pets":   "keep-mine",
			"GET:/owners": "keep-mine",
			"GET:/legacy": "accept-incoming",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	var pets RequestItem
	for _, item := range collection.Items {
		switch item.Name {
		case "Get pets":
			pets = item
		case "List owners":
			t.Fatalf("owners endpoint should have been skipped: %#v", collection.Items)
		case "Legacy request":
			t.Fatalf("legacy endpoint should have been removed: %#v", collection.Items)
		}
	}
	if pets.ID == "" {
		t.Fatalf("pets endpoint missing after sync: %#v", collection.Items)
	}
	if len(pets.Params) != 0 {
		t.Fatalf("pets endpoint was updated despite keep-mine decision: %#v", pets.Params)
	}
}

func TestOpenAPISyncApplyCanKeepRemovedEndpoint(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Keep Removed API
paths:
  /legacy:
    get:
      summary: Legacy request
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: initialSpec, OpenAPISync: true})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	nextSpec := `
openapi: 3.0.3
info:
  title: Keep Removed API
paths:
  /current:
    get:
      summary: Current request
      responses:
        "200":
          description: OK
`
	state, err = app.ApplyOpenAPISync(collection.ID, OpenAPISyncOptions{
		Content: nextSpec,
		EndpointDecisions: map[string]string{
			"GET:/legacy": "keep-mine",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	var legacyFound, currentFound bool
	for _, item := range collection.Items {
		if item.Name == "Legacy request" {
			legacyFound = true
		}
		if item.Name == "Current request" {
			currentFound = true
		}
	}
	if !legacyFound || !currentFound {
		t.Fatalf("expected kept legacy and added current requests, got %#v", collection.Items)
	}
}

func TestUpdateOpenAPISyncConfigPersistsAutoCheckSettings(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Settings Sync API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: initialSpec, SourceURL: "openapi.yml", OpenAPISync: true})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.OpenAPI) != 1 || collection.OpenAPI[0].SpecHash == "" || collection.OpenAPI[0].LastSyncDate == "" {
		t.Fatalf("expected initial openapi sync config: %#v", collection.OpenAPI)
	}
	originalHash := collection.OpenAPI[0].SpecHash
	originalSyncDate := collection.OpenAPI[0].LastSyncDate

	state, err = app.UpdateOpenAPISyncConfig(collection.ID, OpenAPISyncConfig{
		SourceURL:         "updated.yml",
		GroupBy:           "path",
		AutoCheck:         false,
		AutoCheckInterval: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.OpenAPI) != 1 {
		t.Fatalf("expected one openapi config: %#v", collection.OpenAPI)
	}
	config := collection.OpenAPI[0]
	if config.SourceURL != "updated.yml" || config.GroupBy != "path" || config.AutoCheck || config.AutoCheckInterval != 15 {
		t.Fatalf("settings were not persisted in state: %#v", config)
	}
	if config.SpecHash != originalHash || config.LastSyncDate != originalSyncDate {
		t.Fatalf("settings update should preserve sync metadata: before=%s/%s after=%#v", originalHash, originalSyncDate, config)
	}

	configData, err := os.ReadFile(filepath.Join(collection.Path, "bruno.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"sourceUrl": "updated.yml"`, `"groupBy": "path"`, `"autoCheck": false`, `"autoCheckInterval": 15`} {
		if !strings.Contains(string(configData), expected) {
			t.Fatalf("bruno.json missing %q:\n%s", expected, string(configData))
		}
	}

	nextSpec := `
openapi: 3.0.3
info:
  title: Settings Sync API
  version: 1.1.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
  /owners:
    get:
      summary: List owners
      responses:
        "200":
          description: OK
`
	state, err = app.ApplyOpenAPISync(collection.ID, OpenAPISyncOptions{Content: nextSpec, SourceURL: "updated.yml", GroupBy: "path"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.OpenAPI) != 1 {
		t.Fatalf("expected one openapi config after apply: %#v", collection.OpenAPI)
	}
	config = collection.OpenAPI[0]
	if config.SourceURL != "updated.yml" || config.GroupBy != "path" || config.AutoCheck || config.AutoCheckInterval != 15 {
		t.Fatalf("apply should preserve saved auto-check settings: %#v", config)
	}
	if config.SpecHash == "" || config.SpecHash == originalHash || config.LastSyncDate == "" {
		t.Fatalf("apply should refresh sync metadata while preserving settings: %#v", config)
	}
}

func TestCheckOpenAPIUpdatesComparesRemoteSpecHash(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Remote Update API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
`
	nextSpec := `
openapi: 3.0.3
info:
  title: Remote Update API
  version: 1.1.0
paths:
  /pets:
    get:
      summary: List pets v2
      parameters:
        - name: page
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: OK
  /owners:
    get:
      summary: List owners
      responses:
        "200":
          description: OK
`
	currentSpec := initialSpec
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("_") == "" {
			t.Errorf("expected cache-busting query parameter")
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, currentSpec)
	}))
	defer server.Close()

	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: initialSpec})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	state, err = app.ConnectOpenAPISync(collection.ID, OpenAPISyncOptions{SourceURL: server.URL + "/openapi.yml"})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.OpenAPI) != 1 || collection.OpenAPI[0].SpecHash == "" {
		t.Fatalf("expected connected openapi config: %#v", collection.OpenAPI)
	}
	storedHash := collection.OpenAPI[0].SpecHash

	check, err := app.CheckOpenAPIUpdates(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.HasUpdates || check.StoredSpecHash != storedHash || check.RemoteSpecHash != storedHash || check.CheckedAt == "" {
		t.Fatalf("unexpected no-update check result: %#v", check)
	}

	currentSpec = nextSpec
	check, err = app.CheckOpenAPIUpdates(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !check.HasUpdates || check.StoredSpecHash != storedHash || check.RemoteSpecHash == "" || check.RemoteSpecHash == storedHash {
		t.Fatalf("unexpected update check result: %#v", check)
	}
	if hits < 3 {
		t.Fatalf("expected connect plus two check fetches, got %d", hits)
	}
}

func TestGetOpenAPISyncSpecReturnsCachedSpec(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Spec Viewer Cached API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		Kind:        "openapi",
		Content:     spec,
		SourceURL:   "openapi.yml",
		OpenAPISync: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]

	result, err := app.GetOpenAPISyncSpec(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceURL != "openapi.yml" || !result.FromCache || result.Fetched || result.NoStoredSpec {
		t.Fatalf("unexpected cached spec result: %#v", result)
	}
	if !strings.Contains(result.Content, "Spec Viewer Cached API") || !strings.Contains(result.Content, "/pets") {
		t.Fatalf("expected cached spec content, got:\n%s", result.Content)
	}
}

func TestGetOpenAPISyncSpecFetchesWhenCacheMissing(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Spec Viewer Initial API
  version: 1.0.0
paths:
  /initial:
    get:
      summary: Initial request
      responses:
        "200":
          description: OK
`
	remoteSpec := `
openapi: 3.0.3
info:
  title: Spec Viewer Remote API
  version: 1.1.0
paths:
  /remote:
    get:
      summary: Remote request
      responses:
        "200":
          description: OK
`
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("_") == "" {
			t.Errorf("expected cache-busting query parameter")
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, remoteSpec)
	}))
	defer server.Close()

	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		Kind:        "openapi",
		Content:     initialSpec,
		SourceURL:   server.URL + "/openapi.yml",
		OpenAPISync: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	app.mu.Lock()
	app.cleanupOpenAPISyncSpecLocked(collection.Path)
	app.mu.Unlock()

	result, err := app.GetOpenAPISyncSpec(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceURL != server.URL+"/openapi.yml" || result.FromCache || !result.Fetched || !result.NoStoredSpec {
		t.Fatalf("unexpected fetched spec result: %#v", result)
	}
	if !strings.Contains(result.Content, "Spec Viewer Remote API") || !strings.Contains(result.Content, "/remote") {
		t.Fatalf("expected fetched spec content, got:\n%s", result.Content)
	}
	if hits != 1 {
		t.Fatalf("expected one fallback fetch, got %d", hits)
	}
}

func TestGetOpenAPISyncSpecDiffComparesStoredAndIncomingSpec(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Spec Diff API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
`
	nextSpec := `
openapi: 3.0.3
info:
  title: Spec Diff API
  version: 1.1.0
paths:
  /pets:
    get:
      summary: List pets v2
      parameters:
        - name: page
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: OK
  /owners:
    get:
      summary: List owners
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		Kind:        "openapi",
		Content:     initialSpec,
		SourceURL:   "openapi.yml",
		OpenAPISync: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]

	diff, err := app.GetOpenAPISyncSpecDiff(collection.ID, OpenAPISyncOptions{Content: nextSpec})
	if err != nil {
		t.Fatal(err)
	}
	if diff.SourceURL != "openapi.yml" || diff.NoStoredSpec {
		t.Fatalf("unexpected diff metadata: %#v", diff)
	}
	if diff.StoredSpecHash == "" || diff.NewSpecHash == "" || diff.StoredSpecHash == diff.NewSpecHash {
		t.Fatalf("expected distinct stored/new hashes: %#v", diff)
	}
	if diff.Added != 1 || diff.Updated != 1 || diff.Removed != 0 || diff.Unchanged != 0 {
		t.Fatalf("unexpected endpoint counts: %#v", diff)
	}
	if !strings.Contains(diff.StoredContent, "version: 1.0.0") || !strings.Contains(diff.NewContent, "/owners") {
		t.Fatalf("diff content missing expected specs: stored=\n%s\nnew=\n%s", diff.StoredContent, diff.NewContent)
	}
	var sawChanged, sawAdded bool
	for _, line := range diff.Lines {
		if line.Kind == "changed" && strings.Contains(line.NewText, "1.1.0") {
			sawChanged = true
		}
		if line.Kind == "added" && strings.Contains(line.NewText, "/owners") {
			sawAdded = true
		}
	}
	if !sawChanged || !sawAdded {
		t.Fatalf("expected changed and added diff rows, got %#v", diff.Lines)
	}
}

func TestGetOpenAPISyncSpecDiffFetchesConfiguredSource(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	initialSpec := `
openapi: 3.0.3
info:
  title: Spec Diff Remote API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
`
	remoteSpec := `
openapi: 3.0.3
info:
  title: Spec Diff Remote API
  version: 1.1.0
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200":
          description: OK
  /remote:
    get:
      summary: Remote endpoint
      responses:
        "200":
          description: OK
`
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("_") == "" {
			t.Errorf("expected cache-busting query parameter")
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, remoteSpec)
	}))
	defer server.Close()

	state, err = app.ImportCollection(workspace.ID, ImportPayload{
		Kind:        "openapi",
		Content:     initialSpec,
		SourceURL:   server.URL + "/openapi.yml",
		OpenAPISync: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	diff, err := app.GetOpenAPISyncSpecDiff(collection.ID, OpenAPISyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if diff.SourceURL != server.URL+"/openapi.yml" || diff.NoStoredSpec || hits != 1 {
		t.Fatalf("unexpected source diff result hits=%d diff=%#v", hits, diff)
	}
	if diff.Added != 1 || !strings.Contains(diff.NewContent, "/remote") || !strings.Contains(diff.StoredContent, "version: 1.0.0") {
		t.Fatalf("unexpected fetched diff content/counts: %#v", diff)
	}
}

func TestOpenAPILocalDriftDetectsAndAppliesCollectionChanges(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Local Drift API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List pets
      parameters:
        - name: page
          in: query
          schema:
            type: integer
            default: 1
        - name: X-Trace
          in: header
          schema:
            type: string
            default: spec-trace
      requestBody:
        content:
          application/json:
            example:
              id: 0
              name: ""
      responses:
        "200":
          description: OK
  /legacy:
    get:
      summary: Legacy request
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec, OpenAPISync: true})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	var pets, legacy RequestItem
	for _, item := range collection.Items {
		switch openAPIEndpointID(item) {
		case "GET:/pets":
			pets = item
		case "GET:/legacy":
			legacy = item
		}
	}
	if pets.ID == "" || legacy.ID == "" {
		t.Fatalf("expected imported pets and legacy requests: %#v", collection.Items)
	}

	params := append([]KeyValue(nil), pets.Params...)
	params = append(params, KeyValue{Name: "local", Value: "true", Enabled: true})
	state, err = app.UpdateRequest(collection.ID, pets.ID, RequestPatch{Params: &params})
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.DeleteRequest(collection.ID, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateRequest(collection.ID, "http", "Local only")
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	localOnly := collection.Items[len(collection.Items)-1]
	localMethod := http.MethodGet
	localURL := "{{baseUrl}}/local-only"
	state, err = app.UpdateRequest(collection.ID, localOnly.ID, RequestPatch{Method: &localMethod, URL: &localURL})
	if err != nil {
		t.Fatal(err)
	}

	drift, err := app.CheckOpenAPILocalDrift(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Modified != 1 || drift.Missing != 1 || drift.LocalOnly != 1 || !drift.HasChanges {
		t.Fatalf("unexpected local drift result: %#v", drift)
	}
	changes := map[string]string{}
	for _, change := range drift.Changes {
		changes[change.ID] = change.Change
	}
	if changes["GET:/pets"] != "modified" || changes["GET:/legacy"] != "missing" || changes["GET:/local-only"] != "local-only" {
		t.Fatalf("unexpected local drift changes: %#v", drift.Changes)
	}

	state, err = app.ApplyOpenAPILocalDrift(collection.ID, OpenAPILocalDriftOptions{
		ResetIDs:   []string{"GET:/pets"},
		RestoreIDs: []string{"GET:/legacy"},
		DeleteIDs:  []string{"GET:/local-only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	drift, err = app.CheckOpenAPILocalDrift(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Modified != 0 || drift.Missing != 0 || drift.LocalOnly != 0 || drift.HasChanges || drift.InSync != 2 {
		t.Fatalf("expected drift to be resolved, got %#v", drift)
	}
	var restoredLegacy, removedLocal bool
	for _, item := range collection.Items {
		switch openAPIEndpointID(item) {
		case "GET:/pets":
			if len(item.Params) != 1 || item.Params[0].Name != "page" {
				t.Fatalf("pets request was not reset to spec params: %#v", item.Params)
			}
		case "GET:/legacy":
			restoredLegacy = true
		case "GET:/local-only":
			removedLocal = true
		}
	}
	if !restoredLegacy || removedLocal {
		t.Fatalf("expected restored legacy and removed local-only request: %#v", collection.Items)
	}
}

func TestOpenAPILocalDriftIgnoresPreservedValues(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Local Drift Values API
paths:
  /pets:
    post:
      summary: Create pet
      parameters:
        - name: q
          in: query
          schema:
            type: string
            default: spec-q
        - name: X-Trace
          in: header
          schema:
            type: string
            default: spec-trace
      requestBody:
        content:
          application/json:
            example:
              id: 0
              name: ""
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec, OpenAPISync: true})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	var pet RequestItem
	for _, item := range collection.Items {
		if openAPIEndpointID(item) == "POST:/pets" {
			pet = item
			break
		}
	}
	if pet.ID == "" {
		t.Fatalf("expected imported pet request: %#v", collection.Items)
	}
	params := []KeyValue{{Name: "q", Value: "user-q", Enabled: false}}
	headers := []KeyValue{
		{Name: "x-trace", Value: "user-trace", Enabled: false},
		{Name: "content-type", Value: "application/json", Enabled: true},
	}
	body := pet.Body
	body.JSON = `{"id": 123, "name": "Milo"}`
	state, err = app.UpdateRequest(collection.ID, pet.ID, RequestPatch{Params: &params, Headers: &headers, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := app.CheckOpenAPILocalDrift(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Modified != 0 || drift.Missing != 0 || drift.LocalOnly != 0 || drift.HasChanges || drift.InSync != 1 {
		t.Fatalf("value-only edits should not count as drift: %#v", drift)
	}
}

func TestOpenAPISyncFetchCacheAndRejectsSwagger(t *testing.T) {
	dataDir := t.TempDir()
	app := newAppInDirForTest(t, dataDir)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[0]
	spec := `
openapi: 3.0.3
info:
  title: Remote Sync API
  version: 1.0.0
paths:
  /ping:
    get:
      summary: Ping
      responses:
        "200":
          description: OK
`
	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "openapi", Content: spec})
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("_") == "" {
			t.Errorf("expected cache-busting query parameter")
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, spec)
	}))
	defer server.Close()

	state, err = app.ConnectOpenAPISync(collection.ID, OpenAPISyncOptions{SourceURL: server.URL + "/openapi.yml", GroupBy: "path"})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("expected one remote fetch, got %d", hits)
	}
	collection = state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(collection.OpenAPI) != 1 || collection.OpenAPI[0].SourceURL != server.URL+"/openapi.yml" || collection.OpenAPI[0].GroupBy != "path" || !collection.OpenAPI[0].AutoCheck || collection.OpenAPI[0].AutoCheckInterval != 5 {
		t.Fatalf("unexpected openapi sync config: %#v", collection.OpenAPI)
	}
	metadataData, err := os.ReadFile(filepath.Join(dataDir, "specs", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string][]openAPISpecMetadataEntry
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		t.Fatal(err)
	}
	entries := metadata[filepath.Clean(collection.Path)]
	if len(entries) != 1 || entries[0].SourceURL != server.URL+"/openapi.yml" {
		t.Fatalf("unexpected cached spec metadata: %#v", metadata)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "specs", entries[0].Filename)); err != nil {
		t.Fatalf("cached spec file missing: %v", err)
	}
	swagger := `swagger: "2.0"
info:
  title: Old API
paths:
  /old:
    get:
      responses:
        "200":
          description: OK
`
	if _, err := app.CheckOpenAPISync(collection.ID, OpenAPISyncOptions{Content: swagger}); err == nil || !strings.Contains(err.Error(), "OpenAPI 3.x") {
		t.Fatalf("expected Swagger rejection, got %v", err)
	}
}

// TestKeyBindingPresetNormalizes is US-057's persistence half.
//
// An unrecognised preset id must be coerced rather than stored: kept as-is it
// would resolve to no overrides while the selector still showed it, leaving
// the user looking at a preset that is demonstrably not in effect.
func TestKeyBindingPresetNormalizes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"postman", "postman"},
		{"Postman", "postman"},
		{"  POSTMAN  ", "postman"},
		{"default", ""},
		{"", ""},
		{"nonsense", ""},
	} {
		if got := normalizeKeyBindingPreset(tc.in); got != tc.want {
			t.Errorf("normalizeKeyBindingPreset(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKeyBindingPresetPersists(t *testing.T) {
	app := newAppForTest(t)

	state, err := app.UpdatePreferences(Preferences{KeyBindingPreset: "postman"})
	if err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}
	if state.Preferences.KeyBindingPreset != "postman" {
		t.Fatalf("preset = %q, want postman", state.Preferences.KeyBindingPreset)
	}

	// And it survives a reload, or selecting a preset would silently revert on
	// the next launch. The flush is required: writes are deferred behind a
	// dirty flag, so reading the directory without it races the write and the
	// test would fail for a reason that has nothing to do with the preset.
	if err := app.FlushPendingWrites(); err != nil {
		t.Fatalf("FlushPendingWrites: %v", err)
	}
	// newAppInDirForTest, not NewAppWithDir: a raw constructor leaves a
	// background persist writer running past the test, writing into a
	// t.TempDir() that cleanup is concurrently removing. There is a guard test
	// for exactly this, and it caught my first attempt.
	reloaded := newAppInDirForTest(t, app.dataDir)
	loaded, err := reloaded.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if loaded.Preferences.KeyBindingPreset != "postman" {
		t.Errorf("after reload preset = %q, want postman", loaded.Preferences.KeyBindingPreset)
	}

	// Switching back clears it rather than storing "default", so the zero value
	// and the default preset are the same thing on disk.
	back, err := reloaded.UpdatePreferences(Preferences{KeyBindingPreset: "default"})
	if err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}
	if back.Preferences.KeyBindingPreset != "" {
		t.Errorf("preset = %q, want empty for the default", back.Preferences.KeyBindingPreset)
	}
}

// sha256Hex and awsV4SSOCacheFilename moved to internal/auth/awsv4 with the code
// they belong to. These two tests are *App integration tests and stay here, so
// they carry their own copies -- three lines each, and a test recomputing the
// value it expects independently of the code under test is no bad thing.

// The SigV4 assertions live in internal/auth/awsv4 now, where the
// canonicalisation they depend on lives. These two *App tests drive the app's
// full request path, so they stay here and call the package's own verifier
// rather than carrying a second copy of the algorithm.
func assertAWSV4Signature(t *testing.T, r *http.Request, accessKeyID, secretAccessKey, region, service string) {
	t.Helper()
	if err := awsv4.VerifySignature(r, accessKeyID, secretAccessKey, region, service); err != nil {
		t.Fatal(err)
	}
}
