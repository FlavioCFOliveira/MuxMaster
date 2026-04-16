---
name: MuxMaster implementation status as of 2026-04-16
description: What is currently implemented in code vs. what is specified but not yet implemented
type: project
---

Status snapshot taken 2026-04-16 from reading mux.go, group.go, params.go, tree.go.

**Why:** Knowing implementation status helps avoid falsely describing unimplemented features as present when answering questions or generating code.

**How to apply:** Verify against the actual source files before acting on this. This snapshot decays.

## Implemented (verified from source)

### mux.go
- Mux struct with: RedirectTrailingSlash, RedirectFixedPath, HandleMethodNotAllowed, HandleOPTIONS (bool, default true via New()), NotFound, MethodNotAllowed, PanicHandler
- New() constructor
- Use(middleware...)
- Handle(method, pattern, handler) — with panics on empty method, non-absolute path, nil handler
- HandleFunc(method, pattern, handler)
- Convenience methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS
- Group(prefix) *Group
- ServeHTTP — with TSR (301 GET / 307 others), fixed-path redirect, 405 + Allow header, 204 OPTIONS auto-response, PanicHandler recovery

### group.go
- Group struct with mux reference, prefix, middleware slice
- Use(middleware...)
- Handle, HandleFunc
- Convenience methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS
- Group(prefix) — copies parent middleware, extends prefix

### params.go
- Param struct {Key, Value string}
- Params []Param
- Params.Get(name) string
- PathParam(r, name) string
- ParamsFromContext(ctx) Params
- sync.Pool with maxParams=16 capacity
- acquireParams / releaseParams
- withParams (internal)

### tree.go
- Radix tree with node types: static, root, param, wildcard
- addRoute — with panics on duplicate, unnamed wildcard, catch-all not at end, catch-all conflicts, multiple wildcards per segment
- getValue — returns handler + tsr bool
- hasHandler — used for Allow header building
- Priority-ordered children

## NOT yet implemented (from specification, as of 2026-04-16)

- CONNECT, TRACE convenience methods
- ANY(path, handler)
- Match([]string, path, handler)
- RegisterMethod(custom)
- Regex parameters {name:expr}
- Optional parameters {/:name}
- Lookup(method, path)
- With(mw...) per-route middleware
- Pre(mw...) pre-routing middleware
- Route(prefix, fn) inline group
- Mount(prefix, handler)
- Params.Lookup, Params.Int/Int64/Uint64/Float64/Bool, Params.Map
- RoutePattern(r)
- RouteInfo, Routes(), Walk()
- RedirectCode, CaseInsensitive, UseRawPath, UnescapePathValues config fields
- GlobalOPTIONS field on Mux
- ServeFiles(prefix, root)
- JSON, XML, Text, Redirect, NoContent response helpers
- HandlerFuncE, GETE/POSTE/etc., ErrorHandler, HTTPError
- All muxmaster/middleware sub-package middleware
