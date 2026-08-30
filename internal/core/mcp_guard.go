package core

// The new-host guard — rule 4 of docs/mcp-agent-interface.md.
//
//   "Every secret variable carries a host allowlist learned from the requests
//    that already use it. A run (agent-initiated) that would resolve a secret
//    into a request aimed at a host outside that allowlist blocks and raises an
//    approval prompt in the app UI."
//
// THE ALLOWLIST IS COMPUTED, NOT STORED. There is no per-secret host list in
// AppState and there deliberately is not one: it would have to be maintained on
// every edit of every request, would go stale the moment a user changed a
// {{baseUrl}}, and would be a second source of truth about something the
// collections already say. Instead the allowlist is derived on demand — the
// hosts every request that references this secret resolves to — unioned with the
// hosts the user has explicitly remembered through an approval.
//
// WHAT THE GUARD ACTUALLY DEFENDS AGAINST. The read tier gives an agent the
// request definitions but never a secret value, and the write tier is off, so an
// agent cannot author a request that points a credential somewhere new. What it
// CAN do is ask for a run with variable overrides. Overriding {{baseUrl}} to a
// host it controls, on a request whose Authorization header reads {{apiToken}},
// is the exfiltration path — and it is exactly the case this guard catches,
// because the known-host set is computed WITHOUT the overrides applied while the
// target host is resolved WITH them.
//
// WHAT AN AGENT-SUPPLIED VALUE MAY NOT DO. interp.Interpolate is MULTI-PASS: it
// re-scans its own output for new {{tokens}}. So a value the agent chose is not
// inert data — an override of {"smuggle": "{{apiToken}}"} on a request whose
// header reads "Bearer {{smuggle}}" resolves the real credential at send time,
// and the scan above sees only the name "smuggle", which is not a secret, so it
// would find nothing to guard and return before a host was ever computed. That
// path is closed by mcpRefuseSecretInjectingValues, which REFUSES any
// agent-supplied value that reaches a secret — the same inversion argument
// mcpValidatedOverrides (mcp_run.go) already makes for override NAMES, applied
// to their values. Separately, the "is a secret in play" scan now also follows
// the overrides (mcpSecretsReachedByOverrides), so a secret a USER-authored
// flow step var aims at a request is host-checked rather than invisible.
//
// KNOWN LIMITATIONS, STATED RATHER THAN PAPERED OVER. The guard reasons about
// the request's DEFINITION, and it runs once, before the send. Two consequences:
//
//   - A user-authored pre-request script can rewrite req.url after the guard has
//     run, so a script could retarget a secret mid-run and this check would not
//     see it. Accepted on a specific ground: scripts are written by the user, and
//     an agent cannot author or edit one while the write tier is off — so the
//     only way a hostile script gets into a collection is by the user putting it
//     there, which is a different threat than the one this tier introduces. If
//     agents ever gain script authoring, this guard must move to the send path
//     (after pre-request scripts, before the transport) or the property it
//     claims stops holding.
//
//   - A redirect is followed without re-checking. Go's client strips
//     Authorization across a HOSTNAME change but not a port change, so a known
//     host can 302 a credential to another port of itself and the guard never
//     sees the hop (pinned by TestMCPRunRequestRedirectToADifferentPortReaches-
//     AnUncheckedService). Accepted because the redirecting host already holds
//     the credential — it can gain nothing by forwarding it to itself — and the
//     residual case, unrelated tenants sharing one hostname across ports, is in
//     practice loopback. Re-guarding each hop would fork the transport's
//     redirect policy away from the user's own send path, which is the drift
//     the run tier's design forbids.

import (
	"context"
	"fmt"
	"net/url"
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
// resolves, so it must also be SEEN here — a guard that missed the spaced form
// would be bypassable by adding a space.
var mcpTemplatePattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// mcpNormalizeHost reduces a host to the form the allowlist compares: lowercase,
// no port, no surrounding whitespace.
//
// The port is dropped on purpose. A credential that may go to api.example.com:443
// may go to api.example.com:8443 — it is the same operator, the same DNS name,
// and the same trust decision — while treating them as different hosts would
// prompt the user for a staging port they already approved in production, which
// is the kind of noise that trains people to click approve without reading.
func mcpNormalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	// An IPv6 literal keeps its brackets through url.Hostname(), which already
	// stripped them; this only has to handle a raw "host:port" string.
	if index := strings.LastIndex(host, ":"); index > 0 && !strings.Contains(host[index+1:], ":") {
		if candidate := host[:index]; !strings.Contains(candidate, "]") || strings.HasSuffix(candidate, "]") {
			host = candidate
		}
	}
	return strings.Trim(host, "[]")
}

