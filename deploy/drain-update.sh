#!/bin/sh
# Roll each Switch title onto :latest, but only while nobody is playing it.
#
# Replaces AutoUpdate=registry for these units. podman-auto-update restarts a
# container the moment a new image lands, which for a game server means dropping
# whoever is mid-match; it is still right for the stateless services, so the
# timer stays enabled and only the switch-* quadlets drop the label.
#
# A title is busy when it has an established TCP connection on its auth or
# secure port: switch-nexd holds one secure connection open for a whole play
# session, so "connected" is a good proxy for "playing". A title that is busy is
# simply left for the next tick — no queue, no state.
#
# Idempotent and safe to run by hand: with no new image it does nothing.
set -eu

# --dry-run reports what it would do and touches nothing. The busy path is
# otherwise only reachable with a real player connected to a real title.
DRY=""
[ "${1:-}" = "--dry-run" ] && DRY=1

IMAGE="${OPENPAK_SWITCH_IMAGE:-ghcr.io/openpak/switch-nex:latest}"
UNITDIR="${OPENPAK_UNIT_DIR:-$HOME/.config/containers/systemd}"
# The status page's portcheck opens and drops a connection on every title; it is
# not a player, and it would otherwise make every title look busy forever.
PROBE_IP="${OPENPAK_PROBE_IP:-145.241.228.207}"

if ! podman pull -q "$IMAGE" >/dev/null 2>&1; then
    echo "pull failed for $IMAGE; nothing to do"
    exit 0
fi
want=$(podman image inspect "$IMAGE" --format '{{.Digest}}')

updated=0 deferred=0 current=0
for f in "$UNITDIR"/openpak-switch-*.container; do
    [ -f "$f" ] || continue
    unit=$(basename "$f" .container)
    title=${unit#openpak-switch-}
    container="switch-$title"

    have=$(podman inspect "$container" --format '{{.ImageDigest}}' 2>/dev/null) || continue
    if [ "$have" = "$want" ]; then
        current=$((current + 1))
        continue
    fi

    # A title pinned to a version is being held back deliberately; leave it.
    case "$(sed -n 's/^Image=//p' "$f")" in
        *:latest) ;;
        *) echo "$title: pinned, skipped"; continue ;;
    esac

    ports=$(sed -n 's/^# openpak-ports=//p' "$f")
    [ -n "$ports" ] || { echo "$title: no openpak-ports marker, skipped"; continue; }
    filter=""
    for p in $ports; do
        [ -n "$filter" ] && filter="$filter or "
        filter="${filter}sport = :$p"
    done

    busy=$(ss -tnH state established "$filter" 2>/dev/null | grep -vc "$PROBE_IP" || true)
    if [ "${busy:-0}" -gt 0 ]; then
        echo "$title: $busy connection(s), deferred"
        deferred=$((deferred + 1))
        continue
    fi

    if [ -n "$DRY" ]; then
        echo "$title: idle, would update"
    else
        systemctl --user restart "$unit.service"
        echo "$title: updated"
    fi
    updated=$((updated + 1))
done
echo "drain-update: $updated updated, $deferred deferred, $current already current"
