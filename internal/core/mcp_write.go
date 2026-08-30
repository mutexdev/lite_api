package core

// The write tier — Phase 4 of docs/mcp-agent-interface.md, and the first tier
// where an agent changes something inside LiteAPI rather than outside it.
//
// FOUR RULES GOVERN THIS FILE, and three of them are refusals.
//
// 1. THE TIER GATE IS READ PER CALL. Rule 5 says the write tools are "rejected
//    until the user enables writes in Settings". The preference is read fresh
//    from state on every call, not captured when the server started: the user
//    can turn it off while an agent is mid-task, and the next call must see
//    that. The tools stay in tools/list either way — a tool that vanished would
//    tell the agent the capability does not exist, and it would go and build a
//    worse substitute by hand instead of asking for the one switch that works.
//
// 2. NOTHING IS AUTHORED THROUGH A PARALLEL WRITER. Everything here goes
//    through the same App methods the UI's own CRUD goes through —
//    CreateRequestInFolder, UpdateRequest, SaveRequest, CreateFlow, UpdateFlow —
//    so an agent-authored request gets the same model, the same validation, the
//    same file writer, the same watcher fingerprint and the same tab as one the
//    user typed. A second writer that produced almost the same bytes is the
//    failure mode this tier cannot afford, because the drift would only show up
//    as the user's collection quietly disagreeing with itself.
//
// 3. NO SCRIPTS, EVER. mcp_guard.go's KNOWN LIMITATIONS header states the
//    property this whole design rests on: the new-host guard reasons about a
//    request's DEFINITION and runs before the send, so a pre-request script can
//    rewrite req.url after the guard has passed. That is accepted only because
//    "an agent cannot author or edit one while the write tier is off". Phase 4
//    turns the write tier on, so the exemption has to be paid for HERE: create
//    refuses a script or a test outright, and update preserves the stored ones
//    byte-for-byte. An agent echoing get_request's output back is therefore
//    fine; an agent editing a script is refused. Without this, mcp_guard.go's
//    stated limitation would become a hole and the guard would claim a property
//    it no longer has.
//
// 4. THE AUTHORING-TIME HOST GUARD, which is the load-bearing piece. The run
//    guard's allowlist is COMPUTED from the requests that reference each secret
//    (mcpKnownHostsForSecret). Authoring therefore writes the allowlist: a
//    request an agent saved pointing {{apiToken}} at evil.test would teach the
//    guard that evil.test is a legitimate destination for that secret, and
//    every later run to it would pass unchecked. So the same question the run
//    guard asks is asked BEFORE the save, with the same helpers, and a
//    (secret, host) pair the collections have never used raises the same
//    approval prompt. A request that references no secret, or whose URL does
//    not resolve to a host yet, saves without a prompt — an unresolvable URL
//    contributes no host to the allowlist either, so it teaches the guard
//    nothing and cannot be sent until it resolves, at which point the run guard
//    checks it.
//
// LOCKING follows mcp_backend.go's rule: nothing here holds a.mu. The copy-out
// helpers take it briefly, and the App methods this file calls take it
// themselves — which is why the guard, which can block on a user for a minute,
// runs strictly between them and never inside one.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/scripting"
	"github.com/mutexdev/lite_api/internal/types"
)

// mcpAuthorableRequestTypes are the kinds an agent may create.
//
// ws and grpc are excluded because they are not requests in the same sense: a
// WebSocket carries a message list and a gRPC call needs a proto path and a
// method type, neither of which an agent can supply from a collection listing.
// A refusal that names them is better than a half-built socket request.
var mcpAuthorableRequestTypes = map[string]bool{"http": true, "graphql": true}

// mcpAuthorableBodyModes are the body modes an agent may author. Multipart and
// file bodies are absent on purpose: both reference paths on the user's disk,
// which an agent has no business choosing.
var mcpAuthorableBodyModes = map[string]bool{
	"none": true, "json": true, "text": true, "xml": true,
	"form-urlencoded": true, "graphql": true,
}

// mcpAuthFields is the auth block an agent may author, per mode. The modes that
// are missing (oauth2, awsv4, oauth1, ntlm, wsse) are configured by the user in
// the app: they carry flows, callbacks and signing parameters that belong to
// the person who owns the account.
var mcpAuthFields = map[string]map[string]bool{
	"none":    {},
	"inherit": {},
	"basic":   {"username": true, "password": true},
	"bearer":  {"token": true},
	"apikey":  {"key": true, "value": true, "addto": true},
}

// --- the tier gate ---------------------------------------------------------

// WriteTierEnabled reports the preference as it stands right now.
func (b *mcpBackend) WriteTierEnabled() (bool, error) {
	return b.app.mcpWriteTierEnabled()
}

func (a *App) mcpWriteTierEnabled() (bool, error) {
	enabled := false
	if err := a.readStateForMCP(func(state *AppState) {
		enabled = state.Preferences.MCP.WriteTierEnabled
	}); err != nil {
		return false, err
	}
	return enabled, nil
}

