// Package gitignore reads and rewrites the block of .gitignore entries that
// LiteAPI manages on a workspace's behalf.
//
// Its own package because two packages edit that block: the collection writer,
// when a Git-backed collection is added or removed, and the recovery store,
// which snapshots and restores the file around a delete. A second copy of the
// marker strings in either place would silently orphan every entry between
// them.
package gitignore

import (
	"sort"
	"strings"
)

func Entries(content string) map[string]bool {
	entries := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# LiteAPI managed Git-backed collections":
			inBlock = true
			continue
		case "# End LiteAPI managed Git-backed collections":
			inBlock = false
			continue
		}
		if inBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			entries[trimmed] = true
		}
	}
	return entries
}

func ReplaceBlock(content string, entries map[string]bool) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# LiteAPI managed Git-backed collections" {
			inBlock = true
			continue
		}
		if trimmed == "# End LiteAPI managed Git-backed collections" {
			inBlock = false
			continue
		}
		if !inBlock {
			kept = append(kept, line)
		}
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	keys := make([]string, 0, len(entries))
	for entry := range entries {
		keys = append(keys, entry)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, "# LiteAPI managed Git-backed collections")
		kept = append(kept, keys...)
		kept = append(kept, "# End LiteAPI managed Git-backed collections")
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}
