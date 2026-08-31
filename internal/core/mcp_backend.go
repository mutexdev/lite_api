package core

// The adapter that lets internal/mcpserver read this App without importing it.
//
// mcpserver.Backend is a frozen contract (see its package comment): the DTOs it
// defines have no field that may carry a resolved secret, and the masking
// helpers it exports are the single definition of "masked" that both sides
// agree on. This file is the only place where live app state is turned into
// those DTOs, so it is the only place where a leak could be introduced — which
// is why mcp_backend_test.go calls every method with sentinel credentials
// planted in the fixture and greps the marshalled output for them.
//
// TWO RULES GOVERN EVERY METHOD HERE.
//
// Locking. Backend methods run on the MCP server's HTTP goroutines, which own
// none of this App's locks. Each method takes a.mu for WRITING (ensureReadyLocked
// mutates unconditionally — see app_bootstrap.go:26), copies what it needs,
// releases, and only then builds DTOs. Nothing that sends a request, binds a
// socket or reads a large file happens under a.mu.
//
// No interpolation. Type, method, URL, headers, params and body are reported
// AS AUTHORED, with their {{templates}} intact. Resolving them here would be
// the leak: a URL or header that reads a secret variable would arrive at the
// agent with the secret substituted in. Resolution belongs at send time, inside
// LiteAPI, and nowhere in this file.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpHistoryBodyLimit bounds the response body attached to one history run.
// History bodies are unbounded on disk and an agent asking for ten runs of a
// paginated endpoint would otherwise be handed megabytes it has to pay tokens
// to read past.
const mcpHistoryBodyLimit = 100000

// mcpHistoryDefaultLimit / mcpHistoryMaxLimit and the search equivalents are
// the "list tools support a limit" convention from docs/mcp-agent-interface.md.
// The caps are the agent-facing half of the same argument as the body limit.
const (
	mcpHistoryDefaultLimit = 10
	mcpHistoryMaxLimit     = 50
	mcpSearchDefaultLimit  = 25
	mcpSearchMaxLimit      = 200
)

// What get_history says instead of an entry recorded before the §7 projection
// existed. Written for the agent rather than the maintainer: it has to be
// obvious that the run is real, that the withholding is deliberate, and what
// makes a readable record appear.
const (
	mcpHistoryUnprojectedURL  = "<withheld: recorded before agent-safe history>"
	mcpHistoryUnprojectedBody = "This run predates LiteAPI's agent-safe history record, so its URL, headers and body are withheld rather than served. " +
		"The stored copy was written after variables were substituted in, and it was never masked against the secret values that were live at the time — " +
		"a credential rotated or deleted since then would no longer be recognised, so serving it now could disclose one. " +
		"Re-run the request to get a readable record of it."
)

// mcpBackend adapts *App to mcpserver.Backend.
type mcpBackend struct {
	app *App
}

// Compile-time proof that this file implements the frozen contract. Without it
// a Backend method whose signature drifted would only fail where the server is
// constructed, which is one file away from the mistake.
var _ mcpserver.Backend = (*mcpBackend)(nil)

// readStateForMCP runs collect with the state lock held and hands back nothing
// else. collect must COPY — scalars, cloned slices — and must not retain
// pointers into a.state or do any work that can block: the lock it runs under
// is the one every binding in the app contends for.
func (a *App) readStateForMCP(collect func(state *AppState)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return err
	}
	collect(&a.state)
	return nil
}

// requestRow is one request copied out from under the lock, before any DTO
// exists. Keeping the copy step in its own type is what makes it obvious at a
// glance that nothing here is a pointer back into a.state.
type requestRow struct {
	collectionID string
	item         types.RequestItem
}

func (b *mcpBackend) ListCollections() ([]mcpserver.CollectionSummary, error) {
	var rows []mcpserver.CollectionSummary
	if err := b.app.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				rows = append(rows, mcpserver.CollectionSummary{
					ID:           collection.ID,
					Name:         collection.Name,
					RequestCount: len(collection.Items),
					FlowCount:    len(collection.Flows),
				})
			}
		}
	}); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []mcpserver.CollectionSummary{}
	}
	return rows, nil
}

func (b *mcpBackend) ListRequests(collectionID string) ([]mcpserver.RequestSummary, error) {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return nil, errors.New("collectionId is required")
	}
	var rows []requestRow
	found := false
	if err := b.app.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				if collection.ID != collectionID {
					continue
				}
				found = true
				for ii := range collection.Items {
					rows = append(rows, requestRow{
						collectionID: collection.ID,
						item:         mcpItemCopy(collection.Items[ii]),
					})
				}
				return
			}
		}
	}); err != nil {
		return nil, err
	}
	if !found {
		// Names the field and the fix, per the tool design conventions in
		// docs/mcp-agent-interface.md. The id itself is the user's, not a
		// secret, so echoing it back is what makes the error actionable.
		return nil, fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	out := make([]mcpserver.RequestSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, mcpRequestSummary(row))
	}
	return out, nil
}

func (b *mcpBackend) SearchRequests(query string, limit int) ([]mcpserver.RequestSummary, error) {
	limit = mcpBoundedLimit(limit, mcpSearchDefaultLimit, mcpSearchMaxLimit)
	needle := strings.ToLower(strings.TrimSpace(query))

	var rows []requestRow
	if err := b.app.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				for ii := range collection.Items {
					item := &collection.Items[ii]
					if !mcpItemMatches(item, needle) {
						continue
					}
					rows = append(rows, requestRow{collectionID: collection.ID, item: mcpItemCopy(*item)})
					if len(rows) >= limit {
						return
					}
				}
			}
		}
	}); err != nil {
		return nil, err
	}
	out := make([]mcpserver.RequestSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, mcpRequestSummary(row))
	}
	return out, nil
}