// mcpWriteTierGate refuses every write while the preference is off.
//
// The message is written for the agent that reads it, like every other denial
// in this tier: it says what is off, that only the USER can change it, exactly
// where, and that there is nothing to work around. An agent told merely "not
// allowed" reliably tries the next thing it can think of.
func (a *App) mcpWriteTierGate(tool string) error {
	enabled, err := a.mcpWriteTierEnabled()
	if err != nil {
		return err
	}
	if enabled {
		return nil
	}
	return fmt.Errorf("%w: %s needs LiteAPI's write tier, which is off. Only the user can turn it on, in LiteAPI's Settings → AI access (\"Allow AI tools to create and edit requests\"). Ask them to enable it and try again; you cannot enable it yourself, and there is no way around it",
		mcpserver.ErrDenied, tool)
}

// --- create_request --------------------------------------------------------

// CreateRequest authors a request and returns the row it created.
func (b *mcpBackend) CreateRequest(ctx context.Context, params mcpserver.CreateRequestParams) (mcpserver.RequestSummary, error) {
	if err := b.app.mcpWriteTierGate("create_request"); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	collectionID := strings.TrimSpace(params.CollectionID)
	name := strings.TrimSpace(params.Name)
	if collectionID == "" {
		return mcpserver.RequestSummary{}, errors.New("collectionId is required; call list_collections for the ids that exist")
	}
	if name == "" {
		return mcpserver.RequestSummary{}, errors.New("name is required; it is what the request is called in the user's tree and on disk")
	}
	if err := mcpRefuseAuthoredScripts("create_request", params.PreScript, params.PostScript, params.Tests); err != nil {
		return mcpserver.RequestSummary{}, err
	}

	requestType, err := mcpAuthoredType(params.Type)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	// The base a create folds onto MIRRORS types.NewRequestItem's auth default
	// ("none", not the zero value), because that is what CreateRequestInFolder
	// will really store. An empty mode would inherit the collection's auth
	// block in the guard's eyes and not in the saved request's, and a guard
	// reasoning about a different request than the one being saved is worth
	// nothing in either direction.
	base := types.RequestItem{Type: requestType, Auth: types.AuthConfig{Mode: "none", APILocation: "header"}}
	draft, err := mcpAuthoredFields(mcpAuthoredRequest{
		Type:             requestType,
		Method:           params.Method,
		URL:              params.URL,
		Headers:          &params.Headers,
		Params:           &params.Params,
		PathParams:       &params.PathParams,
		Vars:             &params.Vars,
		BodyType:         &params.BodyType,
		Body:             &params.Body,
		GraphQLVariables: &params.GraphQLVariables,
		FormData:         &params.FormData,
		Auth:             params.Auth,
	}, base)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	if strings.TrimSpace(draft.URL) == "" {
		return mcpserver.RequestSummary{}, errors.New("url is required; write it as the user would, with {{templates}} unresolved, e.g. \"{{baseUrl}}/terminals\"")
	}
	draft.Name = name
	// RESOLVED WITH THE APP'S OWN LOOKUP, before the guard rather than after.
	// A folder can be addressed by its display path, and CreateRequestInFolder
	// stores the real one; guarding the raw string would mean guarding a
	// request whose folder chain — and therefore whose folder-level variables —
	// differ from the one that ends up saved, which is precisely the shape of a
	// guard that resolves no host and waves the save through.
	folderPath, err := b.app.mcpResolveFolderPath(collectionID, params.FolderPath)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	draft.FolderPath = folderPath

	// THE GUARD RUNS BEFORE ANYTHING IS CREATED. Not after: a request that
	// exists in state while the user is being asked about it is a request the
	// watcher, a save, or another binding could reach, and a denial would then
	// have something to undo.
	if err := b.app.enforceMCPAuthoringGuard(ctx, collectionID, draft, ""); err != nil {
		return mcpserver.RequestSummary{}, err
	}

	before, err := b.app.mcpCollectionItemIDs(collectionID)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	// draft.FolderPath is already the resolved form, and passing it back through
	// the same resolver is a no-op — which is the point: the request that is
	// created lands exactly where the guard assumed it would.
	state, err := b.app.CreateRequestInFolder(collectionID, requestType, name, draft.FolderPath)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	requestID, found := mcpCreatedItemID(state, collectionID, before)
	if !found {
		return mcpserver.RequestSummary{}, errors.New("the request was created but could not be identified afterwards; this is a bug in LiteAPI, not something to retry")
	}

	// A failure from here on leaves the request in the app as an UNSAVED DRAFT,
	// which is exactly what a failed save leaves for the user, and is why there
	// is no rollback: DeleteRequest requires a file on disk that this request
	// does not have yet, and inventing a second removal path for one error
	// branch would be a worse risk than a visible draft the user can discard.
	if err := b.app.mcpApplyAuthoredRequest(collectionID, requestID, draft); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	return b.app.mcpRequestSummaryByID(collectionID, requestID)
}

// --- update_request --------------------------------------------------------

