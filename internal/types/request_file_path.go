// Where a request's file goes inside its collection directory.
//
// Shared by the collection writer and the export builders: an export lays the
// same files out in a zip that a save lays out on disk, so both must agree on
// the name a request gets and on how a duplicate is disambiguated. Two copies
// would export a collection that no longer round-trips.
package types

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
)

func EnsureRequestFilePaths(collection *Collection, defaultExt string) {
	used := map[string]bool{}
	for i := range collection.Items {
		target := ""
		if scalar.PathInside(collection.Path, collection.Items[i].FilePath) {
			target = filepath.Clean(collection.Items[i].FilePath)
			if used[target] {
				target = ""
			}
		}
		if target == "" {
			target = uniqueRequestFilePath(*collection, collection.Items[i], defaultExt, used)
		}
		collection.Items[i].FilePath = target
		used[target] = true
	}
}

func uniqueRequestFilePath(collection Collection, item RequestItem, defaultExt string, used map[string]bool) string {
	filename := scalar.SanitizeFilename(item.Name)
	if filename == "" {
		filename = item.ID
	}
	folder := filepath.Clean(collection.Path)
	if strings.TrimSpace(item.FolderPath) != "" {
		folder = filepath.Join(folder, filepath.FromSlash(item.FolderPath))
	}
	for index := 0; ; index++ {
		candidateName := filename
		if index > 0 {
			candidateName = fmt.Sprintf("%s %d", filename, index+1)
		}
		candidate := filepath.Clean(filepath.Join(folder, candidateName+defaultExt))
		if !used[candidate] {
			return candidate
		}
	}
}
