# US-003 — CI step for golangci-lint

## Status: already present in `.github/workflows/ci.yml`

When this story started, `.github/workflows/ci.yml` did not exist and US-003 was told not to
create it. US-001 created it mid-story and **already includes the required `go-lint` job**, using
the official action with the version pinned. No edit to `ci.yml` is needed from US-003.

The job as US-001 wrote it (verbatim, `ci.yml` lines 65–95):

```yaml
  go-lint:
    name: Go — golangci-lint
    runs-on: ubuntu-24.04
    steps:
      - name: Check out source
        uses: actions/checkout@v7
        with:
          persist-credentials: false

      - name: Install Linux build dependencies
        run: |
          sudo apt-get update
          sudo apt-get install --yes --no-install-recommends \
            build-essential \
            libgtk-3-dev \
            libwebkit2gtk-4.1-dev \
            pkg-config

      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum

      # US-003. Pinned rather than floating: golangci-lint gates the build, so a
      # new release adding a check must be an explicit, reviewable bump.
      - name: Run golangci-lint
        uses: golangci/golangci-lint-action@v9
        with:
          version: ${{ env.GOLANGCI_LINT_VERSION }}   # "v2.12.2"
          args: --timeout 15m --build-tags webkit2_41
```

## Verification performed by US-003

US-001's plan recorded an open risk: *"`golangci/golangci-lint-action@v9` must accept
`--build-tags webkit2_41`; if the action rejects the arg, fall back to installing the binary
directly."* That risk is now **closed**:

- `golangci-lint run --help` in v2.12.2 lists `--build-tags strings   Build tags`. The flag
  survived the v1 → v2 migration and is still a `run` flag, not config-only.
- The exact CI invocation was run locally against the committed config:

  ```
  $ golangci-lint run --timeout 15m --build-tags webkit2_41 ./...
  0 issues.
  EXIT=0
  ```

No fallback is required.

## Notes for whoever reviews `ci.yml`

1. **`version:` must stay `v2.12.2` or later-v2.** `.golangci.yml` is written against the
   **v2 schema** (`version: "2"`, `linters.default`, `linters.exclusions`). golangci-lint v1
   rejects it outright, so downgrading the pin does not merely change results, it breaks the job.

2. **The `go-lint` job does not run `npm ci`, and must not start.** `.golangci.yml` excludes
   `frontend/node_modules` because that tree vendors third-party Go sources
   (`flatted/golang/pkg/flatted`). The `go` tool skips `node_modules` directories automatically;
   golangci-lint does not, so without the exclusion it lints vendored code that is never built.
   The exclusion makes the job correct whether or not `node_modules` happens to be present.

3. **`--timeout 15m` is passed on the command line and is also set in `.golangci.yml`**
   (`run.timeout: 15m`). Harmless duplication; the CLI flag wins. Kept in both so a local
   `golangci-lint run ./...` with no arguments behaves like CI.
