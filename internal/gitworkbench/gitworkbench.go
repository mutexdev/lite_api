package gitworkbench

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/scripting"
)

const (
	Timeout        = 12 * time.Second
	NetworkTimeout = 45 * time.Second
	OutputLimit    = 256 * 1024
	DisplayLimit   = 1024
)

type CollectionGitFile struct {
	Path       string `json:"path"`
	Index      string `json:"index"`
	Worktree   string `json:"worktree"`
	Staged     bool   `json:"staged"`
	Untracked  bool   `json:"untracked"`
	Conflicted bool   `json:"conflicted"`
	Binary     bool   `json:"binary"`
}

type CollectionGitRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type CollectionGitSnapshot struct {
	Available   bool                  `json:"available"`
	Initialized bool                  `json:"initialized"`
	RootLabel   string                `json:"rootLabel,omitempty"`
	Branch      string                `json:"branch,omitempty"`
	Detached    bool                  `json:"detached"`
	Upstream    string                `json:"upstream,omitempty"`
	Ahead       int                   `json:"ahead"`
	Behind      int                   `json:"behind"`
	Clean       bool                  `json:"clean"`
	Conflicts   bool                  `json:"conflicts"`
	Remotes     []CollectionGitRemote `json:"remotes"`
	Branches    []string              `json:"branches"`
	Files       []CollectionGitFile   `json:"files"`
}

type CollectionGitDiff struct {
	Path      string `json:"path"`
	Staged    bool   `json:"staged"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
}

type CollectionGitOperationResult struct {
	Snapshot CollectionGitSnapshot `json:"snapshot"`
	Message  string                `json:"message"`
}

type LimitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *LimitedBuffer) Write(p []byte) (int, error) {
	// The b.Buffer.Write calls below must stay explicitly qualified: b.Write is
	// this method, so dropping the embedded field would recurse forever.
	if b.Len() < b.limit {
		remaining := b.limit - b.Len()
		if len(p) > remaining {
			_, _ = b.Buffer.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.Buffer.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

var gitWorkbenchURLUserInfoPattern = regexp.MustCompile(`(?i)(https?|ssh|git)://[^\s/@]+@`)

var gitWorkbenchURLQueryPattern = regexp.MustCompile(`(?i)(https?|ssh|git|file)://[^\s?#]+\?[^\s]+`)

var gitWorkbenchAbsolutePathPattern = regexp.MustCompile(`(?:^|[\s:'\"])(?:/[^\s:'\"]+|[A-Za-z]:\\[^\s:'\"]+)`)

func RedactText(value string) string {
	value = gitWorkbenchURLUserInfoPattern.ReplaceAllString(value, "$1://<redacted>@")
	value = gitWorkbenchURLQueryPattern.ReplaceAllString(value, "$1://<redacted>")
	value = gitWorkbenchAbsolutePathPattern.ReplaceAllStringFunc(value, func(match string) string {
		prefix := ""
		if len(match) > 0 && (match[0] == ' ' || match[0] == ':' || match[0] == '\'' || match[0] == '"') {
			prefix = match[:1]
		}
		return prefix + "<path>"
	})
	return strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
}

func SanitizeDiagnostic(value string) string {
	value = RedactText(value)
	if len(value) > DisplayLimit {
		value = value[:DisplayLimit] + "…"
	}
	return value
}

func SanitizeDiff(value string) string {
	return RedactText(value)
}

func Error(action, output string, err error) error {
	if err == nil {
		return nil
	}
	safe := SanitizeDiagnostic(output)
	lower := strings.ToLower(safe)
	if strings.Contains(lower, "please tell me who you are") || strings.Contains(lower, "unable to auto-detect email") || strings.Contains(lower, "empty ident name") || strings.Contains(lower, "empty ident email") {
		return errors.New("git author identity is not configured; set user.name and user.email before committing")
	}
	if safe == "" {
		safe = SanitizeDiagnostic(err.Error())
	}
	if safe == "" {
		return fmt.Errorf("git %s failed", action)
	}
	return fmt.Errorf("git %s failed: %s", action, safe)
}

func StatusRows(repo Repository, output string) []CollectionGitFile {
	rows := []CollectionGitFile{}
	parts := strings.Split(output, "\x00")
	for index := 0; index < len(parts); index++ {
		record := parts[index]
		if len(record) < 4 || strings.HasPrefix(record, "## ") {
			continue
		}
		indexStatus, worktreeStatus := record[0:1], record[1:2]
		path := record[3:]
		if indexStatus == "R" || indexStatus == "C" {
			if index+1 < len(parts) {
				index++
			}
		}
		absolute := filepath.Join(repo.RepositoryPath, filepath.FromSlash(path))
		if !scripting.PathInside(repo.CollectionPath, absolute) {
			continue
		}
		relative, err := filepath.Rel(repo.CollectionPath, absolute)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		conflicted := strings.ContainsAny(indexStatus+worktreeStatus, "U") || (indexStatus == "A" && worktreeStatus == "A") || (indexStatus == "D" && worktreeStatus == "D")
		rows = append(rows, CollectionGitFile{Path: filepath.ToSlash(relative), Index: indexStatus, Worktree: worktreeStatus, Staged: indexStatus != " " && indexStatus != "?", Untracked: indexStatus == "?" && worktreeStatus == "?", Conflicted: conflicted})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	return rows
}

func ParseNameStatusPaths(output string) ([]string, error) {
	records := strings.Split(output, "\x00")
	paths := []string{}
	for index := 0; index < len(records); {
		if records[index] == "" {
			if index == len(records)-1 {
				break
			}
			return nil, errors.New("malformed staged Git name-status output")
		}
		status := records[index]
		if len(status) == 0 {
			return nil, errors.New("malformed staged Git name-status output")
		}
		switch status[0] {
		case 'R', 'C':
			if len(records) < index+3 || records[index+1] == "" || records[index+2] == "" {
				return nil, errors.New("malformed staged Git rename or copy output")
			}
			paths = append(paths, records[index+1], records[index+2])
			index += 3
		case 'A', 'M', 'D', 'T', 'U':
			if len(records) < index+2 || records[index+1] == "" {
				return nil, errors.New("malformed staged Git name-status output")
			}
			paths = append(paths, records[index+1])
			index += 2
		default:
			return nil, errors.New("unsupported staged Git name-status record")
		}
	}
	return paths, nil
}

func PathWithinActiveCollection(repo Repository, path string) bool {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return false
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return false
	}
	absolute := filepath.Join(repo.RepositoryPath, cleaned)
	return scripting.PathInside(repo.RepositoryPath, absolute) && scripting.PathInside(repo.CollectionPath, absolute)
}

func ValidateBranch(branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") {
		return "", errors.New("git branch name is required")
	}
	if strings.ContainsAny(branch, "\r\n\x00") {
		return "", errors.New("git branch name is invalid")
	}
	return branch, nil
}