// UpdateRequest edits a stored request in place, matched by id.
func (b *mcpBackend) UpdateRequest(ctx context.Context, params mcpserver.UpdateRequestParams) (mcpserver.RequestSummary, error) {
	if err := b.app.mcpWriteTierGate("update_request"); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	collectionID := strings.TrimSpace(params.CollectionID)
	requestID := strings.TrimSpace(params.RequestID)
	if collectionID == "" || requestID == "" {
		return mcpserver.RequestSummary{}, errors.New("collectionId and requestId are both required; call list_requests for the ids that exist")
	}

	existing, err := b.app.mcpStoredRequest(collectionID, requestID)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	// The scripts are checked against what is STORED, not against emptiness:
	// an agent that read the request with get_request and sent the whole thing
	// back must succeed, and an agent that changed one line must not.
	if err := mcpRefusePreservedScript("preScript", params.PreScript, existing.PreScript); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	if err := mcpRefusePreservedScript("postScript", params.PostScript, existing.PostScript); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	if err := mcpRefusePreservedScript("tests", params.Tests, existing.Tests); err != nil {
		return mcpserver.RequestSummary{}, err
	}

	merged, err := mcpAuthoredFields(mcpAuthoredRequest{
		Type:             existing.Type,
		Method:           mcpPatchedString(params.Method),
		URL:              mcpPatchedString(params.URL),
		Headers:          params.Headers,
		Params:           params.Params,
		PathParams:       params.PathParams,
		Vars:             params.Vars,
		BodyType:         params.BodyType,
		Body:             params.Body,
		GraphQLVariables: params.GraphQLVariables,
		FormData:         params.FormData,
		Auth:             params.Auth,
	}, existing)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	if strings.TrimSpace(merged.URL) == "" {
		return mcpserver.RequestSummary{}, errors.New("url cannot be emptied; pass the URL you want, with {{templates}} unresolved, or omit url to keep the stored one")
	}
	// Identity and placement are carried over rather than patched: the write
	// tier does not rename or move (see the tool description), and the guard
	// below reads FolderPath to find the folder chain this request inherits
	// auth and variables from.
	merged.ID = existing.ID
	merged.Name = existing.Name
	merged.FolderPath = existing.FolderPath
	merged.PreScript = existing.PreScript
	merged.PostScript = existing.PostScript
	merged.Tests = existing.Tests

	if err := b.app.enforceMCPAuthoringGuard(ctx, collectionID, merged, requestID); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	if err := b.app.mcpApplyAuthoredRequest(collectionID, requestID, merged); err != nil {
		return mcpserver.RequestSummary{}, err
	}
	return b.app.mcpRequestSummaryByID(collectionID, requestID)
}

// mcpApplyAuthoredRequest is the persistence half, and it is deliberately three
// lines of calling the app's own bindings: patch the item the way the request
// editor does, then save it the way the save button does. The file writer, the
// US-015 fingerprint gate and the watcher all follow from SaveRequest.
func (a *App) mcpApplyAuthoredRequest(collectionID, requestID string, authored types.RequestItem) error {
	patch := RequestPatch{
		Method:     &authored.Method,
		URL:        &authored.URL,
		Headers:    &authored.Headers,
		Params:     &authored.Params,
		PathParams: &authored.PathParams,
		Body:       &authored.Body,
		Auth:       &authored.Auth,
		Vars:       &authored.Vars,
	}
	if _, err := a.UpdateRequest(collectionID, requestID, patch); err != nil {
		return err
	}
	if _, err := a.SaveRequest(collectionID, requestID); err != nil {
		return err
	}
	return nil
}

// --- the authoring-time host guard -----------------------------------------