// mcpHostOfURL resolves a URL's host after interpolation.
//
// An unparseable or hostless URL yields "", which the caller treats as "no
// target host was learned" rather than as a match: a request whose URL is still
// full of unresolved templates must never contribute a host to an allowlist, and
// must never be run past the guard on the strength of one.
func mcpHostOfURL(rawURL string, vars map[string]string) string {
	resolved := strings.TrimSpace(interp.Interpolate(rawURL, vars))
	if resolved == "" {
		return ""
	}
	// A URL authored without a scheme ("api.example.com/v1") parses as a path
	// with no host at all, and the send path prepends a scheme later. Do the
	// same here so such a request is guarded rather than waved through.
	if !strings.Contains(resolved, "://") {
		resolved = "https://" + resolved
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		return ""
	}
	return mcpNormalizeHost(parsed.Hostname())
}

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
// a script that mentions a secret does not necessarily send it, and the known
// limitation at the top of this file already states that scripts are outside
// what a definition-level guard can reason about.
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

// mcpSecretOwner names the DEFINITION SITE a secret's host allowlist is scoped
// to.
//
// A secret has no identity beyond where it is written down. Two collections that
// both declare "apiToken" — the single most reusable name a real workspace has —
// hold two different credentials, for two different APIs, that have never shared
// a host; treating them as one secret let either one's requests widen the
// other's allowlist, which is the whole point of scoping by site rather than by
// name.
type mcpSecretOwner struct {
	// workspacePath is the workspace the secret is defined in. Hosts are NEVER
	// unioned across workspaces: separate workspaces are the user's own coarsest
	// separation and nothing in one may teach the guard about the other.
	workspacePath string
	// collectionID is the collection that owns the secret, or "" when it is
	// defined in a workspace GLOBAL environment. A global secret legitimately
	// serves every collection in its workspace — that is what a global
	// environment is for — so its allowlist unions that workspace's collections.
	collectionID string
}

// mcpCollectionScopedSecretNames is every name this collection declares SECRET
// at collection scope or narrower: its own variables, any of its environments,
// any folder, or any request's own vars.
//
// The sources match mcpSecretNamesInScope's, minus the workspace globals, for
// the same reason that function gives — "which scope is this name secret in" has
// to agree with "which value would this name resolve to". Every environment is
// considered rather than only the selected one, and that is the conservative
// direction: a name a collection declares secret in ANY environment means that
// collection's own credential somewhere, so treating it as collection-owned
// narrows the allowlist rather than widening it.
func mcpCollectionScopedSecretNames(collection types.Collection) map[string]bool {
	names := map[string]bool{}
	collect := func(variables []types.Variable) {
		for _, variable := range variables {
			name := strings.TrimSpace(variable.Name)
			if name == "" || !variable.Secret {
				continue
			}
			names[name] = true
		}
	}
	collect(collection.Variables)
	for _, environment := range collection.Environments {
		collect(environment.Variables)
	}
	for _, folder := range collection.Folders {
		collect(folder.Variables)
	}
	for index := range collection.Items {
		collect(collection.Items[index].Vars.Req)
	}
	return names
}

// mcpSecretOwnerIn works out where a secret name is defined for a request being
// judged inside one collection: the collection when it declares the name secret
// itself, the workspace otherwise.
//
// Collection scope wins when both declare it, because that is the precedence
// scripting.BuildVariableMap applies — the collection's value is the one that
// would resolve — and because it is the narrower allowlist of the two.
func mcpSecretOwnerIn(owner mcpGuardCollection, collectionScoped map[string]bool, secretName string) mcpSecretOwner {
	site := mcpSecretOwner{workspacePath: owner.workspacePath}
	if collectionScoped[secretName] {
		site.collectionID = owner.collection.ID
	}
	return site
}

