#!/usr/bin/env bash
# Waste-hunt reproduction script — rmp task #249, sprint 18.
#
# ONE command re-runs the whole campaign:
#
#     reports/perf-lab-2026-09-24/waste-hunt/run.sh
#
# Phases (each can also be run alone: ./run.sh examples|attribute|strace|bench|escape|experiments):
#   examples     build every example from a TEMPORARY copy (examples/ is never
#                modified), inject a private pprof listener (probe/), drive it
#                with loadgen/ + scenarios/, capture CPU + alloc profiles.
#   attribute    (re-)run the nearest-owner attribution on the saved profiles.
#   strace       syscall accounting per example: loaded run minus idle run,
#                both under `strace -f -c` (ptrace_scope=1 forces launching the
#                server under strace rather than attaching).
#   bench        micro-benchmarks in bench/ that quantify every candidate
#                finding (current code vs a harness-local alternative).
#   escape       `go build -gcflags=-m=2` escape analysis of the hot paths.
#   experiments  A/B of root-package fixes: each experiments/*.diff is applied
#                to a scratch copy of the module; the project's own benchmarks
#                plus bench/ are run interleaved (baseline, patched, ...), and
#                the project's tests are run on the patched copy.
#
# Environment knobs: CONNS (32), LOAD_S (12), PROF_S (10), STRACE_S (5),
# BENCH_COUNT (10), EXAMPLES (space-separated subset).
#
# Outputs: profiles/<example>/*.pb.gz, results/<phase>/... . Compiled binaries
# and example copies live in a mktemp directory that is deleted on exit.
set -euo pipefail
export LC_ALL=C

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/wastehunt.XXXXXX")"
PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do [[ -n "$p" ]] && kill -KILL "$p" 2>/dev/null || true; done
  rm -rf "$WORK"
}
trap cleanup EXIT

CONNS="${CONNS:-32}"
LOAD_S="${LOAD_S:-12}"
PROF_S="${PROF_S:-10}"
STRACE_S="${STRACE_S:-5}"
BENCH_COUNT="${BENCH_COUNT:-10}"
PPROF_ADDR="127.0.0.1:6061"
ALL_EXAMPLES="authn cache graceful-shutdown jwt max-performance oauth2 rest-api reverse-proxy server-sent-events server-side-render static-site upload-file versioning"
EXAMPLES="${EXAMPLES:-$ALL_EXAMPLES}"
PHASE="${1:-all}"

log() { printf '\n== %s ==\n' "$*"; }

record_env() {
  mkdir -p "$HERE/results"
  {
    echo "date: $(date -Iseconds)"
    echo "go: $(go version)"
    echo "kernel: $(uname -srm)"
    echo "cpu: $(lscpu | awk -F: '/Model name/{gsub(/^ +/,"",$2);print $2}')"
    echo "nproc: $(nproc)"
    echo "governor: $(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo n/a)"
    echo "loadavg: $(cat /proc/loadavg)"
    echo "git HEAD: $(git -C "$ROOT" rev-parse --short HEAD)"
    echo "CONNS=$CONNS LOAD_S=$LOAD_S PROF_S=$PROF_S STRACE_S=$STRACE_S BENCH_COUNT=$BENCH_COUNT"
  } | tee "$HERE/results/environment-$PHASE.txt"
}

port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

wait_port() {
  local port="$1"
  for _ in $(seq 1 200); do
    if port_open "$port"; then return 0; fi
    sleep 0.05
  done
  echo "port $port did not open" >&2
  return 1
}

assert_ports_free() {
  for p in 8080 6061 9001 9002; do
    if port_open "$p"; then echo "port $p is busy — stop whatever listens on it first" >&2; exit 1; fi
  done
}

build_tools() {
  mkdir -p "$WORK/bin"
  (cd "$HERE/loadgen" && go build -o "$WORK/bin/loadgen" .)
}

