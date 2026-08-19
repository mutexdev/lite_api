package scripting

// The variable scope a script sees: building it, merging it, and prompt variables.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

type VariableContext struct {
	Runtime    map[string]interface{}
	Env        map[string]interface{}
	Global     map[string]interface{}
	Collection map[string]interface{}
	Folder     map[string]interface{}
	Request    map[string]interface{}
	// Data is the current iteration's row from a runner data file (US-046).
	Data       map[string]interface{}
	Prompt     map[string]interface{}
	ProcessEnv map[string]string
	Combined   map[string]string

	RuntimeDirty    bool
	EnvDirty        bool
	GlobalDirty     bool
	CollectionDirty bool
}

func BuildVariableMap(globalEnvs []types.Environment, collection *types.Collection, environmentID string, item types.RequestItem, workspacePath ...string) map[string]string {
	vars := map[string]string{}
	addVars := func(values []types.Variable) {
		for _, variable := range values {
			if !variable.Enabled || variable.Name == "" {
				continue
			}
			vars[variable.Name] = fmt.Sprint(variable.Value)
		}
	}
	for _, env := range globalEnvs {
		addVars(env.Variables)
	}
	addVars(collection.Variables)
	if environmentID != "" {
		for _, env := range collection.Environments {
			if env.ID == environmentID {
				addVars(env.Variables)
				break
			}
		}
	}
	for _, folder := range FolderChain(*collection, item) {
		addVars(folder.Variables)
	}
	addVars(item.Vars.Req)
	addVars(collection.RuntimeVariables)
	addProcessEnvVars(vars, ProcessEnvForCollection(collection, firstString(workspacePath)))
	return vars
}

func NewScriptVariableContext(globalEnvs []types.Environment, collection *types.Collection, environmentID string, item types.RequestItem, promptValues map[string]string, workspacePath ...string) *VariableContext {
	ctx := &VariableContext{
		Runtime:    map[string]interface{}{},
		Env:        map[string]interface{}{},
		Global:     map[string]interface{}{},
		Collection: map[string]interface{}{},
		Folder:     map[string]interface{}{},
		Request:    map[string]interface{}{},
		Data:       map[string]interface{}{},
		Prompt:     promptVariableMap(promptValues),
		ProcessEnv: ProcessEnvForCollection(collection, firstString(workspacePath)),
		Combined:   map[string]string{},
	}
	for _, env := range globalEnvs {
		mergeVariableMap(ctx.Global, env.Variables)
	}
	if collection != nil {
		mergeVariableMap(ctx.Collection, collection.Variables)
		mergeVariableMap(ctx.Runtime, collection.RuntimeVariables)
		for _, folder := range FolderChain(*collection, item) {
			mergeVariableMap(ctx.Folder, folder.Variables)
		}
		if environmentID != "" {
			for _, env := range collection.Environments {
				if env.ID == environmentID {
					mergeVariableMap(ctx.Env, env.Variables)
					break
				}
			}
		}
	}
	mergeVariableMap(ctx.Request, item.Vars.Req)
	ctx.Recompute()
	return ctx
}

func NewFlatScriptVariableContext(vars map[string]string) *VariableContext {
	ctx := &VariableContext{
		Runtime:    map[string]interface{}{},
		Env:        map[string]interface{}{},
		Global:     map[string]interface{}{},
		Collection: map[string]interface{}{},
		Folder:     map[string]interface{}{},
		Request:    map[string]interface{}{},
		Prompt:     map[string]interface{}{},
		ProcessEnv: interp.ProcessEnvFromCombinedVars(vars),
		Combined:   vars,
	}
	for name, value := range vars {
		if strings.HasPrefix(name, interp.ProcessEnvPrefix) {
			continue
		}
		ctx.Runtime[name] = value
	}
	ctx.Recompute()
	return ctx
}

func ScriptVariableContextForItem(parent *VariableContext, collection *types.Collection, environmentID string, item types.RequestItem) *VariableContext {
	if parent == nil {
		return NewScriptVariableContext(nil, collection, environmentID, item, nil)
	}
	ctx := &VariableContext{
		Runtime:         parent.Runtime,
		Env:             parent.Env,
		Global:          parent.Global,
		Collection:      parent.Collection,
		Folder:          map[string]interface{}{},
		Request:         map[string]interface{}{},
		Prompt:          parent.Prompt,
		ProcessEnv:      parent.ProcessEnv,
		Combined:        map[string]string{},
		RuntimeDirty:    parent.RuntimeDirty,
		EnvDirty:        parent.EnvDirty,
		GlobalDirty:     parent.GlobalDirty,
		CollectionDirty: parent.CollectionDirty,
	}
	if collection != nil {
		for _, folder := range FolderChain(*collection, item) {
			mergeVariableMap(ctx.Folder, folder.Variables)
		}
	}
	mergeVariableMap(ctx.Request, item.Vars.Req)
	ctx.Recompute()
	return ctx
}

