# Deploy — switch-nexd to the production host (arm64)

The deployable artifact is `switch-nexd-arm64` (static, CGO off), wrapped by
`Dockerfile.deploy` into `localhost/switch-nexd:latest` on the host. The
deployed shape since the 2026-09-16 cutover: one `switch-nexd` container
serving every title, quadlet `openpak-switch-nexd.service` (a **user** unit —
`systemctl --user`, not the system manager), Traefik SNI for the auth stage,
secure ports published directly.

Host access: `ssh <prod-host>`.

## 1. Build (from a dev machine)

```sh
GOWORK=off go test ./... && GOWORK=off go vet ./...
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o switch-nexd-arm64 ./cmd/switch-nexd
```

Local smoke test before shipping (no prod cert needed): self-signed dev
cert, station address 127.0.0.1, one `-only <title>`.

## 2. Ship + rebuild the image

```sh
scp switch-nexd-arm64 <prod-host>:openpak/deploy/switch-nex/
```

On the host — the build context is `~/openpak/deploy/switch-nex/` and the
Dockerfile is named `Dockerfile.deploy` (pass `-f`):

```sh
cd ~/openpak/deploy/switch-nex
podman build -f Dockerfile.deploy -t localhost/switch-nexd:latest .
```

## 3. Apply config + restart

- **New title**: register it in `cmd/switch-nexd/main.go` (registry), pick a
  port block from ports.md, then on the host:
  - quadlet `~/.config/containers/systemd/openpak-switch-nexd.container`:
    add `PublishPort=0.0.0.0:<secure>:<secure>` (auth rides Traefik; only
    the +1 secure port is published);
  - Traefik `~/openpak/deploy/traefik/dynamic.yml`: one `nex-<title>` router
    (`HostSNI(g<id>-lp1.s.n.srv.nintendo.net)`, `tls.passthrough`) + service
    → `switch-nexd:<base>`. Back up the file first
    (`dynamic.before-<change>-<date>.yml`);
  - firewall: the INPUT chain ends in a REJECT — insert a multiport ACCEPT
    for new secure ports **before it**, then persist
    (`sudo sh -c "iptables-save > /etc/iptables/rules.v4"`).

```sh
systemctl --user daemon-reload        # picks up quadlet changes
systemctl --user restart openpak-switch-nexd.service
```

## 4. Verify

- `systemctl --user is-active openpak-switch-nexd.service` and
  `podman logs switch-nexd` — one `serving auth :<base> secure :<base+1>`
  line per title; `login pid=…`, `secure: connected`; `NotImplemented`
  lines are the live backlog.
- Edge SNI handshake (auth stage through Traefik, passthrough — the cert
  served must be the OpenPak `*.s.n.srv.nintendo.net` leaf, NOT the nx-baas
  catch-all's per-hostname cert):
  `echo | openssl s_client -connect 145.241.199.19:443 -servername g<id>-lp1.s.n.srv.nintendo.net`
- Secure port direct dial (the console hits this straight from the station
  URL): `echo | openssl s_client -connect 145.241.199.19:<secure>`
- Any new 2306-xxxx on the console is a regression until triaged (SPEC.md
  error table).

## MHGU (22120)

Not servable here — raw PRUDP/UDP + kerberos v0. Its nex-go container stays.
