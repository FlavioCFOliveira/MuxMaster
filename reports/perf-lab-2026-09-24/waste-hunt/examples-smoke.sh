#!/usr/bin/env bash
# examples-smoke.sh — post-fix smoke test for rmp task #255 (sprint 18).
#
# Builds and starts every example UNPATCHED (examples/ is exercised exactly
# as shipped — no patches/*.diff from the waste-hunt campaign are applied,
# because the panics and bugs those patches worked around are now fixed in
# examples/ itself) and checks that every documented request (README.md /
# the example's own doc comment) returns its documented status.
#
# Usage:
#   reports/perf-lab-2026-09-24/waste-hunt/examples-smoke.sh            # all 13
#   reports/perf-lab-2026-09-24/waste-hunt/examples-smoke.sh authn jwt  # subset
#
# Exit status: 0 if every check in every example passed, 1 otherwise. A
# per-example PASS/FAIL summary is printed at the end. reverse-proxy also
# gets a short concurrent-load burst (the same profile that used to crash it
# under PoolRequestBundle=true) to confirm the gateway survives.
set -uo pipefail
export LC_ALL=C

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/examples-smoke.XXXXXX")"
PIDS=()
FAILED=0
RESULTS=()

cleanup() {
  for p in "${PIDS[@]:-}"; do [[ -n "$p" ]] && kill -KILL "$p" 2>/dev/null || true; done
  rm -rf "$WORK"
}
trap cleanup EXIT

ALL_EXAMPLES="authn cache graceful-shutdown jwt max-performance oauth2 rest-api reverse-proxy server-sent-events server-side-render static-site upload-file versioning"
EXAMPLES="${*:-$ALL_EXAMPLES}"

log() { printf '\n=== %s ===\n' "$*"; }

port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

wait_port() {
  local port="$1"
  for _ in $(seq 1 200); do
    port_open "$port" && return 0
    sleep 0.05
  done
  echo "port $port did not open" >&2
  return 1
}

stop_pid() {
  local pid="$1"
  kill -TERM "$pid" 2>/dev/null || true
  for _ in $(seq 1 100); do kill -0 "$pid" 2>/dev/null || return 0; sleep 0.05; done
  kill -KILL "$pid" 2>/dev/null || true
}

# build NAME — builds examples/NAME (unmodified, no patches) into $WORK/bin/NAME.
build() {
  local name="$1"
  (cd "$ROOT/examples/$name" && go build -o "$WORK/bin/$name" .) 2>"$WORK/$name-build.log"
  if [[ $? -ne 0 ]]; then
    echo "BUILD FAILED: $name"
    cat "$WORK/$name-build.log"
    RESULTS+=("$name: FAIL (build)")
    FAILED=1
    return 1
  fi
  return 0
}

# check NAME METHOD PATH EXPECT [curl-args...] — records PASS/FAIL.
check() {
  local name="$1" method="$2" path="$3" expect="$4"; shift 4
  local got
  got=$(curl -s -m 10 -o /dev/null -w '%{http_code}' -X "$method" "$@" "http://localhost:8080$path")
  if [[ "$got" == "$expect" ]]; then
    RESULTS+=("$name: PASS  $method $path -> $got")
  else
    RESULTS+=("$name: FAIL  $method $path -> got $got, want $expect")
    FAILED=1
  fi
}

# ─── authn ────────────────────────────────────────────────────────────────────
test_authn() {
  local name=authn
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /health 200
  check "$name" GET / 200
  check "$name" GET /admin/dashboard 401
  check "$name" GET /admin/dashboard 200 -u admin:s3cr3t
  check "$name" GET /admin/dashboard 401 -u admin:wrong
  check "$name" GET /api/profile 401
  check "$name" GET /api/profile 200 -H 'X-API-Key: key-alice'
  check "$name" GET /api/profile 401 -H 'X-API-Key: bad'
  stop_pid "${PIDS[-1]}"
}

