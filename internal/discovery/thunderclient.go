// Reading Thunder Client's collections (US-062).
//
// Thunder Client is a VS Code extension, so its data sits in the editor's
// global storage as plain JSON -- no database, no lock, no encryption. It is
// the least effortful client to support and, for anyone who has been using VS
// Code rather than a standalone app, the one most likely to be there.
//
// The output is a Postman v2.1 collection rather than a bespoke conversion into
// this app's own types. That is deliberate: the Postman importer already knows
// how to read folders, header rows, body modes, auth and saved examples, and
// has been taught to tolerate every odd shape a real export contains. A second
// converter would start out simpler and drift.
package discovery

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

type thunderCollectionFile struct {
	ID      string `json:"_id"`
	Name    string `json:"colName"`
	Folders []struct {
		ID          string `json:"_id"`
		Name        string `json:"name"`
		ContainerID string `json:"containerId"`
	} `json:"folders"`
}

type thunderRequestFile struct {
	ID           string `json:"_id"`
	CollectionID string `json:"colId"`
	ContainerID  string `json:"containerId"`
	Name         string `json:"name"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	SortNum      int    `json:"sortNum"`
	Headers      []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Params []struct {
		Name       string `json:"name"`
		Value      string `json:"value"`
		IsPath     bool   `json:"isPath"`
		IsDisabled bool   `json:"isDisabled"`
	} `json:"params"`
	Body struct {
		Type string `json:"type"`
		Raw  string `json:"raw"`
		Form []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"form"`
	} `json:"body"`
}

func readThunderClientCollections(directory string) ([]Discovered, error) {
	collections := []thunderCollectionFile{}
	requests := []thunderRequestFile{}
	// Three layouts have shipped. The .db file is JSON despite its extension,
	// which is exactly the sort of thing that gets skipped by a glob written
	// from an assumption.
	for _, name := range []string{"thunderCollection.json", "ThunderCollection.db", "thunderCollection.db"} {
		if data, err := readBoundedFile(filepath.Join(directory, name)); err == nil {
			var parsed []thunderCollectionFile
			if json.Unmarshal(data, &parsed) == nil {
				collections = append(collections, parsed...)
			}
		}
	}
	for _, name := range []string{"thunderclient.json", "thunderClient.json", "ThunderRequest.db", "thunderActivity.json"} {
		if data, err := readBoundedFile(filepath.Join(directory, name)); err == nil {
			var parsed []thunderRequestFile
			if json.Unmarshal(data, &parsed) == nil {
				requests = append(requests, parsed...)
			}
		}
	}
	sort.SliceStable(collections, func(left, right int) bool { return collections[left].ID < collections[right].ID })

	discovered := make([]Discovered, 0, len(collections))
	for _, collection := range collections {
		owned := []thunderRequestFile{}
		for _, request := range requests {
			if request.CollectionID == collection.ID {
				owned = append(owned, request)
			}
		}
		sort.SliceStable(owned, func(left, right int) bool { return owned[left].SortNum < owned[right].SortNum })
		content, err := json.Marshal(postmanDocumentFromThunderClient(collection, owned))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(collection.Name)
		if name == "" {
			name = "Thunder Client collection"
		}
		discovered = append(discovered, Discovered{
			Client:       ClientThunderClient,
			Name:         name,
			Content:      string(content),
			Kind:         "postman",
			RequestCount: len(owned),
		})
	}
	return discovered, nil
}

// postmanDocumentFromThunderClient builds the v2.1 document. Folders become
// nested items, which is how Postman expresses them and therefore how the
// importer already reads them.
func postmanDocumentFromThunderClient(collection thunderCollectionFile, requests []thunderRequestFile) map[string]interface{} {
	itemsByContainer := map[string][]interface{}{}
	for _, request := range requests {
		itemsByContainer[request.ContainerID] = append(itemsByContainer[request.ContainerID], postmanItemFromThunderRequest(request))
	}
	items := append([]interface{}{}, itemsByContainer[""]...)
	for _, folder := range collection.Folders {
		// Only top-level folders are emitted as folders; Thunder Client nests
		// by containerId, and a folder inside a folder carries its parent's id
		// here, which the same lookup handles.
		if folder.ContainerID != "" {
			continue
		}
		items = append(items, map[string]interface{}{
			"name": folder.Name,
			"item": append([]interface{}{}, itemsByContainer[folder.ID]...),
		})
	}
	return map[string]interface{}{
		"info": map[string]interface{}{
			"name":   collection.Name,
			"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		"item": items,
	}
}

func postmanItemFromThunderRequest(request thunderRequestFile) map[string]interface{} {
	headers := make([]interface{}, 0, len(request.Headers))
	for _, header := range request.Headers {
		headers = append(headers, map[string]interface{}{"key": header.Name, "value": header.Value})
	}
	query := make([]interface{}, 0, len(request.Params))
	for _, param := range request.Params {
		if param.IsPath {
			continue
		}
		query = append(query, map[string]interface{}{"key": param.Name, "value": param.Value, "disabled": param.IsDisabled})
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "GET"
	}
	document := map[string]interface{}{
		"method": method,
		"url":    map[string]interface{}{"raw": request.URL, "query": query},
		"header": headers,
	}
	if body := postmanBodyFromThunderRequest(request); body != nil {
		document["body"] = body
	}
	return map[string]interface{}{"name": request.Name, "request": document}
}

func postmanBodyFromThunderRequest(request thunderRequestFile) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(request.Body.Type)) {
	case "json":
		return map[string]interface{}{
			"mode":    "raw",
			"raw":     request.Body.Raw,
			"options": map[string]interface{}{"raw": map[string]interface{}{"language": "json"}},
		}
	case "xml":
		return map[string]interface{}{
			"mode":    "raw",
			"raw":     request.Body.Raw,
			"options": map[string]interface{}{"raw": map[string]interface{}{"language": "xml"}},
		}
	case "text":
		return map[string]interface{}{"mode": "raw", "raw": request.Body.Raw}
	case "formencoded", "form":
		rows := make([]interface{}, 0, len(request.Body.Form))
		for _, entry := range request.Body.Form {
			rows = append(rows, map[string]interface{}{"key": entry.Name, "value": entry.Value})
		}
		return map[string]interface{}{"mode": "urlencoded", "urlencoded": rows}
	}
	if strings.TrimSpace(request.Body.Raw) != "" {
		return map[string]interface{}{"mode": "raw", "raw": request.Body.Raw}
	}
	return nil
}
