// Package api provides the HTTP/JSON handlers, wire DTOs, and Server-Sent
// Events stream served over the Unix-domain socket.
//
// The API is versioned from the beginning under /v1 but is not promised stable
// before v1. Endpoints are listed in docs/06-technical-architecture.md.
//
// Rules this package must enforce:
//
//   - wire DTOs are distinct from domain types, so the domain can evolve
//     without breaking clients;
//   - SSE carries domain state changes, never raw terminal streams or secrets;
//   - destructive requests take task and resource IDs plus a server-produced
//     cleanup-plan token, never arbitrary filesystem paths;
//   - every request is validated for path, capability, size, and task
//     ownership before it reaches an orchestration service.
//
// The payload types are a third representation, separate from the domain
// entities and from the documents in internal/store/fs, for the reason ADR-026
// separated the first two: renaming a Go field must not silently change a
// published surface. Golden files pin the response bodies.
//
// This package holds no persistent state and reaches for no adapter. It declares
// the Service interface it needs and the error vocabulary it translates, and the
// daemon implements them; the dependency points that way so that the transport
// can be tested without a daemon and the daemon can build this handler without
// an import cycle.
package api
