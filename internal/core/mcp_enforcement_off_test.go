//go:build !mcpenforcement

package core

// The switch that lets an enforcement-dependent test COMPILE NOW AND SKIP.
//
// WHY A CONSTANT AND NOT A BUILD-TAGGED TEST FILE. A test excluded by a build
// tag is not compiled, so it rots: a signature it calls can change, a helper it
// uses can disappear, and nothing says so until someone flips the tag months
// later and finds a file that no longer builds. A constant keeps the test in
// every ordinary `go test` run — type-checked, its fixtures exercised up to the
// skip — while honestly reporting that the property is not measurable yet.
//
// WHAT IS NOT MEASURABLE YET. This wave BUILDS the destination policy and
// attaches it to the run's context; the engine checkpoints and the guard
// transport that consult it land in the next wave, and the shipped host guard is
// still the enforcing boundary. A test that asserted "the policy blocked this
// send" would therefore be asserting something no code does.
//
// HOW TO RUN THEM ANYWAY: `go test -tags mcpenforcement ./internal/core/...`.
//
// TO RETIRE THIS: the wave that flips strict deletes this file, its
// mcpenforcement twin, and every `if !mcpEnforcementWired { t.Skip(...) }`.
const mcpEnforcementWired = false
