# Protocol: switch-nex

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


## Switch-era NEX vs Wii U / 3DS

Independently written; every protocol fact in [SPEC.md](../SPEC.md) comes from OpenPak's own wire
captures or public protocol documentation. It is a separate lineage from nex-go because sharing
one framework produced exactly this class of bug (observed 2026-09-14, MK8D 3.0.5):

| | Wii U / 3DS (nex-go lineage) | Switch (this lineage) |
|---|---|---|
| Transport | PRUDP over UDP (v0/v1, prudps) | PRUDPLite over TCP/TLS, bare or inside a websocket; the client chooses (see SPEC.md) |
| Response framing | matches read path | must match the client: framed to one that upgraded, bare to one that did not; a mismatch is dropped silently |
| Kerberos | v0 ticket, 16-byte source keys | v1 ticket: 32-byte source key, random key in the clear, body RC4(MD5(key + random)) |
| Byte stream | 4-byte PIDs, no structure headers | 8-byte PIDs, structure headers on every structure |
| Secure connect | console presents the kerberos ticket | MK8D connects ticketless; the server recalls the login PID by client IP |
| Pia | n/a | needs nncs1/nncs2 on two distinct IPs or the NAT check never completes |

The nex-go lineage keeps serving the Wii U / 3DS titles (`platforms/wiiu-3ds/games/nex`).
