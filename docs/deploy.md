# Deploy — switch-nexd to the production host (arm64)

The deployable artifact is `switch-nexd-arm64` (static, CGO off), wrapped by
`Dockerfile.deploy` into `localhost/switch-nexd:latest` on the host. The
deployed shape since the 2026-09-17 **host-networking cutover**: one
`switch-nexd` container serving every title, quadlet `openpak-switch-nexd.service`
(a **user** unit — `systemctl --user`, not the system manager), Traefik SNI for
the auth stage, secure ports bound by the process itself on the host netns
(`Network=host`, no `PublishPort=`), so the server sees each client's real
source address — the P2P stations it synthesises name the client, not a
forwarder.

Host access: `ssh <prod-host>`.

## 1. Build (from a dev machine)

```sh
GOWORK=off go test ./... && GOWORK=off go vet ./...
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o switch-nexd-arm64 ./cmd/switch-nexd
```

**Commit first.** Go stamps the binary with the commit it was built from and
whether the tree was dirty (`go version -m <binary> | grep vcs.`). A build from
uncommitted changes records `vcs.modified=true` and is traceable to nothing: it
is not the previous commit and not the next one, so "what is running in
production?" has no answer. Check the stamp before publishing, and after
deploying:

```sh
go version -m switch-nexd-arm64 | grep -E 'vcs\.(revision|modified)'
```

Local smoke test before shipping (no prod cert needed): self-signed dev
cert, station address 127.0.0.1, one `-only <title>`.

## 2. Publish the image (registry path — this is what production runs)

Production runs `ghcr.io/openpak/switch-nex:v0.1.0` as of 2026-09-16; it was
the last service still building a `localhost/` image on the host. Build and
publish from a dev machine:

```sh
podman build --platform linux/arm64 -f Dockerfile.deploy \
  -t ghcr.io/openpak/switch-nex:vX.Y.Z -t ghcr.io/openpak/switch-nex:latest .
gh auth token | podman login ghcr.io -u <user> --password-stdin
podman push ghcr.io/openpak/switch-nex:vX.Y.Z
podman push ghcr.io/openpak/switch-nex:latest
```

The build has no `RUN` steps — it copies the already-cross-compiled binary into
alpine — so an arm64 image builds on an x86 host without emulation.

The package is **private**, like every other openpak package. The production
host authenticates from `~/.config/containers/auth.json` (not the runtime
`auth.json`, which is empty); if a pull ever fails with a 403, that file is
where the credential lives.

The quadlet names a pinned version tag, never `latest`:

```
Image=ghcr.io/openpak/switch-nex:vX.Y.Z
```

`latest` would make a restart redeploy whatever was pushed most recently
instead of what you chose. With a pin, rolling back is editing one line and
restarting — and the previous quadlet is saved beside it as
`.before-registry-<stamp>`.

Attach the same binary to a GitHub release (`gh release create vX.Y.Z
switch-nexd-arm64 mk8d-arm64 SHA256SUMS.txt`). The binary is not in git; the
release and the image are the two records of a build, and their hashes must
agree with `podman exec switch-nexd sha256sum /app/switch-nexd` in production.

## 2b. Ship + rebuild on the host (legacy path, no registry needed)

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
  - Traefik `~/openpak/deploy/traefik/dynamic.yml`: one `nex-<title>` router
    (`HostSNI(g<id>-lp1.s.n.srv.nintendo.net)`, `tls.passthrough`) + service
    → `host.containers.internal:<base>`. Back up the file first
    (`dynamic.before-<change>-<date>.yml`). Traefik watches the file
    (`providers.watch: true`), so an edit applies without a restart;
  - firewall: the INPUT chain ends in a REJECT — the title's secure port must
    be in the multiport ACCEPT **before it** (add to the existing
    `--dports 22111,…` rule — or, since a multiport rule holds at most 15
    ports and the first is full, the second one after it:
    `--dports 22371,22381`), then persist
    (`sudo sh -c "iptables-save > /etc/iptables/rules.v4"`). With host
    networking there is no `PublishPort=` and no DNAT: this INPUT rule is the
    only thing standing between a console and the port. (2026-09-17 lesson:
    MK8D's 22151 was missing from that rule for weeks and nothing noticed,
    because published-port DNAT bypassed INPUT. The cutover would have
    blackholed exactly that port.)

```sh
systemctl --user daemon-reload        # picks up quadlet changes
systemctl --user restart openpak-switch-nexd.service
```