// mcpKnownHostsForSecret collects the hosts the requests that reference this
// secret resolve to — from the collections its DEFINITION SITE lets it serve,
// and no others.
//
// EACH CANDIDATE IS RESOLVED UNDER EVERY ENVIRONMENT ITS OWN COLLECTION
// DEFINES, plus none. A collection with staging and production environments
// aims the same secret at two hosts by design, and the user configured both; a
// resolution that used only one of them would prompt every time the agent picked
// the other, for a host the user had already chosen to send the credential to.
// What this does NOT widen is overrides — those are the agent's input, and they
// are excluded here precisely so that retargeting through one is what trips the
// guard.
//
// A COLLECTION THAT REDEFINES THE NAME IS SKIPPED when the secret under guard is
// the workspace-global one. Inside such a collection the name means that
// collection's own credential, at least under the environments that declare it,
// and there is no way to tell from a request's braces which of the two it meant.
// Skipping is the fail-closed answer: at worst it costs one approval prompt for
// a host the global secret really does already use, whereas the other direction
// is one collection silently widening another's allowlist.
func mcpKnownHostsForSecret(collections []mcpGuardCollection, site mcpSecretOwner, secretName string) map[string]bool {
	hosts := map[string]bool{}
	for _, owner := range collections {
		if owner.workspacePath != site.workspacePath {
			continue
		}
		if site.collectionID != "" {
			if owner.collection.ID != site.collectionID {
				continue
			}
		} else if mcpCollectionScopedSecretNames(owner.collection)[secretName] {
			continue
		}
		collection := owner.collection
		environmentIDs := []string{""}
		for _, environment := range collection.Environments {
			environmentIDs = append(environmentIDs, environment.ID)
		}
		for index := range collection.Items {
			item := collection.Items[index]
			effective := scripting.EffectiveRequest(collection, item)
			secretsInScope := map[string]bool{secretName: true}
			if len(mcpReferencedSecrets(effective, secretsInScope)) == 0 {
				continue
			}
			for _, environmentID := range environmentIDs {
				vars := scripting.BuildVariableMap(owner.globalEnvironments, &collection, environmentID, item, owner.workspacePath)
				if host := mcpHostOfURL(effective.URL, vars); host != "" {
					hosts[host] = true
				}
			}
		}
	}
	return hosts
}

// mcpGuardCollection is one collection copied out from under the state lock,
// together with the two things resolving its requests needs from its workspace.
type mcpGuardCollection struct {
	collection         types.Collection
	globalEnvironments []types.Environment
	workspacePath      string
}

