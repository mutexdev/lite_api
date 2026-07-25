package main

// US-006 — large-workspace fixture.
//
// Builds a deterministic workspace of 500 requests across 10 collections, three
// of which carry a 5 MB cached response, so the US-005 benchmarks measure the
// case improvement_v2.md §2.1.B describes rather than a toy state.
//
// The shape that matters here is that AppState transitively reaches every cached
// response body:
//
//	AppState.Workspaces -> Workspace.Collections -> Collection.Items ->
//	RequestItem.Response -> Response.Body + Response.BodyBase64
//
// and that each body is stored twice. A 5 MB response therefore costs ~5 MB in
// Body plus ~6.7 MB in BodyBase64, and persistLocked re-serialises all of it on
// every keystroke. A fixture that stored the body once would understate the cost
// this program exists to remove.
//
// Determinism is a hard requirement, not a nicety: benchmark timings depend on
// JSON escape-scanning cost, which depends on body content. A fixture seeded
// from the wall clock would make .ralph/baseline/bench.txt incomparable between
// runs and every later ">5% regression" verdict meaningless.

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

type largeWorkspaceOptions struct {
	Collections       int
	RequestsPerColl   int
	LargeResponses    int
	LargeResponseSize int
	SmallResponseSize int
	Seed              int64
}

func defaultLargeWorkspaceOptions() largeWorkspaceOptions {
	return largeWorkspaceOptions{
		Collections:       10,
		RequestsPerColl:   50, // -> 500 requests
		LargeResponses:    3,
		LargeResponseSize: 5 << 20, // 5 MiB
		SmallResponseSize: 2 << 10, // 2 KiB
		Seed:              20260725,
	}
}

func (o largeWorkspaceOptions) normalised() largeWorkspaceOptions {
	d := defaultLargeWorkspaceOptions()
	if o.Collections <= 0 {
		o.Collections = d.Collections
	}
	if o.RequestsPerColl <= 0 {
		o.RequestsPerColl = d.RequestsPerColl
	}
	if o.LargeResponses < 0 {
		o.LargeResponses = d.LargeResponses
	}
	if o.LargeResponseSize <= 0 {
		o.LargeResponseSize = d.LargeResponseSize
	}
	if o.SmallResponseSize <= 0 {
		o.SmallResponseSize = d.SmallResponseSize
	}
	if o.Seed == 0 {
		o.Seed = d.Seed
	}
	return o
}

// fixtureEpoch is fixed so CreatedAt/UpdatedAt/SentAt never vary between runs.
var fixtureEpoch = time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)

// fixtureWords is a small closed vocabulary. Real JSON bodies are mostly short
// repeated keys and words; strings.Repeat("a", n) would escape-scan very
// differently and misrepresent the marshal cost being measured.
var fixtureWords = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
	"india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa",
}

// buildFixtureJSONBody returns a JSON object at least minBytes long.
// Deterministic for a given *rand.Rand.
func buildFixtureJSONBody(rng *rand.Rand, minBytes int) string {
	var b strings.Builder
	b.Grow(minBytes + 1024)
	b.WriteString(`{"records":[`)
	for i := 0; b.Len() < minBytes; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b,
			`{"id":%d,"name":%q,"email":%q,"score":%d,"active":%t,"tags":[%q,%q],`+
				`"nested":{"region":%q,"latency_ms":%d,"note":%q}}`,
			i,
			fixtureWords[rng.Intn(len(fixtureWords))],
			fmt.Sprintf("%s.%d@example.test", fixtureWords[rng.Intn(len(fixtureWords))], i),
			rng.Intn(10000),
			i%2 == 0,
			fixtureWords[rng.Intn(len(fixtureWords))],
			fixtureWords[rng.Intn(len(fixtureWords))],
			fixtureWords[rng.Intn(len(fixtureWords))],
			rng.Intn(2500),
			// Characters the JSON encoder must escape, so the marshal path is
			// exercised realistically rather than over a run of plain bytes.
			fmt.Sprintf("line %d\tvalue \"quoted\" \\ end", i),
		)
	}
	b.WriteString(`]}`)
	return b.String()
}

func buildFixtureResponse(rng *rand.Rand, size int, seq int) *Response {
	body := buildFixtureJSONBody(rng, size)
	return &Response{
		Status:     200,
		StatusText: "OK",
		Headers: map[string]string{
			"Content-Type":   "application/json",
			"Server":         "liteapi-fixture",
			"X-Fixture-Seq":  fmt.Sprintf("%d", seq),
			"Content-Length": fmt.Sprintf("%d", len(body)),
		},
		Body: body,
		// Stored twice on purpose — this is the duplication US-009 removes.
		BodyBase64:   base64.StdEncoding.EncodeToString([]byte(body)),
		Size:         len(body),
		DurationMs:   int64(12 + seq%180),
		PreviewMode:  "pretty",
		RequestedURL: fmt.Sprintf("https://fixture.example.test/api/v1/resource/%d", seq),
		SentAt:       fixtureEpoch.Add(time.Duration(seq) * time.Second),
	}
}

