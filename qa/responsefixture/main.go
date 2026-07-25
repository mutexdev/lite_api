package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	grpc_testing "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
)

const (
	previewBinary = 200 * 1024
	oneMiB        = 1 << 20
	fiveMiB       = 5 << 20
	sseMarker     = "SSE-NEEDLE-42"
)

const sseFixtureBody = "event: ready\ndata: liteapi-sse-ready\n\nevent: message\ndata: " + sseMarker + "\n\nevent: complete\ndata: liteapi-sse-complete\n\n"

func main() {
	address := flag.String("listen", "127.0.0.1:18487", "local fixture listen address")
	grpcAddress := flag.String("grpc-listen", "127.0.0.1:18488", "local gRPC fixture listen address")
	flag.Parse()
	resolvedGRPCAddress, grpcStop, err := startGRPCFixture(*grpcAddress)
	if err != nil {
		log.Fatal(err)
	}
	defer grpcStop()
	server := &http.Server{
		Addr:              *address,
		Handler:           fixtureHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("LiteAPI gRPC fixture listening on grpc://%s (grpc.testing.TestService)", resolvedGRPCAddress)
	log.Printf("LiteAPI response fixtures listening on http://%s", *address)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func fixtureHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", indexFixture)
	mux.HandleFunc("/json-1m", payloadFixture("application/json; charset=utf-8", exactJSON(oneMiB), "json-1m.json"))
	mux.HandleFunc("/json-5m", payloadFixture("application/json; charset=utf-8", exactJSON(fiveMiB), "json-5m.json"))
	mux.HandleFunc("/text-1m", payloadFixture("text/plain; charset=utf-8", exactText(oneMiB), "text-1m.txt"))
	mux.HandleFunc("/text-5m", payloadFixture("text/plain; charset=utf-8", exactText(fiveMiB), "text-5m.txt"))
	mux.HandleFunc("/binary-200k", payloadFixture("application/octet-stream", deterministicBinary(previewBinary), "binary-200k.bin"))
	mux.HandleFunc("/binary-5m", payloadFixture("application/octet-stream", deterministicBinary(fiveMiB), "binary-5m.bin"))
	mux.HandleFunc("/xml", payloadFixture("application/xml; charset=utf-8", []byte(`<?xml version="1.0"?><fixture><message>NEEDLE-42</message><unicode>héllo</unicode></fixture>`), "fixture.xml"))
	mux.HandleFunc("/html-safe", payloadFixture("text/html; charset=utf-8", []byte(`<!doctype html><html><head><title>LiteAPI fixture</title></head><body><h1>Sandbox fixture</h1><p>NEEDLE-42</p><script>document.body.dataset.scriptExecuted="true";window.open("https://example.invalid")</script></body></html>`), "fixture.html"))
	mux.HandleFunc("/image", payloadFixture("image/png", fixturePNG(), "fixture.png"))
	mux.HandleFunc("/pdf", payloadFixture("application/pdf", fixturePDF(), "fixture.pdf"))
	mux.HandleFunc("/compare-a", comparisonFixture(200, "alpha", []string{"one", "two"}))
	mux.HandleFunc("/compare-b", comparisonFixture(201, "beta", []string{"one", "three", "four"}))
	mux.HandleFunc("/timeline", timelineFixture)
	mux.HandleFunc("/sse", sseFixture)
	mux.HandleFunc("/ws", websocketFixture)
	mux.HandleFunc("/grpc", grpcInfoFixture)
	return mux
}

func grpcInfoFixture(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(`{"target":"grpc://127.0.0.1:18488","service":"grpc.testing.TestService","unaryMethod":"grpc.testing.TestService/UnaryCall","serverStreamMethod":"grpc.testing.TestService/StreamingOutputCall","bidiMethod":"grpc.testing.TestService/FullDuplexCall"}`))
}

func indexFixture(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(`{"service":"LiteAPI response fixtures","endpoints":["/json-1m","/json-5m","/text-1m","/text-5m","/binary-200k","/binary-5m","/xml","/html-safe","/image","/pdf","/compare-a","/compare-b","/timeline","/sse","/ws"]}`))
}

func payloadFixture(contentType string, payload []byte, filename string) http.HandlerFunc {
	digest := sha256.Sum256(payload)
	checksum := hex.EncodeToString(digest[:])
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Header().Set("X-Fixture-SHA256", checksum)
		w.Header().Add("X-LiteAPI-Duplicate", "first")
		w.Header().Add("X-LiteAPI-Duplicate", "second")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}
}

func exactJSON(size int) []byte {
	prefix := []byte(`{"fixture":"large-json","needle":"NEEDLE-42","payload":"`)
	suffix := []byte(`"}`)
	if size < len(prefix)+len(suffix) {
		return append(prefix, suffix...)
	}
	result := make([]byte, 0, size)
	result = append(result, prefix...)
	result = append(result, bytes.Repeat([]byte{'x'}, size-len(prefix)-len(suffix))...)
	result = append(result, suffix...)
	return result
}