build_example() {
  local name="$1" src="$WORK/src/$1"
  rm -rf "$src"
  mkdir -p "$WORK/src"
  cp -r "$ROOT/examples/$name" "$src"
  rm -f "$src/$name" # a stray locally built binary, if any
  if [[ -f "$HERE/patches/$name.diff" ]]; then
    (cd "$src" && patch -s -p1 < "$HERE/patches/$name.diff")
  fi
  cp "$HERE/probe/zz_wastehunt_probe.go.in" "$src/zz_wastehunt_probe.go"
  (cd "$src" && go mod edit -replace "github.com/FlavioCFOliveira/MuxMaster=$ROOT" && go build -o "$WORK/bin/$name" .)
}

start_aux() {
  if [[ "$1" == "reverse-proxy" ]]; then
    "$WORK/bin/reverse-proxy" backend 9001 >/dev/null 2>&1 & PIDS+=("$!")
    "$WORK/bin/reverse-proxy" backend 9002 >/dev/null 2>&1 & PIDS+=("$!")
    wait_port 9001; wait_port 9002
  fi
}

stop_pid() {
  local pid="$1"
  kill -TERM "$pid" 2>/dev/null || true
  for _ in $(seq 1 150); do kill -0 "$pid" 2>/dev/null || return 0; sleep 0.1; done
  kill -KILL "$pid" 2>/dev/null || true
}

stop_aux() {
  for p in "${PIDS[@]:-}"; do [[ -n "$p" ]] && stop_pid "$p"; done
  PIDS=()
}

# Nearest-owner attribution of an example's saved profiles (analyze/owner.py).
attribute_example() {
  local name="$1" out="$HERE/profiles/$1" res="$HERE/results/examples/$1"
  {
    local idx
    for idx in cpu alloc_space alloc_objects; do
      local prof="$out/allocs.pb.gz"; [[ "$idx" == cpu ]] && prof="$out/cpu.pb.gz"
      go tool pprof -sample_index="$idx" -traces "$prof" 2>/dev/null > "$WORK/traces.txt"
      echo "### $idx"
      python3 "$HERE/analyze/owner.py" "$WORK/traces.txt" 25
      echo
    done
  } > "$res/attribution.txt"
}

profile_example() {
  local name="$1" out="$HERE/profiles/$1" res="$HERE/results/examples/$1"
  mkdir -p "$out" "$res"
  log "profile $name"
  assert_ports_free
  start_aux "$name"
  (cd "$WORK/src/$name" && WH_PPROF_ADDR="$PPROF_ADDR" exec "$WORK/bin/$name" >/dev/null 2>"$WORK/server-stderr.log") &
  local pid=$!
  PIDS+=("$pid")
  wait_port 8080; wait_port 6061
  "$WORK/bin/loadgen" -scenario "$HERE/scenarios/$name.json" -conns "$CONNS" -duration 2s > "$res/warmup.txt"
  echo "rss_kb_before_load: $(ps -o rss= -p "$pid")" > "$res/rss.txt"
  "$WORK/bin/loadgen" -scenario "$HERE/scenarios/$name.json" -conns "$CONNS" -duration "${LOAD_S}s" > "$res/load.txt" &
  local lg=$!
  sleep 1
  curl -s -o "$out/cpu.pb.gz" "http://$PPROF_ADDR/debug/pprof/profile?seconds=$PROF_S" &
  local c1=$!
  curl -s -o "$out/allocs.pb.gz" "http://$PPROF_ADDR/debug/pprof/allocs?seconds=$PROF_S" &
  local c2=$!
  wait "$c1" "$c2" "$lg"
  echo "rss_kb_after_load: $(ps -o rss= -p "$pid")" >> "$res/rss.txt"
  stop_pid "$pid"
  stop_aux
  # Keep a bounded excerpt of the example's own stderr (some examples log
  # one line per request) plus its total line count.
  { echo "# server stderr: $(wc -l < "$WORK/server-stderr.log") lines in total; first 40 shown"; head -n 40 "$WORK/server-stderr.log"; } > "$res/server-stderr.log"
  cat "$res/load.txt"
  attribute_example "$name"
  go tool pprof -top -nodecount=40 "$out/cpu.pb.gz" > "$res/cpu-top.txt" 2>&1
  go tool pprof -top -cum -nodecount=70 "$out/cpu.pb.gz" > "$res/cpu-topcum.txt" 2>&1
  go tool pprof -sample_index=alloc_space -top -nodecount=40 "$out/allocs.pb.gz" > "$res/alloc_space-top.txt" 2>&1
  go tool pprof -sample_index=alloc_objects -top -nodecount=40 "$out/allocs.pb.gz" > "$res/alloc_objects-top.txt" 2>&1
  go tool pprof -sample_index=alloc_space -top -nodecount=60 -focus='FlavioCFOliveira/MuxMaster' "$out/allocs.pb.gz" > "$res/alloc_space-muxmaster-focus.txt" 2>&1
  go tool pprof -top -nodecount=60 -focus='FlavioCFOliveira/MuxMaster' "$out/cpu.pb.gz" > "$res/cpu-muxmaster-focus.txt" 2>&1
  cat "$res/attribution.txt"
}