**Host networking, why and how.** Behind published ports, rootless podman's
forwarder rewrote every source to the gateway, so Register could only
synthesise public stations naming the server's own bridge — the reason a room
held a mesh it could not form. With `Network=host` the process binds
`0.0.0.0:<secure>` itself (all ports >1024, fine unprivileged) and sees real
client addresses; the by-IP recall is exact again. Three wires keep working:

- **Traefik** (itself a rootless container on the `openpak` network) reaches
  the auth stage via `host.containers.internal:<base>` — resolved to
  `10.0.0.249` inside that container; `127.0.0.1` there would be Traefik
  itself. Note the auth stage therefore logs Traefik's address as the source;
  only the secure stage, which consoles dial directly, sees the client.
- **nx-baas**: the internal identity API is port 20070 inside that container
  (baked into the image, not an env-configured listener). The nx-baas quadlet
  publishes it on host loopback — `PublishPort=127.0.0.1:20070:20070` — and
  the quadlet passes `NX_INTERNAL_URL=http://127.0.0.1:20070`. A working
  chain logs `identity: resolving against http://127.0.0.1:20070 …`.
- **Firewall**: INPUT accepts the fifteen secure ports via the multiport rule
  above; the 443→21443 REDIRECT that feeds Traefik is untouched.

**The certificate names the station address.** The game reaches the auth stage by
hostname and the secure stage by the raw IP in the station URL, verifying the name both
times. `certs/nex.pem` therefore carries `IP:145.241.199.19` beside the DNS names; if
`-station-address` ever changes, reissue the leaf from `~/openpak/deploy/ca` with the new
IP (same key, SANs copied from the old leaf — the 2026-09-17 reissue left
`nex.pem.before-ipsan-20260917` beside it). Check with
`openssl s_client -connect <ip>:<secure> -CAfile ca.pem -verify_ip <ip>`.

**Recall persistence.** `-recall-file <path>` persists the IP-to-PID table that
lets a console re-CONNECT to a secure port without replaying a full login. Left
unset the table is memory-only and a restart refuses every console already
mid-session. Live since 2026-09-16; the quadlet carries

```
Volume=%h/openpak/deploy/switch-nex/state:/state:Z
Exec=… -recall-file /state/recall.json
```

Check the mount is writable after any quadlet change
(`podman exec switch-nexd touch /state/.probe`): the flusher ignores write
errors, so an unwritable mount makes persistence silently do nothing.

## Rollback

Each deploy leaves a timestamped binary and image tag beside the live ones:

```sh
cd ~/openpak/deploy/switch-nex
ls switch-nexd-arm64.before-*                      # binaries
podman images localhost/switch-nexd                # tags
cp switch-nexd-arm64.before-<stamp> switch-nexd-arm64
podman build -f Dockerfile.deploy -t localhost/switch-nexd:latest .
cp ~/.config/containers/systemd/openpak-switch-nexd.container.before-<stamp> \
   ~/.config/containers/systemd/openpak-switch-nexd.container
systemctl --user daemon-reload && systemctl --user restart openpak-switch-nexd.service
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

  **Take `g<id>` from the Traefik `dynamic.yml` on the host, never from
  memory.** An invented hostname reaches the catch-all, which answers TLS with
  a per-hostname cert and looks like a pass — it is the failure this check
  exists to catch, wearing the face of a success. If the CN comes back as the
  hostname you asked for rather than the wildcard, you routed to the wrong
  backend.

- **End to end, without a console:** `cmd/nexprobe` speaks the console's half —
  TLS with the title's SNI, the PRUDPLite handshake with its access-key
  signature, and a real TicketGranting login.

  ```sh
  go run ./cmd/nexprobe -addr 145.241.199.19:443 \
    -sni g2b309e01-lp1.s.n.srv.nintendo.net -key 09c1c475 -token fake
  ```

  `-token fake` sends a well-formed but unsigned token, which the server must
  carry to nx-baas and be told no about: `login UNPROVEN (id token not proven)`
  in the log is proof the identity call really happens, as distinct from
  `(no token)`, which never leaves the process.
- Secure port direct dial (the console hits this straight from the station
  URL): `echo | openssl s_client -connect 145.241.199.19:<secure>`
- Any new 2306-xxxx on the console is a regression until triaged (SPEC.md
  error table).

## MHGU (22120)

Not servable here — its secure stage is raw PRUDP/UDP and its ticket is kerberos
v0. Its auth stage *is* wss on the shared SNI route, so the Traefik entry for
`g2896bd04-lp1.s.n.srv.nintendo.net` stays pointed at the nex-go container, which
now listens TCP there rather than UDP.
