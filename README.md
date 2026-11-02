# Xbox 360 Presence and Friends Server

OpenPak's Xbox 360 presence and friends server (Live service 1, `xpnfront`).

| | |
| --- | --- |
| Network | Xbox 360 |
| Role | Presence alive/logoff and the friend and block lists the console shows (`XUserGetFriends`, the Friends blade) |
| Depends on | [`x360-common`](https://github.com/openpak/x360-common) (vendored); the account core (social graph); [`x360-xuacs`](https://github.com/openpak/x360-xuacs) internal API (online XUIDs, gamertags, console profiles) |
| Image | `ghcr.io/openpak/x360-xpnfront` |
| Used by | XAM through the security gateway |
| Licence | AGPL-3.0-only |

## What it serves

| Path | XRL | What |
| --- | --- | --- |
| `POST /xpnfront/xpresence.srf` | 0x2, 0x7, 0x9, 0xA, 0xF, ... | Presence alive (reply: friend and block lists with versions) and logoff |
| `GET /healthz` | | Health probe |

Identity headers (`X-X360-Account`, `X-X360-Users`) are trusted only with `X360_GATEWAY_KEY`. Split out of
[`x360-xuacs`](https://github.com/openpak/x360-xuacs), one repository per Live service.

## Run

```sh
GOWORK=off go run -mod=vendor ./cmd/x360-xpnfront
```

Configuration:

- `X360_XPNFRONT_ACCOUNT_CORE_URL / _TOKEN`: account core
- `X360_XUACS_INTERNAL_URL / _KEY`: xuacs internal API

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
