// The first-run offer: another client's collections, and a managed machine's
// settings (US-064).
//
// Two things decide whether this app is usable in its first five minutes:
// whether the requests somebody already has can come across, and whether it can
// reach anything on a network that only routes out through a proxy. Both are
// offered once, on a launch that finds something to offer.
//
// The boundary from internal/discovery is kept here rather than re-litigated:
// DiscoverImportSources stats directories and reads nothing inside them.
// ReadDiscoveredCollections opens files, and is a separate call precisely so
// that the user's yes sits between the two.
package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/discovery"
	"github.com/mutexdev/lite_api/internal/prefs"
	"github.com/mutexdev/lite_api/internal/transport"
)

// DiscoveredClient is an API client found installed on this machine.
//
// Mirrors discovery.Installation rather than exposing it, so the whole bound
// surface stays inside the core namespace the generated bindings already use.
type DiscoveredClient struct {
	Client      string `json:"client"`
	DisplayName string `json:"displayName"`
	Path        string `json:"path"`
	Readable    bool   `json:"readable"`
	Guidance    string `json:"guidance,omitempty"`
}

// DiscoveredCACertificate is a certificate authority found on disk, described
// well enough for somebody to decide about it.
type DiscoveredCACertificate struct {
	Path           string `json:"path"`
	Subject        string `json:"subject"`
	Issuer         string `json:"issuer"`
	Fingerprint    string `json:"fingerprint"`
	NotAfter       string `json:"notAfter"`
	Expired        bool   `json:"expired"`
	AlreadyTrusted bool   `json:"alreadyTrusted"`
}

// DiscoveredCollection is one collection found inside an installed client.
type DiscoveredCollection struct {
	Client string `json:"client"`
	// ID identifies this collection within one read, because a name does not.
	// Two workspaces in another client can share a name -- two Insomnia
	// workspaces both left at the default is the ordinary case -- and selecting
	// by name meant one tick box chose both, importing a collection the user
	// never saw.
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	SourcePath   string   `json:"sourcePath,omitempty"`
	RequestCount int      `json:"requestCount"`
	Warnings     []string `json:"warnings,omitempty"`
	// content is deliberately unexported: it is the converted document, which
	// belongs to the import call and has no business crossing to the frontend
	// and back.
	content string
}

// DiscoveryReport is what a launch found worth offering.
type DiscoveryReport struct {
	Installations  []DiscoveredClient        `json:"installations"`
	CACertificates []DiscoveredCACertificate `json:"caCertificates"`
	// Proxy describes the proxy this machine is configured with, when the
	// operating system has one and this app is not already using it.
	Proxy DiscoveryProxyReport `json:"proxy"`
	// ShouldPrompt is false once the offer has been dismissed, and false when
	// there is nothing to say. The feature stays reachable either way.
	ShouldPrompt bool `json:"shouldPrompt"`
}

// DiscoveryProxyReport describes the machine's proxy configuration.
//
// There is nothing to adopt: "system" is already the default proxy mode, and
// since US-061 that reads the Windows registry and the GNOME settings as well
// as the environment. This exists to say so, because a user who has just been
// told their requests fail behind a corporate proxy needs to know the app is
// already using it.
type DiscoveryProxyReport struct {
	Detected    bool   `json:"detected"`
	Description string `json:"description,omitempty"`
	InUse       bool   `json:"inUse"`
}

// discoveryProbeURL is what the proxy resolver is asked about. A public
// https URL is the case the user cares about and the one a bypass list is least
// likely to exempt. Nothing is sent to it.
const discoveryProbeURL = "https://api.example.com/"

func (a *App) discoveryRoots() discovery.Roots {
	if a.discoveryOverride != nil {
		return a.discoveryOverride.roots
	}
	return discovery.SystemRoots()
}

func (a *App) discoveryCADirectories() []string {
	if a.discoveryOverride != nil {
		return a.discoveryOverride.caDirectories
	}
	return discovery.SystemCACertificateDirectories()
}