# ─── cache ────────────────────────────────────────────────────────────────────
test_cache() {
  local name=cache
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /health 200
  check "$name" GET /articles 200
  local etag
  # The ETag value itself may contain spaces (it embeds the article title),
  # so extract everything after "ETag:" rather than splitting on whitespace.
  etag=$(curl -si http://localhost:8080/articles/1 | grep -i '^etag:' | sed -E 's/^[Ee][Tt][Aa][Gg]: *//' | tr -d '\r')
  check "$name" GET /articles/1 200
  check "$name" GET /articles/1 304 -H "If-None-Match: $etag"
  check "$name" POST /articles 201 -H 'Content-Type: application/json' -d '{"title":"x"}'
  check "$name" PUT /articles/1/done 200
  stop_pid "${PIDS[-1]}"
}

# ─── graceful-shutdown ────────────────────────────────────────────────────────
test_graceful_shutdown() {
  local name=graceful-shutdown
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET / 200
  check "$name" GET /nope 404
  check "$name" GET /slow 200 -m 5
  stop_pid "${PIDS[-1]}"
}

# ─── jwt ──────────────────────────────────────────────────────────────────────
test_jwt() {
  local name=jwt
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /health 200
  local token
  token=$(curl -s -X POST http://localhost:8080/auth/login -H 'Content-Type: application/json' \
    -d '{"username":"alice","password":"secret"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
  check "$name" GET /api/me 401
  check "$name" GET /api/me 200 -H "Authorization: Bearer $token"
  check "$name" GET /api/secret 200 -H "Authorization: Bearer $token"
  check "$name" POST /auth/refresh 200 -H "Authorization: Bearer $token"
  stop_pid "${PIDS[-1]}"
}

# ─── max-performance ──────────────────────────────────────────────────────────
test_max_performance() {
  local name=max-performance
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /v1/health 200
  check "$name" GET /v1/users/42 200
  check "$name" GET /v1/orgs/acme/repos/api/issues/123 200
  check "$name" GET /bench 200
  check "$name" GET /debug/pprof/profile?seconds=1 200
  check "$name" GET /debug/pprof/cmdline 200
  stop_pid "${PIDS[-1]}"
}

# ─── oauth2 ───────────────────────────────────────────────────────────────────
test_oauth2() {
  local name=oauth2
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /api/me 401
  check "$name" GET /api/me 200 -H 'Authorization: Bearer good-token'
  stop_pid "${PIDS[-1]}"
}

# ─── rest-api ─────────────────────────────────────────────────────────────────
test_rest_api() {
  local name=rest-api
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /health 200
  check "$name" GET /metrics 200
  check "$name" POST /api/v1/fast/echo 200 -d 'hi'
  check "$name" GET /api/v1/books 200
  check "$name" POST /api/v1/books 201 -H 'Content-Type: application/json' \
    -d '{"title":"The Go Programming Language","author":"Donovan","year":2015}'
  check "$name" GET /api/v1/books/1 200
  check "$name" GET /api/v1/books/1/details 200
  check "$name" GET /api/v1/books/abc 404
  check "$name" GET /api/v1/books/featured 200
  check "$name" GET /api/v1/books/ping 200
  check "$name" GET /api/v1/books/1/reviews 200
  check "$name" GET /api/v1/authors/1/xml 200
  check "$name" GET /api/v1/categories 200
  check "$name" GET /api/v1/categories/tech 200
  check "$name" GET /api/v1/admin/dashboard 401
  check "$name" GET /api/v1/admin/dashboard 200 -u 'admin:s3cr3t!'
  check "$name" GET /debug/routes 200
  check "$name" GET /legacy/api/books 308
  stop_pid "${PIDS[-1]}"
}

# ─── reverse-proxy ────────────────────────────────────────────────────────────
test_reverse_proxy() {
  local name=reverse-proxy
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name" backend 9001) >"$WORK/$name-9001.log" 2>&1 &
  PIDS+=("$!")
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name" backend 9002) >"$WORK/$name-9002.log" 2>&1 &
  PIDS+=("$!")
  wait_port 9001 || true; wait_port 9002 || true
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  local gw_pid=$!
  PIDS+=("$gw_pid")
  wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; return; }
  check "$name" GET /api/users 200
  check "$name" GET /static/x.png 200
  check "$name" GET /admin/dashboard 403
  check "$name" GET /admin/dashboard 200 -H 'X-Admin-Token: letmein'
  check "$name" GET / 200

  # Load burst — the exact profile that crashed the gateway pre-fix
  # (reports/.../results/defects/reverse-proxy-pool-crash.txt): 32 conns.
  if [[ -x "$WORK/bin/loadgen" ]] || (cd "$HERE/loadgen" && go build -o "$WORK/bin/loadgen" . 2>"$WORK/loadgen-build.log"); then
    "$WORK/bin/loadgen" -scenario "$HERE/scenarios/reverse-proxy.json" -target 127.0.0.1:8080 -conns 32 -duration 8s \
      >"$WORK/$name-load.log" 2>&1
    if kill -0 "$gw_pid" 2>/dev/null && grep -q 'STATUS-OK' "$WORK/$name-load.log"; then
      RESULTS+=("$name: PASS  32-conn/8s load burst — gateway survived, all statuses matched")
    else
      RESULTS+=("$name: FAIL  load burst — gateway crashed or a status mismatched (see $WORK/$name-load.log)")
      FAILED=1
    fi
  else
    RESULTS+=("$name: FAIL  could not build loadgen")
    FAILED=1
  fi
  stop_pid "$gw_pid"
}

# ─── server-sent-events ───────────────────────────────────────────────────────
test_server_sent_events() {
  local name=server-sent-events
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET / 200
  check "$name" POST /publish 200 -d '{"topic":"news","msg":"hello"}'
  local n
  n=$(timeout 2 curl -s -N http://localhost:8080/events/news | head -c 200 | wc -c)
  if [[ "$n" -gt 0 ]]; then RESULTS+=("$name: PASS  GET /events/news streams bytes"); else RESULTS+=("$name: FAIL  GET /events/news produced no bytes"); FAILED=1; fi
  stop_pid "${PIDS[-1]}"
}

# ─── server-side-render ───────────────────────────────────────────────────────
test_server_side_render() {
  local name=server-side-render
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET / 200
  check "$name" GET /about 200
  check "$name" GET "/guestbook?ok=1" 200
  check "$name" POST /guestbook 303 -d 'name=x&message=y' # POST-Redirect-GET to /guestbook?ok=1
  check "$name" GET /static/style.css 200
  check "$name" GET /nope 404
  stop_pid "${PIDS[-1]}"
}

# ─── static-site ──────────────────────────────────────────────────────────────
test_static_site() {
  local name=static-site
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /health 200
  check "$name" HEAD /health 200
  check "$name" GET / 200
  check "$name" GET /api/config 200
  check "$name" GET /doc 301
  # The four commands documented in the package doc comment.
  check "$name" HEAD /assets/style.css 200
  check "$name" GET /assets/style.css 200 -H 'Accept-Encoding: gzip'
  check "$name" GET /assets/style.css 206 -r 0-499
  check "$name" OPTIONS /assets/style.css 204 -H 'Origin: https://example.com'
  # /docs/v1 and /docs/v2 (Mount + sub-mux ServeFiles + a global CleanPath
  # Pre that strips the trailing slash before routing) — fixed by registering
  # the canonical, slash-less form directly (see main.go); both the bare and
  # trailing-slash forms must resolve without a redirect loop.
  check "$name" GET /docs/v1 200
  check "$name" GET /docs/v1/ 200
  check "$name" HEAD /docs/v1 200
  check "$name" GET /docs/v2 200
  check "$name" GET /docs/v2/ 200
  stop_pid "${PIDS[-1]}"
}

# ─── upload-file ──────────────────────────────────────────────────────────────
test_upload_file() {
  local name=upload-file
  build "$name" || return
  mkdir -p /tmp/muxmaster-uploads
  echo "smoke test payload" > "$WORK/upload.txt"
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET / 200
  check "$name" POST /upload 200 -F "file=@$WORK/upload.txt"
  check "$name" POST /multi 200 -F "file=@$WORK/upload.txt" -F "file=@$WORK/upload.txt"
  check "$name" POST /async 202 -F "file=@$WORK/upload.txt"
  stop_pid "${PIDS[-1]}"
}

# ─── versioning ───────────────────────────────────────────────────────────────
test_versioning() {
  local name=versioning
  build "$name" || return
  (cd "$ROOT/examples/$name" && exec "$WORK/bin/$name") >"$WORK/$name.log" 2>&1 &
  PIDS+=("$!"); wait_port 8080 || { RESULTS+=("$name: FAIL (did not start)"); FAILED=1; stop_pid "${PIDS[-1]}"; return; }
  check "$name" GET /api/v1/users/42 200
  check "$name" GET /api/v2/users/42 200
  check "$name" GET /api/users/42 200
  check "$name" GET /api/users/42 200 -H 'Accept: application/vnd.muxmaster+json;v=2'
  check "$name" GET /api/v1/admin/dashboard 403
  check "$name" GET /api/v1/admin/dashboard 200 -H 'X-Admin-Token: letmein'
  check "$name" GET /api/v2/admin/dashboard 200 -H 'X-Admin-Token: letmein'
  check "$name" GET /api/v1/admin/users/42/audit 200 -H 'X-Admin-Token: letmein'
  check "$name" GET /api/v2/admin/users/42/audit 200 -H 'X-Admin-Token: letmein'
  check "$name" GET / 200
  stop_pid "${PIDS[-1]}"
}

for ex in $EXAMPLES; do
  log "$ex"
  fn="test_${ex//-/_}"
  if declare -f "$fn" >/dev/null; then
    "$fn"
  else
    echo "no smoke test defined for $ex" >&2
    FAILED=1
  fi
  sleep 0.2
done

log "SUMMARY"
printf '%s\n' "${RESULTS[@]}"
echo
if [[ "$FAILED" -eq 0 ]]; then
  echo "ALL EXAMPLES PASS"
else
  echo "SOME CHECKS FAILED — see above"
fi
exit "$FAILED"
