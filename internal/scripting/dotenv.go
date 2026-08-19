package scripting

// Reading .env files and the process-env view a script sees.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"
)

func DotEnvFilesForContext(workspace *types.Workspace, collection *types.Collection) ([]types.DotEnvFile, error) {
	files := []types.DotEnvFile{}
	if workspace != nil {
		workspaceFiles, err := dotEnvFilesInScope("workspace", workspace.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, workspaceFiles...)
	}
	if collection != nil {
		collectionFiles, err := dotEnvFilesInScope("collection", collection.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, collectionFiles...)
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Scope != files[j].Scope {
			return files[i].Scope == "workspace"
		}
		if files[i].Runtime != files[j].Runtime {
			return files[i].Runtime
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return files, nil
}

func NormalizeDotEnvFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !isDotEnvFilename(name) || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", errors.New(".env file name must be .env or .env.<name>")
	}
	return name, nil
}

func isDotEnvFilename(name string) bool {
	return dotEnvFilenamePattern.MatchString(name)
}

func readDotEnvFile(path string) map[string]string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseDotEnv(string(data))
}

func parseDotEnv(content string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		values[name] = parseDotEnvValue(raw)
	}
	return values
}

func parseDotEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if value[0] == '"' {
		parsed, ok := parseQuotedDotEnvValue(value, '"')
		if ok {
			return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\"`, `"`, `\\`, `\`).Replace(parsed)
		}
	}
	if value[0] == '\'' {
		if parsed, ok := parseQuotedDotEnvValue(value, '\''); ok {
			return parsed
		}
	}
	if index := strings.IndexByte(value, '#'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}

func parseQuotedDotEnvValue(value string, quote byte) (string, bool) {
	if len(value) < 2 || value[0] != quote {
		return "", false
	}
	for index := 1; index < len(value); index++ {
		if value[index] == quote && value[index-1] != '\\' {
			return value[1:index], true
		}
	}
	return "", false
}
