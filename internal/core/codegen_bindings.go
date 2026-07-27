// The Wails-bound side of code generation.
//
// These stayed with the App when internal/codegen was extracted: they are
// methods on *App, which is what Wails binds, and a method cannot move to
// another package without its receiver.
package core

import "github.com/mutexdev/lite_api/internal/codegen"

func (a *App) CodeGenerationTargets() []codegen.Target {
	out := []codegen.Target{
		{ID: "curl", Label: "cURL"},
		{ID: "fetch", Label: "JavaScript (fetch)"},
	}
	for _, target := range codegen.Languages {
		out = append(out, codegen.Target{ID: target.ID, Label: target.Label})
	}
	return out
}