// mcpItemMatches is the search predicate: case-insensitive substring over the
// four fields an agent can plausibly know about a request it has not seen —
// what it is called, how it is sent, where it goes, and where it sits in the
// tree. An empty query matches everything, which is what makes search with a
// limit a usable "show me anything" call.
func mcpItemMatches(item *types.RequestItem, needle string) bool {
	if needle == "" {
		return true
	}
	for _, field := range []string{item.Name, item.Method, item.URL, item.FolderPath} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

func (b *mcpBackend) GetRequest(collectionID, requestID string) (mcpserver.RequestDetail, error) {
	collectionID = strings.TrimSpace(collectionID)
	requestID = strings.TrimSpace(requestID)
	if collectionID == "" || requestID == "" {
		return mcpserver.RequestDetail{}, errors.New("collectionId and requestId are both required")
	}
	var row requestRow
	// owner carries the three fields the auth walk reads — Path and Folders for
	// the folder chain, Auth for the fallback — copied out under the lock like
	// everything else. Not the whole collection: this is the narrowest thing
	// that can answer "what auth would a run of this request actually use".
	var owner types.Collection
	found := false
	if err := b.app.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				if collection.ID != collectionID {
					continue
				}
				for ii := range collection.Items {
					if collection.Items[ii].ID != requestID {
						continue
					}
					row = requestRow{collectionID: collection.ID, item: mcpItemCopy(collection.Items[ii])}
					owner = types.Collection{
						Path:    collection.Path,
						Auth:    types.CloneAuthConfig(collection.Auth),
						Folders: mcpFolderCopies(collection.Folders),
					}
					found = true
					return
				}
				return
			}
		}
	}); err != nil {
		return mcpserver.RequestDetail{}, err
	}
	if !found {
		return mcpserver.RequestDetail{}, fmt.Errorf("no request with id %q in collection %q; call list_requests for the ids that exist", requestID, collectionID)
	}

	item := row.item
	effectiveAuth, authSource := mcpEffectiveAuth(owner, item)
	detail := mcpserver.RequestDetail{
		RequestSummary: mcpRequestSummary(row),
		Headers:        mcpserver.RedactRows(mcpKeyValueRows(item.Headers)),
		Params:         mcpserver.RedactRows(mcpKeyValueRows(item.Params)),
		PathParams:     mcpserver.RedactRows(mcpKeyValueRows(item.PathParams)),
		BodyType:       strings.TrimSpace(item.Body.Mode),
		Body:           types.RequestBodySnapshot(item.Body),
		// RequestBodySnapshot maps mode "graphql" to the QUERY alone, so the
		// variables document had no field on the wire at all and an agent
		// reading a GraphQL request could not see what it declares. Reported
		// only for the mode that has one, and as authored: it is body content,
		// so it is unscanned and unresolved exactly like Body.
		GraphQLVariables: mcpGraphQLVariables(item.Body),
		// The EFFECTIVE auth, not the stored mode: an inheriting request
		// reports what a run of it would actually use, and AuthSource names the
		// level that supplied it. Reporting "inherit" with no rows — which is
		// what reading item.Auth alone produces — tells the agent nothing about
		// how the request authenticates, and most imported collections inherit.
		AuthType:   strings.TrimSpace(effectiveAuth.Mode),
		AuthSource: authSource,
		Auth:       mcpserver.MaskAuthRows(mcpAuthRows(effectiveAuth)),
		Vars:       mcpserver.RedactRows(mcpVariableRows(item.Vars)),
		// Scripts travel verbatim. They are code the user wrote and the agent
		// may need to reproduce; a script that hardcodes a credential is a
		// problem in the collection, not something this layer can fix by
		// mangling the source into something that no longer runs.
		PreScript:  item.PreScript,
		PostScript: item.PostScript,
		Tests:      item.Tests,
		Settings: mcpserver.RequestSettings{
			// Reported exactly as stored, with no re-defaulting. The send path
			// reads this same bool (requestTLSVerificationEnabled ANDs it with
			// the app preference), so a request whose stored VerifyTLS is false
			// really would be sent without verification — inventing a `true`
			// here would tell the agent the opposite of what happens.
			VerifyTLS:       item.Settings.VerifyTLS,
			FollowRedirects: item.Settings.FollowRedirects,
			MaxRedirects:    item.Settings.MaxRedirects,
		},
	}
	return detail, nil
}

// mcpGraphQLVariables reports the variables document of a graphql body and
// nothing for any other mode. RequestBody carries the field whatever the mode
// is, and a request switched from graphql to json keeps its old document —
// reporting that would describe a call the request no longer makes.
func mcpGraphQLVariables(body types.RequestBody) string {
	if !strings.EqualFold(strings.TrimSpace(body.Mode), "graphql") {
		return ""
	}
	return body.GraphQLVariables
}

// --- inspect_request -------------------------------------------------------
//
// get_request answers "what is written on this request". InspectRequest
// answers the question an agent actually has, which is "what would a run of it
// send, and what do I still have to supply". The gap between the two is the
// inheritance chain: a request can carry no headers, no auth and no variables
// of its own and still send all three, contributed by a folder, by the
// collection, or by an environment.
//
// EVERY EFFECTIVE VALUE HERE MIRRORS THE SEND PATH RATHER THAN GUESSING AT IT,
// the same way mcpEffectiveAuth mirrors scripting.EffectiveRequest. Each
// mirroring helper below names the function it reproduces and the subtlety it
// is reproducing, because a divergence between "what inspect_request reports"
// and "what run_request sends" is worse than no tool at all: it is a confident
// wrong answer that an agent will act on.
//
// THE REDACTION RULES DO NOT CHANGE. This is authored data, so the rule is
// get_request's: templates unresolved, secret-flagged values never read,
// credential-shaped literals masked with mcpserver's own helpers. Nothing here
// interpolates, and the resolved variable table is consulted only for the
// boolean "does this name resolve" — its values never reach a DTO.

// mcpInspectionSource is everything InspectRequest copies out from under the
// state lock, in one read. Collected into a struct rather than a fistful of
// closure captures because the copy is the part that has to be obviously
// pointer-free, and a named type makes that reviewable at a glance.
type mcpInspectionSource struct {
	collection      types.Collection
	item            types.RequestItem
	globals         []types.Environment
	workspacePath   string
	preferences     types.RequestPreferences
	environmentName string

	collectionFound        bool
	itemFound              bool
	environmentFound       bool
	globalEnvironmentMatch bool
}

// mcpInspectionCollectionCopy copies the collection fields the inspection
// reads. It is deliberately NOT mcpCollectionCopy (mcp_guard.go): that one
// exists for the egress guard and drops the three script fields, which are
// precisely what the script half of this inspection is about.
func mcpInspectionCollectionCopy(collection *types.Collection) types.Collection {
	return types.Collection{
		ID:               collection.ID,
		Name:             collection.Name,
		Path:             collection.Path,
		Auth:             types.CloneAuthConfig(collection.Auth),
		Headers:          types.CloneKeyValues(collection.Headers),
		Variables:        types.CloneVariables(collection.Variables),
		RuntimeVariables: types.CloneVariables(collection.RuntimeVariables),
		Environments:     mcpEnvironmentCopies(collection.Environments),
		Folders:          mcpFolderCopies(collection.Folders),
		PreScript:        collection.PreScript,
		PostScript:       collection.PostScript,
		Tests:            collection.Tests,
	}
}