func exactText(size int) []byte {
	prefix := []byte("LiteAPI deterministic text fixture\nNEEDLE-42\n")
	line := []byte("0123456789 abcdefghijklmnopqrstuvwxyz\n")
	result := make([]byte, 0, size)
	result = append(result, prefix...)
	for len(result)+len(line) <= size {
		result = append(result, line...)
	}
	result = append(result, bytes.Repeat([]byte{'x'}, size-len(result))...)
	return result
}

func deterministicBinary(size int) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = byte(index % 256)
	}
	return result
}

func fixturePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 160, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 160; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y * 2), B: uint8(210 - x/2), A: 255})
		}
	}
	var output bytes.Buffer
	_ = png.Encode(&output, img)
	return output.Bytes()
}

func fixturePDF() []byte {
	stream := "BT /F1 18 Tf 72 100 Td (LiteAPI PDF fixture NEEDLE-42) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 360 180] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}

func comparisonFixture(status int, value string, items []string) http.HandlerFunc {
	payload := []byte(fmt.Sprintf(`{"fixture":"comparison","value":%q,"items":[%s],"needle":"NEEDLE-42"}`, value, quoteItems(items)))
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Compare-Value", value)
		w.Header().Set("X-Compare-Stable", "unchanged")
		w.WriteHeader(status)
		_, _ = w.Write(payload)
	}
}

func quoteItems(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, strconv.Quote(item))
	}
	return strings.Join(quoted, ",")
}

func timelineFixture(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Server-Timing", "dns;dur=1, connect;dur=2, tls;dur=3, upload;dur=4, wait;dur=25, download;dur=5")
	w.Header().Set("X-Timeline-Token", "TIMELINE-NEEDLE-42")
	time.Sleep(25 * time.Millisecond)
	_, _ = w.Write([]byte(`{"timeline":true,"phases":["dns","connect","tls","upload","wait","download"],"needle":"NEEDLE-42"}`))
}

func sseFixture(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	for _, event := range strings.SplitAfter(sseFixtureBody, "\n\n") {
		if event == "" {
			continue
		}
		_, _ = io.WriteString(w, event)
		flusher.Flush()
	}
}

var fixtureUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func websocketFixture(w http.ResponseWriter, r *http.Request) {
	connection, err := fixtureUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = connection.Close() }()
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if err := connection.WriteMessage(messageType, payload); err != nil {
			return
		}
	}
}

type grpcFixtureService struct {
	grpc_testing.UnimplementedTestServiceServer
}

func (grpcFixtureService) UnaryCall(ctx context.Context, _ *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	if err := grpc.SendHeader(ctx, metadata.Pairs("x-liteapi-fixture", "initial", "x-liteapi-fixture-duplicate", "one", "x-liteapi-fixture-duplicate", "two")); err != nil {
		return nil, err
	}
	_ = grpc.SetTrailer(ctx, metadata.Pairs("x-liteapi-fixture-trailer", "complete"))
	return &grpc_testing.SimpleResponse{Username: "NEEDLE-42", ServerId: "liteapi-response-fixture"}, nil
}

func (grpcFixtureService) StreamingOutputCall(_ *grpc_testing.StreamingOutputCallRequest, stream grpc.ServerStreamingServer[grpc_testing.StreamingOutputCallResponse]) error {
	if err := stream.SendHeader(metadata.Pairs("x-liteapi-fixture", "stream-initial")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-liteapi-fixture-trailer", "stream-complete"))
	for _, payload := range [][]byte{[]byte("server-one"), {0x00, 0xff, 0x42}, []byte("NEEDLE-42")} {
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{Body: payload}}); err != nil {
			return err
		}
	}
	return nil
}

func (grpcFixtureService) StreamingInputCall(stream grpc.ClientStreamingServer[grpc_testing.StreamingInputCallRequest, grpc_testing.StreamingInputCallResponse]) error {
	total := 0
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&grpc_testing.StreamingInputCallResponse{AggregatedPayloadSize: int32(total)})
		}
		if err != nil {
			return err
		}
		total += len(request.GetPayload().GetBody())
	}
}

func (grpcFixtureService) FullDuplexCall(stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse]) error {
	if err := stream.SendHeader(metadata.Pairs("x-liteapi-fixture", "bidi-initial")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-liteapi-fixture-trailer", "bidi-complete"))
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{Payload: request.GetPayload()}); err != nil {
			return err
		}
	}
}

func startGRPCFixture(address string) (string, func(), error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return "", nil, err
	}
	server := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(server, grpcFixtureService{})
	reflection.Register(server)
	go func() {
		if err := server.Serve(listener); err != nil {
			log.Printf("gRPC fixture stopped: %v", err)
		}
	}()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}, nil
}
