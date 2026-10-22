# Titles — per-game configuration for the switch-nex lineage

One directory per migrated title. The runtime (`cmd/switch-nexd`) carries the
per-title protocol sets and behaviour overrides as code — see
`cmd/switch-nexd/main.go` for the registry and `docs/parity.md` for
the per-title comparison against the measured server implementations. The shared
transport, kerberos, RMC and result code live in the module root (SPEC.md for
the wire facts).

Live: `mario-kart-8-deluxe/` (port base 22150, wire-verified end to end).

Ports follow `ports.md` NEX blocks: auth = base, secure = base+1 (the station
URL carries base+1; the console dials it directly), healthz = base+2.
