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
9. A named parameter matches exactly one segment. It does not match across a `/` character. That segment must also be non-empty; see section 12 for the exact rule and its rationale.
10. The captured value is stored in `Params` with the key equal to `name` (without the leading `:`).
11. Example: the pattern `/users/:id` matches `/users/42` and captures `id = 42`. It does not match `/users/` or `/users/42/posts`. The `/users/` case is a terminal one: the path ends immediately after the parameter's boundary `/`, so there is no candidate segment for the router to even attempt to capture. Section 12 states the distinct, more general rule that also rejects a mid-path empty segment, such as `/users//posts` against `/users/:id/posts`.
12. Named parameter names within a single pattern must be unique.

### 1.4 Catch-all Parameters

13. A catch-all parameter captures the remainder of the path including all `/` characters. It is written as `*name` where `name` is a non-empty identifier.
14. A catch-all parameter must be the last element of the pattern. A pattern such as `/*name/suffix` is invalid and causes a panic at registration time.
15. The captured value includes the leading `/`. Example: the pattern `/static/*filepath` matched against `/static/img/logo.png` captures `filepath = /img/logo.png`.
16. There must be a literal `/` immediately before the `*` token in the pattern.
17. Only one catch-all parameter is permitted per pattern.

### 1.5 Regex Parameters

18. A regex parameter validates and captures one path segment using a regular expression. It is written as `{name:expr}` where `name` is a non-empty identifier and `expr` is a valid Go regular expression (`regexp/syntax` package).
19. Matching fails (the route is not selected) when the segment value does not match `expr`. The router continues to the next candidate. This check never runs against an empty segment value in the first place; section 12 states the rule that rejects an empty segment before `expr` is evaluated, regardless of what `expr` would itself accept.
20. The regular expression `expr` is implicitly anchored. That is, the full segment value must match `expr`, not merely a substring of it.
21. An invalid regular expression in a regex parameter causes a panic at registration time.
22. The captured value is stored in `Params` with the key equal to `name`.
23. Example: the pattern `/users/{id:\d+}` matches `/users/42` and captures `id = 42`. It does not match `/users/abc`.

    See section 11 for the panic raised when a `{` is never closed by a matching `}`; that is a distinct, registration-time malformation from an invalid regular expression (rule 21).

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
35. MuxMaster provides no mechanism to register a custom or extension method. There is no function, or other mechanism, on `*Mux` or `*Group` to declare or register one. The set of recognized method tokens is fixed in the router's source and cannot be extended at runtime. See [out-of-scope.md](out-of-scope.md) section 2.7 for the rationale.
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

    See section 10 for the asterisk-form request target (`OPTIONS *`, RFC 9110 section 9.3.7): under `net/http`'s default server configuration, this sequence never runs at all for that request, because `net/http` answers it before `Mux.ServeHTTP` is called; when it does run, it always falls through every step to step 10.

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

    This automatic OPTIONS response requires a matched path (rules 58 and 60 both key off "the matched path"). It therefore never applies to the asterisk-form request target (`OPTIONS *`), for which no path is ever matched; see section 10.

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

See section 11 for the distinct panic raised when a regex parameter's opening `{` is never closed by a matching `}` at all.

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

---

## 10. Asterisk-Form Request Target (`OPTIONS *`)

90. RFC 9110 section 9.3.7 defines the asterisk-form request target: an `OPTIONS` request whose request-target is the single character `*` rather than a path, used to query the capabilities of a server as a whole rather than of a specific resource. When such a request is parsed, `r.URL.Path` is set to the literal two-byte string `*`. This is the only request-target form MuxMaster ever observes that is not, and cannot become, a path beginning with `/` (contrast section 1.1, rule 1, which requires every registered pattern to begin with `/`).

