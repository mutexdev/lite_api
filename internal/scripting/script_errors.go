package scripting

// Turning a goja failure into a sentence the user can act on.
//
// Three things stood between the runtime's error text and a usable message.
//
// The script is wrapped in an async IIFE before it runs, so every line number
// goja reports is offset from the source the user typed. Levels made that worse:
// the joined document the numbers pointed into existed nowhere on disk.
//
// goja stringifies a SyntaxError by prefixing the error type onto a message that
// already begins with it, so the user read "SyntaxError: SyntaxError:".
//
// And an assertion failure arrives as a GoError carrying the Go call stack:
// "GoError: expected 1 to equal 2 at github.com/mutexdev/lite_api/internal/
// scripting.expectMatch… (native)". The only part of that a script author can
// use is the first six words.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// scriptWrapperLineOffset is how many lines scriptAsyncWrapper puts above the
// user's first line. Kept beside the wrapper's own test so the two cannot drift.
const scriptWrapperLineOffset = 2

var (
	// " at github.com/…/scripting.expectMatch.func1 (native)" and the multi-line
	// stack form of the same.
	scriptNativeFrameExpr = regexp.MustCompile(`(?s)[\s\n]+at\s+\S+\s+\(native\)`)
	// " at <eval>:5:12(3)" — goja's runtime frame.
	scriptEvalFrameExpr = regexp.MustCompile(`[\s\n]+at\s+<eval>:(\d+):(\d+)(?:\([-\d]+\))?`)
	// " at 5:12" — the tail of a SyntaxError, which has no frame at all.
	scriptBareLocationExpr = regexp.MustCompile(`\s+at\s+(\d+):(\d+)\s*$`)
	// Leftover stack lines once the interesting frame has been taken.
	scriptResidualFrameExpr = regexp.MustCompile(`(?s)[\s\n]+at\s+\S+.*$`)
)

var scriptErrorTypeNames = []string{
	"SyntaxError", "TypeError", "ReferenceError", "RangeError",
	"EvalError", "URIError", "GoError", "Error",
}

// CleanScriptErrorMessage strips the Go call stack and the duplicated error-type
// prefix, leaving the message a script author wrote or would recognise.
func CleanScriptErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	text, _ := cleanScriptErrorText(err.Error())
	return text
}

// cleanScriptErrorText returns the cleaned message and the 1-based line number
// the runtime reported, or 0 when it reported none.
func cleanScriptErrorText(text string) (string, int) {
	text = scriptNativeFrameExpr.ReplaceAllString(text, "")

	line := 0
	if match := scriptEvalFrameExpr.FindStringSubmatch(text); match != nil {
		line, _ = strconv.Atoi(match[1])
		text = scriptEvalFrameExpr.ReplaceAllString(text, "")
	} else if match := scriptBareLocationExpr.FindStringSubmatch(text); match != nil {
		line, _ = strconv.Atoi(match[1])
		text = scriptBareLocationExpr.ReplaceAllString(text, "")
	}
	text = scriptResidualFrameExpr.ReplaceAllString(text, "")

	// "GoError: " names an implementation detail of the bridge, never anything
	// the script did.
	text = strings.TrimPrefix(strings.TrimSpace(text), "GoError: ")
	for _, name := range scriptErrorTypeNames {
		duplicate := name + ": " + name + ": "
		for strings.HasPrefix(text, duplicate) {
			text = strings.TrimPrefix(text, name+": ")
		}
	}
	return strings.TrimSpace(text), line
}

// ScriptLevelError names the script a failure came from and rewrites the
// runtime's line number into that script's own numbering.
//
// A user with a collection script, two folder scripts and a request script used
// to get "SyntaxError: SyntaxError: … at 41:7" for a document with no line 41.
// They now get the file and the line they can open.
func ScriptLevelError(err error, label string) error {
	if err == nil {
		return nil
	}
	message, line := cleanScriptErrorText(err.Error())
	if message == "" {
		message = err.Error()
	}
	label = strings.TrimSpace(label)
	userLine := line - scriptWrapperLineOffset
	switch {
	case label == "" && userLine < 1:
		return errors.New(message)
	case label == "":
		return fmt.Errorf("%s (line %d)", message, userLine)
	case userLine < 1:
		return fmt.Errorf("%s (%s)", message, label)
	default:
		return fmt.Errorf("%s (%s, line %d)", message, label, userLine)
	}
}
