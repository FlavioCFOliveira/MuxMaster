//go:build !race

// Package harness — race-build detection (non-race variant).
//
// DOS-2026-0060 (closed-task audit #285, row #171): the Logger
// sanitiseForLog CPU-complexity test needs a per-byte cost threshold that
// is valid both in a normal build and under `go test -race`. Race
// instrumentation adds roughly an order of magnitude of overhead to every
// memory access (measured: 4.579 ns/byte without -race vs 63 ns/byte with
// -race for the same workload), so a single hard-coded threshold either
// false-fails under -race or has to be so loose it stops catching a real
// regression without -race.
//
// This pair of build-tagged files (mirroring the standard-library idiom
// used to detect the race build, e.g. runtime/race.go / race0.go)
// exposes that fact as a compile-time constant the test can branch on.
package harness

// raceBuild is true when this test binary was compiled with -race.
const raceBuild = false
