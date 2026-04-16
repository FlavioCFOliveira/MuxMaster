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

30. The following HTTP methods have dedicated convenience registration methods on `*Mux` and `*Group`:

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

31. Any HTTP method string (including custom methods such as `PURGE` or `PROPFIND`) can be registered via `Handle(method, pattern, handler)` or `HandleFunc(method, pattern, handler)`.

### 2.2 ANY and Match

32. `ANY(pattern, handler)` registers `handler` for every standard HTTP method: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS, CONNECT, and TRACE. Each registration is independent. A subsequent call to `GET(pattern, other)` panics because GET is already registered for that pattern.
33. `Match(methods []string, pattern string, handler http.Handler)` registers `handler` for each method in `methods`. The same panic-on-duplicate rule applies.

### 2.3 Custom Methods

34. `RegisterMethod(method string)` declares a new custom HTTP method. After this call, `Handle(method, ...)` is valid for that method. The method string must be non-empty and must be a valid HTTP token as defined in RFC 9110. An empty or invalid method string causes a panic.
35. Custom methods do not receive convenience shorthand methods.
36. `ANY` does not include custom methods. Custom methods must be registered individually.

### 2.4 Method String Validation

37. An empty method string passed to `Handle`, `HandleFunc`, or `Match` causes a panic.
38. Method strings are case-sensitive. `get` and `GET` are treated as different methods.

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
    1. Look up the route tree for the request method.
    2. Traverse the tree using the request's URL path (`r.URL.Path`).
    3. If a handler is found, call it and return.
    4. If `RedirectTrailingSlash` is true, check for a TSR candidate and redirect if found.
    5. If `RedirectFixedPath` is true, check whether `path.Clean` of the request path has a handler and redirect if found.
    6. If `HandleOPTIONS` is true and the method is OPTIONS, respond with the Allow header.
    7. If `HandleMethodNotAllowed` is true and other methods are registered at the path, respond with 405.
    8. Call the `NotFound` handler.

### 4.2 Matching Precedence

48. When multiple route types could match a path, they are evaluated in the following order, from highest to lowest priority:
    1. Static routes
    2. Named parameters (including regex parameters)
    3. Catch-all parameters

49. This means `/users/list` matches a static route `/users/list` before a named parameter route `/users/:id`.

### 4.3 Priority Within Named Parameters

50. Within the named parameter category, routes are matched in the order they were registered. Priority counters in the tree cause more frequently matched routes to be checked first. This is an internal optimization and does not change observable matching behavior when patterns are distinct.
51. Regex parameters are evaluated before non-regex named parameters at the same position. If a regex parameter does not match, the router falls through to the non-regex named parameter at the same position.

### 4.4 Trailing Slash Redirect (TSR)

52. When `RedirectTrailingSlash` is true and no handler matches the exact path:
    - If the path ends with `/` and a handler exists at the path without the trailing `/`, the router issues a redirect to the path without the trailing `/`.
    - If the path does not end with `/` and a handler exists at the path with a trailing `/`, the router issues a redirect to the path with a trailing `/`.
53. For GET and HEAD requests, the redirect uses status code 301 (Moved Permanently). For all other methods, status code 307 (Temporary Redirect) is used.
54. TSR does not apply to the root path `/`.
55. TSR does not apply to CONNECT requests.

### 4.5 Fixed Path Redirect

56. When `RedirectFixedPath` is true and no handler matches the exact path and no TSR applies, the router computes `path.Clean(r.URL.Path)`. If the cleaned path differs from the original and a handler is registered for the cleaned path, the router issues a redirect.
57. For GET and HEAD requests, the redirect uses status code 301. For all other methods, status code 307 is used.

### 4.6 OPTIONS Handling

58. When `HandleOPTIONS` is true and the method is OPTIONS, the router builds the Allow header from all methods registered at the matched path and responds. The response body is empty with status 204 No Content, unless `GlobalOPTIONS` or a per-path OPTIONS handler is configured (see [configuration.md](configuration.md) and [error-handling.md](error-handling.md)).
59. If a handler is explicitly registered for `OPTIONS` at a path, that handler takes precedence over the automatic OPTIONS response for that path.

### 4.7 Method Not Allowed

60. When `HandleMethodNotAllowed` is true and the path is registered for at least one method but not the requested method, the router sets the `Allow` header and calls the `MethodNotAllowed` handler (or writes a default 405 plain-text response if `MethodNotAllowed` is nil).
61. The `Allow` header value is a comma-separated list of all methods registered for the path, always including OPTIONS.

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
71. A wildcard conflicts with an already-registered wildcard at the same position.