// enforceMCPAuthoringGuard decides whether the authored definition may be
// saved, and blocks on the user when it points a secret somewhere new.
//
// replacingID names the request this definition would REPLACE, so an update is
// judged against the collection minus its own previous version — otherwise a
// request would authorise its own retargeting by already being in the
// allowlist. It is empty for a create.
//
// Everything here resolves the way mcpKnownHostsForSecret resolves, and through
// the same helpers: mcpSecretNamesInScope for which names are secret,
// mcpReferencedSecrets for which ones the definition uses, mcpHostOfURL for
// where it points, under every environment the collection defines plus none.
// Any divergence between "the hosts authoring checks" and "the hosts running
// learns" would be a gap in exactly the shape of the attack this closes.
func (a *App) enforceMCPAuthoringGuard(ctx context.Context, collectionID string, candidate types.RequestItem, replacingID string) error {
	collections, err := a.mcpGuardCollections()
	if err != nil {
		return err
	}
	owner, found := mcpFindGuardCollection(collections, collectionID)
	if !found {
		return fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}

	// The collection AS IT WOULD BE, so folder auth, collection auth and the
	// collection's own variables all apply to the candidate exactly as they
	// will once it is saved.
	hypothetical := owner.collection
	items := make([]types.RequestItem, 0, len(owner.collection.Items)+1)
	for _, item := range owner.collection.Items {
		if replacingID != "" && item.ID == replacingID {
			continue
		}
		items = append(items, item)
	}
	hypothetical.Items = append(items, candidate)
	effective := scripting.EffectiveRequest(hypothetical, candidate)

	environmentIDs := []string{""}
	for _, environment := range hypothetical.Environments {
		environmentIDs = append(environmentIDs, environment.ID)
	}

	// host -> the secrets this definition would send there.
	targets := map[string]map[string]bool{}
	for _, environmentID := range environmentIDs {
		secretsInScope := mcpSecretNamesInScope(owner.globalEnvironments, hypothetical, environmentID, candidate)
		referenced := mcpReferencedSecrets(effective, secretsInScope)
		if len(referenced) == 0 {
			continue
		}
		vars := scripting.BuildVariableMap(owner.globalEnvironments, &hypothetical, environmentID, candidate, owner.workspacePath)
		host := mcpHostOfURL(effective.URL, vars)
		if host == "" {
			// Nowhere to send it yet. A URL that resolves to no host teaches
			// the allowlist nothing (mcpKnownHostsForSecret skips it too) and
			// cannot reach a server, so there is nothing to approve; when the
			// variables that complete it exist, the run guard is what checks.
			continue
		}
		if targets[host] == nil {
			targets[host] = map[string]bool{}
		}
		for _, name := range referenced {
			targets[host][name] = true
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// The allowlist as it stands WITHOUT this definition: the collections as
	// they are on disk, plus what the user has already remembered.
	//
	// SCOPED BY DEFINITION SITE, exactly as the run guard scopes it
	// (mcpSecretOwner, mcp_guard.go). The candidate is judged against the
	// collection as it WOULD BE, so a secret the candidate's own vars introduce
	// is collection-owned here too. The remembered half is unscoped by design —
	// see mcpSecretsWithoutHost.
	collectionScoped := mcpCollectionScopedSecretNames(hypothetical)
	known := map[string]map[string]bool{}
	knownFor := func(secretName string) (map[string]bool, error) {
		if hosts, cached := known[secretName]; cached {
			return hosts, nil
		}
		hosts, err := a.mcpRememberedHostsForSecret(secretName)
		if err != nil {
			return nil, err
		}
		site := mcpSecretOwnerIn(owner, collectionScoped, secretName)
		for host := range mcpKnownHostsForSecret(collections, site, secretName) {
			hosts[host] = true
		}
		known[secretName] = hosts
		return hosts, nil
	}

	for _, host := range mcpSortedNames(mcpMapKeys(targets)) {
		var unknown []string
		for _, name := range mcpSortedNames(mcpMapKeys(targets[host])) {
			hosts, err := knownFor(name)
			if err != nil {
				return err
			}
			if !hosts[host] {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) == 0 {
			continue
		}
		// ONE PROMPT PER HOST, naming every secret that would travel there.
		// Per (secret, host) pair would ask the same question twice about the
		// same decision, and a user answering a queue of near-identical dialogs
		// stops reading them — which is the failure mode that makes an approval
		// prompt worthless.
		if a.requestMCPApproval(ctx, types.MCPApprovalRequest{
			RequestName: candidate.Name,
			Host:        host,
			SecretNames: unknown,
		}) {
			continue
		}
		return fmt.Errorf("%w: saving this request would point the secret %s at %s, which no request in the open collections sends it to, so it was not saved. Ask the user to approve that host in LiteAPI (or to point you at the right one); do not retry and do not work around it",
			mcpserver.ErrDenied, mcpJoinSecretNames(unknown), host)
	}
	return nil
}

func mcpFindGuardCollection(collections []mcpGuardCollection, collectionID string) (mcpGuardCollection, bool) {
	for _, candidate := range collections {
		if candidate.collection.ID == collectionID {
			return candidate, true
		}
	}
	return mcpGuardCollection{}, false
}

func mcpMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

// --- the refusals ----------------------------------------------------------

// mcpRefuseAuthoredScripts is rule 3 for a create: any script or test at all is
// refused, with the reason stated rather than implied.
func mcpRefuseAuthoredScripts(tool, preScript, postScript, tests string) error {
	var supplied []string
	for _, field := range []struct {
		name  string
		value string
	}{{"preScript", preScript}, {"postScript", postScript}, {"tests", tests}} {
		if strings.TrimSpace(field.value) != "" {
			supplied = append(supplied, field.name)
		}
	}
	if len(supplied) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s cannot author %s. Scripts run inside the user's own engine and can rewrite a request after LiteAPI's new-host guard has already checked it, which would let a credential be retargeted past the guard. Send the request without them and ask the user to write any script themselves in the app",
		mcpserver.ErrDenied, tool, strings.Join(supplied, " or "))
}

// mcpRefusePreservedScript is rule 3 for an update: equal or empty passes and
// changes nothing, different is refused.
//
// EMPTY PASSES rather than clearing. An agent that omits the field, or sends
// "", is not asking to delete the user's script — it is composing a patch out
// of the fields it cares about — and treating that as a deletion would let an
// agent destroy work it never read.
func mcpRefusePreservedScript(field string, supplied *string, stored string) error {
	if supplied == nil || *supplied == "" || *supplied == stored {
		return nil
	}
	return fmt.Errorf("%w: update_request cannot change %s, and the value supplied differs from the stored one. Pass it back exactly as get_request returned it, or leave it out — it is preserved either way. Scripts run inside the user's own engine and can rewrite a request after LiteAPI's new-host guard has checked it, so editing one over MCP would let a credential be retargeted past the guard; ask the user to make the change in the app",
		mcpserver.ErrDenied, field)
}

// mcpRefuseSecretRow is rule 4: an agent may reference a secret anywhere and
// define one nowhere.
func mcpRefuseSecretRow(field, name string) error {
	return fmt.Errorf("%w: the %s row %q declares secret:true, and an agent cannot define a secret. Reference an existing secret variable by name instead — write {{%s}} as the value and LiteAPI resolves it at send time — or ask the user to create the secret in the app",
		mcpserver.ErrDenied, field, name, name)
}

// --- authored definition assembly ------------------------------------------

// mcpAuthoredRequest is the union of what create and update supply. Pointer
// fields are "not supplied", which is what makes one assembler serve both: a
// create passes everything (its base is an empty item), an update passes only
// what it patches (its base is the stored request).
type mcpAuthoredRequest struct {
	Type             string
	Method           string
	URL              string
	Headers          *[]mcpserver.AuthoredRow
	Params           *[]mcpserver.AuthoredRow
	PathParams       *[]mcpserver.AuthoredRow
	Vars             *[]mcpserver.AuthoredRow
	BodyType         *string
	Body             *string
	GraphQLVariables *string
	FormData         *[]mcpserver.AuthoredRow
	Auth             map[string]string
}

// mcpAuthoredFields folds the authored fields onto base and returns the item as
// it would be stored.
func mcpAuthoredFields(authored mcpAuthoredRequest, base types.RequestItem) (types.RequestItem, error) {
	item := base
	item.Type = authored.Type

	if method := strings.TrimSpace(authored.Method); method != "" {
		normalized, err := mcpAuthoredMethod(method)
		if err != nil {
			return types.RequestItem{}, err
		}
		item.Method = normalized
	}
	if item.Method == "" {
		item.Method = "GET"
	}
	if url := strings.TrimSpace(authored.URL); url != "" {
		item.URL = url
	}

	var err error
	if item.Headers, err = mcpAuthoredKeyValues("headers", authored.Headers, item.Headers); err != nil {
		return types.RequestItem{}, err
	}
	if item.Params, err = mcpAuthoredKeyValues("params", authored.Params, item.Params); err != nil {
		return types.RequestItem{}, err
	}
	if item.PathParams, err = mcpAuthoredKeyValues("pathParams", authored.PathParams, item.PathParams); err != nil {
		return types.RequestItem{}, err
	}
	if authored.Vars != nil {
		variables, varsErr := mcpAuthoredVariables(*authored.Vars)
		if varsErr != nil {
			return types.RequestItem{}, varsErr
		}
		// Only the request scope is authorable. Vars.Res are post-response
		// captures, which is script-adjacent work an agent does not do here.
		item.Vars = types.RequestVars{Req: variables, Res: base.Vars.Res}
	}
	if item.Body, err = mcpAuthoredBody(authored, item.Body); err != nil {
		return types.RequestItem{}, err
	}
	if authored.Auth != nil {
		if item.Auth, err = mcpAuthoredAuth(authored.Auth); err != nil {
			return types.RequestItem{}, err
		}
	}
	return item, nil
}

func mcpAuthoredType(requestType string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(requestType))
	if normalized == "" {
		return "http", nil
	}
	if !mcpAuthorableRequestTypes[normalized] {
		return "", fmt.Errorf("type %q cannot be authored over MCP; pass \"http\" (the default) or \"graphql\". WebSocket and gRPC requests carry per-kind state that has to be set up in the app", requestType)
	}
	return normalized, nil
}

// mcpAuthoredMethod uppercases and sanity-checks the method. Letters only,
// rather than a fixed list: WebDAV and custom verbs are legitimate, and a list
// would refuse a request the app itself would send happily.
func mcpAuthoredMethod(method string) (string, error) {
	for _, letter := range method {
		if letter < 'A' || (letter > 'Z' && letter < 'a') || letter > 'z' {
			return "", fmt.Errorf("method %q is not a method; pass a verb such as GET, POST, PUT, PATCH or DELETE", method)
		}
	}
	return strings.ToUpper(method), nil
}

// mcpAuthoredKeyValues converts one row array, refusing any row that declares
// itself secret. A nil supplied array keeps the stored rows.
func mcpAuthoredKeyValues(field string, supplied *[]mcpserver.AuthoredRow, stored []types.KeyValue) ([]types.KeyValue, error) {
	if supplied == nil {
		return stored, nil
	}
	rows := make([]types.KeyValue, 0, len(*supplied))
	for index, row := range *supplied {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			return nil, fmt.Errorf("%s row %d has no name; every row needs one, e.g. {\"name\":\"Accept\",\"value\":\"application/json\"}", field, index+1)
		}
		if row.Secret {
			return nil, mcpRefuseSecretRow(field, name)
		}
		rows = append(rows, types.KeyValue{Name: name, Value: row.Value, Enabled: mcpRowEnabled(row)})
	}
	return rows, nil
}

