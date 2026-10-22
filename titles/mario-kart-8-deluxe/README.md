# Mario Kart 8 Deluxe — switch-nex live

> **Current Switch client (2026-10-13): Harbor**, at
> `~/REPOS/Openpak/emulators/harbor`. Harbor is an independent
> OpenPak project based on Eden. Citron, Ryujinx (desktop and Android), and
> the former OpenPak Eden checkouts have been removed. References to those
> forks below record historical evidence only: do not run their commands,
> reinstall them, or select them for current work. Use Harbor's README and
> current handoff for builds and testing; old results do not establish Harbor parity.

Plays online: four full races console ↔ Ryujinx, 2026-09-20, on switch-nexd
v0.2.21. `live` in the catalogue (row id 4).

Port base 22150 (auth +0, secure +1, healthz +2). Station URL address:
the server's public IP (raw IP — the console dials station URLs without DNS).

Wire facts specific to this title (SPEC.md): speaks raw PRUDPLite over
TLS-TCP; LoginEx = TicketGranting 0x0A method 0x02 with a 1337-byte parameter
block carrying the full BAAS id token; expects the ValidateAndRequestTicket
Result structure (v1 header, 8-byte PID); secure stage is a ticketless CONNECT
matched by client IP.

Parity notes (docs/legacy-parity.md): matchmaking trio + Ranking
(scores, common data), served by `cmd/switch-nexd` (per-title registry) or
standalone via `cmd/mk8d`.