91. Under `net/http`'s default server configuration — `http.Server.DisableGeneralOptionsHandler == false`, which is the default value when a `*Mux` is served via `http.ListenAndServe` or an unconfigured `http.Server` — an incoming `OPTIONS * HTTP/1.1` request is intercepted and answered by `net/http`'s own internal handler before the registered `http.Handler`, including `Mux.ServeHTTP`, is ever invoked. The response is a bare 200 OK with `Content-Length: 0` and no `Allow` header. Consequently, none of MuxMaster's request handling runs for this request: not pre-routing middleware (see [middleware.md](middleware.md) rule 6), not route lookup, not `GlobalOPTIONS` (see [error-handling.md](error-handling.md) section 4), and not any global, group, or per-route middleware.

92. Setting `http.Server.DisableGeneralOptionsHandler = true` disables that interception. It is the only way for an asterisk-form request to reach `Mux.ServeHTTP` at all. When it does, `r.URL.Path` is the literal string `*`, which cannot equal any registered pattern (rule 1). Consequently, in every configuration of the router, and regardless of which or how many routes are registered:
    - Steps 1-2 of the lookup sequence (section 4.1, rule 47) find no matching route — static, named, regex, or catch-all — for any method: the tree can only be reached through paths beginning with `/`.
    - No trailing-slash redirect (section 4.4) is produced: the path `*` shares no common prefix with any registered pattern for the TSR check to apply to.
    - No fixed-path redirect (section 4.5) is produced: `path.Clean("*")` equals `*`, unchanged, so the "differs from the original" precondition in rule 56 is never satisfied.
    - Step 6 of the lookup sequence, the internal `"*"` method-wildcard tree used by `Mount` (section 2.1, rule 31), is unrelated to the asterisk-form request path even though both use the character `*`: that tree is keyed by the literal HTTP method string `"*"`, not by the request path, and every pattern registered in it must still begin with `/` (rules 1 and 15). It does not special-case, and cannot match, the request path `*`.
    - The automatic OPTIONS response (section 4.6, rules 58 and 59) and the 405 response (section 4.7, rule 60) both require at least one method to be registered at the matched path. Since no path ever matches `*`, neither applies, and `GlobalOPTIONS` (see [error-handling.md](error-handling.md) section 4) is never invoked. No `Allow` header is ever produced for this request.
    - The request falls through to the `NotFound` handler (section 4.1, rule 47, step 10; see [error-handling.md](error-handling.md) section 1), exactly as any other unmatched path does. This holds regardless of the `HandleOPTIONS` setting, and regardless of whether an explicit `OPTIONS` handler is registered elsewhere in the tree.

93. Pre-routing middleware does run for this request when `DisableGeneralOptionsHandler` is `true`, because nothing intercepts the request before `Mux.ServeHTTP` in that configuration; see [middleware.md](middleware.md) rule 6.

---

## 11. Unclosed Regex Parameter Brace

94. A pattern segment that begins with an opening brace `{` but contains no matching closing brace `}` before the next `/` character or the end of the pattern is malformed. Registering such a pattern with `Handle`, `HandleFunc`, `HandleE`, or `HandleFast` (directly on `*Mux`, or through the equivalent `*Group` methods) causes a panic at registration time with the message `muxmaster: regex param '{' in path '<pattern>' is missing its closing '}'`, where `<pattern>` is the exact pattern string that was passed in. Examples of patterns that trigger this panic: `/{` (bare, unclosed, at the root segment), `/a/{id` (named-looking but never closed), and `/x/{id:[0-9]+/y` (the segment `{id:[0-9]+` has no closing `}` before the `/` that starts the next segment; the search for a closing brace does not cross a segment boundary, so the fact that the pattern contains no `}` anywhere after that point is irrelevant — even a pattern like `/x/{id:[0-9]+/y}` would still panic, because the `}` appears in the following segment, not the one containing the unclosed `{`).

95. This panic is distinct from rule 70 (an invalid Go regular expression inside a properly closed `{name:expr}` regex parameter). Rule 70 fires only once a complete `{...}` token has been parsed as a regex parameter; this rule fires when no closing `}` can be found at all within the segment, so no regex parameter is ever parsed and rule 70's check is never reached.

96. Registering a pattern that triggers this panic does not modify the router's existing route tree. Any route registered before the panicking call remains fully intact and reachable by `Lookup` and `Walk`, with the same handler, exactly as before the panicking call; the malformed pattern itself is never added to the tree. This holds even when the malformed pattern would, absent this panic, have been inserted at or reused the very same tree node that an existing, unrelated route already occupies.

