# Routing

## Scope

This file specifies path pattern syntax, HTTP method support, route registration rules, request matching behavior, matching precedence, and the conditions that cause a panic at registration time.

This file does not cover middleware application order (see [middleware.md](middleware.md)), group prefixes (see [groups.md](groups.md)), path parameter access (see [params.md](params.md)), or programmatic route lookup (see [introspection.md](introspection.md)).

---

## 1. Path Pattern Syntax

### 1.1 General Rules

1. Every pattern must begin with `/`.
2. A pattern that does not begin with `/` causes a panic at registration time.
3. Patterns are case-sensitive. The string `/Users` is distinct from `/users`.
4. Patterns may not contain a fragment (`#`) or query string (`?`). Only the path component is matched.
5. The empty string is not a valid pattern.

### 1.2 Static Patterns

6. A static pattern contains only literal characters and no wildcard tokens.
7. Examples of valid static patterns: `/`, `/users`, `/api/v1/status`.

### 1.3 Named Parameters

8. A named parameter captures one path segment. It is written as `:name` where `name` is a non-empty identifier.
9. A named parameter matches exactly one segment. It does not match across a `/` character.
10. The captured value is stored in `Params` with the key equal to `name` (without the leading `:`).
11. Example: the pattern `/users/:id` matches `/users/42` and captures `id = 42`. It does not match `/users/` or `/users/42/posts`.
12. Named parameter names within a single pattern must be unique.

### 1.4 Catch-all Parameters

13. A catch-all parameter captures the remainder of the path including all `/` characters. It is written as `*name` where `name` is a non-empty identifier.
14. A catch-all parameter must be the last element of the pattern. A pattern such as `/*name/suffix` is invalid and causes a panic at registration time.
15. The captured value includes the leading `/`. Example: the pattern `/static/*filepath` matched against `/static/img/logo.png` captures `filepath = /img/logo.png`.
16. There must be a literal `/` immediately before the `*` token in the pattern.
17. Only one catch-all parameter is permitted per pattern.

### 1.5 Regex Parameters

18. A regex parameter validates and captures one path segment using a regular expression. It is written as `{name:expr}` where `name` is a non-empty identifier and `expr` is a valid Go regular expression (`regexp/syntax` package).
19. Matching fails (the route is not selected) when the segment value does not match `expr`. The router continues to the next candidate.
20. The regular expression `expr` is implicitly anchored. That is, the full segment value must match `expr`, not merely a substring of it.
21. An invalid regular expression in a regex parameter causes a panic at registration time.
22. The captured value is stored in `Params` with the key equal to `name`.
23. Example: the pattern `/users/{id:\d+}` matches `/users/42` and captures `id = 42`. It does not match `/users/abc`.

### 1.6 Optional Parameters

24. An optional parameter declares that a segment is present or absent. It is written as `{/:name}` (optional named segment) or `{/:name:expr}` (optional regex segment).
25. Registering a route with an optional parameter is equivalent to registering two routes: one without the optional segment and one with it as a named or regex parameter. Both registrations use the same handler.
26. If either of the two expanded routes conflicts with an already-registered route, a panic occurs at registration time.

### 1.7 Wildcard Constraints

27. A pattern must not contain an unnamed wildcard. `:` or `*` not followed by a name causes a panic.
28. A pattern must not contain more than one wildcard token per path segment. The segment `:a:b` is invalid.
29. A pattern must not contain both a named parameter and a catch-all in the same path segment.

---

## 2. HTTP Methods

### 2.1 Supported Methods

30. The following HTTP methods have dedicated convenience registration methods on `*Mux` and `*Group`. All of them, including `QUERY`, are standard methods, not custom ones (see section 2.3); `QUERY` is standardized by RFC 10008, and section 9 specifies its full semantics, including its `HandlerFuncE` and `FastHandler` convenience methods:

| Method | `*Mux` method | `*Group` method |
|---|---|---|
| GET | `GET(pattern, handler)` | `GET(pattern, handler)` |
| HEAD | `HEAD(pattern, handler)` | `HEAD(pattern, handler)` |
| POST | `POST(pattern, handler)` | `POST(pattern, handler)` |
| PUT | `PUT(pattern, handler)` | `PUT(pattern, handler)` |
| PATCH | `PATCH(pattern, handler)` | `PATCH(pattern, handler)` |
| DELETE | `DELETE(pattern, handler)` | `DELETE(pattern, handler)` |
| OPTIONS | `OPTIONS(pattern, handler)` | `OPTIONS(pattern, handler)` |
| CONNECT | `CONNECT(pattern, handler)` | `CONNECT(pattern, handler)` |
| TRACE | `TRACE(pattern, handler)` | `TRACE(pattern, handler)` |
| QUERY | `QUERY(pattern, handler)` | `QUERY(pattern, handler)` |

