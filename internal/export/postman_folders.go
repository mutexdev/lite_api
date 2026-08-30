package export

// The folder tree a Postman export walks.
//
// The export used to be driven by collection.Folders alone: it asked for the
// child folders of a path, then for the requests at that path, and recursed.
// Any request whose FolderPath named a folder with no FolderConfig row — or
// whose intermediate parent had none — was therefore visited from nowhere. It
// was emitted nowhere, counted nowhere, and reported nowhere: the export
// succeeded, the summary said what the user expected, and the requests were
// simply not in the file.
//
// That is not a hypothetical shape. A partial import (US-051 selection lists)
// filters Items and Folders through INDEPENDENT lists, so keeping a request
// while dropping its folder produces exactly it; so does a FolderConfig whose
// Path and DisplayPath differ, because the old walk matched DisplayPath only
// while items carry whichever of the two the store wrote.
//
// The tree here is therefore built from the UNION of every FolderConfig and
// every item FolderPath, synthesising a node for any segment that has no
// config. Every item lands on exactly one node, and a node exists for every
// path any item names.

import (
	"sort"
	"strings"

	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

type postmanFolderNode struct {
	path    string
	name    string
	config  *types.FolderConfig
	folders []*postmanFolderNode
	items   []types.RequestItem
	order   int
}

func (n *postmanFolderNode) seq() int {
	if n.config == nil {
		return 0
	}
	return n.config.Seq
}

func (n *postmanFolderNode) label() string {
	if n.config != nil {
		return scalar.FirstNonEmpty(n.config.Name, n.name)
	}
	return n.name
}

// postmanFolderTree maps every path spelling seen in a collection onto one
// canonical node. aliases is what makes a FolderConfig{Path: "users",
// DisplayPath: "Users"} and an item at "users" meet on the same node.
type postmanFolderTree struct {
	root    *postmanFolderNode
	nodes   map[string]*postmanFolderNode
	aliases map[string]string
	created int
}

func newPostmanFolderTree() *postmanFolderTree {
	root := &postmanFolderNode{}
	return &postmanFolderTree{
		root:    root,
		nodes:   map[string]*postmanFolderNode{"": root},
		aliases: map[string]string{"": ""},
	}
}

// ensure resolves a raw folder path onto its node, creating the node and every
// missing ancestor. Resolution is segment-wise through aliases, so an item at
// "users/admins" reaches "Users/admins" when only "users" has been aliased.
func (t *postmanFolderTree) ensure(raw string) *postmanFolderNode {
	key := types.NormalizeFolderPathKey(raw)
	if key == "" {
		return t.root
	}
	if canonical, ok := t.aliases[key]; ok {
		return t.nodes[canonical]
	}
	parent := t.ensure(types.ParentFolderDisplayPath(key))
	segment := key
	if index := strings.LastIndex(key, "/"); index >= 0 {
		segment = key[index+1:]
	}
	canonical := segment
	if parent.path != "" {
		canonical = parent.path + "/" + segment
	}
	node, ok := t.nodes[canonical]
	if !ok {
		t.created++
		node = &postmanFolderNode{path: canonical, name: segment, order: t.created}
		t.nodes[canonical] = node
		parent.folders = append(parent.folders, node)
	}
	t.aliases[key] = canonical
	return node
}

func (t *postmanFolderTree) count() int {
	return len(t.nodes) - 1
}

func buildPostmanFolderTree(collection types.Collection) *postmanFolderTree {
	tree := newPostmanFolderTree()

	// Shallowest first, so a nested folder always resolves against a parent
	// whose alias is already registered.
	folders := append([]types.FolderConfig(nil), collection.Folders...)
	sort.SliceStable(folders, func(i, j int) bool {
		return postmanFolderDepth(folders[i]) < postmanFolderDepth(folders[j])
	})
	for _, folder := range folders {
		displayKey := types.NormalizeFolderPathKey(scalar.FirstNonEmpty(folder.DisplayPath, folder.Path))
		if displayKey == "" {
			continue
		}
		node := tree.ensure(displayKey)
		if node.config == nil {
			config := folder
			if config.DisplayPath == "" {
				config.DisplayPath = displayKey
			}
			node.config = &config
		}
		// A request stored under the folder's on-disk Path must reach the same
		// node as one stored under its DisplayPath.
		if pathKey := types.NormalizeFolderPathKey(folder.Path); pathKey != "" {
			if _, exists := tree.aliases[pathKey]; !exists {
				tree.aliases[pathKey] = node.path
			}
		}
	}

	for _, item := range collection.Items {
		if item.Transient {
			continue
		}
		node := tree.ensure(item.FolderPath)
		node.items = append(node.items, item)
	}

	sortPostmanFolderNode(tree.root)
	return tree
}

func postmanFolderDepth(folder types.FolderConfig) int {
	key := types.NormalizeFolderPathKey(scalar.FirstNonEmpty(folder.DisplayPath, folder.Path))
	if key == "" {
		return 0
	}
	return strings.Count(key, "/") + 1
}

// sortPostmanFolderNode reproduces the ordering the docs walk used: folders by
// Bruno sequence then name, requests by sequence then name. A synthesised
// folder has no sequence and sorts by name after the configured ones, which is
// where SortFoldersLikeBruno puts a folder with Seq 0 too.
func sortPostmanFolderNode(node *postmanFolderNode) {
	sort.SliceStable(node.folders, func(i, j int) bool {
		left, right := node.folders[i], node.folders[j]
		leftValid, rightValid := left.seq() > 0, right.seq() > 0
		if leftValid && rightValid && left.seq() != right.seq() {
			return left.seq() < right.seq()
		}
		if leftValid != rightValid {
			return leftValid
		}
		return strings.ToLower(left.label()) < strings.ToLower(right.label())
	})
	sort.SliceStable(node.items, func(i, j int) bool {
		if node.items[i].Seq != node.items[j].Seq {
			return node.items[i].Seq < node.items[j].Seq
		}
		return strings.ToLower(node.items[i].Name) < strings.ToLower(node.items[j].Name)
	})
	for _, child := range node.folders {
		sortPostmanFolderNode(child)
	}
}