// mcpAuthoredVariables is mcpAuthoredKeyValues for request variables, which are
// the rows most likely to be where an agent tries to define a secret.
func mcpAuthoredVariables(supplied []mcpserver.AuthoredRow) ([]types.Variable, error) {
	variables := make([]types.Variable, 0, len(supplied))
	for index, row := range supplied {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			return nil, fmt.Errorf("vars row %d has no name; every variable needs one", index+1)
		}
		if row.Secret {
			return nil, mcpRefuseSecretRow("vars", name)
		}
		variables = append(variables, types.Variable{
			ID:      newID("var"),
			Name:    name,
			Value:   row.Value,
			Enabled: mcpRowEnabled(row),
		})
	}
	return variables, nil
}

// mcpRowEnabled reads the tri-state: omitted means enabled, which is what an
// agent listing headers means by listing them.
func mcpRowEnabled(row mcpserver.AuthoredRow) bool {
	if row.Enabled == nil {
		return true
	}
	return *row.Enabled
}

// mcpAuthoredBody assembles the body. The mode decides which field the content
// lands in, which is why body arrives as one string rather than as a shape the
// agent has to guess.
func mcpAuthoredBody(authored mcpAuthoredRequest, stored types.RequestBody) (types.RequestBody, error) {
	if authored.BodyType == nil && authored.Body == nil && authored.FormData == nil && authored.GraphQLVariables == nil {
		return stored, nil
	}
	mode := strings.ToLower(strings.TrimSpace(stored.Mode))
	if authored.BodyType != nil && strings.TrimSpace(*authored.BodyType) != "" {
		mode = strings.ToLower(strings.TrimSpace(*authored.BodyType))
	}
	if mode == "" {
		if authored.Type == "graphql" {
			mode = "graphql"
		} else {
			mode = "none"
		}
	}
	if !mcpAuthorableBodyModes[mode] {
		return types.RequestBody{}, fmt.Errorf("bodyType %q cannot be authored over MCP; pass none, json, text, xml, form-urlencoded or graphql. Multipart and file bodies reference files on the user's machine and have to be set up in the app", mode)
	}

	body := stored
	body.Mode = mode
	content := ""
	if authored.Body != nil {
		content = *authored.Body
	}
	// A BODY WITH NOWHERE TO GO IS AN ERROR, not a silent drop. An agent that
	// passes body without bodyType would otherwise get a request whose payload
	// is missing, discover it only when the run returns the wrong answer, and
	// have nothing in the tool's reply to explain it.
	if strings.TrimSpace(content) != "" && (mode == "none" || mode == "form-urlencoded") {
		return types.RequestBody{}, fmt.Errorf("body was supplied but bodyType is %q, which carries no body text; pass bodyType json, text, xml or graphql — or, for a form, send the fields as formData with bodyType form-urlencoded", mode)
	}
	if authored.FormData != nil && len(*authored.FormData) > 0 && mode != "form-urlencoded" {
		return types.RequestBody{}, fmt.Errorf("formData was supplied but bodyType is %q; pass bodyType \"form-urlencoded\" to send form fields", mode)
	}
	if authored.GraphQLVariables != nil && strings.TrimSpace(*authored.GraphQLVariables) != "" && mode != "graphql" {
		return types.RequestBody{}, fmt.Errorf("graphqlVariables was supplied but bodyType is %q; pass bodyType \"graphql\" to send a GraphQL document", mode)
	}
	switch mode {
	case "json":
		if authored.Body != nil {
			body.JSON = content
		}
	case "text":
		if authored.Body != nil {
			body.Text = content
		}
	case "xml":
		if authored.Body != nil {
			body.XML = content
		}
	case "graphql":
		if authored.Body != nil {
			body.GraphQLQuery = content
		}
		if authored.GraphQLVariables != nil {
			body.GraphQLVariables = *authored.GraphQLVariables
		}
	case "form-urlencoded":
		if authored.FormData != nil {
			rows, err := mcpAuthoredKeyValues("formData", authored.FormData, nil)
			if err != nil {
				return types.RequestBody{}, err
			}
			body.FormURLEncoded = rows
		}
	case "none":
		// Nothing to carry. The other fields are left as they are rather than
		// cleared: a user who set a body, then switched the mode to none in the
		// app, keeps the draft, and an agent flipping the mode should not
		// destroy it.
	}
	return body, nil
}

