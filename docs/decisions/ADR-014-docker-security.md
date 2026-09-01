# ADR-014 — Docker security

Status: accepted

The dogfood profile's agent is non-root and receives no Docker socket/CLI. A normal devcontainer is not claimed to resist deliberate kernel/runtime exploitation.
