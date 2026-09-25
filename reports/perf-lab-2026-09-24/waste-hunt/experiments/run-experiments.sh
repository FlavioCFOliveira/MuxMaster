#!/usr/bin/env bash
# A/B experiments for root-package waste candidates (rmp #249).
#
# For every experiments/<ID>.diff:
#   1. copy the module (sources only) twice into a scratch dir: base/ and <ID>/;
#      apply the diff to <ID>/ with `patch -p1` (the repository is untouched);
#   2. run the project's full test suite on the patched copy — an experiment
#      whose tests fail is reported as such and its numbers are not used;
#   3. run the bench/ benchmarks named in <ID>.bench against base and patched,
#      INTERLEAVED (base, patched, base, patched, ...) COUNT times, then
#      benchstat base vs patched.
#
# Usage (normally called by ../run.sh experiments):
#   run-experiments.sh <module-root> <scratch-dir> <results-dir> <count>
set -euo pipefail
export LC_ALL=C

ROOT="$1"; WORK="$2"; RES="$3"; COUNT="$4"
HERE="$(cd "$(dirname "$0")" && pwd)"
BENCHSRC="$HERE/../bench"
mkdir -p "$RES"

copy_module() { # <dst>
  mkdir -p "$1"
  (cd "$ROOT" && tar --exclude=./.git --exclude=./reports --exclude=./examples \
      --exclude=./competitor --exclude=./.claude --exclude='*.test' -cf - .) | (cd "$1" && tar -xf -)
}

prepare_bench() { # <dst> <module-copy>
  mkdir -p "$1"
  cp "$BENCHSRC"/*.go "$BENCHSRC/go.mod" "$1/"
  (cd "$1" && go mod edit -replace "github.com/FlavioCFOliveira/MuxMaster=$2")
}

rm -rf "$WORK/exp"
copy_module "$WORK/exp/base"
prepare_bench "$WORK/exp/bench-base" "$WORK/exp/base"

for diff in "$HERE"/*.diff; do
  id="$(basename "$diff" .diff)"
  regex="$(head -n1 "$HERE/$id.bench")"
  echo "== experiment $id (bench: $regex) =="
  copy_module "$WORK/exp/$id"
  (cd "$WORK/exp/$id" && patch -s -p1 < "$diff")
  if (cd "$WORK/exp/$id" && go vet ./... && go test -count=1 ./...) > "$RES/$id-tests.txt" 2>&1; then
    echo "tests: PASS (full suite on patched copy)" | tee -a "$RES/$id-tests.txt"
  else
    echo "tests: FAIL — see $RES/$id-tests.txt" | tee -a "$RES/$id-tests.txt"
  fi
  prepare_bench "$WORK/exp/bench-$id" "$WORK/exp/$id"
  (cd "$WORK/exp/bench-base" && go test -c -o "$WORK/exp/base.test" .)
  (cd "$WORK/exp/bench-$id" && go test -c -o "$WORK/exp/$id.test" .)
  : > "$RES/$id-base.txt"; : > "$RES/$id-patched.txt"
  for _ in $(seq 1 "$COUNT"); do
    (cd "$WORK/exp/bench-base" && "$WORK/exp/base.test" -test.run '^$' -test.bench "$regex" -test.benchmem -test.count 1) >> "$RES/$id-base.txt"
    (cd "$WORK/exp/bench-$id" && "$WORK/exp/$id.test" -test.run '^$' -test.bench "$regex" -test.benchmem -test.count 1) >> "$RES/$id-patched.txt"
  done
  if command -v benchstat >/dev/null; then
    benchstat base="$RES/$id-base.txt" patched="$RES/$id-patched.txt" | tee "$RES/$id-benchstat.txt"
  fi
done