// mcpAuthoredAuth converts the flat auth map, refusing modes and fields that
// are not the agent's to set.
func mcpAuthoredAuth(supplied map[string]string) (types.AuthConfig, error) {
	mode := ""
	for key, value := range supplied {
		if strings.EqualFold(strings.TrimSpace(key), "mode") {
			mode = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if mode == "" {
		return types.AuthConfig{}, errors.New("auth needs a mode; pass {\"mode\":\"none\"}, \"inherit\", \"basic\", \"bearer\" or \"apikey\"")
	}
	fields, known := mcpAuthFields[mode]
	if !known {
		return types.AuthConfig{}, fmt.Errorf("auth mode %q cannot be authored over MCP; pass none, inherit, basic, bearer or apikey. OAuth2, AWS SigV4 and OAuth1 carry flows and signing parameters the user configures in the app", mode)
	}

	auth := types.AuthConfig{Mode: mode, APILocation: "header"}
	for _, key := range mcpSortedNames(mcpMapKeys(supplied)) {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "mode" {
			continue
		}
		if !fields[normalized] {
			return types.AuthConfig{}, fmt.Errorf("auth field %q does not belong to mode %q; %s", key, mode, mcpAuthFieldHint(mode))
		}
		value := supplied[key]
		switch normalized {
		case "username":
			auth.Username = value
		case "password":
			auth.Password = value
		case "token":
			auth.Token = value
		case "key":
			auth.APIKey = value
		case "value":
			auth.APIValue = value
		case "addto":
			location := strings.ToLower(strings.TrimSpace(value))
			if location != "header" && location != "query" {
				return types.AuthConfig{}, fmt.Errorf("auth addTo is %q; it must be \"header\" or \"query\"", value)
			}
			auth.APILocation = location
		}
	}
	return auth, nil
}

func mcpAuthFieldHint(mode string) string {
	switch mode {
	case "basic":
		return "basic takes username and password"
	case "bearer":
		return "bearer takes token"
	case "apikey":
		return "apikey takes key, value and addTo (header or query)"
	default:
		return mode + " takes no fields beyond mode"
	}
}

// mcpPatchedString adapts update's pointer patch to the assembler's union: an
// unsupplied field reads as empty, which the assembler treats as "keep what the
// base already has".
func mcpPatchedString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// --- create_flow / update_flow ---------------------------------------------

// CreateFlow stores a new flow through the app's own binding.
//
// THE VALIDATOR IS NOT REPEATED HERE. App.CreateFlow calls validateFlow with
// flowSecretNamesInScope, which flows.go's header names as the single gate both
// authoring paths share; calling it is what keeps the write tier from
// accepting a flow the app's own Flow tab would reject. Its errors already name
// the flow, the step and the fix, so they are returned verbatim.
func (b *mcpBackend) CreateFlow(params mcpserver.CreateFlowParams) (mcpserver.FlowSummary, error) {
	if err := b.app.mcpWriteTierGate("create_flow"); err != nil {
		return mcpserver.FlowSummary{}, err
	}
	collectionID := strings.TrimSpace(params.CollectionID)
	if collectionID == "" {
		return mcpserver.FlowSummary{}, errors.New("collectionId is required; call list_collections for the ids that exist")
	}
	flow := mcpFlowFromDefinition(params.Flow)
	if flow.ID == "" {
		// Assigned here rather than left to CreateFlow so the id can be
		// returned: "every id a tool returns is accepted by every tool that
		// takes one" is only true if the tool returns the id it created.
		flow.ID = newID("flow")
	}
	if _, err := b.app.CreateFlow(collectionID, flow); err != nil {
		return mcpserver.FlowSummary{}, err
	}
	return mcpFlowSummary(flow), nil
}

// UpdateFlow replaces a stored flow by id.
func (b *mcpBackend) UpdateFlow(params mcpserver.UpdateFlowParams) (mcpserver.FlowSummary, error) {
	if err := b.app.mcpWriteTierGate("update_flow"); err != nil {
		return mcpserver.FlowSummary{}, err
	}
	collectionID := strings.TrimSpace(params.CollectionID)
	if collectionID == "" {
		return mcpserver.FlowSummary{}, errors.New("collectionId is required; call list_collections for the ids that exist")
	}
	flow := mcpFlowFromDefinition(params.Flow)
	if flow.ID == "" {
		return mcpserver.FlowSummary{}, errors.New("flow.id is required to update a flow; call list_flows for the ids that exist, or call create_flow to add a new one")
	}
	if _, err := b.app.UpdateFlow(collectionID, flow); err != nil {
		return mcpserver.FlowSummary{}, err
	}
	return mcpFlowSummary(flow), nil
}

// mcpFlowFromDefinition maps the contract's flow onto the app's.
//
// Nothing is validated or normalised on the way through — not even a trimmed
// name — because validateFlow is where those decisions live and a second
// opinion here would be the fork flows.go forbids. Step vars are copied
// VERBATIM for the same reason get_flow returns them verbatim: {{apiToken}} in
// a step var is literal text that flow scope never resolves.
func mcpFlowFromDefinition(definition mcpserver.FlowDefinition) types.Flow {
	flow := types.Flow{
		ID:          strings.TrimSpace(definition.ID),
		Name:        definition.Name,
		Description: definition.Description,
	}
	for _, input := range definition.Inputs {
		flow.Inputs = append(flow.Inputs, types.FlowInput{
			Name:        input.Name,
			Required:    input.Required,
			Description: input.Description,
		})
	}
	for _, step := range definition.Steps {
		converted := types.FlowStep{ID: step.ID, RequestID: step.RequestID}
		if len(step.Vars) > 0 {
			converted.Vars = make(map[string]string, len(step.Vars))
			for name, value := range step.Vars {
				converted.Vars[name] = value
			}
		}
		for _, extract := range step.Extract {
			converted.Extract = append(converted.Extract, types.FlowExtract{
				Name: extract.Name, From: extract.From, Path: extract.Path,
			})
		}
		for _, assertion := range step.Assert {
			converted.Assert = append(converted.Assert, types.FlowAssert{
				Type:     assertion.Type,
				Equals:   assertion.Equals,
				In:       append([]int(nil), assertion.In...),
				Path:     assertion.Path,
				Contains: assertion.Contains,
				Exists:   assertion.Exists,
			})
		}
		flow.Steps = append(flow.Steps, converted)
	}
	for _, output := range definition.Outputs {
		flow.Outputs = append(flow.Outputs, types.FlowOutput{Name: output.Name, Value: output.Value})
	}
	return flow
}

// --- state helpers ---------------------------------------------------------

// mcpStoredRequest copies one request out of state, with the same "names the
// field and the fix" errors every other lookup in this tier uses.
func (a *App) mcpStoredRequest(collectionID, requestID string) (types.RequestItem, error) {
	var item types.RequestItem
	collectionFound, itemFound := false, false
	if err := a.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				if collection.ID != collectionID {
					continue
				}
				collectionFound = true
				for ii := range collection.Items {
					if collection.Items[ii].ID == requestID {
						item = mcpItemCopy(collection.Items[ii])
						itemFound = true
						break
					}
				}
				return
			}
		}
	}); err != nil {
		return types.RequestItem{}, err
	}
	if !collectionFound {
		return types.RequestItem{}, fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	if !itemFound {
		return types.RequestItem{}, fmt.Errorf("no request with id %q in collection %q; call list_requests for the ids that exist", requestID, collectionID)
	}
	return item, nil
}

