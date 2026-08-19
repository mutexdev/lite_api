# Collection import fixtures

These deterministic, non-secret inputs support automated and packaged-native import acceptance.

- `postman-primary.json`: nested Postman collection with variables and two requests.
- `postman-unicode.json`: Unicode names, URL, and JSON body.
- `insomnia-v4.json`: Insomnia workspace, environment, folder, and request.
- `openapi.yaml`: OpenAPI 3 document with two operations.
- `request.bru`: single Bruno request.
- `request.curl.txt`: common cURL flags, headers, and JSON body.
- `bruno-folder/`: whole-folder Bruno collection with hierarchy and environment.
- `invalid.json`: row-scoped malformed/ambiguous source.
- `swagger-2.yaml`, `service.wsdl`, and `yaak-export.json`: recognized unsupported inputs that must never create an empty collection.

The loopback port is the stable `qa/responsefixture` development default. Tests that bind an ephemeral port may rewrite copied fixture inputs inside their own temporary directory; committed fixtures remain immutable.
