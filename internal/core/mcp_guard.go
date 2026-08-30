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
// KNOWN LIMITATION, STATED RATHER THAN PAPERED OVER. The guard reasons about the
// request's DEFINITION. A user-authored pre-request script can rewrite req.url
// after the guard has run, so a script could retarget a secret mid-run and this
// check would not see it. That is accepted for now on a specific ground: scripts
// are written by the user, and an agent cannot author or edit one while the
// write tier is off — so the only way a hostile script gets into a collection is
// by the user putting it there, which is a different threat than the one this
// tier introduces. If agents ever gain script authoring, this guard must move to
// the send path (after pre-request scripts, before the transport) or the
// property it claims stops holding.

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

// mcpKnownHostsForSecret walks every request of every open collection and
// collects the hosts the ones referencing secretName resolve to.
//
// EACH CANDIDATE IS RESOLVED UNDER EVERY ENVIRONMENT ITS OWN COLLECTION
// DEFINES, plus none. A collection with staging and production environments
// aims the same secret at two hosts by design, and the user configured both; a
// resolution that used only one of them would prompt every time the agent picked
// the other, for a host the user had already chosen to send the credential to.
// What this does NOT widen is overrides — those are the agent's input, and they
// are excluded here precisely so that retargeting through one is what trips the
// guard.
func mcpKnownHostsForSecret(collections []mcpGuardCollection, secretName string) map[string]bool {
	hosts := map[string]bool{}
	for _, owner := range collections {
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

// enforceMCPHostGuard is the decision point, called from RunRequest before
// anything is sent.
//
// Returns nil to proceed. Returns an ErrDenied-wrapped error to refuse, naming
// the host and the secret NAMES — never a value, which is what makes the error
// safe to hand back to the agent verbatim.
func (a *App) enforceMCPHostGuard(ctx context.Context, plan mcpRunPlan, overrides map[string]string) error {
	referenced := mcpReferencedSecrets(plan.effective, plan.secretsInScope)
	if len(referenced) == 0 {
		// Nothing secret is in play. A run with no credential to protect is not
		// the guard's business, whatever host it points at.
		return nil
	}

	// The target host, resolved WITH the overrides — this is where the run would
	// actually send the credential.
	targetVars := map[string]string{}
	for name, value := range plan.vars {
		targetVars[name] = value
	}
	for name, value := range overrides {
		targetVars[name] = value
	}
	targetHost := mcpHostOfURL(plan.effective.URL, targetVars)
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
		if mcpKnownHostsForSecret(collections, name)[targetHost] {
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
