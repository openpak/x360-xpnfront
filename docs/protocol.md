# Protocol: x360-xpnfront

Live service 1 (`xpnfront`), XAM 17559.

The presence-alive request (XRL 0x401 elements: 32-byte header, then a 0x98-byte body per user) names an online
XUID that must be on the tunnel's ticket (`X-X360-Users`). The reply echoes the header and carries, per user,
`u32 0, u32 friends version, u16 n, u32 blocks version, u16 m`, then n x `{u64 xuid, u16 name length incl. NUL,
u8 state (0 accepted), name, NUL}` and m x `u64 xuid`. Versions are a hash of the list bytes (0 for an empty list).
Friends come from the account core, filtered by its discovery check and the user's blocks; XUIDs and gamertags
come from x360-xuacs. Native limits: 100 friends, 250 blocks, gamertags of 15 ASCII bytes.

A logoff (0x400 elements, 40 bytes) is accepted for a ticket user, or after a console-only ticket replaced the
user's, for a profile x360-xuacs issued on the authenticated console.
