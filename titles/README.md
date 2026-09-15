# Titles — per-game configuration for the switch-nex lineage

One directory per migrated title. The runtime (cmd/*) reads a title config and
registers that title's protocol set; the shared transport, kerberos, RMC and
result code live in the module root (see SPEC.md for the wire facts).

Pilot: `mario-kart-8-deluxe/` (port base 22150, the only title whose online
sequence we have wire-verified end to end — SPEC.md).

Ports follow `ports.md` NEX blocks: auth = base, secure = base+1 (the station
URL carries base+1; the console dials it directly), healthz = base+2.
