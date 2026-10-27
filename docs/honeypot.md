# NEX SNI honeypot — handshake capture + offline key recovery

Some Switch-era NEX titles are playable but their access key is unknown
(Risk of Rain Returns ships no config and no 8-hex string anywhere in the
binary; see its registry comment). An unknown key costs nothing while the
server never verifies it — but recovering it turns an unverified endpoint
into a verified one, and rules out the key as the cause of anything odd.

One captured CONNECT is enough. The client's Lite proof is
`HMAC-MD5(md5(key), md5(key) ‖ connSig)` over the 16-byte connection
signature the server's SYN-ACK carried (`prudplite/crypto.go`), so an 8-hex
key is a straight offline search over 2^32 with no server contact:
~2.6M keys/s on 8 x86 cores, the full space in under half an hour
(measured 2026-10-27, `scratch/honeypot/brute-key.py`).

## Shape

`switch-nexd -honeypot-base 23190 -honeypot-log <file>` serves a capture
endpoint on base (auth), base+1 (secure) and base+2 (healthz, the same
`ok honeypot auth=… secure=…` shape as a title). With no `-only` it serves
only the honeypot; name titles explicitly to run both (local testing).

- Accepts any SNI: TLS terminates with the wildcard for every name, and
  `GetCertificate` records which name each peer asked for.
- Logs two JSONL record types to the capture file: `tls` (every ClientHello:
  timestamp, remote, SNI — the routing proof) and `connect` (timestamp,
  endpoint, remote, SNI, `connsig`, `litesig`, `raw` CONNECT frame bytes).
- Answers nothing playable: the secure stage rejects every CONNECT
  (no kerberos key + `RequireTicket`), the auth stage acks the handshake
  and answers every RMC with `NotImplemented`. No logins, no sessions.
- The only shared change is the nil-guarded `prudplite.OnHandshake` hook,
  called before the access-key check; every title leaves it nil.

Traefik (`~/openpak/deploy/traefik/dynamic.yml` on prod) routes unknown
game-server SNIs here:

```yaml
nex-honeypot:
  entryPoints: [console]
  rule: HostSNIRegexp(`^g[0-9a-f]+-lp1\.s\.n\.srv\.nintendo\.net$`)
  priority: 30 # below every nex-* specific (default 45), above the adapter catch-all (21)
  service: nex-honeypot # host.containers.internal:23190 + PROXY v2
  tls: {passthrough: true}
```

Priority semantics (Traefik 3.3, verified against the running 3.3.7 config):
higher wins, default is the rule length. Every specific `nex-*` router has no
explicit priority and a 45-character rule; the `adapter` catch-all
(`HostSNIRegexp(`^.+$`)`) defaults to 21. So known titles keep winning (45),
unknown NEX SNIs land here (30) instead of falling through to nx-baas, and
the explicit 30 also guards the honeypot's own 59-character rule, which by
default would outrank the specifics and steal every title.

Firewall (before the final REJECT, persisted in `/etc/iptables/rules.v4`):

- public: secure `23191` (consoles and probes dial it directly)
- internal (`10.89.2.0/24`): `23190:23192` (Traefik → auth; the shared
  `22000:23102` rule does not reach this block)

The quadlet (`openpak-switch-honeypot.container`) pins a localhost image, so
`drain-update.sh` skips it as pinned; it carries no `openpak-ports=` marker.

## Capturing a key

1. Confirm the title's SNI reaches the honeypot: `openssl s_client -connect
   145.241.199.19:443 -servername g<id>-lp1.s.n.srv.nintendo.net` must append
   a `tls` line with that SNI to `/state/handshakes.jsonl`
   (`~/openpak/deploy/switch-nex/state/honeypot/` on prod).
2. Boot the title on a console pointed at prod; its SYN/CONNECT appends a
   `connect` line. One is enough — retries just add lines.
3. Copy the capture home and run the search:
   `python3 brute-key.py handshakes.jsonl` (add `--record N` past the first).
4. Verify the hit proves the capture (`--check <key>`), then set the key in
   the title's registry entry: the next boot's CONNECT verifies.

Keys that are not 8 hex (some titles use 8 ASCII letters) are outside the
2^32 space; the tool reports a miss, not a wrong key.

## Files

- `cmd/switch-nexd/honeypot.go` — endpoints, SNI table, JSONL log
- `cmd/switch-nexd/honeypot_test.go` — both stages against a bogus SNI
- `scratch/honeypot/brute-key.py` — offline search + `--check` verifier
- `scratch/honeypot/openpak-switch-honeypot.container` — the prod quadlet
- `scratch/honeypot/traefik-snippet.yml` — the router + service blocks
