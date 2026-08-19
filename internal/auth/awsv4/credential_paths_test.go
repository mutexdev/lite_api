// Where the SigV4 signer looks for shared credentials.
//
// These three functions decide which file on disk supplies an access key. That
// makes them the point where a request can end up signed by the wrong AWS
// account — not refused, signed, and accepted by whatever account the wrong
// credentials belong to.
package awsv4

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aws-vault, CI runners and containerised setups all point this variable
// somewhere other than ~/.aws. Ignoring it signs with the developer's personal
// default profile instead of the one the environment selected.
func TestSharedPathsHonourTheirEnvironmentOverrides(t *testing.T) {
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "  /custom/creds  ")
	t.Setenv("AWS_CONFIG_FILE", "/custom/config")
	t.Setenv("AWS_SSO_CACHE_DIR", "/custom/sso")

	if got := awsSharedCredentialsPath(); got != "/custom/creds" {
		t.Errorf("credentials: got %q, want the override trimmed", got)
	}
	if got := awsSharedConfigPath(); got != "/custom/config" {
		t.Errorf("config: got %q", got)
	}
	if got := awsV4SSOCacheDir(); got != "/custom/sso" {
		t.Errorf("sso: got %q", got)
	}
}

func TestSharedPathsDefaultToTheDotAWSDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")
	t.Setenv("AWS_SSO_CACHE_DIR", "")

	if got := awsSharedCredentialsPath(); got != filepath.Join(home, ".aws", "credentials") {
		t.Errorf("credentials: got %q", got)
	}
	if got := awsSharedConfigPath(); got != filepath.Join(home, ".aws", "config") {
		t.Errorf("config: got %q", got)
	}
	if got := awsV4SSOCacheDir(); got != filepath.Join(home, ".aws", "sso", "cache") {
		t.Errorf("sso: got %q", got)
	}
}

// With no home directory the credential paths return EMPTY, not a relative
// path. filepath.Join("", ".aws", "credentials") is ".aws/credentials", which
// resolves against the working directory — so a .aws directory sitting in
// whatever folder the app was launched from would be read as the user's real
// credentials. An empty path is refused by the caller instead.
func TestCredentialPathsAreEmptyRatherThanRelativeWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		t.Skip("this platform resolves a home directory without HOME")
	}

	for name, got := range map[string]string{"credentials": awsSharedCredentialsPath(), "config": awsSharedConfigPath()} {
		if got != "" {
			t.Errorf("%s: got %q, want empty", name, got)
		}
		if strings.HasPrefix(got, ".aws") {
			t.Errorf("%s: a relative path would read credentials from the working directory", name)
		}
	}
}

// The SSO cache is deliberately the other way round: it falls back to a temp
// directory rather than to empty, because an app started without HOME (a
// launchd or systemd service, say) should still be able to complete an SSO
// login rather than re-authenticating on every request. The file itself is
// written 0600.
func TestSSOCacheFallsBackToTempRatherThanGivingUp(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("AWS_SSO_CACHE_DIR", "")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		t.Skip("this platform resolves a home directory without HOME")
	}

	got := awsV4SSOCacheDir()
	if got == "" {
		t.Fatal("an empty cache dir would make every request re-authenticate")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("got %q; a relative cache path would follow the working directory", got)
	}
	if got != filepath.Join(os.TempDir(), ".aws", "sso", "cache") {
		t.Errorf("got %q", got)
	}
}

// The cache filename is a hash of the cache key, which is how the AWS CLI names
// these files. Using the key directly would put a start-URL — slashes and all —
// into a filename.
func TestSSOCacheFilenameIsAHashOfTheKey(t *testing.T) {
	got := awsV4SSOCacheFilename("https://example.awsapps.com/start")
	if !strings.HasSuffix(got, ".json") || len(got) != 45 {
		t.Errorf("got %q, want a 40-char hex digest plus .json", got)
	}
	if strings.ContainsAny(got, "/:") {
		t.Errorf("got %q; the raw key would not be a legal filename", got)
	}
	if again := awsV4SSOCacheFilename("https://example.awsapps.com/start"); again != got {
		t.Error("the same key must map to the same file or the cache never hits")
	}
	if other := awsV4SSOCacheFilename("https://other.awsapps.com/start"); other == got {
		t.Error("two start URLs sharing a cache file would hand one account's token to the other")
	}
}
