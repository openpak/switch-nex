# SPEC — verified protocol facts (Switch NEX, MK8D 3.0.5 / NEX 4.0.0)

Every fact below was verified on OpenPak's own wire captures (server-side packet dumps,
2026-09-14, MK8D 3.0.5 connecting to quilted-waffle). Sources: our own server logs, our
own packet captures. Public documentation (Kinnay's NEX pages) used as cross-reference.

## Transport

- MK8D speaks **PRUDPLite over TLS-TCP**, and both carriages have been observed:
  bare frames (server-side dumps, 2026-09-14) and a websocket upgrade — `GET / HTTP/1.1`,
  `Upgrade: websocket` as the first plaintext, then one frame per binary message
  (guest-side SSL trace, Ryujinx fork, 2026-09-17). Why the same title did both is
  **unexplained**. The server follows the client per connection (`prudplite/websocket.go`).
- nex-go's websocket listener accepts these bare packets for reading (`packetData[0] == 0x80`)
  but writes responses **websocket-framed** — the client cannot parse them and stalls
  silently. This lineage writes **unframed** (read and write paths symmetric).
- TCP connect → TLS handshake (~150–300 ms) → PRUDPLite frames flow directly.
- PRUDPLite frames self-delimit (length in header); the TCP stream carries them back to back.

## Login (TicketGranting 0x0A, method 0x02 = ValidateAndRequestTicketWithCustomData)

- Request (observed): 1337-byte parameters — carries the account username string plus a
  DataHolder containing the full OpenPak BAAS id token JWT (~1 kB). The "918-byte ceiling"
  concern was disproven: the full JWT fits and the server parsed it.
- Response body (LoginEx 0x02 — flat layout, all little-endian, pinned to the
  working reference server's shape; the earlier "version+length header" reading
  of our own capture was a MISPARSE of the u32 retval `0x00010001`):

```
u32    retval                       (0x00010001 success)
u64    SourcePID                    (8-byte PIDs on Switch)
u32    BufResponse length           (148 bytes: the two-layer ticket, below)
...    BufResponse                  (ticket)
structure v1: u8 version=1, u32 length, RVConnectionData:
  String StationURL                 (u16 length incl. NUL + bytes + NUL)
  u32    SpecialProtocols count + u8 each
  String SpecialStation             ("prudp")
  u64    ServerTime                 (packed NEX DateTime)
String ReturnMsg
String SourceKey                   (64 = hex of the 32-byte source key)
```

- Method 0x06 (ValidateAndRequestTicketWithParam) wraps the same six fields in
  `u8 version=1, u32 content length`; its request reads
  `{header, u32 PlatformType, String username, extraData}`.

- The Switch v1 ticket has TWO layers, each RC4-encrypted with a 16-byte
  HMAC-MD5 of the ciphertext appended:
  - inner ServerTicket: `u64le DateTime, u64le source PID, u8[32] session key`
    wrapped as `u32le 16 ‖ ticketKey(16) ‖ u32le len ‖ RC4(MD5(targetKey ‖
    ticketKey)) + HMAC`. Encrypted with the SECURE SERVER account key
    (new derivation: `md5(password)` once, append PID u64le, `md5` once).
  - outer ClientTicket: `sessionKey(32) ‖ u64le targetPID ‖ u32le len ‖ inner`,
    RC4 + HMAC under the SOURCE key. The client recovers its session key here
    and hands the inner blob untouched to the secure endpoint.
  - 148 bytes total with a 32-byte session key — matching the 148 bytes in our
    2026-09-15 capture, now explained.
  - Timestamps are packed NEX DateTimes (`year<<26|month<<22|day<<17|
    hour<<12|minute<<6|second`), NOT Unix time.

## RMC framing

- Packed RMC, carried as the DATA payload: `u32le size` (everything after it),
  `u8 protocol|0x80` request bit on the PROTO byte (0x7F escapes to u16le),
  request `{u32 callID, u32 method, params}`, success response
  `{u8 1, u32 callID, u32 method|0x8000, body}`, error response
  `{u8 0, u32 code|0x80000000, u32 callID}`.
- NEX String = u16le length INCLUDING the trailing NUL, bytes, NUL. NEX Bool =
  one byte. The earlier u32-string/u32-bool reading was wrong.

## PRUDP handshake facts (from the working reference)

- SYN-ACK and CONNECT-ACK carry flags `ACK | HAS_SIZE`. Without HAS_SIZE the
  client completes the handshake but sends an EMPTY CONNECT payload — that is
  where the "ticketless secure CONNECT" came from, not a client quirk.
- DATA frames carry `RELIABLE | NEEDS_ACK` (0x6), never HAS_SIZE.
- SYN/CONNECT carry the support option (id 0): `u32le minorVersion |
  supportedFunctions<<8`.
- A client CONNECT carries the Lite-signature option (0x80):
  `HMAC-MD5(md5(accessKey), md5(accessKey) ‖ connectionSig)` — MK8D's access
  key is `09c1c475`. A SYN-ACK carries the connection signature (id 1):
  `HMAC-MD5(constant Lite key, ipv4 ‖ port)`.
- Secure CONNECT payload: `Buffer(serverTicket) ‖ Buffer(checkData)` where
  checkData = RC4/HMAC under the session key over `{u64 PID, u32 CID, u32
  check}`; the CONNECT-ACK payload answers `{u32le 4, u32le check+1}`.
- The secure station URL keeps `type=2` (public).

## StationURL

- Format: `prudp:/k=v;k=v;…` — parameters are a dictionary; order is free.
- Secure station URL handed to MK8D (verified accepted shape):
  `prudp:/PID=2;sid=1;stream=10;type=2;address=<IP>;port=<secure port>;CID=1`
- `address` may be a raw IP; the console dials it without DNS.
- Scheme `prudp` with a TLS listener is what the working reference uses.

## Post-login (observed)

- After the LoginEx response, the console ACKs and goes quiet on the auth connection; the
  next stage is the secure dial to the StationURL address:port.
- The secure CONNECT carries the ticket now that the handshake answers with
  `HAS_SIZE` (see PRUDP handshake facts). The by-IP PID recall remains only as
  the leniency path for genuinely ticketless CONNECTs (with a TTL); a placeholder
  PID breaks Pia self-recognition and the console reports a communication error
  instead of proceeding.
- MK8D then goes quiet again waiting for the server's first secure-stage data.

## Structures

- An inherited structure is serialised as **siblings, parent first**, each with its own
  `u8 version, u32 length` header; the child's length covers only the child's fields.
  Observed in MK8D 3.0.5's AutoMatchmakeWithParam_Postpone (0x6D/0x28, 2026-09-17):
  param header `00 11010000`, Gathering `00 2b000000` (43 bytes), then MatchmakeSession
  `00 53000000`. Both versions were 0. The MatchmakeSession was 5 bytes longer than its
  bare fields: `MatchmakeParam` is itself a structure (`00 04000000` over an empty map).

## Calls outside NEX

- After a private-room create MK8D's NEX library calls the Switch Online voice-chat API,
  `POST api-lp1.znc.srv.nintendo.net/v1/NX/getToken`, and today gets 404 from nx-baas.
  Not fatal — the title goes on to request its room code. Catalogued, not served:
  `nx-baas/docs/open-questions.md` U4 carries the request as observed and the route list.

## Pia / NAT check

- The NAT check requires **nncs1 and nncs2 on two distinct IPs**; on one IP the console
  deduplicates and never sends the second probe, and the NAT check never completes
  (observed error family 2618-xxx on titles that got that far elsewhere).
- Responder ports seen: UDP 10025, 10125 (+ 33334/33335 relay). These must be reachable
  on the server's public IP per-title-block notes in `ports.md`.

## Error codes observed (MK8D module 2306)

- `2306-0112` — generic communication error; seen when the game's online watchdog (30 s
  from the connect call) expires during the post-login wait.
- `2306-0502` — shown within a second of going online when the server hung up on the
  websocket upgrade it did not speak (2026-09-17). Transport-level: the connection died
  before any PRUDP frame.
  The same code appears when the **secure** connection's TLS fails: the game dials the
  station URL's raw IP with hostname verification on, so the leaf needs an
  `IP:<station-address>` SAN beside the `*.s.n.srv.nintendo.net` names (2026-09-17).
- `2306-0807` — NEX Authentication::ValidationFailed (0x00680007), raised by the
  client. Seen 2026-09-19 on Kirby when its game-server host was not redirected:
  the console took our BAAS token to Nintendo's NEX server.
- `2618-0502` — Pia, joiner side. Seen 2026-09-19 on Kirby when the joiner was let
  into a room before the host's NAT check and fetched its pre-NAT station
  (`port=1;CID=0`); nothing reachable until the host's lobby closed.
- `2306-0802` — seen when the game received a login response whose ticket it could not
  parse (Token Parse Error), against a reference server that closes the auth connection
  right after responding.
