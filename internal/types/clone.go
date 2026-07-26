// Deep copies of the request document and its parts.
//
// US-060 follow-on. These live with the types they copy: every one exists so a
// caller can edit a copy without the original changing underneath it, and that
// contract is a property of the type, not of whoever is copying it.
package types

import "strings"

func CloneVariables(values []Variable) []Variable {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]Variable, len(values))
	copy(cloned, values)
	return cloned
}

func CloneOAuth2AdditionalParams(values []OAuth2AdditionalParam) []OAuth2AdditionalParam {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]OAuth2AdditionalParam, len(values))
	copy(cloned, values)
	return cloned
}

func CloneAuthConfig(auth AuthConfig) AuthConfig {
	auth.OAuth2.AuthorizationAdditionalParams = CloneOAuth2AdditionalParams(auth.OAuth2.AuthorizationAdditionalParams)
	auth.OAuth2.TokenAdditionalParams = CloneOAuth2AdditionalParams(auth.OAuth2.TokenAdditionalParams)
	auth.OAuth2.RefreshAdditionalParams = CloneOAuth2AdditionalParams(auth.OAuth2.RefreshAdditionalParams)
	auth.OAuth2.AdditionalParams = CloneKeyValues(auth.OAuth2.AdditionalParams)
	return auth
}

func CloneRequestVars(vars RequestVars) RequestVars {
	return RequestVars{
		Req: CloneVariables(vars.Req),
		Res: CloneVariables(vars.Res),
	}
}

func CloneRequestBody(body RequestBody) RequestBody {
	body.FormURLEncoded = CloneKeyValues(body.FormURLEncoded)
	body.Multipart = CloneFormParts(body.Multipart)
	body.Files = CloneFileBodyEntries(body.Files)
	return body
}

func CloneAssertions(values []Assertion) []Assertion {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]Assertion, len(values))
	copy(cloned, values)
	return cloned
}

func CloneGrpcMessages(values []GrpcMessage) []GrpcMessage {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]GrpcMessage, len(values))
	copy(cloned, values)
	return cloned
}

func CloneWSMessages(values []WSMessage) []WSMessage {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]WSMessage, len(values))
	copy(cloned, values)
	return cloned
}

func CloneTags(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func CloneResponseExamples(values []ResponseExample) []ResponseExample {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]ResponseExample, len(values))
	for i := range values {
		cloned[i] = CloneResponseExample(values[i])
	}
	return cloned
}

func CloneFolderConfigForFolderClone(folder FolderConfig) FolderConfig {
	folder.Headers = CloneKeyValues(folder.Headers)
	folder.Variables = CloneVariables(folder.Variables)
	folder.ResVariables = CloneVariables(folder.ResVariables)
	folder.Auth = CloneAuthConfig(folder.Auth)
	return folder
}

func CloneRequestItemForFolderClone(item RequestItem) RequestItem {
	item.Params = CloneKeyValues(item.Params)
	item.PathParams = CloneKeyValues(item.PathParams)
	item.Headers = CloneKeyValues(item.Headers)
	item.Body = CloneRequestBody(item.Body)
	item.GrpcMessages = CloneGrpcMessages(item.GrpcMessages)
	item.WSMessages = CloneWSMessages(item.WSMessages)
	item.Auth = CloneAuthConfig(item.Auth)
	item.Vars = CloneRequestVars(item.Vars)
	item.Assertions = CloneAssertions(item.Assertions)
	item.Tags = CloneTags(item.Tags)
	item.Examples = CloneResponseExamples(item.Examples)
	item.Response = nil
	item.Timeline = nil
	return item
}

func CloneResponseExample(example ResponseExample) ResponseExample {
	example.Request.Headers = CloneKeyValues(example.Request.Headers)
	example.Request.Params = CloneKeyValues(example.Request.Params)
	example.Request.FormURLEncoded = CloneKeyValues(example.Request.FormURLEncoded)
	example.Request.MultipartForm = CloneFormParts(example.Request.MultipartForm)
	example.Request.File = CloneFileBodyEntries(example.Request.File)
	example.Response.Headers = CloneKeyValues(example.Response.Headers)
	return example
}

// EnabledKeyValues moved here from internal/grpcexec. It landed there first
// only because gRPC happened to be extracted before anything else that needed
// it -- it operates on KeyValue and has nothing to do with gRPC. Same mistake
// shape as applyWSSEHeader in US-063: the compiler tells you what depends on
// what, never where something belongs.
func EnabledKeyValues(rows []KeyValue) []KeyValue {
	result := []KeyValue{}
	for _, row := range rows {
		if row.Enabled && strings.TrimSpace(row.Name) != "" {
			result = append(result, KeyValue{Name: strings.TrimSpace(row.Name), Value: row.Value, Enabled: true})
		}
	}
	return result
}
