# Eagle gameplay relay (SMB35, PAC-MAN 99)

`switch-nexd` serves this on a separate TLS/WebSocket port when SMB35 has
`SMB35_EAGLE_URL` set. The default listener is `:22163` (override with
`SMB35_EAGLE_LISTEN`). Its URL must be an absolute `wss://` URL reachable by the
console. The room's gathering ID is appended as the path. The existing NEX leaf
certificate covers the SMB35 hostname and production IP; no new console patch,
DNS entry or signing key is needed.

The generated SMB35 quadlet configures:

```ini
Environment=SMB35_EAGLE_LISTEN=:22163
Environment=SMB35_EAGLE_URL=wss://g21f12900-lp1.s.n.srv.nintendo.net:22163
```

Admit TCP 22163 in the host firewall, before its REJECT, and persist the rule.
Include 22163 in the quadlet's `openpak-ports` marker so drain updates also see
active gameplay connections. `GET /healthz` on that TLS port returns
`ok eagle smb35`. The ordinary NEX health port remains 22162.

NEX creates the room and issues individual signed tickets at create/join. Clients
perform Eagle v2 login and announce readiness. The relay announces node membership,
answers ping/pong and the 32 shared counter RPCs, and forwards opaque SMB35 game
RPCs to their named recipients. It does not run a copy of the game. NEX departure
revokes admission; a destroyed gathering closes all its relay connections.

Each room admits up to 35 players. Full-room tests cover authenticated node
membership, match-start broadcast, shared counters and bitmap-addressed game
updates. The Eagle referee retains shared round parameters for every player's
end report, including the winner reporting last; new rounds prune entries older
than one hour. Switch/Harbor gameplay is owner-confirmed; the v0.3.31 reporting
fix still needs the owner's end-of-round retest.

Admission tickets expire after three hours; membership can be revoked immediately.
Rooms, participants, token fragments, packet sizes and
send queues are bounded. A slow peer is disconnected without blocking a room.
Logs omit tokens, principals, names, client addresses and game payloads.

The relay supports Eagle v2 over WebSocket/TLS through per-title profiles:
SMB35 (2.0.4, 35 players, login fields enforced) and PAC-MAN 99 (2.0.5, 99 players,
`PACMAN99_EAGLE_URL` / `PACMAN99_EAGLE_LISTEN`, default `:23143`; login fields logged
until measured, see [../docs/pac-man-99.md](../docs/pac-man-99.md)). It does not claim
support for Tetris 99's protocol, F-Zero 99, KCP or plain TCP. Wire sources and test limits are in [provenance.md](provenance.md).