func (b *mcpBackend) InspectRequest(collectionID, requestID, environmentID string) (mcpserver.RequestInspection, error) {
	collectionID = strings.TrimSpace(collectionID)
	requestID = strings.TrimSpace(requestID)
	environmentID = strings.TrimSpace(environmentID)
	if collectionID == "" || requestID == "" {
		return mcpserver.RequestInspection{}, errors.New("collectionId and requestId are both required")
	}

	source := mcpInspectionSource{environmentFound: environmentID == ""}
	if err := b.app.readStateForMCP(func(state *AppState) {
		source.preferences = state.Preferences.Request
		for wi := range state.Workspaces {
			workspace := &state.Workspaces[wi]
			for _, environment := range workspace.GlobalEnvironments {
				if environmentID != "" && environment.ID == environmentID {
					source.globalEnvironmentMatch = true
				}
			}
			for ci := range workspace.Collections {
				if workspace.Collections[ci].ID != collectionID {
					continue
				}
				source.collectionFound = true
				source.collection = mcpInspectionCollectionCopy(&workspace.Collections[ci])
				source.globals = mcpEnvironmentCopies(scripting.ActiveGlobalEnvironmentsForWorkspace(*workspace))
				source.workspacePath = workspace.Path
				for ii := range workspace.Collections[ci].Items {
					if workspace.Collections[ci].Items[ii].ID != requestID {
						continue
					}
					source.item = mcpItemCopy(workspace.Collections[ci].Items[ii])
					source.itemFound = true
					break
				}
				for _, environment := range source.collection.Environments {
					if environment.ID == environmentID {
						source.environmentFound = true
						source.environmentName = environment.Name
						break
					}
				}
				return
			}
		}
	}); err != nil {
		return mcpserver.RequestInspection{}, err
	}
	if !source.collectionFound {
		return mcpserver.RequestInspection{}, fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	if !source.itemFound {
		return mcpserver.RequestInspection{}, fmt.Errorf("no request with id %q in collection %q; call list_requests for the ids that exist", requestID, collectionID)
	}
	if !source.environmentFound {
		// The same two messages mcpRunPlan gives, for the same reason: a global
		// environment id is a real id the agent read from list_environments, so
		// "no such environment" would be both wrong and unactionable.
		if source.globalEnvironmentMatch {
			return mcpserver.RequestInspection{}, fmt.Errorf("environmentId %q names a global environment, which cannot be selected per call; the workspace's active global environment already applies. Pass a collection environment from list_environments, or omit environmentId", environmentID)
		}
		return mcpserver.RequestInspection{}, fmt.Errorf("no environment with id %q in collection %q; call list_environments for the ids that exist, or omit environmentId to inspect with no collection environment", environmentID, collectionID)
	}

	detail, err := b.GetRequest(collectionID, requestID)
	if err != nil {
		return mcpserver.RequestInspection{}, err
	}

	collection, item := source.collection, source.item
	folders := scripting.FolderChain(collection, item)
	// THE RUN'S OWN VARIABLE CONTEXT, built exactly as mcpRunPlan builds it —
	// same constructor, same arguments, no overrides. Only its KEY SET is read
	// (mcpResolvableNames): the values in it are hydrated secrets, and nothing
	// below ever touches one.
	effective := scripting.EffectiveRequest(collection, item)
	resolvable := mcpResolvableNames(scripting.NewScriptVariableContext(
		source.globals, &collection, environmentID, effective, nil, source.workspacePath))
	variables := mcpInspectedVariables(collection, folders, source.globals, environmentID, source.environmentName, item)

	references := mcpVariableReferences(effective, variables, resolvable)
	return mcpserver.RequestInspection{
		Request:             detail,
		Environment:         mcpInspectedEnvironment(environmentID, source.environmentName, source.globals),
		Headers:             mcpInheritedHeaders(collection, folders, item),
		Variables:           mcpInheritedVariableRows(variables),
		Scripts:             mcpInheritedScripts(collection, folders, item),
		References:          references,
		UnresolvedVariables: mcpUnresolvedNames(references),
		Settings:            mcpEffectiveSettings(item, source.preferences),
		NotResolved:         mcpInspectionCaveats,
	}, nil
}

// mcpInspectionCaveats is the honest boundary of this tool: an empty
// unresolvedVariables must not read as "this call will work".
var mcpInspectionCaveats = []string{
	"Nothing is interpolated. Every value here is as authored, so a {{template}} you see is a reference and never a resolved value.",
	"Scripts are reported but not executed. A pre-request script can set variables, rewrite the URL and add headers, so a request with scripts can send something this inspection does not show.",
	"{{process.env.NAME}} references are listed but never checked. They resolve at send time from the process environment and the collection's .env file, which is where credentials live.",
	"{{?name}} prompt variables ask the USER for a value in the app. A run started from here has nobody to ask, so treat one as a blocker and tell the user.",
	"An unresolved name may still be supplied at run time: pass it in run_request's variables, or pick an environment that defines it.",
}

// mcpResolvableNames is the set of variable names a run would resolve, taken
// from the run's own VariableContext.
//
// ONLY THE KEYS LEAVE THIS FUNCTION. Combined holds hydrated secret values by
// the time state is readable, so returning the map — or ranging over its values
// anywhere — is the leak this whole adapter exists to prevent. A set of names
// answers the only question the report asks of it.
func mcpResolvableNames(variables *scripting.VariableContext) map[string]bool {
	names := map[string]bool{}
	if variables == nil {
		return names
	}
	for name := range variables.Combined {
		names[name] = true
	}
	return names
}

// mcpInspectedVariable is one variable in scope, with the level that wins for
// its name.
type mcpInspectedVariable struct {
	variable  types.Variable
	level     string
	levelPath string
}

// mcpInspectedVariables resolves the variable scopes to one winner per name.
//
// THE ORDER IS scripting.VariableContext.Recompute's, and it is the contract:
// Global, then Collection, then the selected Environment, then Folder, then
// Request, then Runtime — each layer overwriting the last. Runtime sits on top
// because a value a script persisted with bru.setVar is a deliberate act that
// must not be silently undone by the row that was configured before it.
//
// Disabled rows and unnamed rows are skipped, exactly as mergeVariableMap skips
// them: they do not resolve, so reporting them as the winning level would tell
// an agent a name resolves when it does not.
func mcpInspectedVariables(
	collection types.Collection,
	folders []types.FolderConfig,
	globals []types.Environment,
	environmentID, environmentName string,
	item types.RequestItem,
) map[string]mcpInspectedVariable {
	winners := map[string]mcpInspectedVariable{}
	merge := func(level, levelPath string, rows []types.Variable) {
		for _, variable := range rows {
			if !variable.Enabled || variable.Name == "" {
				continue
			}
			winners[variable.Name] = mcpInspectedVariable{variable: variable, level: level, levelPath: levelPath}
		}
	}
	for _, environment := range globals {
		merge(mcpserver.LevelGlobal, environment.Name, environment.Variables)
	}
	merge(mcpserver.LevelCollection, "", collection.Variables)
	if environmentID != "" {
		for _, environment := range collection.Environments {
			if environment.ID == environmentID {
				merge(mcpserver.LevelEnvironment, environmentName, environment.Variables)
				break
			}
		}
	}
	for _, folder := range folders {
		merge(mcpserver.LevelFolder, folder.Path, folder.Variables)
	}
	merge(mcpserver.LevelRequest, "", item.Vars.Req)
	merge(mcpserver.LevelRuntime, "", collection.RuntimeVariables)
	return winners
}

// mcpInheritedVariableRows turns the winner table into DTO rows, sorted by
// name so two calls against unchanged state produce byte-identical output.
//
// A secret variable's value is DROPPED rather than masked, for the reason
// ListEnvironments gives: by the time state is readable the value is the real
// decrypted credential, and the safe thing is never to read it at all. Every
// other value goes through mcpserver.RedactRows, which is the same rule
// get_request applies to a request's own vars.
func mcpInheritedVariableRows(winners map[string]mcpInspectedVariable) []mcpserver.InheritedRow {
	names := make([]string, 0, len(winners))
	for name := range winners {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]mcpserver.KeyValue, 0, len(names))
	for _, name := range names {
		row := mcpserver.KeyValue{Name: name, Enabled: true}
		if !winners[name].variable.Secret {
			row.Value = envsecrets.ValueToString(winners[name].variable.Value)
		}
		rows = append(rows, row)
	}
	rows = mcpserver.RedactRows(rows)

	out := make([]mcpserver.InheritedRow, 0, len(names))
	for index, name := range names {
		winner := winners[name]
		out = append(out, mcpserver.InheritedRow{
			Name:      name,
			Value:     rows[index].Value,
			Enabled:   true,
			Level:     winner.level,
			LevelPath: winner.levelPath,
			Secret:    winner.variable.Secret,
		})
	}
	return out
}

