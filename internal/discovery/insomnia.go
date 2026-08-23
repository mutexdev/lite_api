// Reading Insomnia's local database (US-062).
//
// Insomnia stores each model in its own NeDB file -- insomnia.Workspace.db,
// insomnia.Request.db, and around forty more. NeDB is an append-only log of
// newline-delimited JSON: a record is rewritten by appending a new copy, and
// deleted by appending {"_id":..., "$$deleted":true}. Folding the log by _id,
// last write wins, is the whole format.
//
// Two consequences worth stating, because both would otherwise be surprises:
//
//   - A torn final line is normal. The file is appended to while we read it, so
//     the last line can be half-written. It is skipped, not treated as failure;
//     Insomnia's own reader tolerates a proportion of bad records too.
//   - Credentials are in there in plain text. They are read to know a request
//     HAS auth, and then dropped -- see stripInsomniaSecrets.
package discovery

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// insomniaRecord is one NeDB row. Only the fields needed to group records into
// collections are named; the rest travel as-is so the existing importer sees
// everything it knows how to read.
type insomniaRecord struct {
	ID       string
	Type     string
	ParentID string
	Name     string
	fields   map[string]interface{}
}

// insomniaExportType maps NeDB's model names onto the `_type` values the
// export format uses, which is what internal/importers/insomnia.go reads.
var insomniaExportType = map[string]string{
	"Workspace":        "workspace",
	"RequestGroup":     "request_group",
	"Request":          "request",
	"WebSocketRequest": "request",
	"GrpcRequest":      "request",
	"Environment":      "environment",
}

// insomniaSecretFields are dropped rather than copied. A discovery feature that
// lifts live bearer tokens out of another application's store is a credential
// exfiltration feature with a friendly banner on it.
var insomniaSecretFields = []string{"token", "password", "secret", "clientSecret", "accessToken", "privateKey", "passphrase", "apiKey"}

func readInsomniaCollections(directory string) ([]Discovered, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	records := map[string]insomniaRecord{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "insomnia.") || !strings.HasSuffix(name, ".db") {
			continue
		}
		if err := foldInsomniaFile(filepath.Join(directory, name), records); err != nil {
			return nil, err
		}
	}
	return groupInsomniaRecords(records), nil
}

func foldInsomniaFile(path string, records map[string]insomniaRecord) error {
	data, err := readBoundedFile(path)
	if err != nil {
		// A single unreadable model file is not a reason to abandon the rest:
		// the others still describe usable collections.
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxDiscoveryFileBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var fields map[string]interface{}
		if err := json.Unmarshal(line, &fields); err != nil {
			// A torn trailing line, or a record from a newer schema. Skipped.
			continue
		}
		id, _ := fields["_id"].(string)
		if id == "" {
			continue
		}
		if deleted, _ := fields["$$deleted"].(bool); deleted {
			delete(records, id)
			continue
		}
		parentID, _ := fields["parentId"].(string)
		name, _ := fields["name"].(string)
		modelType, _ := fields["type"].(string)
		records[id] = insomniaRecord{ID: id, Type: modelType, ParentID: parentID, Name: name, fields: fields}
	}
	return nil
}

// groupInsomniaRecords turns the folded records into one export document per
// workspace, which is exactly what ImportInsomnia already reads.
func groupInsomniaRecords(records map[string]insomniaRecord) []Discovered {
	workspaces := []insomniaRecord{}
	for _, record := range records {
		if record.Type == "Workspace" {
			workspaces = append(workspaces, record)
		}
	}
	sort.Slice(workspaces, func(left, right int) bool { return workspaces[left].ID < workspaces[right].ID })

	discovered := make([]Discovered, 0, len(workspaces))
	for _, workspace := range workspaces {
		resources := []map[string]interface{}{}
		requestCount, blanked := 0, 0
		for _, record := range records {
			exportType, known := insomniaExportType[record.Type]
			if !known {
				continue
			}
			if record.ID != workspace.ID && !insomniaDescendsFrom(records, record, workspace.ID) {
				continue
			}
			resource := map[string]interface{}{}
			for key, value := range record.fields {
				resource[key] = value
			}
			resource["_id"] = record.ID
			resource["_type"] = exportType
			if stripInsomniaSecrets(resource) {
				blanked++
			}
			resources = append(resources, resource)
			if exportType == "request" {
				requestCount++
			}
		}
		sort.Slice(resources, func(left, right int) bool {
			return fmt.Sprint(resources[left]["_id"]) < fmt.Sprint(resources[right]["_id"])
		})
		document := map[string]interface{}{
			"_type":           "export",
			"__export_format": 4,
			"__export_source": "liteapi.discovery",
			"resources":       resources,
		}
		content, err := json.Marshal(document)
		if err != nil {
			continue
		}
		found := Discovered{
			Client:       ClientInsomnia,
			Name:         strings.TrimSpace(workspace.Name),
			Content:      string(content),
			Kind:         "insomnia",
			RequestCount: requestCount,
		}
		if found.Name == "" {
			found.Name = "Insomnia collection"
		}
		if blanked > 0 {
			found.Warnings = append(found.Warnings, fmt.Sprintf(
				"%d credential value(s) were left empty rather than copied out of Insomnia; re-enter them here.", blanked))
		}
		discovered = append(discovered, found)
	}
	return discovered
}

// insomniaDescendsFrom walks parentId upward. Depth is bounded because a store
// with a parent cycle -- which a crash mid-write can leave -- must not spin.
func insomniaDescendsFrom(records map[string]insomniaRecord, record insomniaRecord, workspaceID string) bool {
	current := record
	for depth := 0; depth < 64; depth++ {
		if current.ParentID == workspaceID {
			return true
		}
		parent, ok := records[current.ParentID]
		if !ok {
			return false
		}
		current = parent
	}
	return false
}

// stripInsomniaSecrets empties credential-shaped values in place, and reports
// whether it emptied any. The field is kept so the request still shows that it
// uses bearer auth; only the value goes.
func stripInsomniaSecrets(resource map[string]interface{}) bool {
	authentication, ok := resource["authentication"].(map[string]interface{})
	if !ok {
		return false
	}
	blanked := false
	for _, field := range insomniaSecretFields {
		value, present := authentication[field]
		if !present {
			continue
		}
		if text, isText := value.(string); !isText || strings.TrimSpace(text) == "" {
			continue
		}
		authentication[field] = ""
		blanked = true
	}
	return blanked
}
