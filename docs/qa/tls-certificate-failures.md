# Certificate failures: what to check before calling it a bug

A request that sends fine in Postman and fails here with a certificate error is
almost never a defect in either app.

**Postman ships with SSL certificate verification switched off.** LiteAPI
verifies by default. That is the whole of the difference in the great majority
of reports, and it is a deliberate difference: silently accepting any
certificate is not a default worth matching.

Every remedy is in the app already:

| Where | What it does |
|---|---|
| Request → Settings → **Verify TLS** | Stops verifying for that one request. |
| Preferences → Request → **SSL/TLS Certificate Verification** | Stops verifying for everything. |
| Preferences → Request → **Use Custom CA Certificate** | Trusts a CA the machine does not, without weakening anything else. |

Since US-059 the failure message names the host, says which of these applies,
and the response pane offers the first one as a button.

## macOS specifics

Go uses the platform verifier on macOS, so a CA installed in a keychain and
marked **Always Trust** is honoured — a corporate root added by MDM generally
works with verification left on. Two cases still fail with the CA installed:

1. **The CA is in a file, not the keychain.** Either add it —

   ```bash
   sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./corp-root.pem
   ```

   — or point Preferences → Request → Custom CA Certificate at the file, which
   needs no administrator rights.

2. **The server does not send its intermediate certificate.** Browsers and Node
   (so, Postman) fetch the missing intermediate over AIA; Go does not. The
   server is misconfigured, and the honest fix is to fix the server. Until then,
   concatenate the intermediate into a PEM file and select it as the custom CA.

To tell the two apart, look at what the server actually presents:

```bash
openssl s_client -connect api.internal:443 -showcerts </dev/null
```

A chain that ends at the leaf, with no intermediate above it, is case 2.

## Linux

The same, minus the keychain: Go reads `/etc/ssl/certs`. A CA installed with
`update-ca-certificates` is trusted; anything else needs the custom CA setting.

## When it is a bug

If verification is on, the CA **is** in the system trust store, the chain from
`s_client` is complete and unexpired, and the request still fails — that is
worth a report. Include the full `s_client` output and the raw error text from
the second line of the message in the response pane.