// mcpInheritedHeaders reproduces scripting.EffectiveRequest's header merge and
// records which level each surviving row came from.
//
// THREE BEHAVIOURS OF THAT MERGE ARE REPRODUCED DELIBERATELY:
//
//   - A name the REQUEST sets suppresses every inherited row of that name,
//     whether or not the request's own row is enabled. EffectiveRequest seeds
//     its seen-set from item.Headers without checking Enabled, so a disabled
//     request header still shadows the collection's.
//   - Among inherited rows the INNERMOST wins: the merge walks the candidate
//     list backwards keeping the first of each name, which is the last one
//     appended, which is the innermost folder.
//   - Disabled and unnamed inherited rows are dropped entirely.
//
// Order is send order: inherited rows in collection-then-outermost-to-innermost
// order, then the request's own.
func mcpInheritedHeaders(collection types.Collection, folders []types.FolderConfig, item types.RequestItem) []mcpserver.InheritedRow {
	type candidate struct {
		row       types.KeyValue
		level     string
		levelPath string
	}
	candidates := make([]candidate, 0, len(collection.Headers))
	for _, header := range collection.Headers {
		candidates = append(candidates, candidate{row: header, level: mcpserver.LevelCollection})
	}
	for _, folder := range folders {
		for _, header := range folder.Headers {
			candidates = append(candidates, candidate{row: header, level: mcpserver.LevelFolder, levelPath: folder.Path})
		}
	}

	seen := map[string]bool{}
	for _, header := range item.Headers {
		seen[strings.ToLower(header.Name)] = true
	}
	reversed := make([]candidate, 0, len(candidates))
	for index := len(candidates) - 1; index >= 0; index-- {
		header := candidates[index]
		key := strings.ToLower(header.row.Name)
		if header.row.Enabled && header.row.Name != "" && !seen[key] {
			reversed = append(reversed, header)
			seen[key] = true
		}
	}

	merged := make([]candidate, 0, len(reversed)+len(item.Headers))
	for index := len(reversed) - 1; index >= 0; index-- {
		merged = append(merged, reversed[index])
	}
	for _, header := range item.Headers {
		merged = append(merged, candidate{row: header, level: mcpserver.LevelRequest})
	}

	// Masked with the same helper get_request uses, so a credential-shaped
	// literal on a FOLDER header is hidden exactly as one on the request is.
	rows := make([]mcpserver.KeyValue, 0, len(merged))
	for _, header := range merged {
		rows = append(rows, mcpserver.KeyValue{Name: header.row.Name, Value: header.row.Value, Enabled: header.row.Enabled})
	}
	rows = mcpserver.RedactRows(rows)

	out := make([]mcpserver.InheritedRow, 0, len(merged))
	for index, header := range merged {
		out = append(out, mcpserver.InheritedRow{
			Name:      rows[index].Name,
			Value:     rows[index].Value,
			Enabled:   rows[index].Enabled,
			Level:     header.level,
			LevelPath: header.levelPath,
		})
	}
	return out
}

// mcpInheritedScripts lists the script levels that run for this request, in
// execution order.
//
// THE ORDER IS INVERTED BETWEEN THE PHASES, and that is
// scripting.MergedRuntimeScripts' contract rather than a choice made here:
// pre-request runs outermost first (collection, folders outside-in, request),
// while post-response and tests run innermost first (request, folders
// inside-out, collection). An agent reproducing a call by hand needs the order,
// not just the set.
//
// Scripts travel verbatim, exactly as get_request reports the request's own —
// see the note there about why mangling the source would be worse than
// reporting it.
func mcpInheritedScripts(collection types.Collection, folders []types.FolderConfig, item types.RequestItem) []mcpserver.InheritedScript {
	out := []mcpserver.InheritedScript{}
	add := func(phase, level, levelPath, script string) {
		if strings.TrimSpace(script) == "" {
			return
		}
		out = append(out, mcpserver.InheritedScript{Phase: phase, Level: level, LevelPath: levelPath, Script: script})
	}

	add("pre", mcpserver.LevelCollection, "", collection.PreScript)
	for _, folder := range folders {
		add("pre", mcpserver.LevelFolder, folder.Path, folder.PreScript)
	}
	add("pre", mcpserver.LevelRequest, "", item.PreScript)

	add("post", mcpserver.LevelRequest, "", item.PostScript)
	for index := len(folders) - 1; index >= 0; index-- {
		add("post", mcpserver.LevelFolder, folders[index].Path, folders[index].PostScript)
	}
	add("post", mcpserver.LevelCollection, "", collection.PostScript)

	add("tests", mcpserver.LevelRequest, "", item.Tests)
	for index := len(folders) - 1; index >= 0; index-- {
		add("tests", mcpserver.LevelFolder, folders[index].Path, folders[index].Tests)
	}
	add("tests", mcpserver.LevelCollection, "", collection.Tests)

	if len(out) == 0 {
		return nil
	}
	return out
}

