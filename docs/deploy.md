# Deploy — switch-nexd to the production host (arm64)

The deployable artifact is `switch-nexd-arm64` (static, CGO off) +
`Dockerfile.deploy`. Deployment follows the MIGRATION.md cutover checklist:
parallel run first, Traefik re-point per title, 2306-xxxx watch.

## 1. Build (from a dev machine)

```sh
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o switch-nexd-arm64 ./cmd/switch-nexd
```

## 2. Ship + run side by side (do NOT stop the nex-go containers yet)

The nex-go containers hold the SNI routes and the real ports. Run
switch-nexd beside them on shifted ports (+1000) until each title is
verified:

```sh
docker load < switch-nexd.tar        # or scp the binary + cert directly
docker run -d --name switch-nexd \
  -v /app/nex.pem:/app/nex.pem:ro -v /app/nex.key:/app/nex.key:ro \
  openpak/switch-nexd \
  -station-address 145.241.199.19 -cert /app/nex.pem -key /app/nex.key
```

For the parallel run, add a port-shift flag pass — the listener bases come
from the registry, so either publish the container on shifted host ports
(`-p 23110:22110 …`) or run one title at a time with
`-only mario-party-superstars` while its nex-go container is stopped for the
test window.

Local smoke test (no prod cert needed): self-signed dev cert, station
address 127.0.0.1, one `-only` title.

## 3. Traefik re-point (per title, after its parallel run is green)

Route `g<id>-lp1.s.n.srv.nintendo.net` from the nex-go container to
switch-nexd's in-container port (22150 etc. — the listeners are on the
title's base ports inside the container, so only the SNI label changes).
One title at a time; MK8D first (most wire-verified).

## 4. Watch

- `docker logs -f switch-nexd` — `login pid=…`, `secure: connected`,
  `NotImplemented` lines are the live backlog
- Any new 2306-xxxx on the console is a regression until triaged
  (SPEC.md error table)

## MHGU (22120)

Not servable here — raw PRUDP/UDP + kerberos v0. Its nex-go container stays.
