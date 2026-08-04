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
// Delivered by slice 2.
package api
