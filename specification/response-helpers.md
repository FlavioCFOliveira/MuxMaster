# Response Helpers

## Scope

This file specifies the package-level response helper functions: `JSON`, `XML`, `Text`, `Redirect`, and `NoContent`.

These functions are pure utilities over `http.ResponseWriter` and `*http.Request`. They do not require a custom context type, do not modify router state, and have no special behavior inside or outside a MuxMaster handler.

This file does not cover the error-return handler pattern (`HandlerFuncE`) or the `HTTPError` type (see [error-handling.md](error-handling.md)).

---

## 1. Design Constraints

1. All response helpers are package-level functions. They are not methods on any type.
2. No helper requires or creates a custom context type.
3. All helpers use only the Go standard library for encoding.
4. These functions are intended to be called at the end of a handler, after all application logic is complete. They are not called on the hot routing path and do not have per-request allocation targets.

---

## 2. JSON

5. `JSON(w http.ResponseWriter, code int, v any) error` serializes `v` to JSON and writes it to `w`.
6. The `Content-Type` header is set to `application/json; charset=utf-8` before writing the body.
7. The HTTP status code `code` is written via `w.WriteHeader(code)` before the body.
8. Serialization uses `encoding/json` from the standard library.
9. If serialization fails, `JSON` returns the error without writing anything to `w` (headers have not been sent yet at that point). The caller is responsible for writing an error response.
10. If `code` is 0, `JSON` uses `http.StatusOK` (200).

---

## 3. XML

11. `XML(w http.ResponseWriter, code int, v any) error` serializes `v` to XML and writes it to `w`.
12. The `Content-Type` header is set to `application/xml; charset=utf-8`.
13. The HTTP status code `code` is written before the body.
14. Serialization uses `encoding/xml` from the standard library.
15. If serialization fails, `XML` returns the error without writing anything to `w`.
16. If `code` is 0, `XML` uses `http.StatusOK` (200).

---

## 4. Text

17. `Text(w http.ResponseWriter, code int, s string) error` writes `s` as a plain-text response.
18. The `Content-Type` header is set to `text/plain; charset=utf-8`.
19. The HTTP status code `code` is written before the body.
20. `Text` always returns nil. The error return exists for API consistency with `JSON` and `XML`.
21. If `code` is 0, `Text` uses `http.StatusOK` (200).

---

## 5. Redirect

22. `Redirect(w http.ResponseWriter, r *http.Request, code int, url string)` issues an HTTP redirect to `url` with status code `code`.
23. `Redirect` delegates to `http.Redirect(w, r, url, code)`.
24. `code` must be a 3xx status code. Passing a non-3xx code produces undefined behavior consistent with `http.Redirect`.

---

## 6. NoContent

25. `NoContent(w http.ResponseWriter)` writes a 204 No Content response with no body.
26. No headers other than the status code are set.
27. `NoContent` does not take a body argument. A 204 response must not have a body per RFC 9110.

---

## 7. Out of Scope

The following are not provided by this package:

- MessagePack, Protocol Buffers, or other binary encoding formats (no stdlib support).
- Content negotiation via `Accept` header.
- Response streaming helpers.
- Cookie helpers (use `http.SetCookie` directly).
- Multipart responses.