// DiscoverImportSources reports what this machine has that is worth offering.
//
// Cheap by construction: directory stats, a certificate parse per candidate
// file, and one proxy resolution. It opens no API client's data.
func (a *App) DiscoverImportSources() (DiscoveryReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return DiscoveryReport{}, err
	}
	report := DiscoveryReport{
		Installations:  []DiscoveredClient{},
		CACertificates: []DiscoveredCACertificate{},
		Proxy:          a.discoveryProxyReport(),
	}
	for _, installation := range discovery.Detect(a.discoveryRoots()) {
		report.Installations = append(report.Installations, DiscoveredClient{
			Client:      installation.Client,
			DisplayName: installation.DisplayName,
			Path:        installation.Path,
			Readable:    installation.Readable,
			Guidance:    installation.Guidance,
		})
	}
	for _, candidate := range discovery.ScanCACertificates(a.discoveryCADirectories()) {
		report.CACertificates = append(report.CACertificates, DiscoveredCACertificate{
			Path:           candidate.Path,
			Subject:        candidate.Subject,
			Issuer:         candidate.Issuer,
			Fingerprint:    candidate.Fingerprint,
			NotAfter:       candidate.NotAfter.UTC().Format(time.RFC3339),
			Expired:        candidate.Expired,
			AlreadyTrusted: candidate.AlreadyTrusted,
		})
	}
	report.ShouldPrompt = strings.TrimSpace(a.state.Preferences.General.DiscoveryPromptedAt) == "" &&
		(len(report.Installations) > 0 || discoveryHasAdoptableCA(report.CACertificates))
	return report, nil
}

// discoveryHasAdoptableCA ignores candidates that would change nothing. A CA
// the system already trusts is not worth interrupting anyone about.
func discoveryHasAdoptableCA(candidates []DiscoveredCACertificate) bool {
	for _, candidate := range candidates {
		if !candidate.Expired && !candidate.AlreadyTrusted {
			return true
		}
	}
	return false
}

func (a *App) discoveryProxyReport() DiscoveryProxyReport {
	proxyURL, err := transport.SystemProxyURLForRequest(discoveryProbeURL)
	if err != nil || proxyURL == nil {
		return DiscoveryProxyReport{}
	}
	preferences := prefs.NormalizeProxy(a.state.Preferences.Proxy, a.state.Preferences.ProxyMode)
	return DiscoveryProxyReport{
		Detected:    true,
		Description: proxyURL.Host,
		InUse:       !preferences.Disabled && preferences.Source == "inherit",
	}
}

// ReadDiscoveredCollections opens one client's store.
//
// Separate from DiscoverImportSources on purpose: this is the call the user's
// yes authorises, and it refuses any client the machine has not been seen to
// have -- a caller naming a client out of nowhere has no consent behind it.
func (a *App) ReadDiscoveredCollections(client string) ([]DiscoveredCollection, error) {
	a.mu.RLock()
	roots := a.discoveryRoots()
	a.mu.RUnlock()
	for _, installation := range discovery.Detect(roots) {
		if installation.Client != strings.TrimSpace(client) {
			continue
		}
		if !installation.Readable {
			return nil, fmt.Errorf("%s: %s", installation.DisplayName, installation.Guidance)
		}
		found, err := discovery.ReadCollections(installation)
		if err != nil {
			return nil, errors.New("this client's collections could not be read")
		}
		collections := make([]DiscoveredCollection, 0, len(found))
		for index, entry := range found {
			collections = append(collections, DiscoveredCollection{
				Client:       entry.Client,
				ID:           discoveredCollectionID(entry.Client, index),
				Name:         entry.Name,
				Kind:         entry.Kind,
				SourcePath:   entry.SourcePath,
				RequestCount: entry.RequestCount,
				Warnings:     entry.Warnings,
				content:      entry.Content,
			})
		}
		return collections, nil
	}
	return nil, errors.New("that client is not installed on this machine")
}

// Source renders a discovered collection as an import source, so nothing about
// the import is special-cased: the preview, the conflict handling and the
// content hash are the ones the user already knows. A folder-backed collection
// travels as a path, and is opened where it lies rather than copied.
func (collection DiscoveredCollection) Source(index int) CollectionImportSource {
	id := fmt.Sprintf("discovered-%s-%d", collection.Client, index+1)
	if strings.TrimSpace(collection.SourcePath) != "" {
		return CollectionImportSource{ID: id, Path: collection.SourcePath}
	}
	return CollectionImportSource{
		ID:           id,
		Name:         collection.Name,
		Content:      collection.content,
		KindOverride: collection.Kind,
	}
}