func ValidateRemoteName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n\t /\\:@") {
		return "", errors.New("git remote name is invalid")
	}
	return name, nil
}

func NormalizeCollectionRemoteURL(raw string) (string, error) {
	remote, err := NormalizeGitRemoteURL(raw)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(remote, "?#") || strings.Contains(remote, "@") && strings.Contains(remote, "://") {
		return "", errors.New("git remote URL must not embed credentials, queries, or fragments")
	}
	if strings.HasPrefix(remote, "file://") {
		parsed, parseErr := url.Parse(remote)
		if parseErr != nil || (parsed.Host != "" && parsed.Host != "localhost") || parsed.User != nil {
			return "", errors.New("file Git remote URL must be credential-free and local")
		}
	}
	return remote, nil
}

type Repository struct {
	CollectionPath string
	RepositoryPath string
	Pathspec       string
}

func NormalizeGitRemoteURL(raw string) (string, error) {
	remote := strings.TrimSpace(raw)
	if remote == "" {
		return "", errors.New("git remote URL is required")
	}
	if strings.ContainsAny(remote, "\r\n\t ") {
		return "", errors.New("git remote URL cannot contain whitespace")
	}
	if strings.Contains(remote, "://") {
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("invalid Git remote URL: %w", err)
		}
		switch parsed.Scheme {
		case "https", "http", "ssh", "git":
			if parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" {
				return "", errors.New("git remote URL must include host and repository path")
			}
		case "file":
			if parsed.Path == "" || parsed.Path == "/" {
				return "", errors.New("file Git remote URL must include a repository path")
			}
		default:
			return "", fmt.Errorf("unsupported Git remote URL scheme %q", parsed.Scheme)
		}
		if parsed.User != nil {
			return "", errors.New("git remote URL must not embed credentials")
		}
		return remote, nil
	}
	if strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, "./") || strings.HasPrefix(remote, "../") {
		return "", errors.New("local Git remotes must use file:// URLs")
	}
	colon := strings.Index(remote, ":")
	if colon <= 0 || colon == len(remote)-1 {
		return "", errors.New("git remote URL must be https://, ssh://, file://, or git@host:path")
	}
	userHost := remote[:colon]
	repoPath := remote[colon+1:]
	at := strings.Index(userHost, "@")
	if at <= 0 || at == len(userHost)-1 || strings.Contains(repoPath, ":") || strings.HasPrefix(repoPath, "/") {
		return "", errors.New("git remote URL must be https://, ssh://, file://, or git@host:path")
	}
	return remote, nil
}

// NewLimitedBuffer caps how much of a git subprocess's output is retained.
// git diff on a large collection can produce megabytes, and the output is
// headed for a JSON payload and then a webview; the cap is what keeps a large
// repository from turning a status call into a memory problem.
func NewLimitedBuffer(limit int) *LimitedBuffer {
	return &LimitedBuffer{limit: limit}
}

// Truncated reports whether the cap was reached, so a caller can say so rather
// than presenting a silently clipped diff as complete.
func (b *LimitedBuffer) Truncated() bool { return b.truncated }
