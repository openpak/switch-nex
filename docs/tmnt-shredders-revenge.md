# TMNT: Shredder's Revenge — 2026-10-24

Teenage Mutant Ninja Turtles: Shredder's Revenge (`0100FE701475A000`, update v589824) is
registered in `cmd/switch-nexd` as `tmnt-shredders-revenge` on port block 23130. NEX serves
only its leaderboards. Online co-op uses Epic Online Services (lobbies and P2P), which
`servers/eos` serves, not this repository. This is an initial profile, not yet verified
in a game.

## Client facts and provenance

The source is the owner's updated client executable. Only sanitized constants and
instruction observations are recorded here.

| Title | Application ID | Update | Game server ID | Public PRUDP access key | SDK |
| --- | --- | ---: | --- | --- | --- |
| TMNT: Shredder's Revenge | `0100FE701475A000` | 589824 | `20A95200` | `vLOlmxdM` | NEX 4.6.6, no Pia |

- Same-call login pairing in the decompressed `main`: `mov w1, #0x5200` /
  `movk w1, #0x20a9, lsl #16` at `0x14fd1e8`/`0x14fd1ec`, the key pointer loaded into `x2`
  just before (a relocated data slot pointing at the string `vLOlmxdM`), `w3 = 60000`, then
  the login call.
- **Console-confirmed 2026-10-24 11:21 UTC:** a retail Switch dialled
  `g20a95200-lp1.s.n.srv.nintendo.net` with a websocket upgrade. The name was unrouted, so the
  attempt ended at the nx-baas catch-all 404, which the game shows as **2306-0502**
  when Leaderboards is opened. In the same session aauth accepted version `00090000`.
- Linked NEX protocol clients: `Ranking2`, `TicketGranting` and `SecureConnection`. No
  MatchmakeExtension, NATTraversal, DataStore or Pia.

## Profile

- `prudps` secure station URLs.
- `Ranking2 = true`. No matchmaking store: the game never matchmakes over NEX.
- Ports: auth 23130, secure 23131, healthz 23132.

## Open

- First routed boot: CONNECT signature check with the key, and which Ranking2 methods the
  leaderboard screen calls (the NotImplemented log).
