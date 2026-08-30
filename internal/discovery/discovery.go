// Finding the API clients already installed on the machine (US-062).
//
// Someone opening this app for the first time usually has years of requests in
// another one, and the gap between "installed" and "useful" is exactly that
// import. This package finds what is there so the app can offer to bring it
// across.
//
// The package is split in two, and the split is a privacy boundary rather than
// a tidiness one:
//
//   - Detect stats directories. It learns THAT a client is installed and
//     nothing else. It never opens a file.
//   - ReadCollections opens files, and is called only after the user has said
//     yes to this specific client.
//
// The order matters because these stores hold live credentials. An unprompted
// "we found your collections" banner is a banner that read somebody's bearer
// tokens before asking, and no amount of good intent afterwards undoes that.
//
// Nothing here writes to another application's files, ever.
package discovery

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The clients this package knows about.
const (
	ClientPostman       = "postman"
	ClientInsomnia      = "insomnia"
	ClientBruno         = "bruno"
	ClientThunderClient = "thunder-client"
	ClientYaak          = "yaak"
)

// Roots are the directories a machine keeps application data in. They are
// injected rather than read from the environment so the whole package is
// testable against a synthetic tree -- which is the only way it gets tested at
// all, since no API client is installed on a build machine.
type Roots struct {
	Home      string
	ConfigDir string
	DataDir   string
}

// SystemRoots returns the real directories for this machine.
//
// os.UserConfigDir already resolves to the right place per platform --
// ~/Library/Application Support on macOS, ~/.config on Linux, %AppData% on
// Windows -- which is where all of these clients put their data, so most of the
// per-OS branching this would otherwise need does not exist.
func SystemRoots() Roots {
	roots := Roots{}
	if home, err := os.UserHomeDir(); err == nil {
		roots.Home = home
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		roots.ConfigDir = configDir
	}
	roots.DataDir = strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if roots.DataDir == "" && roots.Home != "" {
		roots.DataDir = filepath.Join(roots.Home, ".local", "share")
	}
	return roots
}

// Installation is one API client found on the machine.
type Installation struct {
	Client      string `json:"client"`
	DisplayName string `json:"displayName"`
	// Path is the directory that proved the client is installed. It is reported
	// so the user can see exactly what we looked at before agreeing to a read.
	Path string `json:"path"`
	// Readable reports whether this client's collections can be enumerated. A
	// false here is a statement about the format, not about permissions.
	Readable bool `json:"readable"`
	// Guidance says what to do when Readable is false. An offer that cannot be
	// fulfilled has to end somewhere useful.
	Guidance string `json:"guidance,omitempty"`
}

// Discovered is one collection found inside an installation, expressed as
// something the existing import pipeline already accepts.
//
// Nothing here invents a new import path: a Bruno collection is a folder this
// app already opens, Insomnia becomes the export shape its importer already
// reads, and Thunder Client becomes a Postman collection, which inherits every
// tolerance and mapping that importer has.
type Discovered struct {
	Client string `json:"client"`
	Name   string `json:"name"`
	// SourcePath is set for a collection that lives in a folder, and is the
	// folder itself. Content is set for one that had to be converted.
	SourcePath   string   `json:"sourcePath,omitempty"`
	Content      string   `json:"content,omitempty"`
	Kind         string   `json:"kind"`
	RequestCount int      `json:"requestCount"`
	Warnings     []string `json:"warnings,omitempty"`
}

// ErrNotReadable is returned when a caller asks to read a client whose store
// this package deliberately does not open.
var ErrNotReadable = errors.New("this client's collections cannot be read directly")

const postmanGuidance = "Postman keeps collections in your Postman account, and its local cache is an " +
	"internal format that cannot be read reliably. Export a collection from Postman, or use Settings → " +
	"Account → Export Data for all of them, and drop the file here."

const yaakGuidance = "Yaak stores collections in a SQLite database this build cannot open yet. " +
	"Export a collection from Yaak and drop the file here."

// Detect reports which clients are installed. It stats directories and opens
// nothing.
func Detect(roots Roots) []Installation {
	installations := []Installation{}
	add := func(client, displayName, path string, readable bool, guidance string) {
		if path == "" || !isDirectory(path) {
			return
		}
		installations = append(installations, Installation{
			Client: client, DisplayName: displayName, Path: path, Readable: readable, Guidance: guidance,
		})
	}
	if roots.ConfigDir != "" {
		add(ClientPostman, "Postman", filepath.Join(roots.ConfigDir, "Postman"), false, postmanGuidance)
		add(ClientInsomnia, "Insomnia", insomniaDirectory(roots), true, "")
		add(ClientBruno, "Bruno", filepath.Join(roots.ConfigDir, "bruno"), true, "")
		if path := thunderClientDirectory(roots); path != "" {
			add(ClientThunderClient, "Thunder Client", path, true, "")
		}
	}
	add(ClientYaak, "Yaak", yaakDirectory(roots), false, yaakGuidance)

	// Readable first, then by name: the rows that can be acted on belong at the
	// top of a list whose only purpose is to be acted on.
	sort.SliceStable(installations, func(left, right int) bool {
		if installations[left].Readable != installations[right].Readable {
			return installations[left].Readable
		}
		return installations[left].DisplayName < installations[right].DisplayName
	})
	return installations
}

// ReadCollections enumerates one installation's collections.
//
// Only ever called after the user has agreed to this client specifically.
func ReadCollections(installation Installation) ([]Discovered, error) {
	if !installation.Readable {
		return nil, ErrNotReadable
	}
	switch installation.Client {
	case ClientInsomnia:
		return readInsomniaCollections(installation.Path)
	case ClientBruno:
		return readBrunoCollections(installation.Path)
	case ClientThunderClient:
		return readThunderClientCollections(installation.Path)
	}
	return nil, ErrNotReadable
}

func insomniaDirectory(roots Roots) string {
	// Insomnia's own override, and the name is INSOMNIA_DATA_PATH -- not the
	// _DIR variant that gets repeated in forum answers.
	if override := strings.TrimSpace(os.Getenv("INSOMNIA_DATA_PATH")); override != "" {
		return override
	}
	return filepath.Join(roots.ConfigDir, "Insomnia")
}

// thunderClientDirectory looks under each VS Code flavour. Thunder Client is an
// extension, so its data sits in the editor's global storage rather than
// anywhere named after itself.
func thunderClientDirectory(roots Roots) string {
	for _, flavour := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor"} {
		path := filepath.Join(roots.ConfigDir, flavour, "User", "globalStorage", "rangav.vscode-thunder-client")
		if isDirectory(path) {
			return path
		}
	}
	// A user-chosen custom location, not a default. Probed last, and only
	// because it costs one stat.
	if roots.Home != "" {
		if path := filepath.Join(roots.Home, ".thunder-client"); isDirectory(path) {
			return path
		}
	}
	return ""
}

func yaakDirectory(roots Roots) string {
	for _, base := range []string{roots.DataDir, roots.ConfigDir} {
		if base == "" {
			continue
		}
		if path := filepath.Join(base, "app.yaak.desktop"); isDirectory(path) {
			return path
		}
	}
	return ""
}

func isDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// readJSONFile is the one place this package opens another application's file.
// Bounded, because a store that has grown pathological is not a reason to hold
// up the app that is merely offering to read it.
const maxDiscoveryFileBytes = 64 << 20

func readBoundedFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDiscoveryFileBytes {
		return nil, errors.New("file is not readable within the discovery limit")
	}
	return os.ReadFile(path)
}
