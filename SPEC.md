# SPEC — verified protocol facts (Switch NEX, MK8D 3.0.5 / NEX 4.0.0)

Every fact below was verified on OpenPak's own wire captures (server-side packet dumps,
2026-09-14, MK8D 3.0.5 connecting to quilted-waffle). Sources: our own server logs, our
own packet captures. Public documentation (Kinnay's NEX pages) used as cross-reference.

## Transport

- MK8D speaks **raw PRUDPLite over TLS-TCP**. It never sends a websocket upgrade.
- nex-go's websocket listener accepts these bare packets for reading (`packetData[0] == 0x80`)
  but writes responses **websocket-framed** — the client cannot parse them and stalls
  silently. This lineage writes **unframed** (read and write paths symmetric).
- TCP connect → TLS handshake (~150–300 ms) → PRUDPLite frames flow directly.
- PRUDPLite frames self-delimit (length in header); the TCP stream carries them back to back.

## Login (TicketGranting 0x0A, method 0x02 = ValidateAndRequestTicketWithCustomData)

- Request (observed): 1337-byte parameters — carries the account username string plus a
  DataHolder containing the full OpenPak BAAS id token JWT (~1 kB). The "918-byte ceiling"
  concern was disproven: the full JWT fits and the server parsed it.
- Response body (verified byte-for-byte from our own server, 348 bytes):

```
u8     structure version = 1
u32    content length
  u64    SourcePID                    (8-byte PIDs on Switch)
  u32    BufResponse length           (148 bytes observed: 32-byte random key in clear,
  ...     ticket body                  followed by RC4-encrypted internal data)
  u32    ServiceNodeURL length
  ...     StationURL string
  u64    CurrentUTCTime
  u32    ReturnMsg length
  ...     ReturnMsg string
  u32    SourceKey length             (64 = hex of the 32-byte source key)
  ...     SourceKey string
```

- The Switch v1 ticket body: `u32 randomKeyLen + randomKey(32)` in the clear, then
  `RC4(MD5(sourceKey ‖ randomKey))` over the internal data. Internal data contains at
  minimum `u64 timestamp, u64 source PID, u8[32] session key`; nex-go's encoder emits 116
  encrypted bytes for the same plaintext core, so additional fields exist and are still
  being mapped (open item).

## StationURL

- Format: `prudp:/k=v;k=v;…` — parameters are a dictionary; order is free.
- Secure station URL handed to MK8D (verified accepted shape):
  `prudp:/PID=2;sid=1;stream=10;type=2;address=<IP>;port=<secure port>;CID=1`
- `address` may be a raw IP; the console dials it without DNS.
- Scheme `prudp` with a TLS listener is what the working reference uses.

## Post-login (observed)

- After the LoginEx response, the console ACKs and goes quiet on the auth connection; the
  next stage is the secure dial to the StationURL address:port.
- The secure CONNECT is **ticketless** (empty payload). The server must recall the login
  PID by client IP (with a TTL) and stamp it on the session — a placeholder PID breaks Pia
  self-recognition and the console reports a communication error instead of proceeding.
- MK8D then goes quiet again waiting for the server's first secure-stage data.

## Pia / NAT check

- The NAT check requires **nncs1 and nncs2 on two distinct IPs**; on one IP the console
  deduplicates and never sends the second probe, and the NAT check never completes
  (observed error family 2618-xxx on titles that got that far elsewhere).
- Responder ports seen: UDP 10025, 10125 (+ 33334/33335 relay). These must be reachable
  on the server's public IP per-title-block notes in `ports.md`.

## Error codes observed (MK8D module 2306)

- `2306-0112` — generic communication error; seen when the game's online watchdog (30 s
  from the connect call) expires during the post-login wait.
- `2306-0802` — seen when the game received a login response whose ticket it could not
  parse (Token Parse Error), against a reference server that closes the auth connection
  right after responding.
