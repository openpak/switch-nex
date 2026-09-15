# Mario Kart 8 Deluxe — switch-nex pilot

Port base 22150 (auth +0, secure +1, healthz +2). Station URL address:
the server's public IP (raw IP — the console dials station URLs without DNS).

Wire facts specific to this title (SPEC.md): speaks raw PRUDPLite over
TLS-TCP; LoginEx = TicketGranting 0x0A method 0x02 with a 1337-byte parameter
block carrying the full BAAS id token; expects the ValidateAndRequestTicket
Result structure (v1 header, 8-byte PID); secure stage is a ticketless CONNECT
matched by client IP.

Migration state: waiting on runtime milestones 1–3 (MIGRATION.md).
