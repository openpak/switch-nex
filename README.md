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
| Transport | PRUDP over UDP (v0/v1, prudps) | **PRUDPLite over TCP/TLS**, bare or inside a websocket — the client chooses (see SPEC.md) |
| Response framing | matches read path | must **match the client**: framed to one that upgraded, bare to one that did not — a mismatch is dropped silently |
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
- `cmd/mk8d/` — standalone MK8D server (live)
- `titles/` — per-title notes (ports, wire facts)
- `docs/legacy-parity.md` — per-title comparison: what the games do against the working reference servers vs what we answer
- `docs/deploy.md` — build + ship + Traefik cutover runbook
- `MIGRATION.md` — lineage split decision and per-title state

## Deployment (current)

Deployed on quilted-waffle since 2026-09-16: `openpak-switch-nexd.service`
(podman quadlet) serves all 14 Switch-era titles — auth through Traefik SNI,
and since 2026-09-17 with `Network=host`, so the secure ports are bound by
the server itself and clients arrive with their real addresses. Each title
also answers `GET /healthz` on its base+2 (e.g. `curl :22152/healthz` →
`ok mario-kart-8-deluxe auth=22150/tls secure=22151/tls`); 200 only while
both of its listeners are up, 503 `down …` otherwise. Ports per title:
`docs/ports.md` or `switch-nexd -dump`. MHGU and
the Wii U / 3DS fleet remain on the nex-go lineage. MHGU is a hybrid — wss auth
like this lineage, but a raw PRUDP/UDP secure stage and a kerberos v0 ticket,
neither of which this lineage provides — and it shares its NEX code, and its
access key, with Pretendo's Monster Hunter XX server, which is on nex-go.

## SMB35 gameplay

Super Mario Bros. 35 additionally uses the [Eagle gameplay relay](eagle/README.md)
on TLS/WebSocket port 22163. Its lobby issues authenticated player-specific
handoffs when `SMB35_EAGLE_URL` is configured. The relay handles node membership,
shared counters and game-packet routing in the same process as the title's NEX
server. The generated SMB35 quadlet enables it; other titles do not.

## Bans

An account banned in the OpenPak admin is refused at login with `RendezVous::AccountDisabled`
and its secure connections are dropped within a minute; nx-baas's
`/internal/switch/identity` (403 `account_banned`) is the source. Nothing to configure beyond
the identity URL and key.

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