// mcpResolveFolderPath turns the folder an agent named into the path the app
// stores, through collectionFolderParentPaths — the same lookup
// CreateRequestInFolder performs, so the two cannot disagree about where a
// request is going. An unknown folder is refused here rather than demoted to
// the collection root.
//
// The lookup runs under the state lock because it is a slice walk over folder
// configs and nothing else: no interpolation, no file read, nothing that
// mcp_backend.go's locking rule forbids.
func (a *App) mcpResolveFolderPath(collectionID, folderPath string) (string, error) {
	if strings.TrimSpace(folderPath) == "" {
		return "", nil
	}
	resolved := ""
	found := false
	var lookupErr error
	if err := a.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				if collection.ID != collectionID {
					continue
				}
				found = true
				resolved, _, lookupErr = collectionFolderParentPaths(collection, folderPath)
				return
			}
		}
	}); err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	if lookupErr != nil {
		return "", fmt.Errorf("folderPath %q does not name a folder in this collection: %w; call list_requests and reuse a folderPath it reports, or omit it for the collection root", folderPath, lookupErr)
	}
	return resolved, nil
}

// mcpCollectionItemIDs is the "before" half of identifying what a create made.
func (a *App) mcpCollectionItemIDs(collectionID string) (map[string]bool, error) {
	ids := map[string]bool{}
	found := false
	if err := a.readStateForMCP(func(state *AppState) {
		for wi := range state.Workspaces {
			for ci := range state.Workspaces[wi].Collections {
				collection := &state.Workspaces[wi].Collections[ci]
				if collection.ID != collectionID {
					continue
				}
				found = true
				for ii := range collection.Items {
					ids[collection.Items[ii].ID] = true
				}
				return
			}
		}
	}); err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("no collection with id %q; call list_collections for the ids that exist", collectionID)
	}
	return ids, nil
}