31. MuxMaster recognizes a fixed, closed set of eleven method tokens: the ten standard methods listed in the table above, plus the internal token `"*"`, which `Mount` uses to register a handler that matches its prefix for every request method regardless of what it is (see [groups.md](groups.md) section 7, rule 28). Recognition is performed by an internal `methodIdx` lookup that maps each of these eleven exact strings to a fixed array index; no other string is recognized. Passing `"*"` to `Handle` or `HandleFunc` directly is accepted by `methodIdx` in the same way — it does not panic, and it registers on the same internal wildcard tree `Mount` uses, matching the given pattern for every request method — but this is the low-level mechanism `Mount` is built on, not a documented way for an application to register a handler for multiple methods; `ANY` (section 2.2) is the supported way to register a handler for every standard method. Any method string other than these eleven tokens — including a lowercase or mixed-case variant of a standard method, and any custom or extension method such as `PURGE` or `PROPFIND` — is not recognized and causes a panic when registered; see section 2.3.

### 2.2 ANY and Match

32. `ANY(pattern, handler)` registers `handler` for every standard HTTP method: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, TRACE, and QUERY. Each registration is independent. A subsequent call to `GET(pattern, other)` panics because GET is already registered for that pattern.
33. `Match(methods []string, pattern string, handler http.Handler)` registers `handler` for each method in `methods`. The same panic-on-duplicate rule applies.

### 2.3 Unsupported Methods

