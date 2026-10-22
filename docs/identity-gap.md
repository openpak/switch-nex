# The switch-nex identity gap (investigation, 2026-09-16)

Asked: does the `platforms/wiiu-3ds/games/nex` → `platforms/switch/games/switch-nex` cutover trade a per-account
identity for a shared secret? **Confirmed, with one correction to the framing.**

**Status 2026-09-16: implemented, off by default, NOT deployed.** The code below
exists and is tested end to end against a stub nx-baas; production still runs
with identity disabled, which is the state section 1 describes. Turning it on is
a configuration change plus a deploy, and a decision about when to enforce.

## 1. What actually happens today

`platforms/wiiu-3ds/games/nex/account.go:66` (`PlayerByToken`) pulls the `openpak` claim out of the
BAAS id_token, falls back to sending the whole token, and asks nx-baas to prove
it. `servers/stardew-valley/internal/npln/identity.go` does the same for NPLN.
Both confirmed by reading.

`platforms/switch/games/switch-nex` has **no equivalent anywhere in its Go**: no `openpak`, no
`id_token`, no nx-baas call, no account lookup. The whole of identity is
`cmd/switch-nexd/main.go:181`:

    pidFor(username) = 0x6C000000 | (sha512_256("openpak-switch-nex-pid:"+username)[:4] & 0x00FFFFFF)

and `handleLoginEx` reads `username, _ := in.String()` straight off the wire.

**Correction to the framing in the brief.** `securePassword`
(`cmd/switch-nexd/main.go:134`) is *not* a per-player secret standing in for
per-account identity — it is the **secure server account's** kerberos password,
the service-account key that encrypts the ticket's internal data. A single value
there is correct and matches both `platforms/wiiu-3ds/games/nex` and the measured server implementations
(`OPENPAK_SECURE_PASSWORD`). It is not the defect.

The defect is narrower and worse than "shared secret":

- **The login username is accepted without proof.** The `extraData` that follows
  it — the AuthenticationInfo DataHolder carrying the BAAS id_token — is read
  into a variable and discarded. Anyone who can reach an auth port and send an
  arbitrary username string is issued that username's PID and a valid kerberos
  ticket for it. That is impersonation, not merely anonymity.
- **nx-baas never sees the player.** No presence, no friends-in-game, no
  friend graph, and a player is a different person to a NEX title than to an
  NPLN title — the property `platforms/wiiu-3ds/games/nex` exists to preserve.
- **The PID space is 24 bits.** `0x6C000000 | 24 bits` is 16.7M values; two
  accounts collide by birthday at roughly 4,800 players and then *are* the same
  principal, with the same sessions and saves.

It is documented as deliberate — `docs/parity.md` deviation 5 and
`next-session.md` open item 4 both name it as the pre-real-console integration
item, and `platforms/wiiu-3ds/games/nex/account.go` is cited as the measured server for the flow. So:
a known, documented shortcut, but not a time-boxed one, and it is already live.

## 2. Blast radius: shipping in production, all fifteen titles

- The quadlet `openpak-switch-nexd.service` publishes every secure port
  22111…22351 plus 22871, and passes **no account or BAAS configuration at all**
  (`Exec=-cert … -key … -station-address …`).
- `systemctl --user list-units "openpak-nex*"` on the host returns **zero
  units** — the nex-go title services are gone, not standing by.
- So this is not pre-cutover. Every Switch-era NEX title is live on the
  unauthenticated path right now.

Doc drift worth fixing separately: `ports.md` lines 82–96 still name
`platforms/wiiu-3ds/games/nex/titles/<name>` as the owner of 22110–22350, contradicting its own
line 72 which says the one `switch-nexd` container serves them.

## 3. The seam

