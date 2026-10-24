# Tetris 99 — 2026-10-24

Tetris 99 (`010040600C5CE000`, update 2.6.0) is registered in `cmd/switch-nexd` as
`tetris-99` on port block 23150 (auth 23150, secure 23151, healthz 23152), Eagle relay on
23153. **Measurement entry, not a working profile.**

| Title | Application ID | Update | Game server ID | Public PRUDP access key | SDK |
| --- | --- | --- | --- | --- | --- |
| Tetris 99 | `010040600C5CE000` | 2.6.0 | `23BDA200` | `cdd6114d` | NEX 4.6.9 (MM, UT), Pia 5.47.0, libeagle (other lineage) |

- The game server ID occurs once as a little-endian literal and the key once as a string in
  the decompressed `main`; both match Kinnay's public Game Server List.
- Its libeagle is not SMB35's: a WebSocket driver class and wslay, no `ParseUrl` messages,
  a different build workspace. Kinnay's public *Eagle Protocol* page documents the original
  Tetris 99 as libeagle 1.x (9-bit nodes, 128-node bitmap, protocol 2, a different node
  notice flow), which this relay does not implement.
- The login request's header is three bytes for both node widths, so the relay's
  `Tetris99` profile can log the client's login fields (`TETRIS99_EAGLE_URL`,
  `TETRIS99_EAGLE_LISTEN`); readiness and routing still use the 2.x framing until the wire
  is measured. Overrides: `TETRIS99_SOURCE_KEY_BYTES`, `TETRIS99_INTEGER_SETTINGS`.