func ScriptMergeVariableContext(parent, child *VariableContext) {
	if parent == nil || child == nil {
		return
	}
	parent.RuntimeDirty = parent.RuntimeDirty || child.RuntimeDirty
	parent.EnvDirty = parent.EnvDirty || child.EnvDirty
	parent.GlobalDirty = parent.GlobalDirty || child.GlobalDirty
	parent.CollectionDirty = parent.CollectionDirty || child.CollectionDirty
	parent.Recompute()
}

func mergeVariableMap(target map[string]interface{}, values []types.Variable) {
	for _, variable := range values {
		if !variable.Enabled || variable.Name == "" {
			continue
		}
		target[variable.Name] = variable.Value
	}
}

func promptVariableMap(promptValues map[string]string) map[string]interface{} {
	out := map[string]interface{}{}
	for key, value := range promptValues {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "?") {
			name = "?" + name
		}
		out[name] = value
	}
	return out
}

func scanBodyPromptVariables(body types.RequestBody, scanText func(string), scanKeyValues func([]types.KeyValue)) {
	switch body.Mode {
	case "json":
		scanText(body.JSON)
	case "xml":
		scanText(body.XML)
	case "graphql":
		scanText(body.GraphQLQuery)
		scanText(body.GraphQLVariables)
	case "text", "sparql":
		scanText(body.Text)
	case "formUrlEncoded":
		scanKeyValues(body.FormURLEncoded)
	case "multipartForm":
		for _, part := range body.Multipart {
			if !part.Enabled {
				continue
			}
			scanText(part.Name)
			scanText(part.Value)
			scanText(part.FilePath)
			scanText(part.ContentType)
		}
	case "file":
		scanText(body.FilePath)
		scanText(body.FileContentType)
		for _, file := range types.FileBodyEntriesOf(body) {
			scanText(file.FilePath)
			scanText(file.ContentType)
		}
	}
}

func scanAuthPromptVariables(auth types.AuthConfig, scanText func(string), scanKeyValues func([]types.KeyValue)) {
	scanText(auth.Mode)
	scanText(auth.Username)
	scanText(auth.Password)
	scanText(auth.Domain)
	scanText(auth.Token)
	scanText(auth.APIKey)
	scanText(auth.APIValue)
	scanText(auth.APILocation)

	oauth1 := auth.OAuth1
	scanText(oauth1.ConsumerKey)
	scanText(oauth1.ConsumerSecret)
	scanText(oauth1.AccessToken)
	scanText(oauth1.AccessTokenSecret)
	scanText(oauth1.CallbackURL)
	scanText(oauth1.Verifier)
	scanText(oauth1.SignatureMethod)
	scanText(oauth1.PrivateKey)
	scanText(oauth1.PrivateKeyType)
	scanText(oauth1.Timestamp)
	scanText(oauth1.Nonce)
	scanText(oauth1.Version)
	scanText(oauth1.Realm)
	scanText(oauth1.Placement)

	oauth2 := auth.OAuth2
	scanText(oauth2.GrantType)
	scanText(oauth2.CallbackURL)
	scanText(oauth2.AuthorizationURL)
	scanText(oauth2.AccessTokenURL)
	scanText(oauth2.RefreshTokenURL)
	scanText(oauth2.Username)
	scanText(oauth2.Password)
	scanText(oauth2.ClientID)
	scanText(oauth2.ClientSecret)
	scanText(oauth2.Scope)
	scanText(oauth2.State)
	scanText(oauth2.CredentialsPlacement)
	scanText(oauth2.CredentialsID)
	scanText(oauth2.TokenSource)
	scanText(oauth2.TokenPlacement)
	scanText(oauth2.TokenHeaderPrefix)
	scanText(oauth2.TokenQueryKey)
	scanOAuth2PromptParams(oauth2.AuthorizationAdditionalParams, scanText)
	scanOAuth2PromptParams(oauth2.TokenAdditionalParams, scanText)
	scanOAuth2PromptParams(oauth2.RefreshAdditionalParams, scanText)
	scanKeyValues(oauth2.AdditionalParams)

	awsv4 := auth.AWSV4
	scanText(awsv4.AccessKeyID)
	scanText(awsv4.SecretAccessKey)
	scanText(awsv4.SessionToken)
	scanText(awsv4.Service)
	scanText(awsv4.Region)
	scanText(awsv4.ProfileName)
	scanText(awsv4.AccessKey)
	scanText(awsv4.SecretKey)
}

