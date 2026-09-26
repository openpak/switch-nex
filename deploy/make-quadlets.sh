#!/bin/sh
# Generate the per-title quadlets (one container per title, docs/deploy.md 2c).
#
# The title list and its ports come from cmd/switch-nexd/main.go, so adding a
# title to the registry is the only place a title is named. Writing these by
# hand is how one ends up sharing a recall file between two titles again.
#
#   sh deploy/make-quadlets.sh latest out/
#   scp out/*.container ubuntu@<host>:~/.config/containers/systemd/
#
# Then, on the host, per title: mkdir -p ~/openpak/deploy/switch-nex/state/<title>
set -e
VERSION="${1:-latest}"
OUT="${2:?usage: make-quadlets.sh [image-tag] <outdir>}"
SRC="$(dirname "$0")/../cmd/switch-nexd/main.go"
mkdir -p "$OUT"
n=0
# Each registry entry opens `{"<title>", <base>, "<access key>", func(...)`.
grep -oE '^	\{"[a-z0-9-]+", [0-9]+' "$SRC" | tr -d '\t{"' | while IFS=, read -r title base; do
  base="$(echo "$base" | tr -d ' ')"
  cat > "$OUT/openpak-switch-$title.container" <<EOF
[Unit]
Description=OpenPak Switch NEX title $title (switch-nex, ports $base/$((base+1))/$((base+2)))
# Read by deploy/drain-update.sh to find the ports it must see idle. Machine
# readable on purpose: the Description above is prose and may be reworded.
# openpak-ports=$base $((base+1)) $((base+2))

[Container]
ContainerName=switch-$title
Image=ghcr.io/openpak/switch-nex:$VERSION
# Network=host, not PublishPort: rootless podman's forwarder rewrote every source
# address, so Register could only synthesise stations naming the server's own
# bridge and a room held a mesh it could not form. See docs/deploy.md.
Network=host
Volume=%h/openpak/deploy/certs/nex.pem:/certs/nex.pem:ro
Volume=%h/openpak/deploy/certs/nex.key:/certs/nex.key:ro
# One recall table per title. A single shared file is how two titles could hand
# each other a PID: the by-title fallback claims the title's latest login
# inside 30 s.
Volume=%h/openpak/deploy/switch-nex/state/$title:/state:Z
Exec=-only $title -cert /certs/nex.pem -key /certs/nex.key -station-address 145.241.199.19 -recall-file /state/recall.json
# NX_INTERNAL_KEY comes from the shared .env, never a copy in the quadlet.
EnvironmentFile=%h/openpak/deploy/.env
Environment=NX_INTERNAL_URL=http://127.0.0.1:20070
# No AutoUpdate=registry: podman-auto-update restarts a title the moment a new
# :latest lands, players in it or not. openpak-switch-drain-update.timer does
# the same job but waits for the title to be empty first. docs/deploy.md 2d.

[Service]
Restart=always

[Install]
WantedBy=default.target
EOF
  n=$((n+1))
done
echo "wrote $(ls "$OUT" | wc -l) quadlets to $OUT"