func buildFixtureRequestItem(collIdx, reqIdx int, resp *Response) RequestItem {
	seq := collIdx*1000 + reqIdx
	id := fmt.Sprintf("fixture-req-%02d-%03d", collIdx, reqIdx)
	return RequestItem{
		ID:     id,
		Name:   fmt.Sprintf("Request %03d", reqIdx),
		Type:   "http",
		Method: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}[reqIdx%5],
		URL:    fmt.Sprintf("https://fixture.example.test/api/v1/resource/%d?page={{page}}&limit={{limit}}", seq),
		Params: []KeyValue{
			{Name: "page", Value: "{{page}}", Enabled: true},
			{Name: "limit", Value: "{{limit}}", Enabled: true},
		},
		Headers: []KeyValue{
			{Name: "Accept", Value: "application/json", Enabled: true},
			{Name: "Authorization", Value: "Bearer {{token}}", Enabled: true},
			{Name: "X-Request-Id", Value: id, Enabled: true},
		},
		Response:   resp,
		FolderPath: fmt.Sprintf("folder-%d", reqIdx%5),
		FilePath:   fmt.Sprintf("fixture-coll-%02d/folder-%d/%s.bru", collIdx, reqIdx%5, id),
		Seq:        seq,
		CreatedAt:  fixtureEpoch,
		UpdatedAt:  fixtureEpoch.Add(time.Duration(seq) * time.Minute),
	}
}

// buildLargeWorkspaceState returns a fully populated AppState. Byte-identical
// across runs for a given seed.
func buildLargeWorkspaceState(opts largeWorkspaceOptions) AppState {
	opts = opts.normalised()
	rng := rand.New(rand.NewSource(opts.Seed))

	// Spread the large responses across different collections so a
	// per-collection write path (US-015) sees a realistic distribution rather
	// than all the weight concentrated in one file.
	total := opts.Collections * opts.RequestsPerColl
	largeAt := map[int]bool{}
	if opts.LargeResponses > 0 && total > 0 {
		stride := total / opts.LargeResponses
		for i := 0; i < opts.LargeResponses; i++ {
			largeAt[i*stride] = true
		}
	}

	collections := make([]Collection, 0, opts.Collections)
	flat := 0
	for c := 0; c < opts.Collections; c++ {
		items := make([]RequestItem, 0, opts.RequestsPerColl)
		for r := 0; r < opts.RequestsPerColl; r++ {
			size := opts.SmallResponseSize
			if largeAt[flat] {
				size = opts.LargeResponseSize
			}
			items = append(items, buildFixtureRequestItem(c, r, buildFixtureResponse(rng, size, flat)))
			flat++
		}
		collections = append(collections, Collection{
			ID:     fmt.Sprintf("fixture-coll-%02d", c),
			Name:   fmt.Sprintf("Fixture Collection %02d", c),
			Format: "bru",
			Items:  items,
			Headers: []KeyValue{
				{Name: "X-Collection", Value: fmt.Sprintf("%d", c), Enabled: true},
			},
			Variables: []Variable{
				{ID: fmt.Sprintf("var-%02d-page", c), Name: "page", Value: "1", Enabled: true},
				{ID: fmt.Sprintf("var-%02d-limit", c), Name: "limit", Value: "50", Enabled: true},
				{ID: fmt.Sprintf("var-%02d-token", c), Name: "token", Value: "fixture-token", Enabled: true},
			},
			Environments: []Environment{{
				ID:   fmt.Sprintf("env-%02d", c),
				Name: "fixture",
				Variables: []Variable{
					{ID: fmt.Sprintf("env-%02d-host", c), Name: "host", Value: "fixture.example.test", Enabled: true},
				},
			}},
		})
	}

	return AppState{
		Workspaces: []Workspace{{
			ID:          "fixture-workspace",
			Name:        "Fixture Workspace",
			Collections: collections,
			CreatedAt:   fixtureEpoch,
			UpdatedAt:   fixtureEpoch,
		}},
		ActiveWorkspaceID: "fixture-workspace",
	}
}

// newLargeWorkspaceApp returns an *App rooted at dir with the fixture installed.
// dir should be a t.TempDir()/b.TempDir() so persistLocked writes to scratch.
func newLargeWorkspaceApp(dir string, opts largeWorkspaceOptions) *App {
	app := NewAppWithDir(dir)
	state := buildLargeWorkspaceState(opts)
	// Preserve whatever defaultState seeded for preferences and the Feature
	// ledger (§2.2 requires the ledger be extended, never dropped); replace only
	// the workspace tree.
	app.mu.Lock()
	app.state.Workspaces = state.Workspaces
	app.state.ActiveWorkspaceID = state.ActiveWorkspaceID
	app.mu.Unlock()
	return app
}