func RunnerPromptVariableSkipMessage(prompts []string) string {
	if len(prompts) == 0 {
		return "Request has been skipped due to containing prompt variables"
	}
	return fmt.Sprintf("Prompt variables detected in request. Runner execution is not supported for requests with prompt variables. Prompts: %s", strings.Join(prompts, ", "))
}

func ScriptVariableString(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		if encoded, err := json.Marshal(typed); err == nil && (strings.HasPrefix(string(encoded), "{") || strings.HasPrefix(string(encoded), "[")) {
			return string(encoded)
		}
		return fmt.Sprint(typed)
	}
}

func mergeScriptVariablesIntoSlice(existing []types.Variable, values map[string]interface{}) []types.Variable {
	next := []types.Variable{}
	seen := map[string]bool{}
	for _, variable := range existing {
		if variable.Name == "" {
			continue
		}
		value, ok := values[variable.Name]
		if !ok {
			if !variable.Enabled {
				next = append(next, variable)
			}
			continue
		}
		variable.Value = value
		variable.Enabled = true
		variable.DataType = scriptVariableDataType(value)
		if variable.Type == "" {
			variable.Type = variable.DataType
		}
		next = append(next, variable)
		seen[variable.Name] = true
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		dataType := scriptVariableDataType(values[name])
		next = append(next, types.Variable{
			ID:       scalar.NewID("var"),
			Name:     name,
			Value:    values[name],
			Type:     dataType,
			DataType: dataType,
			Enabled:  true,
		})
	}
	return next
}

func scriptVariableDataType(value interface{}) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case int, int64, float32, float64, json.Number:
		return "number"
	case map[string]interface{}, []interface{}:
		return "json"
	default:
		return "string"
	}
}

func ApplyScriptVariableContextToState(state *types.AppState, workspace *types.Workspace, collection *types.Collection, environmentID string, ctx *VariableContext) {
	if state == nil || collection == nil || ctx == nil {
		return
	}
	now := time.Now()
	if ctx.CollectionDirty {
		collection.Variables = mergeScriptVariablesIntoSlice(collection.Variables, ctx.Collection)
		collection.UpdatedAt = now
	}
	if ctx.RuntimeDirty {
		collection.RuntimeVariables = mergeScriptVariablesIntoSlice(collection.RuntimeVariables, ctx.Runtime)
		collection.UpdatedAt = now
	}
	if ctx.EnvDirty && environmentID != "" {
		for index := range collection.Environments {
			if collection.Environments[index].ID == environmentID {
				collection.Environments[index].Variables = mergeScriptVariablesIntoSlice(collection.Environments[index].Variables, ctx.Env)
				collection.UpdatedAt = now
				break
			}
		}
	}
	if ctx.GlobalDirty {
		if workspace == nil {
			workspace, _ = FindWorkspaceForCollection(state, collection.ID)
		}
		if workspace == nil {
			return
		}
		if len(workspace.GlobalEnvironments) == 0 {
			workspace.GlobalEnvironments = append(workspace.GlobalEnvironments, types.Environment{
				ID:    scalar.NewID("global-env"),
				Name:  "Global",
				Color: "#2f8cff",
			})
			workspace.ActiveGlobalEnvironmentID = workspace.GlobalEnvironments[0].ID
		}
		if !WorkspaceHasGlobalEnvironment(*workspace, workspace.ActiveGlobalEnvironmentID) {
			workspace.ActiveGlobalEnvironmentID = workspace.GlobalEnvironments[0].ID
		}
		for index := range workspace.GlobalEnvironments {
			if workspace.GlobalEnvironments[index].ID != workspace.ActiveGlobalEnvironmentID {
				continue
			}
			workspace.GlobalEnvironments[index].Variables = mergeScriptVariablesIntoSlice(workspace.GlobalEnvironments[index].Variables, ctx.Global)
			workspace.UpdatedAt = now
			return
		}
	}
}

var promptVariableInterpolationPattern = regexp.MustCompile(`\{\{\?([^{}\s](?:[^{}]*[^{}\s])?)\}\}`)
