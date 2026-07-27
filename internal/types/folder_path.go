// Folder path keys and Bruno-compatible folder ordering.
//
// These operate on this package's own FolderConfig and on the slash-separated
// path key that identifies a folder. They lived in the application package —
// two of them inside the DOCS builder, which is not where anyone would look —
// while six other files reached for them. Here they are beside the type they
// describe, and the export builders can use them without importing the
// application.
package types

import (
	"path/filepath"
	"sort"
	"strings"
)

func NormalizeFolderPathKey(value string) string {
	value = strings.TrimSpace(filepath.ToSlash(value))
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return value
	}
	return cleaned
}

func SortFoldersLikeBruno(folders []FolderConfig) {
	sort.SliceStable(folders, func(i, j int) bool {
		leftValid := folders[i].Seq > 0
		rightValid := folders[j].Seq > 0
		if leftValid && rightValid && folders[i].Seq != folders[j].Seq {
			return folders[i].Seq < folders[j].Seq
		}
		if leftValid != rightValid {
			return leftValid
		}
		return strings.ToLower(firstNonEmpty(folders[i].Name, folders[i].DisplayPath, folders[i].Path)) < strings.ToLower(firstNonEmpty(folders[j].Name, folders[j].DisplayPath, folders[j].Path))
	})
}

func ParentFolderDisplayPath(path string) string {
	path = NormalizeFolderPathKey(path)
	parent := filepath.ToSlash(filepath.Dir(path))
	if parent == "." {
		return ""
	}
	return parent
}