// ImportDiscoveredCollections reads one client's collections and imports the
// chosen ones, in a single call.
//
// The converted documents never cross to the frontend: sending another
// application's requests out to the UI so it can send them straight back is a
// copy of somebody's data taking a trip for no reason. The frontend chooses by
// name; the bytes stay here.
func (a *App) ImportDiscoveredCollections(workspaceID, client string, ids []string) (CollectionImportApplyResult, error) {
	found, err := a.ReadDiscoveredCollections(client)
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	// Selections arrive as the ids handed out by ReadDiscoveredCollections.
	// Matching on the display name instead would import every collection
	// sharing the chosen one's name, which is how a user asking for one of two
	// identically-named workspaces got both.
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[strings.TrimSpace(id)] = true
	}
	sources := []CollectionImportSource{}
	for index, collection := range found {
		if len(wanted) > 0 && !wanted[collection.ID] {
			continue
		}
		sources = append(sources, collection.Source(index))
	}
	if len(sources) == 0 {
		return CollectionImportApplyResult{}, errors.New("none of the selected collections were found")
	}
	preview, err := a.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: workspaceID, Sources: sources})
	if err != nil {
		return CollectionImportApplyResult{}, err
	}
	selections := make([]CollectionImportSelection, 0, len(preview.Rows))
	for _, row := range preview.Rows {
		if row.Error != "" {
			continue
		}
		selections = append(selections, CollectionImportSelection{
			SourceID:            row.SourceID,
			CandidateID:         row.CandidateID,
			ExpectedContentHash: row.ContentHash,
		})
	}
	if len(selections) == 0 {
		return CollectionImportApplyResult{}, errors.New("none of the selected collections could be read")
	}
	return a.ApplyCollectionImport(CollectionImportApplyRequest{WorkspaceID: workspaceID, Sources: sources, Selections: selections})
}

// DismissDiscoveryPrompt records that the offer was made, so it is not made
// again. A prompt that returns every launch is one people learn to dismiss
// without reading, which costs more than it ever saves.
func (a *App) DismissDiscoveryPrompt() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	a.state.Preferences.General.DiscoveryPromptedAt = time.Now().UTC().Format(time.RFC3339)
	return a.state, a.markDirty(persistScopeState)
}

// AdoptDiscoveredCACertificate switches on the custom CA preference for a
// certificate the user has just been shown.
//
// The path must be one this machine's scan actually produced. A caller naming
// an arbitrary path is a caller asking this app to trust something nobody
// looked at, and trust is the one setting that must not be reachable that way.
func (a *App) AdoptDiscoveredCACertificate(path string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	cleaned := filepath.Clean(strings.TrimSpace(path))
	var chosen *discovery.CACandidate
	for _, candidate := range discovery.ScanCACertificates(a.discoveryCADirectories()) {
		if filepath.Clean(candidate.Path) == cleaned {
			found := candidate
			chosen = &found
			break
		}
	}
	if chosen == nil {
		return AppState{}, errors.New("that certificate was not among the ones found on this machine")
	}
	if chosen.Expired {
		return AppState{}, errors.New("that certificate has expired and would not make any request succeed")
	}
	a.state.Preferences.Request.CustomCaCertificate.Enabled = true
	a.state.Preferences.Request.CustomCaCertificate.FilePath = chosen.Path
	// The system roots stay. Replacing them with one corporate CA would break
	// every request to the public internet, which is not what "trust this CA"
	// means to anyone who clicks it.
	keep := true
	a.state.Preferences.Request.KeepDefaultCaCertificates.Enabled = &keep
	a.transportCache.Flush()
	a.notify("success", "Now trusting the certificate authority "+chosen.Subject)
	return a.state, a.markDirty(persistScopeState)
}

// discoveryOverrides lets the tests point discovery at a synthetic tree. No
// API client is installed on a build machine, so without this none of the
// discovery paths would be exercised at all.
type discoveryOverrides struct {
	roots         discovery.Roots
	caDirectories []string
}

func (a *App) discoveryRootsForTest(root string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.discoveryOverride == nil {
		a.discoveryOverride = &discoveryOverrides{}
	}
	a.discoveryOverride.roots = discovery.Roots{
		Home:      root,
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
	}
}

func (a *App) discoveryCADirsForTest(directories ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.discoveryOverride == nil {
		a.discoveryOverride = &discoveryOverrides{}
	}
	a.discoveryOverride.caDirectories = directories
}

// discoveredCollectionID identifies one discovered collection within a read.
//
// The position is the only thing that separates two collections a client has
// given the same name, so the position is what the id is built from. Detection
// sorts its results, so the same machine reads back the same ids, which is what
// lets the modal show one and the import receive the same one.
func discoveredCollectionID(client string, index int) string {
	return fmt.Sprintf("%s:%d", strings.TrimSpace(client), index)
}
