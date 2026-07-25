# LiteAPI

## About

LiteAPI is a local-first desktop API client built with Go, Wails, Svelte, and TypeScript.

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development
server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser
and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect
to this in your browser, and you can call your Go code from devtools.

## Building

To build a redistributable, production mode package, use `wails build`.

## Downloading a release

Every successful update to `main` creates a GitHub release named `v0.1.<run-number>` with four downloadable files:

- `LiteAPI-v0.1.N-macOS-universal.dmg` supports both Apple silicon and Intel Macs. Open the notarized DMG and drag LiteAPI to Applications.
- `LiteAPI-v0.1.N-windows-amd64-installer.exe` installs the 64-bit Windows build and includes the Microsoft WebView2 bootstrapper. The Windows installer is currently unsigned, so SmartScreen may display a warning.
- `LiteAPI-v0.1.N-linux-amd64.tar.gz` contains the 64-bit Linux executable, license, and runtime notes. GTK 3 and WebKit2GTK 4.1 must be installed from the Linux distribution.
- `SHA256SUMS.txt` contains checksums for all three platform packages.

Download packages from the repository's [Releases page](https://github.com/mutexdev/lite_api/releases).

## macOS release signing setup

The macOS release uses the bundle identifier `io.github.mutexdev.liteapi` and is signed and notarized for direct distribution outside the Mac App Store.

1. From the [Apple Developer certificate portal](https://developer.apple.com/account/resources/certificates/list), create a **Developer ID Application** certificate. Install it with its private key in Keychain Access, then export both as a password-protected `.p12` file.
2. In [App Store Connect Users and Access](https://appstoreconnect.apple.com/access/integrations/api), create a Team API key that can submit software for notarization. Download the `.p8` key when it is created; Apple only allows it to be downloaded once.
3. Convert both files to single-line base64 values on macOS:

   ```sh
   base64 -i /path/to/DeveloperIDApplication.p12 | pbcopy
   base64 -i /path/to/AuthKey_KEY_ID.p8 | pbcopy
   ```

4. In **GitHub repository settings → Secrets and variables → Actions**, add:

   - `MACOS_CERTIFICATE_P12_BASE64`: base64-encoded `.p12` contents
   - `MACOS_CERTIFICATE_PASSWORD`: password used when exporting the `.p12`
   - `APPLE_API_KEY_P8_BASE64`: base64-encoded `.p8` contents
   - `APPLE_API_KEY_ID`: App Store Connect API key ID
   - `APPLE_API_ISSUER_ID`: App Store Connect API issuer ID

The workflow reconstructs these credentials only under the hosted runner's temporary directory, signs with hardened runtime and a secure timestamp, submits the DMG using `notarytool`, staples the notarization ticket, and removes the temporary certificate, API key, and keychain. Never commit signing credentials to this repository.

## Automated releases

The release workflow is defined in `.github/workflows/release.yml` and contains exactly four jobs: macOS, Windows, Linux, and release publishing. The three platform builds run independently. The publishing job starts only after all three pass, verifies the expected packages, generates SHA-256 checksums, and creates the GitHub release for the triggering commit.

The workflow responds to every push to `main`. Protect `main` from direct pushes if releases should only be produced by merged pull requests.
