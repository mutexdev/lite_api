package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpc_testing "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
)

func TestLargeFixturesPublishExactLengthAndDigest(t *testing.T) {
	server := httptest.NewServer(fixtureHandler())
	defer server.Close()
	for path, size := range map[string]int{
		"/binary-200k": previewBinary,
		"/json-1m":     oneMiB,
		"/json-5m":     fiveMiB,
		"/text-1m":     oneMiB,
		"/text-5m":     fiveMiB,
		"/binary-5m":   fiveMiB,
	} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK || len(body) != size {
			t.Fatalf("%s status=%d size=%d want=%d", path, response.StatusCode, len(body), size)
		}
		digest := sha256.Sum256(body)
		if got := response.Header.Get("X-Fixture-SHA256"); got != hex.EncodeToString(digest[:]) {
			t.Fatalf("%s digest=%q", path, got)
		}
		if strings.HasPrefix(path, "/json-") && !json.Valid(body) {
			t.Fatalf("%s did not return valid JSON", path)
		}
	}
}

func TestMediaAndComparisonFixtures(t *testing.T) {
	server := httptest.NewServer(fixtureHandler())
	defer server.Close()
	for path, prefix := range map[string]string{"/image": "\x89PNG", "/pdf": "%PDF-1.4", "/html-safe": "<!doctype html>"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.HasPrefix(string(body), prefix) {
			t.Fatalf("%s prefix %q", path, body[:min(len(body), 12)])
		}
	}
	response, err := http.Get(server.URL + "/compare-b")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusCreated || response.Header.Get("X-Compare-Value") != "beta" {
		t.Fatalf("comparison status=%d headers=%v", response.StatusCode, response.Header)
	}
}

func TestSSEFixturePublishesOrderedFlushedEventsAndCompletesQuickly(t *testing.T) {
	server := httptest.NewServer(fixtureHandler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("unexpected SSE response: status=%d headers=%v", response.StatusCode, response.Header)
	}
	if got := string(body); got != sseFixtureBody || !strings.Contains(got, sseMarker) {
		t.Fatalf("unexpected ordered SSE body: %q", got)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("SSE fixture did not complete quickly: %s", elapsed)
	}
}

func TestGRPCFixturePublishesMetadataTrailersAndPayload(t *testing.T) {
	target, stop, err := startGRPCFixture("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	client := grpc_testing.NewTestServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var headers metadata.MD
	var trailers metadata.MD
	response, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}, grpc.Header(&headers), grpc.Trailer(&trailers))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetUsername() != "NEEDLE-42" || headers.Get("x-liteapi-fixture")[0] != "initial" || trailers.Get("x-liteapi-fixture-trailer")[0] != "complete" {
		t.Fatalf("response=%#v headers=%v trailers=%v", response, headers, trailers)
	}
}