// mcpCreatedItemID finds the request CreateRequestInFolder just added.
//
// By difference rather than by position: a file-backed request has its id
// recomputed from its eventual path during creation (app_tree_crud.go), and a
// concurrent watcher refresh can reorder Items, so "the last one" is a fact
// about today's implementation while "the one that was not there before" is a
// fact about what happened.
func mcpCreatedItemID(state AppState, collectionID string, before map[string]bool) (string, bool) {
	for wi := range state.Workspaces {
		for ci := range state.Workspaces[wi].Collections {
			collection := &state.Workspaces[wi].Collections[ci]
			if collection.ID != collectionID {
				continue
			}
			for ii := range collection.Items {
				if !before[collection.Items[ii].ID] {
					return collection.Items[ii].ID, true
				}
			}
			return "", false
		}
	}
	return "", false
}

// mcpRequestSummaryByID reports the request as the READ tier would report it,
// which is what makes a write's answer round-trip: the id, the masking and the
// field names are the ones every other tool uses.
func (a *App) mcpRequestSummaryByID(collectionID, requestID string) (mcpserver.RequestSummary, error) {
	item, err := a.mcpStoredRequest(collectionID, requestID)
	if err != nil {
		return mcpserver.RequestSummary{}, err
	}
	return mcpRequestSummary(requestRow{collectionID: collectionID, item: item}), nil
}
