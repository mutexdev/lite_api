// platformfixture starts deterministic, loopback-only transport fixtures for M5 QA.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	outputDir := flag.String("output-dir", "", "required directory for the generated manifest, certificates, keys, and proxy log")
	targetListen := flag.String("listen", "127.0.0.1:0", "target and GraphQL listen address (loopback only)")
	proxyListen := flag.String("proxy-listen", "127.0.0.1:0", "HTTP proxy listen address (loopback only)")
	httpsListen := flag.String("https-listen", "127.0.0.1:0", "HTTPS listen address (loopback only)")
	mtlsListen := flag.String("mtls-listen", "127.0.0.1:0", "mTLS listen address (loopback only)")
	flag.Parse()

	fixture, err := Start(Config{OutputDir: *outputDir, TargetListen: *targetListen, ProxyListen: *proxyListen, HTTPSListen: *httpsListen, MTLSListen: *mtlsListen})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = fixture.Close(context.Background()) }()
	log.Printf("LiteAPI platform fixture manifest: %s", fixture.ManifestPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
