# Changelog — switch-nex

## 2026-09-16 — Clubhouse Games: 51 Worldwide Classics

- New title `clubhouse-games-51` (010047700D540000, game server 2035BB00) on
  port block 22870 (allocated from the ports.md pool), access key `8c3661ff`.
  First title whose reference facts were taken straight from a public
  earlier network repo — provenance in `docs/legacy-parity.md`.
- `protocols/`: new `DSClubhouse` DataStore mode (GetPersistenceInfos answers
  empty + NotFound per slot — the "nothing saved yet" signal the game's
  account setup needs; GetMeta NotFound; everything else a bare empty list),
  plus two Env knobs: `FinalizeCreateSession` (create-time normalisation:
  bound, host-counted, 32-byte key, start time, active state, padded
  attributes, `@SR`/`@GIR` params) and `JoinEchoSession` (joins answer the
  full session with the pre-join participation count). The title also rides
  the existing forced-open-participation fix.
- Tests: create normalisation + browsability, join echo (new joiner hears the
  pre-join count, store counts the join, rejoin does not double-count), and
  the three DataStore answers.
- Deployed to quilted-waffle 2026-09-16: image rebuilt from the shipped
  arm64 binary, quadlet publishes 22871, Traefik SNI route
  `g2035bb00-lp1.s.n.srv.nintendo.net` → `switch-nexd:22870` (delta backup
  `dynamic.before-clubhouse-20260916.yml`), nftables ACCEPT + rules.v4
  persisted for 22871. Verified: 15/15 listeners, edge SNI handshake serves
  the `*.s.n.srv.nintendo.net` leaf, secure port 22871 reachable externally.
- Firewall closure: the cutover had left every switch-nexd secure port
  except MK8D's blocked at the host REJECT (auth via 443/SNI worked, secure
  direct-dial did not). One multiport ACCEPT for
  22111,22131,22141,22161,22171,22181,22191,22301,22311,22321,22331,22341,22351
  now sits before the REJECT (rules.v4 persisted); all 15 secure ports
  verified externally serving the OpenPak leaf.

## 2026-09-16 — deployed to quilted-waffle (all titles)

- `openpak-switch-nexd.service` quadlet + `localhost/switch-nexd:latest`
  image; Traefik SNI re-pointed for 13 titles (MHGU restored to its nex-go
  container); nex-go Switch-title services stopped and disabled.
- Verified: 14/14 listeners, 13/13 SNI handshakes through the edge with the
  OpenPak-issued leaf cert.
- Known gap carried over: minecraft has no SNI router in dynamic.yml (its
  game-server hostname was never recorded) — auth via the edge is unreachable
  for it until the hostname is identified and a router added.

## 2026-09-15 — earlier parity for every Switch-era NEX title

- `protocols/` new package: SecureConnection, Utility (+AcquireTagId /
  UpdateCurrentUser), NAT traversal, Notification, Messaging/MessageDelivery,
  the matchmaking trio (0x15/0x32/0x6D) over a real in-memory gathering store
  with a full NEX4 MatchmakeSession codec, Ranking (scores + common data),
  SMB35's Ranking2 + MatchmakeReferee (round store, raw param echo), and LM3's
  0x79 presence stub. Response behaviour matched to the measured reference
  answers per title — `docs/legacy-parity.md` is the comparison record.
- `cmd/switch-nexd` serves 14 titles (all Switch-era NEX except MHGU, whose
  UDP transport stays on nex-go), with per-title protocol sets, MPS detail-
  find sanitisation, the MTA open-participation fix, ACNH 0x2C codeword
  updates, SMB35's 17-entry settings table / 16-byte source key / retail
  secure PID, and auth-stage TicketGranting methods 0x02 and 0x06.
- `rmc` gained the Switch byte-stream types (structure headers, strings,
  buffers, variants, result codes).
- Tests: session byte round-trip + header-framing fidelity, create→find
  flows, sanitisation and session-key preservation, SessionVoid/RoundNotFound
  errors, referee lifecycle, RegisterEx shape, 0x79 stub, SMM2 GetUsers shape,
  SMB35 settings table.

## 2026-09-15 — initial import

## 2026-09-16 — reference parity pass

Closed all 43 findings from `docs/legacy-gap-report.md`.

Login and connection setup:
- `ValidateAndRequestTicketWithParam` (0x0A/0x06) now answers its own structure
  instead of the LoginEx body with a header on it — no retval, and the secure
  endpoint as a plain URL string plus the server time. The old shape was
  rejected as an invalid argument by every NEX 4.4+ title.
- `Login` (0x0A/0x01) shares the LoginEx answer.
- The login special-station field renders as `prudp:/`, not the bare word.
- `SecureConnection::Register` (0x0B/0x01) answers the same three fields as
  RegisterEx, and both hand back the CLIENT's public station — derived from the
  stations it reported, stamped with its PID and connection id — rather than our
  own secure address, which Pia cannot recognise as itself.
- Connection ids are per connection; they were all 1.
- `ReplaceURL` stores the re-reported station instead of discarding it.
- Public-station `type` and `Pa` follow each title's Pia version, and the
  station scheme is per title (`prudps` for MPS, Golf, ARMS, SMO).
- The IP-to-PID recall table survives a restart (`-recall-file`).

Wire fixes:
- `DataStore::NotFound` went out as 0x80069004; the measured value is
  0x80690004. The constant had two digits transposed.
- Ranking2 common data uses QBuffers for the Mii and binary blobs, and a
  zero-length string for an unnamed player.
- Ranking2 category settings write the reset month as a u16.
- `GetEstimateMyRank` is method 11, not 10.
- `GetCommonData` for an unknown player answers an empty structure, not an error.
- Referee `EndRoundWithoutReport` is method 5; method 4 is the partial report.
- Utility unique ids derive from the PID and answer a framed structure on
  methods 2 and 5.
- Integer settings and matchmake parameters are written in key order; Go's map
  iteration made both non-deterministic between calls.
- RMC protocol ids from 0x80 up escape to a u16.

New behaviour:
- Server-initiated pushes: participation, disconnect, ownership, host change,
  gathering teardown, referee round start, and the NAT `InitiateProbe` relay.
- `GetRelaySignatureKey` answers a well-formed "no relay offered".
- NAT properties are classified and incompatible peers are not seated together.
- Host migration on 0x15 (0x24/0x28/0x2A/0x2B/0x2C) and when an owner leaves.
- Room and arena codes (0x37/0x38/0x44/0x45) and tournaments (0x39/0x3F/0x42/0x43).
- `CustomGetSimplePlayingSession` (0x6D/0x3C) resolves a friend's gathering; it
  answered an empty list, which is why friend-joins failed.
- `FindMatchmakeSessionByParticipant` parses its structure header, and serves
  real results only for the title that uses it.
- Ranking 0x70 serves the reference's method set, including common-data-by-PIDs.
- Mario Strikers' bring-up shape table on 0x6E/0x73/0x78/0x79/0x7A.
- DataStore stub shapes are per title.

Corrected `docs/legacy-parity.md`: the per-title table wrongly recorded eight
titles as having no Ranking, and SMB35's secure PID as a retail constant rather
than 2.
