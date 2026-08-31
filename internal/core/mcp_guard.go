package core

// The secret-injection refusal, and the secret-name machinery the run tier and
// the write tier share.
//
// WHAT USED TO BE HERE, AND WHY IT IS NOT. This file held the shipped new-host
// guard: every secret variable carried a host allowlist learned from the
// requests that already used it, and an agent-initiated run that would resolve
// a secret into a request aimed at a host outside that allowlist blocked for
// approval. Phase 6 replaced it wholesale with the destination boundary
// (mcp_policy.go, §1.2), and the replacement is strictly stronger in the three
// ways that mattered:
//
//   - IT CHECKS EVERY EGRESS, not the one URL a pre-send scan could see. The old
//     guard reasoned about the request's DEFINITION and ran once, before the
//     send, so a pre-request script that rewrote req.url after it ran retargeted
//     the credential unseen, and a redirect hop was never re-checked. The
//     boundary sits at the egress itself — the main checkpoint, the guard
//     transport under it, the script shims, the OAuth and AWS checkpoints — so
//     the script and the redirect are checked like everything else.
//   - IT IS PORT-EXACT AND SCHEME-EXACT. The old guard compared bare hostnames
//     with the port deliberately dropped, so :3000 and :8080 on one host were one
//     trust decision (§1.4(9) records the fix).
//   - ITS APPROVALS ARE SITE-SCOPED. A remembered (secret, host) pair authorized
//     that pair everywhere; a §6 approval names the workspace, collection,
//     request, selected environment, active globals, origin and kind class.
//
// WHAT SURVIVED, AND WHY. Two things this file did are not the host guard's
// question and are still worth asking:
//
//  1. THE SECRET-INJECTION REFUSAL (§8's "retained read-boundary refusals"). An
//     agent-supplied VALUE that resolves to a secret is refused outright, before
//     any destination is computed. It is not a destination question at all — the
//     destination may be perfectly legitimate — it is the read boundary: an agent
//     that cannot READ a secret must not be able to WRITE one.
//  2. THE SECRET-NAME MACHINERY. Which names are secret in a scope, and which of
//     them a request's authored fields reference, still drives the advisory
//     secret list on an approval prompt (§6) and the write tier's refusal to
//     author a secret row.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mutexdev/lite_api/internal/interp"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpTemplatePattern matches a {{reference}} and captures the trimmed name.
//
// Non-greedy up to the first closing brace, and the character class excludes `}`
// so "{{a}}{{b}}" yields two matches rather than one spanning both. Whitespace
// inside the braces is tolerated because interpolation tolerates it: "{{ token }}"
// resolves, so it must also be SEEN here — a refusal that missed the spaced form
// would be bypassable by adding a space.
var mcpTemplatePattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// mcpSecretNamesInScope is the set of variable names that resolve to a SECRET
// for this run: the workspace's active global environment, the collection's own
// variables, the chosen collection environment, and the request's own vars.
//
// This is the same set of sources scripting.BuildVariableMap draws from, in the
// same order, because "is this name a secret" has to agree with "which value
// would this name resolve to". A name declared secret in one scope and plain in
// another is treated as secret — the conservative direction, and the only one
// that cannot leak.
func mcpSecretNamesInScope(globalEnvironments []types.Environment, collection types.Collection, environmentID string, item types.RequestItem) map[string]bool {
	secrets := map[string]bool{}
	collect := func(variables []types.Variable) {
		for _, variable := range variables {
			name := strings.TrimSpace(variable.Name)
			if name == "" || !variable.Secret {
				continue
			}
			secrets[name] = true
		}
	}
	for _, environment := range globalEnvironments {
		collect(environment.Variables)
	}
	collect(collection.Variables)
	if environmentID != "" {
		for _, environment := range collection.Environments {
			if environment.ID == environmentID {
				collect(environment.Variables)
				break
			}
		}
	}
	for _, folder := range scripting.FolderChain(collection, item) {
		collect(folder.Variables)
	}
	collect(item.Vars.Req)
	return secrets
}

