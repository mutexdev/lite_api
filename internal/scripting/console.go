package scripting

// The sandbox console object and the script log it appends to.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"strings"

	"github.com/mutexdev/lite_api/internal/types"

	"github.com/dop251/goja"
)

func selectedScriptLogs(logs []*[]types.ScriptLog) *[]types.ScriptLog {
	if len(logs) == 0 {
		return nil
	}
	return logs[0]
}

func newScriptConsoleObject(runtime *goja.Runtime, logs *[]types.ScriptLog) *goja.Object {
	console := runtime.NewObject()
	for _, level := range []string{"log", "debug", "info", "warn", "error"} {
		level := level
		_ = console.Set(level, func(call goja.FunctionCall) goja.Value {
			appendScriptLog(logs, level, call.Arguments)
			return goja.Undefined()
		})
	}
	return console
}

func appendScriptLog(logs *[]types.ScriptLog, level string, values []goja.Value) {
	if logs == nil {
		return
	}
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, scriptLogValueString(value))
	}
	*logs = append(*logs, types.ScriptLog{
		Level:   level,
		Message: strings.Join(args, " "),
		Args:    args,
	})
}
