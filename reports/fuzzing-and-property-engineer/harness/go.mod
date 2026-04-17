// Test-only module — isolated to preserve the zero-dep invariant of the
// main github.com/FlavioCFOliveira/MuxMaster module.
//
// This module lives outside the main go.mod tree intentionally. See the
// CLAUDE.md file and the pre-release sprint plan: production code must
// have zero external deps; property-test infrastructure may use rapid.
module github.com/FlavioCFOliveira/MuxMaster/reports/fuzzing-and-property-engineer/harness

go 1.26

require (
	github.com/FlavioCFOliveira/MuxMaster v0.0.0-00010101000000-000000000000
	pgregory.net/rapid v1.2.0
)

replace github.com/FlavioCFOliveira/MuxMaster => ../../../