---

## 12. Empty Segment Rejection for Named and Regex Parameters

97. Neither a named parameter (section 1.3) nor a regex parameter (section 1.5) ever matches an empty segment value. When the router's descent reaches a named-parameter or regex-parameter wildcard child and the candidate segment — the path text between the `/` that precedes it and the next `/` or the end of the path — has zero length, that candidate does not match, and the router does not select it. For a regex parameter, this check is applied before the regular expression is evaluated at all: a regular expression that would itself accept the empty string (for example, `[a-z]*`) never gets the opportunity to, because the empty segment is rejected first — a regex parameter's empty-segment behavior is identical to a named parameter's in every respect covered by this rule.

98. This holds wherever an empty segment occurs in the request path, not only where the pattern's parameter is its last element. For example, with `/:id/posts` or `/{id:[a-z]*}/posts` registered, a request for `//posts` does not match: the candidate segment for `id` is empty, so rule 97 rejects it before the router ever reaches the `posts` segment that follows.

99. **This is a behavior change relative to earlier releases of MuxMaster.** Previously, a named parameter, or a regex parameter whose expression accepted the empty string, could match a zero-length segment: `//profile` matched `/{id:[a-z]*}/profile`, and `//posts` matched `/:id/posts`, both capturing an empty string as the parameter's value. Neither matches after this fix. The rationale is consistency and correctness, not merely symmetry between the two parameter kinds: one parameter is defined to capture one segment (rule 8), and an empty string is not a meaningful representation of "a segment" reaching a handler as if it were genuine, non-degenerate captured input; allowing it also meant that an incidental double slash in a request path — most often a client or proxy defect, not a deliberate request — was silently reinterpreted as a completed, successfully matched parameter route instead of surfacing as the malformed path it actually is. See [params.md](params.md) rule 37 for the resulting guarantee about every `Param` value produced by ordinary route matching.

100. Rule 11 already establishes that `/users/:id` does not match `/users/` — a *terminal* case: the request path ends immediately after the parameter's boundary `/`, with no further path following. That case does not go through the mechanism in rule 97 at all: the router determines there is no segment to attempt to capture in the first place, because no path remains once the preceding static prefix has been consumed, rather than finding a zero-length segment and rejecting it. Rules 97-98 state the general, position-independent form of the same underlying principle — a named or regex parameter always requires a genuinely non-empty captured segment — reached through this distinct, mid-path mechanism, which rule 11's terminal case does not exercise.

101. When rule 97 rejects a candidate and no other route matches the request, the request falls through to the ordinary lookup sequence (section 4.1, rule 47), exactly as any other unmatched path does; rule 97 introduces no special-cased outcome of its own.
     - A trailing-slash redirect (section 4.4) does not apply merely because rule 97 rejected a candidate: it applies only under its own, independent condition — a registered handler exists at the request path with its trailing `/` added or removed — which an empty-segment rejection does not, by itself, create. For example, a request for `//profile` against only `/{id:[a-z]*}/profile` produces no trailing-slash redirect, because neither `//profile/` nor a path one `/` shorter is a registered handler.
     - A fixed-path redirect (section 4.5) applies only when `RedirectFixedPath` is `true` and a handler is registered for `path.Clean` of the request path. `path.Clean("//profile")` is `/profile`. With only `/{id:[a-z]*}/profile` registered and no separate route at `/profile`, a request for `//profile` produces a 404 response (the `NotFound` handler) regardless of `RedirectFixedPath`, because no handler exists at `/profile` either. If a route is additionally registered directly at `/profile`, the same `//profile` request, with `RedirectFixedPath` `true`, does redirect to `/profile` — this is the ordinary fixed-path mechanism operating exactly as section 4.5 already describes it, not a special case introduced by this section.
     - With the default configuration (`RedirectTrailingSlash` `true`, `RedirectFixedPath` `false`) and no separate route registered at the cleaned path, a request that rule 97 rejects therefore ends in a plain 404, with no redirect of any kind.