// mcpInspectedEnvironment reports the environment configuration in effect, and
// states the selection rule that the tool descriptions used to get wrong.
//
// THE RULE, established by reading mcpRunPlan rather than by repeating what a
// description claimed: an omitted environmentId means NO collection
// environment, not "the one selected in the app". LiteAPI's collection
// environment selection is frontend state, persisted in the WebView's own
// storage and never written to AppState, so this process cannot read it and
// there is nothing for a tool to fall back to. The workspace's active GLOBAL
// environment is persisted, is the one thing that is knowable, and applies to
// every run whatever environmentId says.
func mcpInspectedEnvironment(environmentID, environmentName string, globals []types.Environment) mcpserver.InspectedEnvironment {
	out := mcpserver.InspectedEnvironment{
		CollectionEnvironmentID:   environmentID,
		CollectionEnvironmentName: environmentName,
	}
	for _, environment := range globals {
		out.GlobalEnvironmentIDs = append(out.GlobalEnvironmentIDs, environment.ID)
		out.GlobalEnvironmentNames = append(out.GlobalEnvironmentNames, environment.Name)
	}
	if environmentID == "" {
		out.Note = "No collection environment was applied, because environmentId was omitted. " +
			"Omitting it does NOT fall back to whatever is selected in LiteAPI's window: that selection is frontend state this server cannot read. " +
			"Pass an environmentId from list_environments to resolve against a collection environment. " +
			"The workspace's active global environment below applies either way, and cannot be selected per call."
	} else {
		out.Note = "Resolved against this collection environment, which is what run_request will use for the same environmentId. " +
			"The workspace's active global environment below also applies, and cannot be selected per call."
	}
	return out
}

// mcpEffectiveSettings reports the transport posture a run would actually use.
//
// VerifyTLS is the interesting one: requestTLSVerificationEnabled ANDs the
// request's flag with the app's SSL-verification preference, so a request
// stored with VerifyTLS true is still sent unverified when the user has turned
// verification off globally. get_request reports the stored flag, which is the
// right answer to ITS question; this is the right answer to "what happens".
// Naming which side turned it off matters because the fix is in a different
// place for each.
func mcpEffectiveSettings(item types.RequestItem, preferences types.RequestPreferences) mcpserver.EffectiveSettings {
	out := mcpserver.EffectiveSettings{
		VerifyTLS:       requestTLSVerificationEnabled(preferences, item.Settings.VerifyTLS),
		FollowRedirects: item.Settings.FollowRedirects,
		MaxRedirects:    item.Settings.MaxRedirects,
		TimeoutMs:       requestTimeoutMilliseconds(item.Settings.TimeoutMs, preferences),
	}
	if !out.VerifyTLS {
		out.VerifyTLSDisabledBy = "appPreference"
		if !item.Settings.VerifyTLS {
			out.VerifyTLSDisabledBy = "request"
		}
	}
	return out
}

// mcpTemplateTokenPattern finds every {{token}} in authored text.
//
// The inner text is captured RAW rather than trimmed, because interp's own
// substitution does not trim either: `{{ baseUrl }}` does not resolve even when
// baseUrl exists, and reporting it as resolved would hide a real bug in the
// user's collection. The classification below trims only where interp's own
// patterns allow surrounding space, which is the process.env form alone.
var mcpTemplateTokenPattern = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// mcpDynamicTokenPattern mirrors interp.dynamicVariablePattern's inner shape:
// {{$name}}, no spaces, resolved by LiteAPI at send time from the generated
// set (timestamp, guid, randomInt and the rest).
var mcpDynamicTokenPattern = regexp.MustCompile(`^\$[A-Za-z][A-Za-z0-9_]*$`)

// mcpVariableReferences reports every {{token}} the effective request reads.
//
// TRANSITIVE BY DESIGN. interp expands up to eight passes, so a URL of
// {{baseUrl}}/x where baseUrl is "{{host}}/api" and host is undefined is a
// request that fails, and an agent told only that baseUrl resolves has been
// misled. Resolved variables whose own values carry templates are therefore
// followed, with a visited set bounding the walk.
//
// A SECRET VARIABLE'S VALUE IS NOT FOLLOWED. Its value is a hydrated credential;
// scanning it would put the plaintext through a regex and could surface a
// fragment of it as a reported "name".
func mcpVariableReferences(
	effective types.RequestItem,
	winners map[string]mcpInspectedVariable,
	resolvable map[string]bool,
) []mcpserver.VariableReference {
	found := map[string]*mcpserver.VariableReference{}
	order := []string{}
	// pending is the transitive worklist: (variable name whose value to scan).
	pending := []string{}
	scanned := map[string]bool{}

	scan := func(where, text string) {
		if !strings.Contains(text, "{{") {
			return
		}
		for _, match := range mcpTemplateTokenPattern.FindAllStringSubmatch(text, -1) {
			name := match[1]
			reference, known := found[name]
			if !known {
				reference = mcpBuildVariableReference(name, winners, resolvable)
				found[name] = reference
				order = append(order, name)
				if reference.Kind == mcpserver.KindVariable && reference.Resolved && !reference.Secret {
					pending = append(pending, name)
				}
			}
			if !mcpContainsString(reference.Where, where) {
				reference.Where = append(reference.Where, where)
			}
		}
	}
	scanRows := func(prefix string, rows []types.KeyValue) {
		for _, row := range rows {
			if !row.Enabled {
				continue
			}
			scan(prefix+":"+row.Name, row.Name)
			scan(prefix+":"+row.Name, row.Value)
		}
	}

	scan("url", effective.URL)
	scan("method", effective.Method)
	scanRows("header", effective.Headers)
	scanRows("param", effective.Params)
	scanRows("pathParam", effective.PathParams)
	// RequestBodySnapshot is the same projection get_request reports, and it
	// already flattens the form and multipart modes into their name=value
	// text — so scanning it covers every body mode with one call, and cannot
	// drift from what the agent was shown.
	scan("body", types.RequestBodySnapshot(effective.Body))
	scan("graphqlVariables", mcpGraphQLVariables(effective.Body))
	// The auth block is scanned through the rows mcpAuthRows already flattens,
	// so a field this adapter does not report cannot be referenced in the
	// report either — one list of auth fields, not two that can drift.
	for _, row := range mcpAuthRows(effective.Auth) {
		scan("auth:"+row.Name, row.Value)
	}
	for _, variable := range effective.Vars.Req {
		if !variable.Enabled || variable.Secret {
			continue
		}
		scan("var:"+variable.Name, envsecrets.ValueToString(variable.Value))
	}

	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if scanned[name] {
			continue
		}
		scanned[name] = true
		winner, known := winners[name]
		if !known || winner.variable.Secret {
			continue
		}
		scan("variable:"+name, envsecrets.ValueToString(winner.variable.Value))
	}

	out := make([]mcpserver.VariableReference, 0, len(order))
	for _, name := range order {
		out = append(out, *found[name])
	}
	return out
}

