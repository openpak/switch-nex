# Eagle v2 / SMB35 provenance

This Go implementation was written independently from protocol facts below.
A compatible AGPL reference was inspected
for behavior; its code, comments and file structure are not vendored here.

| Fact | Primary source |
| --- | --- |
| MSB-first bit stream; 2-bit relay kind, 8-bit opcode, 11-bit node ID; destination node or 1024-bit bitmap; payload aligned to a byte | [Eagle Protocol](https://github.com/kinnay/NintendoClients/wiki/Eagle-Protocol), packet format and library versions |
| Accepted node ID/time, two login phases, fragmented token, login result, readiness, active-node bitmap, join/leave notices, ping/pong and RPC timestamps | Same public protocol, payload formats and protocol description |
| SMB35 v2.0.4, protocol=3, app protocol=0, DDL hash=0; login success=1 | [kinnay/SMB35](https://github.com/kinnay/SMB35), `source/eagle.py` (AGPL-3.0 compatible), known working retail-client reference |
| Shared counters 0–31; request RPCs 17/18/19, reply RPC 16 (index, u64 value, u16 node=0) | [Eagle RPC](https://github.com/kinnay/NintendoClients/wiki/Eagle-RPC), cross-checked against the compatible reference |
| Game messages 26–40 are relayed opaquely, including client-generated JoinMatch / AcceptMatch / StartMatch and subsequent input/attacks/deaths | [SMB35 Eagle RPC](https://github.com/kinnay/NintendoClients/wiki/Eagle-RPC-(SMB35)); compatible reference's relay routes them without game simulation |
| Handoff is notification 200000, source principal 257049437023956657, Param1=gathering; revision-1 map `url` and `token` | Eagle Protocol notification description; compatible reference `source/main.py`; Pretendo's AGPL notification type for the revision-1 map framing |
| Ticket is base64 JSON version=1, containing a payload and base64 HMAC-SHA256 signature; payload strings contain expiry, lp1, gathering ID in decimal, player PID in 16 hexadecimal digits | Compatible reference `source/main.py` / `source/eagle.py`, cross-checked with public Eagle token documentation. Decimal gathering IDs are the reference's replacement-server convention; retail tokens use a different server ID format. |
| Referee StartRound parameters: u32 personal category, u32 gathering, list<u64> principals, u8 report mode, u32 event ID | NintendoClients' MIT `nintendo/nex/matchmaking_eagle.py` |
| Eagle round-start notification reaches all participants (initiator included) and names the round ID in Param1; detailed participants have decimal PID names; session initially counts its creator | Compatible reference `source/main.py` |
| Each player's EndRound/partial report acknowledges the existing round without deleting shared parameters; the winner can report after eliminated players | Own production journal, 2026-10-12 08:57:07–08:57:08 UTC: first EndRound succeeds, next fails with `0x006F0007`; owner confirms the first-place device fails in either role. Compatible AGPL reference `source/main.py` retains rounds after EndRound and partial reports. |
| Current missing-handoff symptom: matched players, messages relayed to both, CloseParticipation after countdown, no gameplay | Own production journal, 2026-10-12 08:09–08:14 UTC; owner Switch/Harbor test |

OpenPak decisions: the relay is a separate TLS listener in the SMB35 process,
sharing NEX gathering lifetime and an ephemeral random signing key. NEX membership
is the admission authority. Departures revoke tickets and close matching peers;
gathering destruction closes the room. The ticket signature covers the exact JSON
payload bytes issued by this process. Relay headers are restamped with the actual
source node; clients cannot impersonate another node. Unspecified server RPCs are
reported without fabricated replies. Other titles do not enable Eagle.

The sole transport dependency is `github.com/coder/websocket` v1.8.15, ISC licensed;
its notice is retained in `licenses/websocket-ISC.txt` and the runtime image.
Protocol tests use independently encoded golden packets and two real WebSocket
clients. The NEX integration test verifies player-specific map-bearing handoffs
and the Eagle referee's round notification and independent player reports.
The owner confirmed retail Switch/Harbor gameplay on v0.3.30; the subsequent
first-place reporting error is fixed in v0.3.31 and awaits a console retest.
