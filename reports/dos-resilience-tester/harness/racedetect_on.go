//go:build race

// Package harness — race-build detection (race variant).
//
// See racedetect_off.go for the rationale.
package harness

// raceBuild is true when this test binary was compiled with -race.
const raceBuild = true
