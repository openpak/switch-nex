# switch-nex — the OpenPak Switch NEX lineage

Independently written NEX server for **Switch-era titles** (NEX 4.x: Mario Kart 8 Deluxe,
Super Mario Maker 2, Smash Ultimate, …). Inspired by the operational approach of running
such servers; **not** derived from anyone else's code. Every protocol fact in `SPEC.md`
comes from OpenPak's own wire captures or public protocol documentation.

## Why a separate lineage

Switch-era NEX differs from the Wii U / 3DS era in ways that made sharing one server
framework produce exactly the class of bug this lineage exists to avoid (observed live,
2026-09-14, MK8D 3.0.5 vs the nex-go-based server):

| | Wii U / 3DS (nex-go lineage) | Switch (this lineage) |
|---|---|---|
| Transport | PRUDP over UDP (v0/v1, prudps) | **raw PRUDPLite over TCP/TLS** — no websocket upgrade |
| Response framing | matches read path | must be **unframed** — websocket-framed responses are silently dropped by the client |
| Kerberos | v0 ticket, 16-byte source keys | **v1 ticket**: 32-byte source key, random key in the clear, body RC4(MD5(key ‖ random)) |
| Byte stream | 4-byte PIDs, no structure headers | **8-byte PIDs, structure headers** on every structure |
| Secure connect | console presents the kerberos ticket | MK8D connects **ticketless**; the server recalls the login PID by client IP |
| Pia | — | needs nncs1/nncs2 on **two distinct IPs** or the NAT check never completes |

The nex-go lineage keeps serving the Wii U / 3DS titles (`servers/nex`).

## Layout

- `SPEC.md` — the protocol facts this implementation is built against (verified on our wire)
- `stationurl.go` — StationURL parse/format (`prudp:/k=v;…`)
- `kerberos.go` — Switch v1 ticket derive/encrypt/decrypt
- `result.go` — ValidateAndRequestTicketResult response structure (header + fields)
- `prudplite/` — raw PRUDPLite-over-TLS transport (frame codec, connect state machine, ticketless secure CONNECT with IP→PID recall)
- `rmc/` — packed RMC messages + the Switch byte-stream types (structure headers, strings, buffers, variants)
- `protocols/` — the secure-stage protocol sets with response behaviour matched to the working reference servers, title for title (`docs/legacy-parity.md` is the comparison record)
- `cmd/switch-nexd/` — one binary serving every Switch-era NEX title (per-title registry with protocol sets and behaviour overrides)
- `cmd/mk8d/` — standalone MK8D server (the wire-verification pilot)
- `titles/` — per-title notes (ports, wire facts)
- `docs/legacy-parity.md` — per-title comparison: what the games do against the working reference servers vs what we answer
- `docs/deploy.md` — build + ship + Traefik cutover runbook
- `MIGRATION.md` — lineage split decision and per-title state

## Deployment (current)

Deployed on quilted-waffle since 2026-09-16: `openpak-switch-nexd.service`
(podman quadlet, `localhost/switch-nexd:latest`) serves all 14 Switch-era
titles — auth through Traefik SNI, secure ports published directly. MHGU and
the Wii U / 3DS fleet remain on the nex-go lineage (MHGU speaks raw
PRUDP/UDP, a transport this lineage deliberately does not provide).

## Milestones

1. **MK8D login** — ✅ TLS listener on the auth port, PRUDPLite connect, LoginEx
   (ValidateAndRequestTicketWithCustomData) with the OpenPak BAAS token, correct
   ValidateAndRequestTicketResult response. Verified by MK8D proceeding past login.
2. **Secure stage** — ✅ ticketless CONNECT with IP→PID recall, SecureConnection,
   Utility and the full per-title protocol sets (Reference behaviour parity).
3. **Matchmaking + Pia** — ✅ in-memory gatherings with the measured per-title
   rules; the NAT check itself rides the nncs1/nncs2 responders (two distinct
   IPs). Server-initiated pushes (notifications) are the remaining transport
   item — see `docs/legacy-parity.md` deviations.
