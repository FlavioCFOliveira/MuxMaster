//go:build ignore
// +build ignore

package semgreptest

import (
	"bytes"
	"crypto/subtle"
)

// Test cases for muxmaster-non-constant-time-compare rule.
// Usage: semgrep --test reports/go-sast-and-memory-auditor/semgrep-rules/

// POSITIVE TESTS: Should flag these as violations

// bytes.Equal on secrets — classic non-constant-time operation
func testBytesEqualSecret(secret1, secret2 []byte) bool {
	// ruleid: muxmaster-non-constant-time-compare
	return bytes.Equal(secret1, secret2)
}

// bytes.Equal on hashes (another form of secrets)
func comparePasswords(hash1, hash2 []byte) bool {
	// ruleid: muxmaster-non-constant-time-compare
	return bytes.Equal(hash1, hash2)
}

// NEGATIVE TESTS: Should NOT flag these (legitimate checks)

// nil check — not a secret comparison
func testNilCheck(secret []byte) bool {
	// ok: muxmaster-non-constant-time-compare
	return secret == nil
}

// empty string check — not a secret
func testEmptyString(value string) bool {
	// ok: muxmaster-non-constant-time-compare
	return value == ""
}

// zero check — not a secret
func testZeroCheck(count int) bool {
	// ok: muxmaster-non-constant-time-compare
	return count == 0
}

// len check — configuration validation, not secret comparison
func testLengthCheck(secret []byte) bool {
	// ok: muxmaster-non-constant-time-compare
	return len(secret) == 32
}

// Correct way: using ConstantTimeCompare (should not flag)
func BasicAuthCorrect(supplied, stored []byte) bool {
	// ok: muxmaster-non-constant-time-compare
	return subtle.ConstantTimeCompare(supplied, stored) == 1
}

// error check from RSA verification — not a secret comparison
func verifySignatureRSA(result error) bool {
	// ok: muxmaster-non-constant-time-compare
	return result == nil
}

// Configuration validation in Auth function (nil check — not a secret)
func AuthSetup(config interface{}) bool {
	// ok: muxmaster-non-constant-time-compare
	return config == nil
}

