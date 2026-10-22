# The method-2 login shape: two lineages, opposite answers

TicketGranting method `0x02` is answered differently by the two OpenPak servers
that have served these fifteen titles, and both cite console evidence. This
records the evidence and what was done about it. It is **not resolved**.

## The disagreement

| | method 2 answers | claimed evidence |
|---|---|---|
| `platforms/switch/games/switch-nex` (this lineage, **in production**) | the **flat** layout: `u32 retval, PID, Buffer ticket, RVConnectionData structure, String msg, String sourceKey` | `SPEC.md`: a 2026-09-15 capture; says reading it as a structure was a **misparse of the retval** |
| `platforms/wiiu-3ds/games/nex` | the **`ValidateAndRequestTicketResult` structure**, header and all — the same body method 6 returns | `login.go:87-98`: the flat layout "could not be parsed by the console: it stayed silent after the response and gave up with **2306-0112**" |
| Measured server | **flat** | its own working deployment |

Two to one for flat, and production serves flat. That is not the same as being
right: `platforms/wiiu-3ds/games/nex` served these exact titles until the 2026-09-16 cutover, so
its claim is about the same consoles and the same games.

## Why it is easy to get wrong in either direction

`ValidateAndRequestTicketResult` has **no retval field** (confirmed in
nex-protocols-go v2.3.7, `ticket-granting/types`): it is
`SourcePID, BufResponse, ServiceNodeURL, CurrentUTCTime, ReturnMsg, SourceKey`.

The flat layout leads with `retval = 0x00010001`, which on the wire is the bytes
`01 00 01 00`. Read as a structure that is a version byte `0x01` followed by a
length — convincing, and wrong. The same four bytes support both readings, which
is exactly how two careful people reach opposite conclusions from one capture.

Method **6** is not in dispute: it is the structure, field for field as above,
and `SPEC.md`, `platforms/wiiu-3ds/games/nex` and the measured server all agree.

## What was tried, and why it did not settle it

- **The packet captures do not contain it.** `Openpak/tmp/n5w.pcap` and
  `n5w2.pcap` (71 MB / 290 MB, Linux cooked v2) carry no NEX login traffic.
  What looks at a glance like PRUDPLite — frames whose first byte is `0x80` —
  is **SSH on port 22**: the packet-type field is uniformly distributed across
  all sixteen values, which is noise, not protocol. The real login traffic in
  those captures is inside TLS.
- **The library does not decide it.** nex-protocols-go defines the method
  numbers and the result type but leaves the method-2 response to the server,
  so both shapes are legal as far as the protocol definition goes.
- **The harness cannot decide it.** It proves client and server agree, and it
  will agree with whichever shape the server is told to send.

Only a console can answer this.

## What was done instead

The shape is a switch, not a decision: `Env.LoginExStructured`, set globally by
`-loginex-structured`. Default is **false**, the flat layout, which is what
production serves today — this changes nothing until somebody chooses.

`TestLoginExShapeIsSwitchable` drives both through the end-to-end harness: each
is read by a client expecting that shape, insisted on being consumed exactly,
and the ticket it carries is spent on the secure stage. Both work.

## How to settle it

On the first console session, if login fails on the flat layout:

```sh
# one flag, no rebuild
Exec=… -loginex-structured
systemctl --user restart openpak-switch-nexd.service
```

The console error is the evidence to record either way. `2306-0112` — the code
`platforms/wiiu-3ds/games/nex` associates with the flat layout — means the console received a
response it could not parse and gave up; `2306-0116` is `Core::InvalidArgument`,
which the measured server associates with sending method 6's *request* shape the wrong
answer. Write down which one appears, against which setting, in `SPEC.md`.
