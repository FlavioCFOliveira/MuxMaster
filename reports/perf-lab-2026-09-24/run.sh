#!/usr/bin/env bash
# Contention-hunt reproduction script for rmp task #243 (sprint 18).
#
# Produces:
#   reports/perf-lab-2026-09-24/results/scaling-cpu{1,2,4,8,16}.txt   (raw benchstat input)
#   reports/perf-lab-2026-09-24/results/scaling-benchstat.txt         (cross-cpu comparison)
#   reports/perf-lab-2026-09-24/profiles/*.pb.gz                      (mutex/block/cpu/mem profiles)
#   reports/perf-lab-2026-09-24/results/loadgen-*.txt                 (real-socket load test)
#
# Usage: ./run.sh            (full sweep — several minutes)
#        ./run.sh quick       (benchtime=50x, count=3 — smoke run, ~30s)
set -euo pipefail
cd "$(dirname "$0")"

MODE="${1:-full}"
if [[ "$MODE" == "quick" ]]; then
  BENCHTIME="50x"
  COUNT=3
else
  BENCHTIME="200ms"
  COUNT=6
fi

mkdir -p results profiles

echo "== ulimit -n: $(ulimit -n) (hard: $(ulimit -Hn)) =="
echo "== nproc: $(nproc) =="
go version

echo
echo "== 1. Scaling sweep (-cpu 1,2,4,8,16), benchtime=$BENCHTIME count=$COUNT =="
for cpu in 1 2 4 8 16; do
  echo "-- cpu=$cpu --"
  (cd harness && go test -run xxx -bench . -benchmem -cpu="$cpu" -benchtime="$BENCHTIME" -count="$COUNT" .) \
    | tee "results/scaling-cpu${cpu}.txt"
done

echo
echo "== 2. benchstat: cpu1 vs cpu16 =="
if command -v benchstat >/dev/null 2>&1; then
  benchstat results/scaling-cpu1.txt results/scaling-cpu16.txt | tee results/scaling-benchstat.txt
else
  echo "benchstat not on PATH — skipping (raw files are in results/)" | tee results/scaling-benchstat.txt
fi

echo
echo "== 3. Mutex + block + cpu + mem profiles (cpu=16, the contended regime) =="
(cd harness && go test -run xxx -bench . -benchmem -cpu=16 -benchtime=300ms -count=1 \
  -mutexprofile=../profiles/mutex.pb.gz -mutexprofilefraction=1 \
  -blockprofile=../profiles/block.pb.gz \
  -cpuprofile=../profiles/cpu.pb.gz \
  -memprofile=../profiles/mem.pb.gz \
  -o ../profiles/harness.test \
  .) | tee results/profiled-run.txt
rm -f profiles/harness.test

echo
echo "== 4. go tool trace summary (param routes + stateful middlewares) =="
(cd harness && go test -run xxx -bench 'BenchmarkParallelParam1$|BenchmarkThrottlePerIPSingleKey$|BenchmarkOAuth2ManyTokensNoCacheHit$|BenchmarkCompressParallel$|BenchmarkJWTAuthHS256Parallel$' \
  -cpu=16 -benchtime=100ms -trace=../profiles/trace.out -o ../profiles/trace.test .)
rm -f profiles/trace.test
echo "trace written to profiles/trace.out (open with: go tool trace profiles/trace.out)"

echo
echo "== 5. Real-socket load harness (net/http server + Go load generator) =="
(cd loadgen && go run . -conns=1000 -duration=5s | tee ../results/loadgen-1000.txt)
(cd loadgen && go run . -conns=5000 -duration=5s | tee ../results/loadgen-5000.txt)
(cd loadgen && go run . -conns=10000 -duration=5s | tee ../results/loadgen-10000.txt) || \
  echo "10000-connection run failed or was skipped (see ulimit -n above)" | tee ../results/loadgen-10000.txt

echo
echo "Done. See results/ and profiles/ and contention-hunt.md."
