---
name: MuxMaster implementation status snapshot
description: What is implemented in code as of 2026-07-14 (mux.go, params.go, group.go, tree.go, handler.go) vs. what the specification still describes as unimplemented
type: project
---

Status snapshot taken 2026-07-14 from reading mux.go (1283 lines), params.go (584 lines), group.go (213 lines), tree.go (835 lines), handler.go (73 lines) during rmp task 237 (KG-2026-001). Supersedes the 2026-04-16 snapshot, which was badly stale — the project had gone through several performance optimization passes (Opt O1–O13 per source comments) and added a large amount of surface area since then.

**Why:** Knowing implementation status prevents describing unimplemented features as present, and prevents trusting an old spec/memory snapshot over the actual source. See [[task_kg2026001_fastpath_spec]] for the "spec had drifted from code" lesson this snapshot exists to guard against.

**How to apply:** This decays fast on this project. Re-verify against source before relying on any specific claim below, especially anything performance/pooling-related — that's the area most actively optimized.

## Implemented and now specified (as of 2026-07-14, after task 237)

- `HandleFast`/`GETFast`.../`TRACEFast`, `FastHandler` type, `Params` as 3rd arg — mux.go, handler.go. Spec: performance.md §6.
- `UseFast`, `FastMiddleware` type, `wrapFastMiddleware` — mux.go, group.go, handler.go. Spec: middleware.md §6-7.
- `Mux.PoolFastParams` (default false) — params.go `fastParams1/2/3Pool`. Spec: configuration.md §4.5.
- `Mux.PoolRequestBundle` (default false) — params.go `reqBundle1/2/N Pool`. Spec: configuration.md §4.6.
- Lock-free dispatch: `treesPtr atomic.Pointer[methodTrees]`, copy-on-write `cloneTree` under `mu` — mux.go, tree.go. Spec: performance.md §7.
- Tiered request bundle: `reqBundle1`/`reqBundle2`/`reqBundle` fusing context + `*http.Request` copy — params.go. Spec: performance.md §8.
- `Mux.Rebuild()` + frozen `muxConfig` snapshot (`cfg atomic.Pointer[muxConfig]`) — mux.go. Spec: configuration.md §5.

## Other implemented surface confirmed present (already had spec coverage before task 237, re-verified now)

- `Mux`/`Group`: Handle, HandleFunc, HandleE (+ `HandlerFuncE`/`HTTPError`/`Error()`), GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS/CONNECT/TRACE (+ `...E` error variants), ANY, Match, Group, With, Route, Mount, ServeFiles.
- `Params`: Get, Lookup, Int/Int64/Uint64/Float64/Bool, Map.
- `PathParam`, `ParamsFromContext`, `RoutePattern`.
- Introspection: `Lookup`, `Routes()`, `Walk()` — introspection.go (confirmed present via grep; did not deep-read this file in task 237).
- Config fields: RedirectTrailingSlash, RedirectFixedPath, HandleMethodNotAllowed, HandleOPTIONS, CaseInsensitive, UseRawPath, UnescapePathValues, RedirectCode, NotFound, MethodNotAllowed, GlobalOPTIONS, ErrorHandler, PanicHandler.
- Regex params `{name:expr}`, optional params `{/:name}` (capped at 8 optional segments per pattern — `maxOptionalSegments` in tree.go, to bound 2^N expansion DoS).

## NOT found in source when checked 2026-07-14 (spec may still describe these — treat as aspirational/unimplemented until re-verified)

- `RegisterMethod(method string)` — routing.md §2.3 documents it; no matching function found via grep across `*.go`.
- Everything else in the old 2026-04-16 "NOT yet implemented" list should be re-checked before being cited — most of that list (Params helpers, RoutePattern, With, Pre, Route, Mount, response helpers, HandlerFuncE, introspection) turned out to already be implemented by 2026-07-14 and the old memory file was actively misleading. Don't trust that list; grep the source instead.

## Known minor spec/code naming mismatch (not fixed, flagged to user)

- groups.md §7 item 28 says `Mount` registers `Handle("*", prefix+"/*", ...)`; actual internal catch-all param name is `mux_mount` (`prefix+"/*mux_mount"` in mux.go `mountAt`). Cosmetic/internal-detail only — does not affect documented external behavior.