strace_example() {
  local name="$1" res="$HERE/results/strace/$1"
  mkdir -p "$res"
  log "strace $name"
  local mode
  for mode in idle loaded; do
    assert_ports_free
    start_aux "$name"
    (cd "$WORK/src/$name" && exec strace -f -c -o "$res/strace-$mode.txt" -- "$WORK/bin/$name" >/dev/null 2>/dev/null) &
    local spid=$!
    PIDS+=("$spid")
    wait_port 8080
    local srv
    srv="$(pgrep -P "$spid" | head -n1)"
    if [[ "$mode" == "loaded" ]]; then
      "$WORK/bin/loadgen" -scenario "$HERE/scenarios/$name.json" -conns "$CONNS" -duration "${STRACE_S}s" > "$res/load.txt"
    else
      sleep "$STRACE_S"
    fi
    stop_pid "$srv"
    wait "$spid" 2>/dev/null || true
    stop_aux
  done
  local reqs
  reqs="$(awk -F'[= ]' '/^requests=/{print $2}' "$res/load.txt")"
  awk -v reqs="$reqs" '
    FNR==1 {file++}
    /^ *[0-9.]+ +[0-9.]+ +[0-9]+ +[0-9]+/ {
      name=$NF; calls=$4; if (file==1) idle[name]=calls; else loaded[name]=calls; names[name]=1 }
    END {
      printf "requests during loaded run: %d\n", reqs
      printf "%-22s %10s %10s %14s\n", "syscall", "idle", "loaded", "per-request"
      for (n in names) { d=loaded[n]-idle[n]; if (d>0) printf "%-22s %10d %10d %14.3f\n", n, idle[n], loaded[n], d/reqs }
    }' "$res/strace-idle.txt" "$res/strace-loaded.txt" | sort -k4 -nr > "$res/syscalls-per-request.txt"
  cat "$res/syscalls-per-request.txt"
}

phase_examples() {
  build_tools
  for ex in $EXAMPLES; do build_example "$ex"; done
  for ex in $EXAMPLES; do profile_example "$ex"; done
}

phase_attribute() {
  for ex in $EXAMPLES; do attribute_example "$ex"; done
}

phase_strace() {
  command -v strace >/dev/null || { echo "strace not installed — skipping"; return 0; }
  build_tools
  for ex in $EXAMPLES; do build_example "$ex"; done
  for ex in $EXAMPLES; do strace_example "$ex"; done
}