// mcpGuardInput is everything ONE run contributes to the guard's decision
// beyond the request's own definition. It is a struct rather than three
// parameters because the three are only ever passed together, and because the
// distinction between the first two is load-bearing enough to want a name on
// each.
type mcpGuardInput struct {
	// overrides is what the send will actually apply (runner.Iteration.Data).
	// For run_request these ARE the agent's own variables; for run_flow they are
	// the step's USER-AUTHORED vars after flow-scope interpolation.
	overrides map[string]string
	// agentValues is the subset the AGENT itself chose: run_request's variables,
	// run_flow's inputs.
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
// protected by value comparison anyway. The NAME walk above does not depend on
// value length, so the normal smuggling route stays closed regardless.
const mcpMinComparableSecretLength = 8

// enforceMCPHostGuard is the decision point, called from RunRequest before
// anything is sent and from RunFlow's per-step guard before each step.
//
// Returns nil to proceed. Returns an ErrDenied-wrapped error to refuse, naming
// the host, the offending variable and the secret NAMES — never a value, which
// is what makes the error safe to hand back to the agent verbatim.
func (a *App) enforceMCPHostGuard(ctx context.Context, plan mcpRunPlan, input mcpGuardInput) error {
	// The variable map the send path will resolve against: the run's own scope
	// overlaid with the overrides. Both halves below need it — the injection
	// refusal walks agent-supplied values through it, and the target host is
	// resolved with it.
	effective := mcpEffectiveGuardVars(plan.vars, input.overrides)

	// FIRST, AND UNCONDITIONALLY: an agent-supplied value that reaches a secret
	// is refused outright, before any host is computed. It is not a run to be
	// guarded, it is a run that must not be attempted.
	if err := mcpRefuseSecretInjectingValues(plan, effective, input); err != nil {
		return err
	}

	referenced := mcpUnionSecretNames(
		mcpReferencedSecrets(plan.effective, plan.secretsInScope),
		// A secret can also reach the request through an override the USER
		// authored — a flow step var of {"token": "{{apiToken}}"} on a request
		// whose header reads "Bearer {{token}}". The request's own fields never
		// name the secret, so the scan above cannot see it, but the send path
		// will resolve it all the same. Following the overrides is what makes
		// such a run host-checked rather than invisible.
		mcpSecretsReachedByOverrides(effective, input.overrides, plan.secretsInScope),
	)
	if len(referenced) == 0 {
		// Nothing secret is in play. A run with no credential to protect is not
		// the guard's business, whatever host it points at.
		return nil
	}

	// The target host, resolved WITH the overrides — this is where the run would
	// actually send the credential.
	targetHost := mcpHostOfURL(plan.effective.URL, effective)
	if targetHost == "" {
		return fmt.Errorf("%w: this run references the secret %s but its URL does not resolve to a host, so the new-host guard cannot check where the credential would go; fix the URL or the variables it depends on",
			mcpserver.ErrDenied, strings.Join(referenced, ", "))
	}

	unknown, err := a.mcpSecretsWithoutHost(plan, referenced, targetHost)
	if err != nil {
		return err
	}
	if len(unknown) == 0 {
		return nil
	}

	approved := a.requestMCPApproval(ctx, types.MCPApprovalRequest{
		RequestName: plan.requestName,
		Host:        targetHost,
		SecretNames: unknown,
	})
	if approved {
		return nil
	}
	// The message is written for the agent that reads it: it names what was
	// refused, and it says what the fix is, because the wrong reaction to a
	// denial is to retry it or to route around it.
	return fmt.Errorf("%w: this run would send the secret %s to %s, which no request in the open collections sends it to. Ask the user to approve that host in LiteAPI (or to point you at the right one); do not retry and do not work around it",
		mcpserver.ErrDenied, mcpJoinSecretNames(unknown), targetHost)
}

// mcpEffectiveGuardVars is the run's resolved scope overlaid with its overrides:
// the map the send path will interpolate every field against, minus only what a
// pre-request script could add (which the limitation note at the top of this
// file already accounts for).
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
// WHY REFUSE RATHER THAN GUARD. mcpValidatedOverrides (mcp_run.go) already
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

// mcpSecretsReachedByOverrides is the union of what every override value can
// reach — the set the host guard adds to the request's own references.
func mcpSecretsReachedByOverrides(effective, overrides map[string]string, secretsInScope map[string]bool) []string {
	var names []string
	for _, value := range overrides {
		names = append(names, mcpSecretsReachedByTemplate(value, effective, secretsInScope)...)
	}
	return mcpSortedNames(names)
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

// mcpUnionSecretNames merges two name lists into one sorted, deduped list.
func mcpUnionSecretNames(first, second []string) []string {
	if len(second) == 0 {
		return first
	}
	return mcpSortedNames(append(append([]string{}, first...), second...))
}

// mcpSecretsWithoutHost narrows the referenced secrets to the ones whose
// allowlist does not already contain targetHost.
//
// Short-circuiting on the cheap sources first is deliberate. The remembered
// approvals are a small file; the collection walk resolves variable maps and
// reads .env files, so it runs only for a secret the remembered set did not
// already clear, and only once the run's own collection could not answer.
func (a *App) mcpSecretsWithoutHost(plan mcpRunPlan, referenced []string, targetHost string) ([]string, error) {
	var unknown []string
	var collections []mcpGuardCollection
	for _, name := range referenced {
		// REMEMBERED APPROVALS ARE NOT SCOPED, deliberately. mcp-approvals.json
		// is keyed by name+host because a remembered approval is the user's own
		// explicit decision about that pair, made in front of a prompt that
		// named both — it is not something the collections computed, so there is
		// no definition site to scope it to, and re-asking for a pair the user
		// has already answered is the noise that trains people to click through
		// prompts without reading them.
		remembered, err := a.mcpRememberedHostsForSecret(name)
		if err != nil {
			return nil, err
		}
		if remembered[targetHost] {
			continue
		}
		if collections == nil {
			collections, err = a.mcpGuardCollections()
			if err != nil {
				return nil, err
			}
		}
		if mcpKnownHostsForSecret(collections, plan.secretOwner(name), name)[targetHost] {
			continue
		}
		unknown = append(unknown, name)
	}
	return unknown, nil
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

// mcpCollectionCopy deep-copies the parts of a collection the guard resolves
// against. Not the whole struct: proxy settings, client certificates, docs and
// OpenAPI configs take no part in "where would this request's URL point", and
// copying them would be pure cost on a path that already walks every request in
// the workspace.
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