34. A method string that is not one of the eleven tokens `methodIdx` recognizes (the ten standard methods in section 2.1's table, plus the internal `"*"` token, rule 31) causes a panic when passed to `Handle`, `HandleFunc`, `HandleE`, `HandleFast`, or `Match` — whether called directly on `*Mux` or through the equivalent `*Group` methods, which delegate to the same `*Mux` methods and are subject to the identical check. The panic message is `muxmaster: unsupported HTTP method '<method>'`, where `<method>` is the exact string that was passed in. This covers a lowercase or mixed-case variant of a standard method (for example `get`) and any custom or extension method (for example `PURGE` or `PROPFIND`). See section 2.4, rule 37, for the distinct panic used when the method string is empty.
35. MuxMaster provides no mechanism to register a custom or extension method. There is no `RegisterMethod` function, or equivalent, on `*Mux` or `*Group`. The set of recognized method tokens is fixed in the router's source and cannot be extended at runtime. See [out-of-scope.md](out-of-scope.md) section 2.7 for the rationale.
36. `ANY` (section 2.2) registers a handler only for the ten standard methods listed in section 2.1's table. Because no custom method can ever be registered (rule 35), there is no notion of `ANY` including or excluding one.

### 2.4 Method String Validation

37. An empty method string passed to `Handle`, `HandleFunc`, `HandleE`, `HandleFast`, or `Match` (directly, or through the equivalent `*Group` methods) causes a panic before the string is checked against the recognized set: `panic("muxmaster: HTTP method must not be empty")`. This is a distinct message from the one in rule 34; the empty string never reaches the `unsupported HTTP method` panic.
38. Method strings are case-sensitive. `get` and `GET` are distinct strings to `methodIdx`; `get` is not one of the eleven recognized tokens (rule 31), so registering a route with method `get` panics with the message in rule 34. Dispatch at request time is equally case-sensitive: `methodIdx` finds no match for a request whose `Method` is `get`, `PURGE`, or any other unrecognized string, exactly as it finds none for a method that is recognized but was never registered at the requested path. In both cases, before section 4.7 applies, the router checks the internal `"*"` tree (rule 31): if a route registered there — via `Mount`, or directly via `Handle("*", pattern, handler)` — matches the request path, that route serves the request regardless of the request's method, including an unrecognized one such as `get` or `PURGE`. Only when no `"*"` route matches the path does the router fall back to section 4.7: a 405 response with an `Allow` header if `HandleMethodNotAllowed` is true and the path is registered for at least one other method, otherwise a 404 response. A route registered under `"*"` is never listed in that `Allow` header: section 4.7, rule 61, enumerates only the ten standard methods and never the `"*"` tree.

---

## 3. Route Registration

### 3.1 The Handle Method

39. `Handle(method string, pattern string, handler http.Handler)` is the primary registration method. All convenience methods (`GET`, `POST`, etc.) delegate to `Handle`.
40. Calling `Handle` with a nil handler causes a panic.
41. Calling `Handle` with a pattern that does not begin with `/` causes a panic.
42. Calling `Handle` with an empty method string causes a panic.

### 3.2 HandleFunc

43. `HandleFunc(method string, pattern string, h http.HandlerFunc)` is equivalent to `Handle(method, pattern, http.HandlerFunc(h))`.

### 3.3 Middleware Application at Registration Time

44. When `Handle` is called, the middleware chain active at that moment is applied to the handler. The resulting wrapped handler is stored in the route tree.
45. Middleware added after a route is registered does not affect that route. Middleware added via `Use` must be called before the routes it is intended to wrap. This is a deliberate design choice that eliminates per-request middleware overhead.

### 3.4 Duplicate Routes

46. Registering two routes with the same method and the same pattern causes a panic. This includes patterns that are functionally identical, such as two patterns that expand to the same set of routes through optional parameter expansion.

---

## 4. Request Matching

### 4.1 Lookup Sequence

47. On each request, `ServeHTTP` performs the following steps in order:
    1. Look up the route tree for the request method. This step finds no tree when the request's method is not one of the ten standard methods `methodIdx` recognizes (section 2.1, rule 31); step 1 is then effectively skipped and steps 2-5 find nothing, falling straight through to step 6.
    2. Traverse the tree using the request's URL path (`r.URL.Path`).
    3. If a handler is found, call it and return.
    4. If `RedirectTrailingSlash` is true, check for a TSR candidate and redirect if found.
    5. If `RedirectFixedPath` is true, check whether `path.Clean` of the request path has a handler and redirect if found.
    6. Look up the internal `"*"` tree (section 2.1, rule 31) using the same URL path. This step always runs, regardless of whether step 1 found a tree and regardless of the outcome of steps 2-5, because the `"*"` tree — populated by `Mount` or by a route registered directly via `Handle("*", pattern, handler)` — matches a request of any method, including one `methodIdx` does not recognize (see rule 38). If a handler is found there, call it and return.
    7. If `RedirectTrailingSlash` is true, check for a TSR candidate in the `"*"` tree and redirect if found. There is no `RedirectFixedPath` equivalent for the `"*"` tree: step 5 checks `path.Clean` only against the tree found in step 1.
    8. If `HandleOPTIONS` is true and the method is OPTIONS, respond with the Allow header.
    9. If `HandleMethodNotAllowed` is true and other methods are registered at the path, respond with 405.
    10. Call the `NotFound` handler.

### 4.2 Matching Precedence

48. When multiple route types could match a path, they are evaluated in the following order, from highest to lowest priority:
    1. Static routes
    2. Named parameters (including regex parameters)
    3. Catch-all parameters

49. This means `/users/list` matches a static route `/users/list` before a named parameter route `/users/:id`.

### 4.3 Priority Within Named Parameters

50. Within the named parameter category, routes are matched in the order they were registered. Priority counters in the tree cause more frequently matched routes to be checked first. This is an internal optimization and does not change observable matching behavior when patterns are distinct.
51. A regex parameter and a plain named parameter can never occupy the same position in the tree at the same time. The router holds exactly one wildcard child per node; registering the second of the two panics at registration time (rule 71). There is consequently no fall-through from a non-matching regex parameter to a plain named parameter at that position: whichever of the two was registered is the only one that can ever be reached there.

### 4.4 Trailing Slash Redirect (TSR)

52. When `RedirectTrailingSlash` is true and no handler matches the exact path:
    - If the path ends with `/` and a handler exists at the path without the trailing `/`, the router issues a redirect to the path without the trailing `/`.
    - If the path does not end with `/` and a handler exists at the path with a trailing `/`, the router issues a redirect to the path with a trailing `/`.
    - This applies equally when the trailing-slash form is reached only through a catch-all parameter (for example, `/assets` against a registered `/assets/*filepath`) or through a route registered via `Mount` (see [groups.md](groups.md) requirement 28): the trailing-slash form counts as "a handler exists" for this rule exactly like any other registered route.
53. For GET and HEAD requests, the redirect uses status code 301 (Moved Permanently). For all other methods, status code 307 (Temporary Redirect) is used. QUERY is classified under "all other methods" for this purpose: it receives 307 by default even though it is safe and idempotent (RFC 10008 section 2), because MuxMaster does not extend the GET/HEAD 301 treatment to it. This is not a functional loss: unlike 301 or 302, a 307 redirect always preserves both the request method and the request content (RFC 10008 section 2.5), so the historical GET-rewrite behavior that some clients apply to 301/302 redirects of non-GET/HEAD requests is not a concern for the default QUERY redirect. An operator who sets `RedirectCode` to a 301 or 302 value (see [configuration.md](configuration.md) section 4.1) overrides this default for every method, including QUERY, and is responsible for confirming that the resulting client behavior is acceptable.
54. TSR does not apply to the root path `/`.
55. TSR does not apply to CONNECT requests.

### 4.5 Fixed Path Redirect

56. When `RedirectFixedPath` is true and no handler matches the exact path and no TSR applies, the router computes `path.Clean(r.URL.Path)`. If the cleaned path differs from the original and a handler is registered for the cleaned path, the router issues a redirect.
57. For GET and HEAD requests, the redirect uses status code 301. For all other methods, status code 307 is used. The QUERY-specific note in section 4.4, rule 53, applies here without change: QUERY receives 307 by default, preserving both the request method and the request content.

### 4.6 OPTIONS Handling

58. When `HandleOPTIONS` is true and the method is OPTIONS, the router builds the Allow header from all methods registered at the matched path and responds. The response body is empty with status 204 No Content, unless `GlobalOPTIONS` or a per-path OPTIONS handler is configured (see [configuration.md](configuration.md) and [error-handling.md](error-handling.md)). See section 4.7, rule 61, for the exact method order used when building this header, including where QUERY appears in it.
59. If a handler is explicitly registered for `OPTIONS` at a path, that handler takes precedence over the automatic OPTIONS response for that path.

### 4.7 Method Not Allowed

60. When `HandleMethodNotAllowed` is true and the path is registered for at least one method but not the requested method, the router sets the `Allow` header and calls the `MethodNotAllowed` handler (or writes a default 405 plain-text response if `MethodNotAllowed` is nil).
61. The `Allow` header value is a comma-separated list of all methods registered for the path, always including OPTIONS. The methods appear in a fixed order: GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, TRACE, QUERY — restricted to whichever of these are actually registered at the path — followed by OPTIONS last, which always appears regardless of whether an OPTIONS handler was explicitly registered for the path. This order is shared by the automatic OPTIONS response (section 4.6, rule 58) and the 405 response described in this section.

---

## 5. Registration-Time Panics

The following conditions cause a call to the built-in `panic` function at route registration time. None of these are recoverable errors; they indicate a programming mistake.

62. The method string is empty.
63. The pattern does not begin with `/`.
64. The handler is nil.
65. The pattern is already registered for the same method (duplicate route).
66. A wildcard token (`:` or `*`) is not followed by a name.
67. A catch-all parameter is not the last element of the pattern.
68. A catch-all parameter conflicts with an existing handler at the path root segment.
69. A pattern segment contains more than one wildcard token.
70. A regex parameter contains an invalid Go regular expression.
71. A wildcard conflicts with an already-registered wildcard at the same position. This includes a regex parameter registered at the same position as an existing plain named parameter, and a plain named parameter registered at the same position as an existing regex parameter: the tree holds exactly one wildcard child per node, so the second of the two always panics, regardless of which kind was registered first.

---

## 6. Lookup Fallback

72. When a static branch is chosen over an available wildcard sibling (a named parameter, a regex parameter, or a catch-all) at the same tree position, and that static branch does not ultimately lead to a match, the router does not fail the lookup immediately: it falls back to the wildcard sibling and continues matching from there.
73. Example: with `/users/list` and `/users/:id` both registered, `GET /users/listx` and `GET /users/lis` do not match the static route `/users/list` (rule 49 describes precedence, not equality of the strings). The router falls back to `/users/:id`, so both requests match the named parameter route, capturing `id = "listx"` and `id = "lis"` respectively.
74. This fallback can occur at any depth in the tree, and repeats independently at every fork encountered on the way to a match, not only at the first one encountered. There is no limit on how many times a single lookup may fall back.
75. The total work performed by one lookup, counting every branch it tries and abandons, is bounded by the total number of nodes in the registered tree for the requested method. It is never bounded or amplified by the content of the request path itself.
76. This fallback does not change the matching precedence stated in rule 48: a static child is always tried before an available wildcard sibling at each position. The fallback only determines what happens when that static branch fails to produce a match further down the path.
77. A trailing-slash-redirect opportunity (section 4.4) discovered on a static branch that is later abandoned through this fallback is not lost: if the wildcard branch that the router falls back to also fails to match, the redirect opportunity reported by the higher-precedence static branch is the one used.

---

## 7. Static and Wildcard Sibling Registration

78. A static route and a named parameter or regex parameter route may share the same parent position in the tree. Registering the static route before the wildcard route, or the wildcard route before the static route, both succeed and produce an equivalent tree: this registration is legal and order-independent. Rule 48 already establishes that the static route always outranks the wildcard one at match time; this requirement governs registration, not matching.
79. A catch-all route and a static route cannot share the same parent position: registering the catch-all after a conflicting static sibling causes the panic described in rule 68, and registering a static sibling after an existing catch-all at that position causes the panic described in rule 71. This conflict is order-independent: whichever of the two is registered second panics.

---

## 8. Redirect Target Encoding

80. When the router builds the `Location` header value for a trailing-slash redirect (section 4.4) or a fixed-path redirect (section 4.5), any ASCII control byte (0x00-0x1F) or DEL (0x7F) present in the computed target — for example, one that reached the request path through percent-decoding — is percent-encoded before being written to the `Location` header and, for GET requests, into the generated HTML redirect body's link target. Every other byte, including the rest of the path and the query string, is left unchanged. RFC 9110 section 5.5 prohibits raw control bytes in HTTP field values.
81. Aside from this control-byte encoding, the redirect response — status line, headers, and body — is byte-identical to what `net/http.Redirect` produces for the same target and status code.

---

## 9. QUERY Method Semantics

82. `QUERY` is a standard HTTP method, standardized by RFC 10008 (June 2026, https://www.rfc-editor.org/rfc/rfc10008.html). It is a first-class method in MuxMaster: it has dedicated convenience registration methods exactly like GET, POST, and the other methods in section 2.1's table (see that table, and [error-handling.md](error-handling.md) section 5 for `QUERYE`, and [performance.md](performance.md) section 6 for `QUERYFast`). It is one of the eleven tokens `methodIdx` recognizes (section 2.1, rule 31): it is registered and dispatched through the same fixed method table as GET and POST, requires no separate enabling step, and is unaffected by the rules in section 2.3, which govern method strings `methodIdx` does not recognize.
83. The package exports the constant `muxmaster.MethodQuery = "QUERY"`. As of Go 1.27, the standard library's `net/http` package does not define a `MethodQuery` constant (tracked by the Go project as golang/go#80058); MuxMaster's constant fills this gap. If a future Go release adds `http.MethodQuery`, MuxMaster's constant continues to hold the same string value `"QUERY"` and requires no change to code that uses it. See [compatibility.md](compatibility.md) section 6.
84. Per RFC 10008 section 2, `QUERY` is safe and idempotent: sending a `QUERY` request, or sending it more than once, does not modify server state as a consequence of the request itself. Unlike GET or HEAD, a `QUERY` request carries request content (a "query" in the RFC's terminology) in its body, similar to how POST carries a request body. The IANA HTTP Method Registry records QUERY with Safe = yes and Idempotent = yes.
85. `QUERY` is cacheable per RFC 10008 section 2.3, subject to the same HTTP caching rules (RFC 9111) that apply to any cacheable method. MuxMaster does not implement HTTP response caching (see [out-of-scope.md](out-of-scope.md)); response cacheability for `QUERY` requests is the responsibility of the application, a reverse proxy, or a CDN placed in front of the router.
86. The router performs no validation of the `Content-Type` header or the body of a `QUERY` request. RFC 10008 section 2 requires servers to fail the request when the `Content-Type` field is missing or is inconsistent with the request content, and section 2.1 specifies the applicable failure status codes (400, 415, 422). Implementing these checks is the responsibility of the registered handler; MuxMaster dispatches a `QUERY` request to its handler unchanged, exactly as it does for the bodies of POST, PUT, and PATCH requests.
87. The `Accept-Query` response header (RFC 10008 section 3), a Structured Field List advertising the query format(s) a resource accepts, is not set by the router. An application that wants to advertise supported query formats must set this header itself, from a handler or a dedicated middleware.
88. `QUERY` is not a CORS-safelisted method. A cross-origin `QUERY` request triggers a CORS preflight `OPTIONS` request in conforming browsers (RFC 10008 section 4), exactly as a POST request with a non-safelisted `Content-Type` does. This is browser behavior, not router behavior: the `CORS` middleware documented in [middleware-stdlib.md](middleware-stdlib.md) section 10 already handles preflight `OPTIONS` requests generically for any method, including `QUERY`, without requiring any QUERY-specific change to that middleware.
89. See section 4.4, rule 53, and section 4.5, rule 57, for how `RedirectTrailingSlash` and `RedirectFixedPath` apply to `QUERY` requests, and section 4.7, rule 61, for where `QUERY` appears in the `Allow` header.