// mcpBuildVariableReference classifies one token and answers whether it
// resolves.
func mcpBuildVariableReference(
	name string,
	winners map[string]mcpInspectedVariable,
	resolvable map[string]bool,
) *mcpserver.VariableReference {
	reference := &mcpserver.VariableReference{Name: name}
	trimmed := strings.TrimSpace(name)
	switch {
	case strings.HasPrefix(trimmed, "process.env."):
		reference.Kind = mcpserver.KindProcessEnv
		reference.Note = "Resolved at send time from the process environment and the collection's .env file. " +
			"Whether it is set is deliberately not reported: .env is where credentials live, and even a yes/no answer is an oracle over it."
	case mcpDynamicTokenPattern.MatchString(name):
		reference.Kind = mcpserver.KindDynamic
		reference.Resolved = true
		reference.Note = "Generated by LiteAPI at send time, fresh per occurrence. Nothing to supply."
	case strings.HasPrefix(name, "?"):
		reference.Kind = mcpserver.KindPrompt
		reference.Note = "A prompt variable: LiteAPI asks the USER for this value in the app. A run started over MCP has nobody to ask, so tell the user rather than guessing a value."
	default:
		reference.Kind = mcpserver.KindVariable
		reference.Resolved = resolvable[name]
		if winner, known := winners[name]; known {
			reference.Level = winner.level
			reference.LevelPath = winner.levelPath
			reference.Secret = winner.variable.Secret
			if reference.Secret {
				reference.Note = "Resolves to a secret variable. It resolves inside LiteAPI at send time; its value is not readable here and you never need it."
			}
		} else if reference.Resolved {
			// In Combined but not in any authored scope: a value a script
			// persisted, or a process-env row that reached the combined map.
			reference.Level = mcpserver.LevelRuntime
		} else {
			reference.Note = "Nothing in scope defines this name. Supply it in run_request's variables, or pick an environment that defines it."
		}
	}
	return reference
}

// mcpUnresolvedNames is the short answer an agent acts on: the ordinary
// variables nothing in scope defines, sorted and deduplicated.
//
// Prompt and process.env references are deliberately NOT here. Neither is
// supplied the way an unresolved variable is — one needs the user, the other
// needs the machine's environment — so folding them in would produce a list an
// agent cannot act on uniformly. They are reported in References, each with the
// note that says what to do instead.
func mcpUnresolvedNames(references []mcpserver.VariableReference) []string {
	names := []string{}
	for _, reference := range references {
		if reference.Kind == mcpserver.KindVariable && !reference.Resolved {
			names = append(names, reference.Name)
		}
	}
	sort.Strings(names)
	return names
}

func mcpContainsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func (b *mcpBackend) ListEnvironments() ([]mcpserver.EnvironmentSummary, error) {
	type environmentRow struct {
		summary   mcpserver.EnvironmentSummary
		variables []types.Variable
	}
	var rows []environmentRow
	if err := b.app.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			workspace := &state.Workspaces[wi]
			for ei := range workspace.GlobalEnvironments {
				environment := &workspace.GlobalEnvironments[ei]
				rows = append(rows, environmentRow{
					summary: mcpserver.EnvironmentSummary{
						ID:     environment.ID,
						Name:   environment.Name,
						Scope:  "global",
						Active: environment.ID != "" && environment.ID == workspace.ActiveGlobalEnvironmentID,
					},
					variables: types.CloneVariables(environment.Variables),
				})
			}
			for ci := range workspace.Collections {
				collection := &workspace.Collections[ci]
				for ei := range collection.Environments {
					environment := &collection.Environments[ei]
					rows = append(rows, environmentRow{
						summary: mcpserver.EnvironmentSummary{
							ID:           environment.ID,
							Name:         environment.Name,
							Scope:        "collection",
							CollectionID: collection.ID,
							// ALWAYS FALSE, and the reason is worth stating
							// precisely because the tool descriptions used to
							// contradict it. Which collection environment is
							// selected IS persisted — in the WebView's own
							// storage, by workspaceStore.selectedEnvironmentId
							// (frontend/src/lib/environmentSelection.ts) — but
							// it is never written to AppState, so this process
							// cannot read it and claiming one here would be a
							// guess. Only the workspace-global selection lives
							// in state, and that is what Active reports above.
							//
							// The consequence for the run and inspect tools:
							// omitting environmentId cannot mean "use the
							// active one", because there is no active one to
							// use. It means no collection environment applies.
							// mcpInspectedEnvironment says so to the agent, and
							// the tool descriptions now say the same.
							Active: false,
						},
						variables: types.CloneVariables(environment.Variables),
					})
				}
			}
		}
	}); err != nil {
		return nil, err
	}

	out := make([]mcpserver.EnvironmentSummary, 0, len(rows))
	for _, row := range rows {
		summary := row.summary
		summary.Variables = make([]mcpserver.EnvironmentVariable, 0, len(row.variables))
		for _, variable := range row.variables {
			entry := mcpserver.EnvironmentVariable{
				Name:    variable.Name,
				Secret:  variable.Secret,
				Enabled: variable.Enabled,
			}
			// THE BRANCH THAT MATTERS. By the time state is readable here the
			// secret store has been decrypted into memory
			// (hydrateStateEnvironmentSecretsLocked), so variable.Value holds
			// the real credential — not a placeholder. Converting it and then
			// masking would put the plaintext through a code path that could be
			// reordered later; the value is simply never read for a secret.
			if !variable.Secret {
				entry.Value = envsecrets.ValueToString(variable.Value)
			}
			summary.Variables = append(summary.Variables, entry)
		}
		out = append(out, summary)
	}
	return out, nil
}