// mcpRequestTemplateFields is every authored string of a request that can carry
// a {{reference}} and end up on the wire.
//
// HEADER AND PARAM NAMES ARE INCLUDED, not just their values. A header whose
// NAME is "{{tokenHeader}}" is unusual but legal, and a scan that only read
// values would miss a secret riding in one. Scripts are deliberately NOT here:
// a script that mentions a secret does not necessarily send it, and what this
// list feeds is advisory prompt text rather than an enforcement decision.
func mcpRequestTemplateFields(item types.RequestItem) []string {
	fields := []string{item.URL, item.Method}
	addRows := func(rows []types.KeyValue) {
		for _, row := range rows {
			if !row.Enabled {
				continue
			}
			fields = append(fields, row.Name, row.Value)
		}
	}
	addRows(item.Headers)
	addRows(item.Params)
	addRows(item.PathParams)
	fields = append(fields,
		item.Body.Text, item.Body.JSON, item.Body.XML,
		item.Body.GraphQLQuery, item.Body.GraphQLVariables,
	)
	addRows(item.Body.FormURLEncoded)
	for _, part := range item.Body.Multipart {
		if part.Enabled {
			fields = append(fields, part.Name, part.Value)
		}
	}
	fields = append(fields, mcpAuthTemplateFields(item.Auth)...)
	return fields
}

// mcpAuthTemplateFields is every auth field that can carry a template. Listed
// exhaustively rather than reflected over: a new credential field on AuthConfig
// should be a deliberate addition here, and reflection would silently include
// (or silently miss) whatever the struct grows next.
func mcpAuthTemplateFields(auth types.AuthConfig) []string {
	return []string{
		auth.Username, auth.Password, auth.Domain, auth.Token,
		auth.APIKey, auth.APIValue,
		auth.OAuth2.ClientID, auth.OAuth2.ClientSecret, auth.OAuth2.Username,
		auth.OAuth2.Password, auth.OAuth2.Scope, auth.OAuth2.AccessTokenURL,
		auth.OAuth2.AuthorizationURL, auth.OAuth2.RefreshTokenURL,
		auth.OAuth1.ConsumerKey, auth.OAuth1.ConsumerSecret,
		auth.OAuth1.AccessToken, auth.OAuth1.AccessTokenSecret,
		auth.AWSV4.AccessKeyID, auth.AWSV4.SecretAccessKey, auth.AWSV4.SessionToken,
	}
}

