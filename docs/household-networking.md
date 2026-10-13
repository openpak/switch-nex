# Shared household networking — 2026-10-13

The same household can contain several players. A public address is a routing
property, never a player identity. Each participant retains its authenticated
PID, connection ID, LAN address and game port even when another participant
uses the same public address or the same local UDP port on a different device.

## Shared corrections

All 29 Switch NEX title profiles use the same registration and station codec.
Registration no longer substitutes the TLS/TCP carriage's source port for the
reported game port. A client-reported public UDP mapping is preserved; when
none is reported, the fallback retains the local port until a client supplies
an observed mapping. This does not discover a router's rewritten UDP port.
Station field updates emit one value per key, including when the internal
representation changes from string to integer or back.

MatchMaking 0x15/0x10 takes a gathering ID and returns a flat list of station
URLs. The previous per-participant nested lists could not be parsed by a
standard NEX client. Both LAN and public candidates remain in the response.
Specification: [NintendoClients MatchMaking protocol](https://nintendoclients.readthedocs.io/en/latest/reference/nex/matchmaking/).

## Evidence and validation

- Nobody Saves the World: native Switch host and Harbor Android joiner on the
  same Wi-Fi. Authentication, invitation delivery and session join succeeded.
  The joiner requested 0x15/0x10 and immediately reported failed traversal,
  detail 999; no peer probe initiation reached the server.
- Harbor socket debug shows UDP shutdown produces ENOTCONN during teardown.
  It does not establish the cause of the preceding traversal failure. A local
  default-route probe selected Wi-Fi correctly with mobile data also active.
- The corrected TLS regression reads each endpoint as a string, with no
  nested-list decoder. It covers two players behind one address with distinct
  LAN addresses and the same UDP port for the three first-batch titles.
- Registration regressions cover all 29 profiles, Register and RegisterEx,
  LAN-only reports and reports including independent public UDP mappings.
- Full `GOWORK=off go test -race ./...` and `GOWORK=off go vet ./...` passed.
- Raw logs and probe artifacts remain outside this repository under workspace
  `scratch/ryujinx-android/rog3-nobody-20261013/`.

## Live candidate

Commit `b9fcf820a6d882aaacb020f174ad4ad3077f1306` was cross-compiled with
`CGO_ENABLED=0 GOOS=linux GOARCH=arm64`. The clean VCS stamp was verified.
At 2026-10-13 09:19:57 UTC, only the Nobody Saves the World unit was restarted
with that binary mounted over `/app/switch-nexd`. Its running SHA-256 matches
`5509c111455823270b4225fab1d49789102c326fbfda87680bd726c4e7e70bcf`;
health on port 23072 returned OK. The quadlet backup has suffix
`.before-household-b9fcf82`; rollback removes the candidate binary mount,
reloads the user systemd manager and restarts that title. Other titles retain
the registry release. No new image or release tag has been published yet.

## Remaining acceptance

Real-game acceptance is pending: create a fresh Switch room, accept a fresh
invite in Harbor, reach co-op gameplay, then repeat with Harbor hosting. Keep
successful reply parsing separate from successful peer connectivity.

Before declaring shared household support complete, also test different local
game ports, client-reported translated public ports, distinct households,
concurrent accounts, and leave/rejoin. Do not infer LAN adjacency solely from
one public IP: unrelated households can share a carrier-grade NAT address.
Other backend families need the same acceptance cases against their own
protocols; this patch changes the shared Switch NEX stack.

The related 0x32/0x04 handler has a separate IDL mismatch (its parameters are
currently read as participant IDs instead of gathering IDs). It was not called
in this trace and is an outstanding shared protocol audit item.
