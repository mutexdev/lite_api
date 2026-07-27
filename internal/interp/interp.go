// Package interp expands {{variable}} templates.
//
// US-069. It is the leaf that internal/transport and internal/grpcexec were
// both reaching back into the App package for -- each carried a settable
// interpolator variable purely because this code had not moved yet. With it
// here they import it directly and those seams are gone.
package interp

import (
	"os"
	"regexp"
	"strings"
)

// ProcessEnvPrefix marks a variable that resolves from the process environment
// rather than from the collection or environment.
const ProcessEnvPrefix = "process.env."

// processEnvInterpolationPattern matches a {{process.env.NAME}} reference.
var processEnvInterpolationPattern = regexp.MustCompile(`\{\{\s*process\.env\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

const maxPasses = 8

// interpolate replaces every `{{name}}` token in input with the matching value
// from vars, then resolves the dynamic `{{$timestamp}}` / `{{$isoTimestamp}}`
// tokens.
//
// A pass is a single left-to-right scan that writes into one strings.Builder,
// so the cost is O(len(input)) per pass rather than O(len(vars) * len(input)).
// Passes repeat only while the previous pass substituted a value that could
// have introduced a new token, which for ordinary values means a single scan.
//
// Tokens with no matching variable are left verbatim, and keys carrying the
// ProcessEnvPrefix are deliberately not matched here — they are owned
// by replaceProcessEnvVariables, which runs after the plain substitutions of
// each pass exactly as before.
func Interpolate(input string, vars map[string]string) string {
	out := input
	if strings.Contains(out, "{{") {
		scan := variableScan{vars: vars}
		for pass := 0; pass < maxPasses; pass++ {
			next, changed, mayNest := scan.expand(out)
			out = next
			// The pattern cannot match without this literal, so skipping the
			// regex (and the os.Environ() snapshot behind it) is unobservable.
			if strings.Contains(out, ProcessEnvPrefix) {
				if scan.processEnv == nil {
					scan.processEnv = ProcessEnvFromCombinedVars(vars)
				}
				if replaced := replaceProcessEnvVariables(out, scan.processEnv); replaced != out {
					out = replaced
					changed = true
					mayNest = true
				}
			}
			if !changed || !mayNest {
				break
			}
		}
	}
	// US-050. Was two hardcoded ReplaceAll calls; now the full Postman set,
	// resolved per OCCURRENCE rather than once per string. See
	// dynamic_variables.go for why that distinction matters.
	return InterpolateDynamicVariables(out)
}

// variableScan holds the per-call state of an interpolation: the variable map
// plus two side tables that are built lazily, because the common case never
// needs either of them.
type variableScan struct {
	vars       map[string]string
	processEnv map[string]string

	keyStatsDone bool
	bracedKeys   bool
	maxKeyLen    int
}

// value resolves the text between a `{{` and a `}}`. Keys carrying the
// process.env prefix are skipped so that replaceProcessEnvVariables stays the
// only thing that can resolve them.
func (s *variableScan) value(name string) (string, bool) {
	if strings.HasPrefix(name, ProcessEnvPrefix) {
		return "", false
	}
	value, ok := s.vars[name]
	return value, ok
}

// keyStats reports whether any key contains a brace, and the longest key
// length. Both are only consulted after a token has already failed to resolve,
// so a map of ordinary keys never pays for them.
func (s *variableScan) keyStats() (braced bool, maxLen int) {
	if !s.keyStatsDone {
		s.keyStatsDone = true
		for key := range s.vars {
			if len(key) > s.maxKeyLen {
				s.maxKeyLen = len(key)
			}
			if !s.bracedKeys && (strings.IndexByte(key, '{') >= 0 || strings.IndexByte(key, '}') >= 0) {
				s.bracedKeys = true
			}
		}
	}
	return s.bracedKeys, s.maxKeyLen
}

// token resolves the token opened by the `{{` at start. It returns the
// substitution and the offset just past the closing `}}`.
//
// The first `}}` is tried first, which is what a plain `{{`+key+`}}` literal
// search finds for an ordinary key. Only when some key actually contains a
// brace does it keep trying later closers, so that a key such as `a}}b` still
// matches — bounded by the longest key, so an unresolvable token cannot walk
// the rest of the string.
func (s *variableScan) token(input string, start int) (string, int, bool) {
	from := start + 2
	search := from
	for {
		rel := strings.Index(input[search:], "}}")
		if rel < 0 {
			return "", 0, false
		}
		end := search + rel
		if value, ok := s.value(input[from:end]); ok {
			return value, end + 2, true
		}
		braced, maxKeyLen := s.keyStats()
		if !braced || end-from >= maxKeyLen {
			return "", 0, false
		}
		search = end + 1
	}
}

// expand performs one substitution pass. It reports whether the output differs
// from the input, and whether a substituted value could have introduced a new
// token — an empty value can join two literals into one, and any brace in a
// value can complete a token with the text around it.
func (s *variableScan) expand(input string) (string, bool, bool) {
	var b strings.Builder
	copied := 0
	search := 0
	changed := false
	mayNest := false
	for {
		rel := strings.Index(input[search:], "{{")
		if rel < 0 {
			break
		}
		start := search + rel
		value, end, ok := s.token(input, start)
		if !ok {
			// No key matched at this `{{`; a nested one may still open a token
			// a byte later, as in `{{{a}}}`.
			search = start + 1
			continue
		}
		search = end
		if value == input[start:end] {
			continue
		}
		if !changed {
			changed = true
			b.Grow(len(input) + len(value) + 16)
		}
		b.WriteString(input[copied:start])
		b.WriteString(value)
		copied = end
		if value == "" || strings.IndexByte(value, '{') >= 0 || strings.IndexByte(value, '}') >= 0 {
			mayNest = true
		}
	}
	if !changed {
		return input, false, false
	}
	b.WriteString(input[copied:])
	return b.String(), true, mayNest
}

func replaceProcessEnvVariables(input string, processEnv map[string]string) string {
	return processEnvInterpolationPattern.ReplaceAllStringFunc(input, func(token string) string {
		matches := processEnvInterpolationPattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		if value, ok := processEnv[matches[1]]; ok {
			return value
		}
		return token
	})
}

func ProcessEnvFromCombinedVars(vars map[string]string) map[string]string {
	env := ScriptProcessEnv(nil)
	for key, value := range vars {
		if name, ok := strings.CutPrefix(key, ProcessEnvPrefix); ok && name != "" {
			env[name] = value
		}
	}
	return env
}

func ScriptProcessEnv(overrides map[string]string) map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			env[name] = value
		}
	}
	for name, value := range overrides {
		env[name] = value
	}
	return env
}