// mcpReferencedSecrets returns the secret names an effective request references,
// sorted so errors and prompts read the same way twice.
func mcpReferencedSecrets(item types.RequestItem, secretsInScope map[string]bool) []string {
	if len(secretsInScope) == 0 {
		return nil
	}
	found := map[string]bool{}
	for _, field := range mcpRequestTemplateFields(item) {
		if field == "" || !strings.Contains(field, "{{") {
			continue
		}
		for _, match := range mcpTemplatePattern.FindAllStringSubmatch(field, -1) {
			name := strings.TrimSpace(match[1])
			if secretsInScope[name] {
				found[name] = true
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mcpGuardCollection is one collection copied out from under the state lock,
// together with the two things resolving its requests needs from its workspace.
type mcpGuardCollection struct {
	collection         types.Collection
	globalEnvironments []types.Environment
	workspacePath      string
}

// mcpGuardInput is everything ONE run contributes to the secret-injection
// refusal beyond the request's own definition. It is a struct rather than three
// parameters because the three are only ever passed together, and because the
// distinction between the first two is load-bearing enough to want a name on
// each.
type mcpGuardInput struct {
	// overrides is what the send will actually apply (runner.Iteration.Data).
	// For run_request these ARE the agent's own variables; for run_flow they are
	// the step's USER-AUTHORED vars after flow-scope interpolation. They are the
	// map an agent-supplied value is walked THROUGH, not the thing judged.
	overrides map[string]string
	// agentValues is the subset the AGENT itself chose: run_request's variables,
	// run_flow's inputs. This is what is judged.
	//
	// THE SPLIT MATTERS AND IS NOT COSMETIC. A flow step var of
	// {"token": "{{apiToken}}"} is legitimate and documented — the USER wrote
	// that reference into the flow, and flow scope deliberately never resolves
	// it (flow_run.go's header), so the braces travel to the send path and the
	// credential is resolved there, inside LiteAPI. Refusing THAT would break
	// the flow tier's central promise. What must be refused is the same
	// reference arriving from the agent, which for a flow means an INPUT value,
	// not the derived override it ends up inside.
	agentValues map[string]string
	// secretValues is the process's hydrated secret values, fetched by the
	// caller before the run, for the backstop below.
	secretValues []string
}

// mcpTemplateWalkDepth bounds the transitive walk of an agent-supplied value
// through the variable map. It matches interp's own maxPasses because that is
// how far the send path will chase a nested template: a chain the interpolator
// cannot finish is a chain that cannot deliver a credential either.
const mcpTemplateWalkDepth = 8

// mcpMinComparableSecretLength mirrors MaskKnownSecretValues' own threshold
// (internal/mcpserver/redact.go). A value shorter than this matches too much
// ordinary text to attribute anything to — refusing every override that happens
// to contain "1234" would be noise, not safety — and a secret that short is not
// protected by value comparison anyway. The NAME walk below does not depend on
// value length, so the normal smuggling route stays closed regardless.
const mcpMinComparableSecretLength = 8

// enforceMCPSecretInjection is the read-boundary check every MCP-initiated
// execution runs before it starts: RunRequest once, RunFlow once per step.
//
// IT IS NOT A DESTINATION CHECK. Where the run may send anything is the
// destination boundary's question and is answered at every egress
// (mcp_policy.go). This one asks whether the agent's own inputs are trying to
// put a credential INTO the run, which no destination decision can make safe.
//
// Returns nil to proceed, an ErrDenied-wrapped error to refuse. The message
// names the offending variable and the secret NAMES — never a value, which is
// what makes it safe to hand back to the agent verbatim.
func (a *App) enforceMCPSecretInjection(plan mcpRunPlan, input mcpGuardInput) error {
	// The variable map the send path will resolve against: the run's own scope
	// overlaid with the overrides. The walk below chases an agent-supplied
	// value through it exactly as interp will.
	return mcpRefuseSecretInjectingValues(plan, mcpEffectiveGuardVars(plan.vars, input.overrides), input)
}

// mcpEffectiveGuardVars is the run's resolved scope overlaid with its overrides:
// the map the send path will interpolate every field against.
func mcpEffectiveGuardVars(vars, overrides map[string]string) map[string]string {
	effective := make(map[string]string, len(vars)+len(overrides))
	for name, value := range vars {
		effective[name] = value
	}
	for name, value := range overrides {
		effective[name] = value
	}
	return effective
}

// mcpRefuseSecretInjectingValues refuses a run whose AGENT-SUPPLIED values would
// inject a secret into it.
//
// WHY REFUSE RATHER THAN PROMPT. mcpValidatedOverrides (mcp_run.go) already
// makes this argument for override NAMES: an agent that cannot READ a secret
// must not be able to WRITE one, because that inverts the whole boundary. The
// argument applies unchanged to VALUES. interp.Interpolate is multi-pass, so
// {"smuggle": "{{apiToken}}"} is not data — it is a second-order reference that
// the send path will chase to the real credential. There is no legitimate run
// that needs it: a request references a secret because the USER wrote that
// reference into the request definition, and the way for an agent to use that
// credential is to run the request as authored. So this refuses outright rather
// than routing to an approval prompt, which would ask the user to bless a shape
// that has no honest use.
//
// TWO CHECKS, IN ORDER. The name walk follows the value's tokens through the
// effective map — transitively ({"a": "{{b}}"} where b is "{{apiToken}}"),
// cycle-safe (a name is expanded once) and depth-bounded. The backstop then
// interpolates the value for real and compares the result against the process's
// known secret values, which catches routes no name walk can see: an ordinary
// variable whose VALUE literally contains the credential, or a
// {{process.env.X}} form that interp resolves outside the variable map
// entirely.
func mcpRefuseSecretInjectingValues(plan mcpRunPlan, effective map[string]string, input mcpGuardInput) error {
	for _, key := range mcpSortedNames(mcpMapKeys(input.agentValues)) {
		value := input.agentValues[key]
		if names := mcpSecretsReachedByTemplate(value, effective, plan.secretsInScope); len(names) > 0 {
			return mcpSecretInjectionRefusal(key, names)
		}
		resolved := value
		if strings.Contains(value, "{{") {
			resolved = interp.Interpolate(value, effective)
		}
		if !mcpContainsKnownSecretValue(resolved, input.secretValues) {
			continue
		}
		// The resolved text is NEVER put in the message; only the names whose
		// values it turned out to contain, and only when they can be attributed.
		return mcpSecretInjectionRefusal(key, mcpSecretNamesResolvingInto(resolved, plan))
	}
	return nil
}

// mcpSecretInjectionRefusal writes the refusal for the agent that reads it: what
// was refused, why no approval can unlock it, and what to do instead.
func mcpSecretInjectionRefusal(key string, names []string) error {
	reached := "a value this workspace holds as a secret"
	if len(names) > 0 {
		reached = "the secret " + mcpJoinSecretNames(names)
	}
	return fmt.Errorf("%w: the value you supplied for %q resolves to %s, and a value you supply may not inject a secret into a run. A request references a secret because the USER wrote that reference into the request definition; the fix is to run the request as authored and let LiteAPI resolve the credential itself, never to pass the credential in yourself. Drop this variable and run again; do not retry it and do not work around it",
		mcpserver.ErrDenied, key, reached)
}

// mcpSecretsReachedByTemplate walks the {{tokens}} of one value through the
// effective variable map and returns every secret name it can reach.
//
// mcpTemplatePattern is reused rather than a second regex written: two patterns
// for "what is a template reference" is exactly the drift that lets one of them
// miss the spaced form and become the bypass.
func mcpSecretsReachedByTemplate(value string, effective map[string]string, secretsInScope map[string]bool) []string {
	if len(secretsInScope) == 0 || !strings.Contains(value, "{{") {
		return nil
	}
	found := map[string]bool{}
	// Keyed by NAME, so a cycle ({"a": "{{b}}"} with b = "{{a}}") expands each
	// name once and terminates regardless of the depth bound.
	visited := map[string]bool{}
	frontier := []string{value}
	for depth := 0; depth < mcpTemplateWalkDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, text := range frontier {
			if !strings.Contains(text, "{{") {
				continue
			}
			for _, match := range mcpTemplatePattern.FindAllStringSubmatch(text, -1) {
				name := strings.TrimSpace(match[1])
				if secretsInScope[name] {
					found[name] = true
					continue
				}
				if visited[name] {
					continue
				}
				visited[name] = true
				if hop, ok := effective[name]; ok {
					next = append(next, hop)
				}
			}
		}
		frontier = next
	}
	return mcpSortedNames(mcpMapKeys(found))
}

// mcpContainsKnownSecretValue reports whether text carries a hydrated secret
// value verbatim.
func mcpContainsKnownSecretValue(text string, secretValues []string) bool {
	if text == "" {
		return false
	}
	for _, value := range secretValues {
		if len(value) < mcpMinComparableSecretLength {
			continue
		}
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

// mcpSecretNamesResolvingInto attributes a resolved string to the secret names
// in scope whose values it contains, so the refusal can say which credential was
// reached. It can legitimately come back empty — a process.env value has no name
// in this scope — and the refusal has wording for that case.
func mcpSecretNamesResolvingInto(resolved string, plan mcpRunPlan) []string {
	var names []string
	for name := range plan.secretsInScope {
		value := plan.vars[name]
		if len(value) < mcpMinComparableSecretLength {
			continue
		}
		if strings.Contains(resolved, value) {
			names = append(names, name)
		}
	}
	return mcpSortedNames(names)
}

// mcpGuardCollections copies every open collection out from under the state
// lock, with the workspace context each one needs.
//
// Copied whole rather than walked in place for the reason the top of
// mcp_backend.go gives: nothing that resolves a variable map, reads a .env file
// or parses a URL may happen while a.mu is held, and all three happen on the
// walk this feeds.
func (a *App) mcpGuardCollections() ([]mcpGuardCollection, error) {
	var out []mcpGuardCollection
	if err := a.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			workspace := &state.Workspaces[wi]
			globals := scripting.ActiveGlobalEnvironmentsForWorkspace(*workspace)
			for ci := range workspace.Collections {
				out = append(out, mcpGuardCollection{
					collection:         mcpCollectionCopy(workspace.Collections[ci]),
					globalEnvironments: mcpEnvironmentCopies(globals),
					workspacePath:      workspace.Path,
				})
			}
		}
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// mcpCollectionCopy deep-copies the parts of a collection the authoring guard
// resolves against. Not the whole struct: docs and OpenAPI configs take no part
// in "where would this request's URL point", and copying them would be pure cost
// on a path that already walks every request in the workspace.
func mcpCollectionCopy(collection types.Collection) types.Collection {
	out := types.Collection{
		ID:               collection.ID,
		Name:             collection.Name,
		Path:             collection.Path,
		Auth:             types.CloneAuthConfig(collection.Auth),
		Variables:        types.CloneVariables(collection.Variables),
		RuntimeVariables: types.CloneVariables(collection.RuntimeVariables),
		Headers:          append([]types.KeyValue{}, collection.Headers...),
		Folders:          mcpFolderCopies(collection.Folders),
	}
	out.Environments = mcpEnvironmentCopies(collection.Environments)
	out.Items = make([]types.RequestItem, 0, len(collection.Items))
	for _, item := range collection.Items {
		out.Items = append(out.Items, mcpItemCopy(item))
	}
	return out
}

func mcpEnvironmentCopies(environments []types.Environment) []types.Environment {
	if len(environments) == 0 {
		return nil
	}
	out := make([]types.Environment, 0, len(environments))
	for _, environment := range environments {
		copied := environment
		copied.Variables = types.CloneVariables(environment.Variables)
		out = append(out, copied)
	}
	return out
}

// mcpJoinSecretNames renders secret names for an error or a prompt. Quoted so a
// name with a space in it does not read as two names, and joined with "and"
// because the sentence around it is prose the agent (and the user) reads.
func mcpJoinSecretNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}