func (b *mcpBackend) GetHistory(collectionID, requestID string, limit int) ([]mcpserver.HistoryRun, error) {
	collectionID = strings.TrimSpace(collectionID)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, errors.New("requestId is required")
	}
	limit = mcpBoundedLimit(limit, mcpHistoryDefaultLimit, mcpHistoryMaxLimit)

	// THE BELT, NOT THE BOUNDARY (Phase 6 §7). Masking against the values the
	// process holds RIGHT NOW is what this method used to rely on entirely, and
	// it is precisely what rotation defeats: history is recorded after
	// interpolation, so a request templated as ?key={{secret}} has the resolved
	// credential in its recorded URL — and once the variable is rotated or
	// deleted, that old value is no longer in this set and would come straight
	// back out. The real protection is the record-time projection below, which
	// was masked when those values were still live. Current-value masking is
	// kept on top because it costs nothing and catches a value the record-time
	// pass could not have known about.
	secretValues, err := b.app.mcpHydratedSecretValues()
	if err != nil {
		return nil, err
	}

	// HistoryQuery filters by collection but not by item, so the store's own
	// limit cannot be used: applied before the item filter it would return the
	// newest N entries of the collection and then leave nothing for this
	// request. Ask for the store's full retention and cut after filtering.
	entries, err := b.app.history().List(history.HistoryQuery{
		CollectionID: collectionID,
		Limit:        history.Limit,
	})
	if err != nil {
		return nil, err
	}

	// List already returns newest first.
	out := make([]mcpserver.HistoryRun, 0, limit)
	for _, entry := range entries {
		if entry.ItemID != requestID {
			continue
		}
		// The four fields taken from the entry itself are the ones that cannot
		// carry a resolved value: an id, a verb, a status code and a clock. The
		// URL, the headers and the body come ONLY from the projection.
		run := mcpserver.HistoryRun{
			ID:         entry.ID,
			Method:     entry.Method,
			Status:     entry.Status,
			DurationMs: int(entry.DurationMs),
			ExecutedAt: entry.At.UTC().Format(time.RFC3339),
		}
		if projection, ok := b.app.history().MCPProjection(entry.ID); ok {
			run.URL = mcpserver.MaskKnownSecretValues(projection.URL, secretValues)
			run.Headers = mcpMaskRowValues(mcpKeyValueRows(projection.ResponseHeaders), secretValues)
			run.Body = mcpserver.MaskKnownSecretValues(projection.Body, secretValues)
			run.Truncated = projection.Truncated
		} else {
			// A PLACEHOLDER, NOT THE RAW ENTRY, and this is the entire point of
			// §7. An entry recorded before the projection existed has a
			// post-interpolation URL and body that were never masked against
			// the values live at the time. Falling back to them "just for old
			// entries" would preserve exactly the leak this task closes, and
			// would do it silently. An artifact that never contained the value
			// cannot leak it; an artifact that does cannot be made safe after
			// the fact.
			run.URL = mcpHistoryUnprojectedURL
			run.Body = mcpHistoryUnprojectedBody
		}
		out = append(out, run)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// mcpHydratedSecretValues collects every hydrated secret value the process
// knows about — global and collection environment variables plus request-level
// vars flagged secret — so post-interpolation artifacts can be scrubbed by
// exact value. The strings leave the lock, but only into the masker, which
// replaces them; no caller ever returns one.
//
// ONE WALK, TWO DOORS. The walk itself is mcpSecretValuesLocked (app_send.go),
// which takes no lock because the send path calls it with a.mu already held —
// §7 requires the values to be hydrated at the head of the send, and the
// hydrator that takes the lock cannot be reached from there without
// deadlocking. This is the other door: the agent-facing readers run on the MCP
// server's goroutines, which hold nothing, so they get the same walk with the
// lock supplied around it. The duplicate implementation the send path landed
// with is gone; only the two entry points remain.
func (a *App) mcpHydratedSecretValues() ([]string, error) {
	var values []string
	if err := a.readStateForMCP(func(state *AppState) {
		values = mcpSecretValuesLocked(state)
	}); err != nil {
		return nil, err
	}
	return values, nil
}

// mcpMaskRowValues runs the known-value scrub over each row's value.
func mcpMaskRowValues(rows []mcpserver.KeyValue, secretValues []string) []mcpserver.KeyValue {
	for index := range rows {
		rows[index].Value = mcpserver.MaskKnownSecretValues(rows[index].Value, secretValues)
	}
	return rows
}

// RunRequest is the Phase 2 run tier and lives in mcp_run.go, with the new-host
// guard it enforces in mcp_guard.go. It is the one Backend method that is not
// implemented in this file, because it is the one that does not read state: it
// executes the app's own send path, and the "no interpolation" rule at the top
// of this file is exactly inverted for it.

// --- mapping helpers -------------------------------------------------------

// mcpItemCopy deep-copies one request out of state. Reusing the folder-clone
// helper rather than writing a second one: it clones every mutable field of a
// RequestItem and drops Response and Timeline, which is precisely the shape
// this adapter wants — the last response is a run artifact, not part of the
// definition an agent asked for.
func mcpItemCopy(item types.RequestItem) types.RequestItem {
	return types.CloneRequestItemForFolderClone(item)
}

// mcpRequestSummary builds the row every read tool returns — and, because
// RequestDetail embeds it, the URL in get_request too. That makes it the ONE
// place a request's URL leaves this adapter, which is why the query-literal
// masking lives here rather than at each call site: a later tool that reports a
// request cannot forget to apply it.
//
// A URL is not just addressing. "?api_key=sk_live_..." pasted from a working
// curl never becomes a Params row, so RedactRows never sees it; without this it
// would ship byte-for-byte. Only credential-shaped query VALUES that are
// literals are touched — {{templates}} and everything outside the query string
// come back exactly as authored (see mcpserver.RedactURLQueryLiterals).
func mcpRequestSummary(row requestRow) mcpserver.RequestSummary {
	return mcpserver.RequestSummary{
		ID:           row.item.ID,
		CollectionID: row.collectionID,
		Name:         row.item.Name,
		Type:         row.item.Type,
		Method:       row.item.Method,
		URL:          mcpserver.RedactURLQueryLiterals(row.item.URL),
		FolderPath:   row.item.FolderPath,
	}
}

// The levels AuthSource names. A request that configures its own auth reports
// "request"; an inheriting one reports whichever level the send path would
// actually take the credentials from.
const (
	mcpAuthSourceRequest    = "request"
	mcpAuthSourceFolder     = "folder"
	mcpAuthSourceCollection = "collection"
)

// mcpFolderCopies deep-copies a collection's folder configs out from under the
// lock. Only Path and Auth are read afterwards, but cloning whole is cheaper to
// keep correct than a partial copy that a later field would quietly outgrow.
func mcpFolderCopies(folders []types.FolderConfig) []types.FolderConfig {
	if len(folders) == 0 {
		return nil
	}
	out := make([]types.FolderConfig, 0, len(folders))
	for _, folder := range folders {
		out = append(out, types.CloneFolderConfigForFolderClone(folder))
	}
	return out
}

// mcpEffectiveAuth resolves the auth a run of this request would actually use,
// and names the level it came from.
//
// This MIRRORS the send path rather than reinventing it: scripting.EffectiveRequest
// (internal/scripting/run.go:397-407) is what every Send, Flow and code-generation
// path goes through, and it is called here with the same FolderChain helper it
// uses, so a divergence between "what get_request reports" and "what run_request
// will send" cannot open up quietly. Two of its behaviours are subtle and are
// reproduced deliberately:
//
//   - The folder chain runs outermost to innermost (scripting.FolderChain), and
//     the walk keeps the LAST match — so the innermost folder that configures
//     auth wins over its parents, and any folder wins over the collection.
//   - The test is folder.Auth.Mode != "", NOT "is a real mode". A folder whose
//     own mode is "inherit" therefore SHADOWS the collection and ends up
//     applying no auth at all. That is what a run does, so that is what is
//     reported: the caller turns a resolved mode of "inherit" into "nothing is
//     configured" rather than pretending the collection's block applies.
//
// A request whose own mode is anything else is explicit and short-circuits.
func mcpEffectiveAuth(collection types.Collection, item types.RequestItem) (types.AuthConfig, string) {
	if item.Auth.Mode != "inherit" && item.Auth.Mode != "" {
		return item.Auth, mcpAuthSourceRequest
	}
	auth := collection.Auth
	source := mcpAuthSourceCollection
	for _, folder := range scripting.FolderChain(collection, item) {
		if folder.Auth.Mode != "" {
			auth = folder.Auth
			source = mcpAuthSourceFolder
		}
	}
	// EffectiveRequest leaves the item's own (empty or inheriting) auth in place
	// when nothing upstream configures one; nothing is applied at send time, so
	// nothing is reported here.
	if auth.Mode == "" || strings.EqualFold(strings.TrimSpace(auth.Mode), "inherit") {
		return types.AuthConfig{}, ""
	}
	return auth, source
}

// mcpKeyValueRows narrows the app's KeyValue — which also carries Secret and
// Description — to the three fields the contract has. The narrowing is the
// point: there is no field on mcpserver.KeyValue that could carry anything
// else, so nothing extra can ride along by accident.
func mcpKeyValueRows(rows []types.KeyValue) []mcpserver.KeyValue {
	if len(rows) == 0 {
		return nil
	}
	out := make([]mcpserver.KeyValue, 0, len(rows))
	for _, row := range rows {
		out = append(out, mcpserver.KeyValue{Name: row.Name, Value: row.Value, Enabled: row.Enabled})
	}
	return out
}

// mcpVariableRows flattens a request's pre- and post-request variables.
//
// A request variable marked secret has its value dropped before masking rather
// than after, for the same reason as an environment secret: the value is not
// read at all. Everything else goes through RedactRows at the call site, which
// masks literals on credential-shaped names.
func mcpVariableRows(vars types.RequestVars) []mcpserver.KeyValue {
	rows := make([]mcpserver.KeyValue, 0, len(vars.Req)+len(vars.Res))
	for _, group := range [][]types.Variable{vars.Req, vars.Res} {
		for _, variable := range group {
			row := mcpserver.KeyValue{Name: variable.Name, Enabled: variable.Enabled}
			if !variable.Secret {
				row.Value = envsecrets.ValueToString(variable.Value)
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return rows
}

// mcpAuthRows flattens the populated fields of an auth block into named rows,
// which MaskAuthRows then masks: auth rows are credentials by construction, so
// literals are hidden unless the row NAME is on mcpserver's short allowlist of
// fields that address a provider rather than authenticate to it.
//
// Row names are therefore chosen to match that allowlist wherever the field is
// genuinely addressing — "clientId", "accessTokenUrl", "grantType", "key",
// "region". Naming an addressing field something the allowlist does not know
// would mask, say, an AWS region to "<masked>": no safer, and useless to the
// agent that needs it to reproduce the call. Empty fields are skipped so the
// rows describe what is actually configured.
func mcpAuthRows(auth types.AuthConfig) []mcpserver.KeyValue {
	rows := []mcpserver.KeyValue{}
	add := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		rows = append(rows, mcpserver.KeyValue{Name: name, Value: value, Enabled: true})
	}
	switch strings.ToLower(strings.TrimSpace(auth.Mode)) {
	case "", "none", "inherit":
		return nil
	case "bearer":
		add("token", auth.Token)
	case "apikey":
		add("key", auth.APIKey)
		add("value", auth.APIValue)
		add("addTo", auth.APILocation)
	case "awsv4":
		add("accessKeyId", auth.AWSV4.AccessKeyID)
		add("secretAccessKey", auth.AWSV4.SecretAccessKey)
		add("sessionToken", auth.AWSV4.SessionToken)
		add("service", auth.AWSV4.Service)
		add("region", auth.AWSV4.Region)
		add("profile", auth.AWSV4.ProfileName)
	case "oauth1":
		add("consumerKey", auth.OAuth1.ConsumerKey)
		add("consumerSecret", auth.OAuth1.ConsumerSecret)
		add("accessToken", auth.OAuth1.AccessToken)
		add("accessTokenSecret", auth.OAuth1.AccessTokenSecret)
		add("callbackUrl", auth.OAuth1.CallbackURL)
		add("signatureMethod", auth.OAuth1.SignatureMethod)
		add("realm", auth.OAuth1.Realm)
		add("version", auth.OAuth1.Version)
	case "oauth2":
		add("grantType", auth.OAuth2.GrantType)
		add("authorizationUrl", auth.OAuth2.AuthorizationURL)
		add("accessTokenUrl", auth.OAuth2.AccessTokenURL)
		add("refreshTokenUrl", auth.OAuth2.RefreshTokenURL)
		add("callbackUrl", auth.OAuth2.CallbackURL)
		add("clientId", auth.OAuth2.ClientID)
		add("clientSecret", auth.OAuth2.ClientSecret)
		add("scope", auth.OAuth2.Scope)
		add("username", auth.OAuth2.Username)
		add("password", auth.OAuth2.Password)
	default:
		// basic, digest, ntlm, wsse and anything a later build adds: the shared
		// credential fields on AuthConfig. The default branch is deliberate —
		// an unrecognised mode falls through to masking rather than to
		// returning the raw struct.
		add("username", auth.Username)
		add("password", auth.Password)
		add("domain", auth.Domain)
		add("token", auth.Token)
	}
	if len(rows) == 0 {
		return nil
	}
	return rows
}

// mcpBoundedLimit applies "0 means the default, above the cap means the cap".
// A negative limit is a caller mistake rather than a request for everything, so
// it takes the default too.
func mcpBoundedLimit(limit, fallback, ceiling int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > ceiling {
		return ceiling
	}
	return limit
}