# Syscalls per operation of selected benchmarks: strace -f -c over a run with
# N iterations minus a run with 1 iteration, divided by N-1 (the process
# start-up and the benchmark harness itself cancel out).
bench_syscalls() {
  local res="$1" bin="$WORK/bench.test"
  (cd "$HERE/bench" && go test -c -o "$bin" .)
  {
    printf '%-58s %s\n' "benchmark" "syscalls per op (N=$2 minus N=1)"
    local b n
    for b in 'Clock/time.Now$' 'Clock/time.Since$' 'Logger/current$' 'Logger/alternative$' 'Logger/alt-single-clock-only$' \
             'ThrottlePerIP/sequential-one-client/current$' 'ThrottlePerIP/sequential-one-client/alternative$' \
             'ThrottlePerIP/sequential-one-client/alt-timer-fastpath-only$' \
             'FileServe/no-logger/large.bin$' 'FileServe/Logger-current/large.bin$' 'FileServe/Logger-with-ReaderFrom/large.bin$' \
             'FileServe/no-logger/small.css$' 'FileServe/Logger-current/small.css$' 'FileServe/Logger-with-ReaderFrom/small.css$'; do
      for n in 1 "$2"; do
        (cd "$WORK" && strace -f -c -o "$WORK/st-$n.txt" "$bin" -test.run '^$' -test.bench "$b" -test.benchtime="${n}x" -test.count 1 >/dev/null 2>&1)
      done
      printf '%-58s' "$b"
      local sc
      for sc in clock_gettime read write sendfile; do
        awk -v s="$sc" -v n="$2" 'FNR==1{f++} $NF==s{v[f]=$4} END{printf " %s=%.2f", s, (v[2]-v[1])/(n-1)}' "$WORK/st-1.txt" "$WORK/st-$2.txt"
      done
      echo
    done
  } | tee "$res/syscalls-per-op.txt"
}

phase_bench() {
  local res="$HERE/results/bench"
  mkdir -p "$res"
  log "bench (count=$BENCH_COUNT)"
  (cd "$HERE/bench" && go vet . && go test -run 'Test' -count=1 -v . ) | grep -E '^(--- |ok|FAIL|PASS)' | tee "$res/equivalence-tests.txt"
  (cd "$HERE/bench" && go test -run '^$' -bench . -benchmem -count="$BENCH_COUNT" .) | tee "$res/bench.txt"
  if command -v benchstat >/dev/null; then benchstat "$res/bench.txt" > "$res/benchstat.txt"; fi
  if command -v strace >/dev/null; then bench_syscalls "$res" 2001; fi
}

phase_escape() {
  local res="$HERE/results/escape"
  mkdir -p "$res"
  log "escape analysis"
  (cd "$ROOT" && go build -gcflags='-m=2' . 2> "$res/root-m2.txt" || true)
  (cd "$ROOT" && go build -gcflags='-m=2' ./middleware 2> "$res/middleware-m2.txt" || true)
  # Keep only the lines that matter for the report (escapes / heap moves), not the multi-MB inlining trace.
  grep -E 'escapes to heap|moved to heap' "$res/root-m2.txt" | grep -v '_test.go' | sort -u > "$res/root-escapes.txt" || true
  grep -E 'escapes to heap|moved to heap' "$res/middleware-m2.txt" | grep -v '_test.go' | sort -u > "$res/middleware-escapes.txt" || true
  rm -f "$res/root-m2.txt" "$res/middleware-m2.txt"
  wc -l "$res"/*.txt
}

phase_experiments() {
  local res="$HERE/results/experiments"
  mkdir -p "$res"
  bash "$HERE/experiments/run-experiments.sh" "$ROOT" "$WORK" "$res" "$BENCH_COUNT"
}

record_env
case "$PHASE" in
  all) phase_examples; phase_strace; phase_bench; phase_escape; phase_experiments ;;
  examples) phase_examples ;;
  attribute) phase_attribute ;;
  strace) phase_strace ;;
  bench) phase_bench ;;
  escape) phase_escape ;;
  experiments) phase_experiments ;;
  *) echo "unknown phase $PHASE"; exit 2 ;;
esac
log "done — see $HERE/results and $HERE/profiles"
