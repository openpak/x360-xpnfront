# Changelog

## v0.3.1 (2026-11-03)

- Every request logs one stderr line, `http <method> <path> -> <status> <duration>ms` (x360-common v0.5.1 `httplog`): no query, headers, cookies, tokens or bodies; `/healthz` skipped.

## v0.3.0 (2026-11-03)

- Presence blobs move to Postgres (schema `x360_xpnfront` in database `x360`, `X360_XPNFRONT_DATABASE_URL`, required: the
  service refuses to start without it). An existing `state.json` (`X360_XPNFRONT_STATE`) is imported once and left in place.

## v0.2.0

- Friend list status byte: 1 outgoing and 2 incoming pending requests from the account core (x360-common v0.3.0).
- Block (0xA00) and unblock (0xA01) go to the account core; invites 0x580/0x581/0x582/0x207 are validated and
  logged (delivery waits for x360-xmessaging); per-user 60-byte data 0x16/0x17 kept in a JSON state file
  (`X360_XPNFRONT_STATE`).
- New-mail flag: the 0x2 reply carries per-user hr 0x001512F0 when x360-xmessaging reports unread mail
  (`X360_XMESSAGING_INTERNAL_URL`/`_KEY`, fail-soft).

## v0.1.1

- Logoff tests carried over from x360-xuacs v0.10.0.

## v0.1.0

- Live service 1 split out of x360-xuacs v0.9.7, behaviour unchanged.
