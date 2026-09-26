# ADR-103 — No telemetry is enforced at import, and stops at a subprocess

Status: accepted
Recorded: 2026-09-26, with the implementation

ADR-022 says Feat ships with no telemetry. The public-preview roadmap asks for
that to be checked: a test fails if any package outside the socket and its
client gains a way to reach a network host.

Two checks now make the promise:

1. The depguard rule `network-stays-in-the-socket` denies `net`, `net/http`,
   `net/rpc`, `net/smtp`, `crypto/tls`, `log/syslog` and `golang.org/x/net` in
   every non-test file outside `internal/api`, `internal/client` and
   `internal/daemon`. `net/url` and `net/netip` parse and never dial, so they
   stay allowed.
2. `TestOnlyTheSocketReachesTheNetwork` in `internal/guard` reads
   `go list -deps ./cmd/feat` for darwin and linux. It fails when any
   non-standard package linked into the binary imports one of those packages,
   unless it is a socket package or a reviewed dependency. depguard sees only a
   file's own imports, so a library that phones home would pass it alone.

`spf13/pflag` is the one reviewed dependency. It imports `net` to parse IP flag
values and calls nothing that dials.

`internal/domain` and `internal/config` imported `net` for `JoinHostPort` and
`ParseIP`. They now join the host themselves and parse with `net/netip`, so
the rule can deny `net` everywhere else.

The promise is narrower than "no way to reach a network host":

- A subprocess can reach any host. Feat runs Git, tmux, Docker Compose, `gh`,
  `glab`, `osascript` and user-configured commands through `os/exec`.
  Nothing here constrains what they connect to.
- `syscall` and `golang.org/x/sys/unix` can open a raw socket. Neither check
  looks for one.
- `internal/api`, `internal/client` and `internal/daemon` may import
  `net/http`. `TestNoNetworkListenerOrDial` holds them to Unix-domain
  listeners and dials with a literal network, but an `http.Client` given a URL
  would pass both checks.
- Test files are exempt, because they do not ship.
