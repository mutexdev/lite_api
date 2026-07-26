// The two remote-URL validators, and why they are not interchangeable.
//
// This test exists because I nearly swapped them during the extraction.
// NormalizeCollectionRemoteURL calls NormalizeGitRemoteURL and then rejects
// three more things, so the names look like a base and an alias and are not:
// substituting the base for the strict one compiles, passes every other test,
// and quietly lets SetCollectionGitRemote store a URL with credentials in it.
package gitworkbench

import "testing"

// Accepted by both — the ordinary shapes.
func TestBothValidatorsAcceptOrdinaryRemotes(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/owner/repo.git",
		"ssh://github.com/owner/repo.git",
		"git@github.com:owner/repo.git",
	} {
		if _, err := NormalizeGitRemoteURL(remote); err != nil {
			t.Errorf("base rejected %q: %v", remote, err)
		}
		if _, err := NormalizeCollectionRemoteURL(remote); err != nil {
			t.Errorf("strict rejected %q: %v", remote, err)
		}
	}
}

// The whole reason the strict one exists. A collection's remote is persisted
// into the collection's own config, so a URL carrying a password would write
// that password to disk in plaintext and into any export of the collection.
func TestCollectionRemoteRejectsWhatTheBaseAllows(t *testing.T) {
	for name, remote := range map[string]string{
		"embedded credentials": "https://user:token@github.com/owner/repo.git",
		"query string":         "https://github.com/owner/repo.git?token=secret",
		"fragment":             "https://github.com/owner/repo.git#ref",
		"remote file host":     "file://evil.example/repo",
	} {
		if _, err := NormalizeCollectionRemoteURL(remote); err == nil {
			t.Errorf("%s: strict validator accepted %q", name, remote)
		}
	}
}

// Both reject these, so a test that only checked the strict one would pass with
// either function substituted. The pairs above are the ones that discriminate.
func TestBothValidatorsRejectMalformedRemotes(t *testing.T) {
	for name, remote := range map[string]string{
		"empty":          "",
		"whitespace":     "https://github.com/owner/ repo.git",
		"unknown scheme": "ftp://github.com/owner/repo.git",
		"no path":        "https://github.com",
	} {
		if _, err := NormalizeGitRemoteURL(remote); err == nil {
			t.Errorf("%s: base accepted %q", name, remote)
		}
		if _, err := NormalizeCollectionRemoteURL(remote); err == nil {
			t.Errorf("%s: strict accepted %q", name, remote)
		}
	}
}

// A local file remote is legitimate — cloning from a path on the same machine.
func TestCollectionRemoteAllowsLocalFileURLs(t *testing.T) {
	for _, remote := range []string{"file:///srv/repos/thing.git", "file://localhost/srv/repos/thing.git"} {
		if _, err := NormalizeCollectionRemoteURL(remote); err != nil {
			t.Errorf("rejected local file remote %q: %v", remote, err)
		}
	}
}

// Documented, not endorsed: the same remote is accepted in scp form and
// rejected in URL form.
//
//	git@github.com:owner/repo.git        accepted
//	ssh://git@github.com/owner/repo.git  rejected, "must not embed credentials"
//
// For https that check is right — userinfo there is a password headed for disk.
// For ssh, "git" is the login name and authentication happens over the key
// agent, so there is no credential to embed. The scp form being accepted shows
// the intent was never to ban "git@".
//
// Left as-is deliberately: this is validation on a security-adjacent path and
// the workaround (use the scp form) is the spelling most people already use.
// The test records the asymmetry so a future change is a decision rather than a
// surprise.
func TestSSHURLFormWithLoginNameIsRejectedWhileSCPFormIsNot(t *testing.T) {
	if _, err := NormalizeGitRemoteURL("git@github.com:owner/repo.git"); err != nil {
		t.Errorf("scp form rejected: %v", err)
	}
	if _, err := NormalizeGitRemoteURL("ssh://git@github.com/owner/repo.git"); err == nil {
		t.Error("ssh URL form with a login name is now accepted; the asymmetry above has been fixed, update this test")
	}
}
