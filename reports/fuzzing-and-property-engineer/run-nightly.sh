#!/usr/bin/env bash
# MuxMaster nightly fuzz pipeline — 8h aggregate per target.
# Run from any directory. Logs to evidence/<date>/.
# Usage: ./run-nightly.sh [fuzztime_per_target]
# Default: 8h per target (sequential)

set -euo pipefail

HDIR="$(cd "$(dirname "$0")" && pwd)"
DATE="$(date +%Y-%m-%d)"
EVIDENCE_DIR="$HDIR/evidence/$DATE"
CORPUS_DIR="$HDIR/corpora"
HARNESS_DIR="$HDIR/harness"
FUZZTIME="${1:-8h}"

mkdir -p "$EVIDENCE_DIR"
cd "$HARNESS_DIR"

run_fuzz() {
    local target="$1"
    local cachedir="$CORPUS_DIR/$target"
    mkdir -p "$cachedir"
    echo "[$target] start: $(date -Iseconds)"
    go test -run=- \
        -fuzz="^${target}$" \
        -fuzztime="$FUZZTIME" \
        -fuzzminimizetime=30s \
        -test.fuzzcachedir="$cachedir" \
        > "$EVIDENCE_DIR/fuzz-${target}.txt" 2>&1 && echo "[$target] PASS" || echo "[$target] FAIL — see $EVIDENCE_DIR/fuzz-${target}.txt"
    echo "[$target] end: $(date -Iseconds)"
}

# ── Core API ─────────────────────────────────────────────────────────────────
run_fuzz FuzzHandle
run_fuzz FuzzHandleFast
run_fuzz FuzzServeHTTP
run_fuzz FuzzServeHTTPAllMethods

# ── Params / context ─────────────────────────────────────────────────────────
run_fuzz FuzzByName
run_fuzz FuzzParamsFromContext
run_fuzz FuzzParamsRoundtrip
run_fuzz FuzzParamsOverflow

# ── Group / Mount ─────────────────────────────────────────────────────────────
run_fuzz FuzzGroup
run_fuzz FuzzMount
run_fuzz FuzzGroupDepthNoPanic

# ── Middleware ────────────────────────────────────────────────────────────────
run_fuzz FuzzLoggerNoPanic
run_fuzz FuzzRecovererSanitiser

# ── JWT (including S8 targets: H8-42, H8-43, H8-63, H8-64) ─────────────────
run_fuzz FuzzJWTAuth
run_fuzz FuzzJWTAlgConfusion
run_fuzz FuzzJWTAudClaim
run_fuzz FuzzJWTAlgNone
run_fuzz FuzzJWTParserNoPanic
run_fuzz FuzzJWTKidInjection
run_fuzz FuzzJWTHeaderArbitrary

# ── CORS ─────────────────────────────────────────────────────────────────────
run_fuzz FuzzCORS
run_fuzz FuzzCORSPreflight
run_fuzz FuzzCORSCredentials
run_fuzz FuzzCORSOrigin

# ── OAuth2 ───────────────────────────────────────────────────────────────────
run_fuzz FuzzOAuth2Authorization
run_fuzz FuzzOAuth2IntrospectionResponse
run_fuzz FuzzOAuth2CacheEviction

# ── Regex params (H8-52) ─────────────────────────────────────────────────────
run_fuzz FuzzRegexParamRegistration
run_fuzz FuzzRegexParamServeHTTP

# ── RawPath matrix (H8-27) ───────────────────────────────────────────────────
run_fuzz FuzzRawPathMatrixNoPanic

# ── Case-fold UTF-8 (H8-23) ──────────────────────────────────────────────────
run_fuzz FuzzCaseFoldUTF8NoPanic

# ── Rebuild concurrent (H8-70/71) ────────────────────────────────────────────
run_fuzz FuzzRebuildConcurrent

# ── RealIP ───────────────────────────────────────────────────────────────────
run_fuzz FuzzRealIPNoPanic
run_fuzz FuzzRealIPBypassCheck
run_fuzz FuzzRealIPZoneID

# ── Walk on corrupted tree ───────────────────────────────────────────────────
run_fuzz FuzzWalkCorrupted

# ── Property tests (run in non-fuzz mode; high iteration count) ──────────────
echo "[PropertyTests] start: $(date -Iseconds)"
go test -run '^TestProp_' -count=5 -rapid.checks=1000 \
    > "$EVIDENCE_DIR/property-tests.txt" 2>&1 \
    && echo "[PropertyTests] PASS" || echo "[PropertyTests] FAIL"
echo "[PropertyTests] end: $(date -Iseconds)"

echo "Nightly run complete. Evidence at: $EVIDENCE_DIR"
