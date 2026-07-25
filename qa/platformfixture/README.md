# LiteAPI M5 platform fixture

`platformfixture` is a deterministic, **loopback-only** local transport harness for M5 acceptance. It is separate from `qa/responsefixture`; do not use it for response-renderer fixtures.

## Launch contract

An explicit writable output directory is required. It receives all runtime certificates, private keys, the proxy log, and `manifest.json`; it must not be committed.

```sh
fixture_dir="$(mktemp -d /tmp/liteapi-platformfixture.XXXXXX)"
go run ./qa/platformfixture -output-dir "$fixture_dir"
```

All listeners default to an ephemeral `127.0.0.1:0` port. Override any one while retaining a numeric loopback host:

```sh
go run ./qa/platformfixture \
  -output-dir "$fixture_dir" \
  -listen 127.0.0.1:18501 \
  -proxy-listen 127.0.0.1:18502 \
  -https-listen 127.0.0.1:18503 \
  -mtls-listen 127.0.0.1:18504
```

Non-loopback hosts (including `localhost`, `0.0.0.0`, and public addresses) are rejected intentionally. Stop with `Ctrl-C`; servers and listeners shut down, while the caller-owned output directory remains for evidence inspection.

## Manifest

`$fixture_dir/manifest.json` is JSON mode `0600` with these stable keys:

```json
{
  "version": 1,
  "targetURL": "http://127.0.0.1:PORT",
  "graphQLURL": "http://127.0.0.1:PORT/graphql",
  "proxyURL": "http://127.0.0.1:PORT",
  "httpsURL": "https://127.0.0.1:PORT",
  "mtlsURL": "https://127.0.0.1:PORT",
  "proxyMarker": "liteapi-platform-proxy-RANDOM",
  "proxyLogPath": "/absolute/path/proxy.log",
  "caCertPath": "/absolute/path/ca-cert.pem",
  "clientCertPath": "/absolute/path/client-cert.pem",
  "clientKeyPath": "/absolute/path/client-key.pem"
}
```

The generated CA, server, and client material is valid only for the fixture runtime. Every private key and the manifest is written with mode `0600`; never add any generated output to source control.

## Endpoints and assertions

| Surface | Request | Deterministic result |
| --- | --- | --- |
| Direct target | `GET $targetURL/target` | JSON target acknowledgement, no proxy marker |
| GraphQL success | `POST $graphQLURL` with a JSON `query`/`variables` body | `data.echo` returns the supplied query and variables |
| GraphQL error | query containing `fixtureError` | HTTP 200 GraphQL `errors[0].extensions.code = FIXTURE_ERROR` |
| HTTP proxy | Configure explicit proxy as `$proxyURL`, request `$targetURL/target` | response `X-LiteAPI-Proxy-Marker` equals `proxyMarker`; `proxy.log` records it |
| HTTPS | `GET $httpsURL` | system trust must reject; trusting `caCertPath` succeeds |
| mTLS | `GET $mtlsURL` | trusting only the CA is rejected; CA plus `clientCertPath`/`clientKeyPath` succeeds |

The proxy forwards only absolute-form requests addressed to the manifest's own local target, preventing a QA run from becoming a general proxy.
