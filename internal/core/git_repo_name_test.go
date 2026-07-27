package core

import (
	"strings"
	"testing"
)

// deriveGitRepoName chooses the DIRECTORY NAME a cloned repository lands in, and
// it was at 0% coverage. Getting it wrong is visible immediately and awkward to
// undo: the collection appears under a name the user did not choose, inside a
// directory they then have to find and rename by hand.
//
// It handles three remote forms, and the middle one is the one that matters
// most because it is what GitHub hands out for SSH.

func TestDeriveGitRepoNameFromAnHTTPSRemote(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/user/repo.git": "repo",
		"https://github.com/user/repo":     "repo",
		"https://example.test/a/b/c.git":   "c",
	} {
		if got := deriveGitRepoName(remote); got != want {
			t.Errorf("deriveGitRepoName(%q) = %q, want %q", remote, got, want)
		}
	}
}

// THE SCP FORM, which url.Parse rejects outright because '@' is not a legal
// scheme character: this is the address GitHub shows under "SSH", so it is the
// one most users paste. It falls to the colon branch.
//
// That branch splits on the LAST colon, and I could not find a realistic remote
// where that differs from the first. scp syntax has no port field, and a remote
// that does carry one is a URL and takes the scheme branch. The two choices
// diverge only when a colon appears after the last slash — i.e. inside the
// repository name itself ("git@host:user/re:po.git" gives "po" against
// "re:po"), which is not a name a filesystem would accept anyway. Recorded so
// the equivalence is not re-derived; the code is left as it stands.
func TestDeriveGitRepoNameFromAnSCPStyleRemote(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:user/repo.git": "repo",
		"git@github.com:user/repo":     "repo",
		"git@host:repo.git":            "repo",
	} {
		if got := deriveGitRepoName(remote); got != want {
			t.Errorf("deriveGitRepoName(%q) = %q, want %q", remote, got, want)
		}
	}
}

// An ssh:// URL with a port goes through the SCHEME branch, where the port is
// part of the host and not of the path. Routing it through the colon branch
// instead would split at the port and yield "22/user/repo".
func TestDeriveGitRepoNameHandlesAPortWithoutSplittingOnIt(t *testing.T) {
	if got := deriveGitRepoName("ssh://git@host:2222/user/repo.git"); got != "repo" {
		t.Errorf("got %q, want repo — the port was treated as part of the path", got)
	}
}

// A trailing slash must not produce an empty name. A clone into "" would land in
// the parent directory and mix the repository's files into it.
func TestDeriveGitRepoNameIgnoresTrailingSlashes(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/user/repo/",
		"https://github.com/user/repo.git/",
		"git@github.com:user/repo/",
	} {
		if got := deriveGitRepoName(remote); got != "repo" {
			t.Errorf("deriveGitRepoName(%q) = %q, want repo", remote, got)
		}
	}
}

// Only ONE .git is stripped, and only from the end. A repository legitimately
// named "repo.github" keeps its name, and one named ".git.git" is not stripped
// to nothing.
func TestDeriveGitRepoNameStripsOneTrailingGitSuffix(t *testing.T) {
	if got := deriveGitRepoName("https://host/user/repo.github"); got != "repo.github" {
		t.Errorf("got %q, want repo.github", got)
	}
	if got := deriveGitRepoName("https://host/user/my.git.git"); got != "my.git" {
		t.Errorf("got %q, want my.git", got)
	}
}

// THE RESULT IS SANITISED, because it becomes a directory name. A remote whose
// last segment carries a path separator or a control character must not reach
// the filesystem as-is.
func TestDeriveGitRepoNameSanitisesTheResult(t *testing.T) {
	for _, remote := range []string{
		"https://host/user/re:po.git",
		"https://host/user/re*po.git",
		`git@host:user/re"po.git`,
	} {
		got := deriveGitRepoName(remote)
		for _, bad := range []string{":", "*", `"`, "/", `\`} {
			if got != "" && strings.Contains(got, bad) {
				t.Errorf("deriveGitRepoName(%q) = %q, which still contains %q", remote, got, bad)
			}
		}
	}
}

// A remote with no usable last segment falls back to a PLACEHOLDER, not to an
// empty string. I expected empty and was wrong; the fallback is the better
// behaviour and worth pinning as such.
//
// A clone into "" lands in the PARENT directory and mixes the repository's
// files into it — which is far harder to undo than a directory named
// "untitled". The name is wrong either way; only one of the two is recoverable
// by renaming.
func TestDeriveGitRepoNameFallsBackRatherThanReturningEmpty(t *testing.T) {
	for _, remote := range []string{"", "   ", "/", "///"} {
		got := deriveGitRepoName(remote)
		if got == "" {
			t.Errorf("deriveGitRepoName(%q) returned empty; a clone would land in the parent directory", remote)
		}
		if strings.ContainsAny(got, `/\:*?"<>|`) {
			t.Errorf("deriveGitRepoName(%q) = %q, which is not a usable directory name", remote, got)
		}
	}
}

func TestPathBaseTakesTheLastSegment(t *testing.T) {
	for value, want := range map[string]string{
		"a/b/c":   "c",
		"c":       "c",
		"a/b/c/":  "c",
		"  a/b  ": "b",
		"":        "",
		"/":       "",
	} {
		if got := pathBase(value); got != want {
			t.Errorf("pathBase(%q) = %q, want %q", value, got, want)
		}
	}
}

// FOUND BY WRITING THESE TESTS, and a real defect rather than a missing case.
//
// `myserver:repo.git` is the scp form with the host supplied by an ssh config
// alias — a very common way to clone from a private host. url.Parse reports
// scheme="myserver" for it, because "myserver" is a syntactically valid scheme
// and there is no "//" to say otherwise. The remainder lands in Opaque, and
// Path is EMPTY, so the scheme branch derives a name from nothing.
//
// The result is that cloning `myserver:repo.git` produced a collection named
// "untitled". The user@ form escapes this only by accident: '@' is not a legal
// scheme character, so url.Parse fails outright and the colon branch runs.
func TestDeriveGitRepoNameFromAnSCPRemoteWithNoUsername(t *testing.T) {
	for remote, want := range map[string]string{
		"myserver:repo.git":         "repo",
		"github.com:user/repo.git":  "repo",
		"myserver:path/to/repo.git": "repo",
		"myserver:repo":             "repo",
	} {
		if got := deriveGitRepoName(remote); got != want {
			t.Errorf("deriveGitRepoName(%q) = %q, want %q", remote, got, want)
		}
	}
}

// The guard is the presence of an OPAQUE part, not a guess at which schemes are
// real: a URL with a proper "//" authority always has an empty Opaque, so the
// two forms separate cleanly without a list of known schemes to maintain.
func TestDeriveGitRepoNameStillPrefersARealURLOverTheOpaqueForm(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/user/repo.git":  "repo",
		"ssh://git@host:2222/user/repo.git": "repo",
		"file:///srv/git/repo.git":          "repo",
	} {
		if got := deriveGitRepoName(remote); got != want {
			t.Errorf("deriveGitRepoName(%q) = %q, want %q", remote, got, want)
		}
	}
}