**nx-baas needs to expose nothing new.** `POST /internal/switch/identity`
(`nx-baas/internal.go:23`, routed at `gameapi.go:417`) already accepts exactly
the three shapes this needs — `{openpak}`, `{id_token}`, or `{pid}` — behind an
`X-Internal-Key` header, and answers `{pid, baas_user_id, nickname, friends[],
country}`. `platforms/wiiu-3ds/games/nex` is AGPL-3.0 (OpenPak's own), so its pattern is
reusable as input under `docs/clean-room-policy.md` §"What is allowed as input".

What switch-nex would have to grow, in order:

1. **Read the login extra data.** `handleLoginEx` already has the bytes; parse
   the DataHolder and pull the id_token out. Today's `username` becomes a
   fallback, not the identity.
2. **An identity client.** One `POST /internal/switch/identity` call with
   `{id_token, openpak}`, internal key from env, mapping 401 → the login failure
   result and 404 → validation failure. The `openpak`-claim extraction is ~20 lines
   and is the only part that needs writing fresh.
3. **PID from nx-baas, not from a hash.** `pidFor` is deleted; the PID in the
   kerberos ticket becomes the account's real PID. This is the change that makes
   the ticket meaningful: the secure stage already recovers the PID from the
   ticket's inner blob, so once the ticket carries a *proven* PID the secure
   stage is authenticated end-to-end with no further work.
4. **Retire the by-IP recall as an identity source.** `recall`
   (`cmd/switch-nexd/main.go:139`) lets a ticketless CONNECT inherit whoever last
   logged in from that IP. With real identities that is an account-takeover path
   for anyone behind the same CGNAT; it should become a same-IP *plus* same-PID
   assertion, or go.

**PRUDPLite/kerberos v1 needs nothing different.** The transport already carries
identity correctly — the ticket's inner `ServerTicket{timestamp, source PID,
session key}` is encrypted under the secure account key and the CONNECT
check-data proves the client holds the session key. The v1 two-layer ticket is
byte-identical to what `platforms/wiiu-3ds/games/nex` mints. The gap is entirely upstream of the
transport: nothing proves the PID that goes *into* the ticket.

## 4. What was built

`cmd/switch-nexd/identity.go` plus the login path:

- **`authenticationToken`** reads the BAAS id token out of the login's extra
  data: a DataHolder naming `AuthenticationInfo`, two length prefixes, an empty
  `Data` base header, then the structure carrying the JWT. A holder naming
  something else is refused rather than read speculatively.
- **`openpakClaim`** pulls the nx-baas-signed binding claim when there is one.
  Absent is the NORMAL case for a retail console and never an error.
- **`identity.resolve`** posts `{id_token, openpak}` to
  `/internal/switch/identity` with the internal key. nx-baas needed no change.
- **Refusal codes are not interchangeable**: 401 maps to
  `Authentication::TokenExpired` (0x00680006), which makes the console fetch a
  fresh token and retry; 404 maps to `ValidationFailed` (0x00680007), which
  makes it stop. Answering the retryable code for an account we do not know is
  a refresh loop.
- **The proven PID goes into the kerberos ticket**, so the secure stage — which
  already recovers the PID from that ticket — becomes authenticated end to end
  with no further work. This also dissolves the 24-bit collision problem: the
  PID is nx-baas's, not a hash.
- **The ticketless recall closes under enforcement.** Inheriting a PID by IP is
  leniency, not authentication, and with real accounts it hands a stranger
  behind the same public address somebody else's session.

Three flags: `-identity-url`, `-identity-key`, `-require-identity`. With no URL
the behaviour is exactly what it is today, including the log line saying so.

## 5. Rollout

1. Deploy with `-identity-url`/`-identity-key` set and `-require-identity`
   **off**. Every login still succeeds; the ones that cannot be proven log
   `login UNPROVEN` with the reason.
2. Watch that line. It is the migration's progress bar: while it appears,
   enforcing would lock those players out.
3. Flip `-require-identity` on when it stops. That is also what closes the
   ticketless recall path.

The honest caveat: none of this has seen a real BAAS token. The tests drive a
stub that answers the way nx-baas documents, which proves the wiring and the
shapes but not that a console's actual token parses. Step 1 is safe precisely
because an unparseable token falls back instead of refusing.

## 6. Recommendation

Steps 1–3 are roughly a day and touch one file plus a new small client. The
honest question is whether the fifteen live titles should keep serving in the
meantime.

They are already live and the shortcut is documented, so ripping them offline is
probably an overreaction — but the impersonation path is real and the 24-bit PID
space is a data-correctness problem that gets worse with every account created,
because colliding accounts cannot be separated after the fact. The sequencing I
would argue for: **widen the PID derivation first** (a one-line change that
stops the collision debt accruing), then do the nx-baas binding, then the recall
hardening. Pausing the cutover buys little that the binding work would not buy
faster.

Flagged for decision, not acted on.
