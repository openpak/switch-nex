# Protocol: switch-nex

## SMB35 gameplay

Super Mario Bros. 35 additionally uses the [Eagle gameplay relay](eagle/README.md)
on TLS/WebSocket port 22163. Its lobby issues authenticated player-specific
handoffs when `SMB35_EAGLE_URL` is configured. The relay handles node membership,
shared counters and game-packet routing in the same process as the title's NEX
server. The generated SMB35 quadlet enables it; other titles do not.

## Bans

An account banned in the OpenPak admin is refused at login with `RendezVous::AccountDisabled`
and its secure connections are dropped within a minute; nx-baas's
`/internal/switch/identity` (403 `account_banned`) is the source. Nothing to configure beyond
the identity URL and key.

