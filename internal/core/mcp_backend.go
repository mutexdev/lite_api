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
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/history"
	"github.com/mutexdev/lite_api/internal/mcpserver"
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
	detail := mcpserver.RequestDetail{
		RequestSummary: mcpRequestSummary(row),
		Headers:        mcpserver.RedactRows(mcpKeyValueRows(item.Headers)),
		Params:         mcpserver.RedactRows(mcpKeyValueRows(item.Params)),
		PathParams:     mcpserver.RedactRows(mcpKeyValueRows(item.PathParams)),
		BodyType:       strings.TrimSpace(item.Body.Mode),
		Body:           types.RequestBodySnapshot(item.Body),
		AuthType:       strings.TrimSpace(item.Auth.Mode),
		Auth:           mcpserver.MaskAuthRows(mcpAuthRows(item.Auth)),
		Vars:           mcpserver.RedactRows(mcpVariableRows(item.Vars)),
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
							// Never active: which collection environment is
							// selected is frontend state and has no home in
							// AppState, so claiming one here would be a guess.
							// Only the workspace-global selection is persisted.
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
		run := mcpserver.HistoryRun{
			ID:         entry.ID,
			Method:     entry.Method,
			URL:        entry.URL,
			Status:     entry.Status,
			DurationMs: int(entry.DurationMs),
			ExecutedAt: entry.At.UTC().Format(time.RFC3339),
			// Already redacted by internal/history's rules on the way in — the
			// stored value for a credential-shaped header is the literal
			// "<redacted>", never the token. Mapped straight across rather than
			// re-masked, so a marker found in tool output identifies the layer
			// that produced it.
			Headers: mcpKeyValueRows(entry.ResponseHeaders),
		}
		body, err := b.app.GetHistoryBody(entry.ID)
		if err == nil && len(body) > mcpHistoryBodyLimit {
			body = body[:mcpHistoryBodyLimit]
			run.Truncated = true
		}
		// A body the store has since pruned is not an error: history outlives
		// the content-addressed bodies it points at by design.
		run.Body = body
		out = append(out, run)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- mapping helpers -------------------------------------------------------

// mcpItemCopy deep-copies one request out of state. Reusing the folder-clone
// helper rather than writing a second one: it clones every mutable field of a
// RequestItem and drops Response and Timeline, which is precisely the shape
// this adapter wants — the last response is a run artifact, not part of the
// definition an agent asked for.
func mcpItemCopy(item types.RequestItem) types.RequestItem {
	return types.CloneRequestItemForFolderClone(item)
}

func mcpRequestSummary(row requestRow) mcpserver.RequestSummary {
	return mcpserver.RequestSummary{
		ID:           row.item.ID,
		CollectionID: row.collectionID,
		Name:         row.item.Name,
		Type:         row.item.Type,
		Method:       row.item.Method,
		URL:          row.item.URL,
		FolderPath:   row.item.FolderPath,
	}
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
