# Static Files

## Scope

This file specifies `ServeFiles`, the method for serving static files from a file system.

This file does not cover the `Mount` method (see [groups.md](groups.md)), which can also be used to serve files via `http.FileServer` as an external handler.

---

## 1. ServeFiles

1. `(*Mux).ServeFiles(prefix string, root http.FileSystem)` registers a handler that serves static files from `root` under the URL path `prefix`.
2. `(*Group).ServeFiles(prefix string, root http.FileSystem)` is equivalent, with the group prefix prepended to `prefix`.
3. `prefix` must end with `/*name` where `name` is a non-empty identifier. A `prefix` that does not end with this pattern causes a panic.
4. `ServeFiles` registers the route for both GET and HEAD. No other methods are registered.
5. Internally, `ServeFiles` uses `http.FileServer(root)` to serve files. No custom file-serving logic is implemented.
6. `http.FileServer` handles path traversal protection (e.g., `../` sequences). MuxMaster does not add additional protection beyond what `http.FileServer` provides.
7. The wildcard parameter name (the `name` in `/*name`) is used to strip the path prefix before delegating to `http.FileServer`. The file path passed to the file server is the value of the catch-all parameter.
8. `root` may be any value that implements `http.FileSystem`, including `http.Dir`, `http.FS` (wrapping an `fs.FS`), and `embed.FS` wrapped with `http.FS`.
9. Calling `ServeFiles` with a nil `root` causes a panic.
10. Calling `ServeFiles` with a pattern that conflicts with an already-registered route causes a panic (same rule as any other route registration).

---

## 2. Behavior

11. A request to `/static/img/logo.png` with the route `ServeFiles("/static/*filepath", http.Dir("./public"))` serves the file at `./public/img/logo.png`.
12. A request to `/static/` serves the directory listing for `./public/`, subject to `http.FileServer` behavior.
13. HTTP caching headers (`ETag`, `Last-Modified`, `Cache-Control`) are managed by `http.FileServer`. MuxMaster does not modify them.
14. Directory index files (e.g., `index.html`) are served by `http.FileServer` when a directory path is requested, if such a file exists. MuxMaster does not control this behavior.

---

## 3. Out of Scope

The following behaviors are not provided by `ServeFiles`:

- Custom cache control headers (use the `SetHeader` middleware from [middleware-stdlib.md](middleware-stdlib.md)).
- Serving a single file rather than a directory tree (use a regular handler with `http.ServeFile`).
- Restricting access to specific file types.
- Range request handling beyond what `http.FileServer` provides natively.
