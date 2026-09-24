#!/usr/bin/env bash
# Drives one server-process + client-process load test at a given connection
# count. Usage: ./driver.sh <conns> <duration_seconds>
set -euo pipefail
cd "$(dirname "$0")"

CONNS="${1:-1000}"
DURATION_S="${2:-5}"
PROFDIR="../profiles"
RESULTDIR="../results"
mkdir -p "$PROFDIR" "$RESULTDIR"

PROFDUR_S=$((DURATION_S + 3))

BIN="$(mktemp -d)/loadgen"
go build -o "$BIN" .

LOGFILE="$(mktemp)"
ERRFILE="$(mktemp)"
"$BIN" -mode=server -addr=127.0.0.1:0 -profduration="${PROFDUR_S}s" -tag="$CONNS" -profdir="$PROFDIR" > "$LOGFILE" 2>"$ERRFILE" &
SRV_PID=$!

for _ in $(seq 1 50); do
  if [[ -s "$LOGFILE" ]]; then break; fi
  sleep 0.1
done
ADDR="$(head -n1 "$LOGFILE")"
if [[ -z "$ADDR" ]]; then
  echo "server did not print an address in time; log:"
  cat "$LOGFILE"
  kill "$SRV_PID" 2>/dev/null || true
  exit 1
fi

echo "server listening on $ADDR (pid $SRV_PID)"
"$BIN" -mode=client -target="$ADDR" -conns="$CONNS" -duration="${DURATION_S}s" | tee "$RESULTDIR/loadgen-${CONNS}.txt"

wait "$SRV_PID" 2>/dev/null || true
echo "server stderr log:"
cat "$ERRFILE"
rm -f "$LOGFILE" "$ERRFILE"
