# Protocol: x360-xpnfront

Live service 1 (`xpnfront`), XAM 17559.

The presence-alive request (XRL 0x401 elements: 32-byte header, then a 0x98-byte body per user) names an online
XUID that must be on the tunnel's ticket (`X-X360-Users`). The reply echoes the header and carries, per user,
`u32 0, u32 friends version, u16 n, u32 blocks version, u16 m`, then n x `{u64 xuid, u16 name length incl. NUL,
u8 state (0 accepted, 1 outgoing request pending, 2 incoming request pending), name, NUL}` and m x `u64 xuid`. Versions are a hash of the list bytes (0 for an empty list).
Friends come from the account core, filtered by its discovery check and the user's blocks; XUIDs and gamertags
come from x360-xuacs. Native limits: 100 friends, 250 blocks, gamertags of 15 ASCII bytes.

The first u32 (the per-user hr) is 0, or 0x001512F0 when x360-xmessaging's `/internal/v1/unread?xuid=` reports
unread mail, which makes the console fetch its inbox. The lookup is fail-soft (error = 0).

Other requests on the same endpoint (layouts in the RE note re-friends-requests.md; all ticket-validated):
- 0xA00 / 0xA01 block / unblock, 48 bytes (header, u64 owner, u64 target): the target XUID is resolved through
  x360-xuacs and the account core's Block/Unblock is called; reply HTTP 200, empty body.
- 0x581 / 0x582 / 0x580 / 0x207 game invites: validated, logged, empty 200 (`ponytail:` delivery to the recipient's
  inbox comes with x360-xmessaging v0.2).
- 0x16 set / 0x17 get the 60-byte per-user presence data (state file `X360_XPNFRONT_STATE`). 0x16 reply: request
  header with msg_len 4 + `u32 0`; 0x17 reply: header with msg_len 6+n + `u32 0, u16 n, u8[n]` (n 0 or 60).

A logoff (0x400 elements, 40 bytes) is accepted for a ticket user, or after a console-only ticket replaced the
user's, for a profile x360-xuacs issued on the authenticated console.

## Fixtures

Replies are byte-identical to x360-xuacs v0.9.7, whose fixtures come from a console request and replies encoded independently with the XAM 17559 XRL codec.
