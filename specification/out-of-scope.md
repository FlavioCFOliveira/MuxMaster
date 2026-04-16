# Out of Scope

## Scope

This file lists features and capabilities that will never be implemented in MuxMaster, along with the reason for each exclusion.

The purpose of this file is to prevent repeated requests for these features and to document the deliberate constraints that define MuxMaster's identity.

A feature listed here must not be added to any other specification file without first removing it from this list and documenting the decision to change scope.

---

## 1. Protocol-Level Exclusions

### 1.1 HTTP/3 (QUIC)

MuxMaster will not implement HTTP/3 support. HTTP/3 is built on QUIC, which requires the `quic-go` external library. Introducing a dependency of this size and complexity violates the zero-external-dependencies principle. Applications that need HTTP/3 should place a reverse proxy (e.g., Caddy, Nginx, Cloudflare) in front of the MuxMaster server.

### 1.2 WebSocket

MuxMaster will not provide WebSocket upgrade handling. WebSocket is a protocol upgrade negotiated over HTTP but is not a routing concern. It is outside the scope of a request multiplexer. Applications should use `golang.org/x/net/websocket` or `github.com/gorilla/websocket` alongside MuxMaster; both work with any `http.Handler`.

### 1.3 gRPC

MuxMaster will not support gRPC. gRPC uses HTTP/2 framing and binary protobuf encoding. It is a fundamentally different protocol from HTTP REST and requires a separate server implementation. Use `google.golang.org/grpc` independently.

### 1.4 fasthttp

MuxMaster will not support the `fasthttp` server. `fasthttp` uses incompatible `*fasthttp.RequestCtx` types instead of `http.ResponseWriter` and `*http.Request`. Supporting both would require two parallel codebases or interface indirection that violates the performance-first principle.

---

## 2. Framework Capabilities

### 2.1 Template Engine

MuxMaster will not include a template engine or rendering layer. Go's standard library includes `html/template` and `text/template`. Template rendering is an application responsibility.

### 2.2 ORM and Database Access

MuxMaster will not include database access or ORM functionality. This is firmly in framework territory. MuxMaster is a router, not a framework.

### 2.3 Dependency Injection

MuxMaster will not provide a dependency injection container. Dependency injection is an application architecture concern, not a routing concern.

### 2.4 Struct Validation

MuxMaster will not include struct validation (e.g., binding and validation of request bodies into typed structs). This is the responsibility of the application or a dedicated library.

### 2.5 Session Management

MuxMaster will not provide session management. Session state is application state, not routing state.

### 2.6 Authentication and Authorization Logic

MuxMaster will not provide authentication or authorization engines. The `BasicAuth` middleware in [middleware-stdlib.md](middleware-stdlib.md) is a narrow, low-level HTTP primitive. Full auth systems (OAuth, JWT, RBAC) are out of scope.

---

## 3. Operational Concerns

### 3.1 Hot Reload of Routes

MuxMaster will not support registering or removing routes after the server has begun serving requests. Allowing runtime route mutation would require either a global write lock on every request (unacceptable performance cost) or a lock-free tree structure (significant complexity). The current design registers all routes at startup and then serves them read-only. This is the correct model for the vast majority of Go HTTP servers.

### 3.2 Graceful Shutdown

MuxMaster will not provide a graceful shutdown wrapper. `http.Server.Shutdown` in the standard library handles graceful shutdown correctly. Wrapping it adds no value and would duplicate stdlib functionality.

### 3.3 Metrics and Tracing Integration

MuxMaster will not include built-in Prometheus metrics, OpenTelemetry tracing, or any monitoring framework integration. These integrations are application concerns and can be implemented as middleware using the `RoutePattern` function for cardinality-safe labeling.

### 3.4 Server-Sent Events

MuxMaster will not provide built-in SSE support. SSE is a response pattern, not a routing feature. Standard `http.Flusher` interface support works with MuxMaster handlers without any router-level changes.

---

## 4. Encoding and Content Negotiation

### 4.1 MessagePack and Protocol Buffers

MuxMaster will not provide response helpers for binary encoding formats. The standard library does not include MessagePack or Protocol Buffer support. Adding such helpers would require external dependencies.

### 4.2 Automatic Content Negotiation

MuxMaster will not implement automatic content negotiation (selecting a response format based on the `Accept` header). This is application logic, not routing logic.

---

## 5. Scope Boundaries for the middleware Sub-Package

The `muxmaster/middleware` sub-package provides general-purpose HTTP middleware. The following are explicitly out of scope for that sub-package:

- Rate limiting backed by external stores (Redis, Memcached). The `ThrottleBacklog` middleware is in-process only.
- OAuth2 or JWT middleware.
- CSRF protection middleware (requires session state or signed tokens, which is application territory).
- Prometheus middleware (would require importing the Prometheus client, violating zero-dependency).
- OpenTelemetry middleware (same reason).
- IP allowlist/denylist middleware.
