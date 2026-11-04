# Deploy

Primary host, quadlet `deploy/openpak-x360-xpnfront.container` (IP pinned in openpak.network), Traefik routes from
`deploy/traefik.yml` merged into the shared dynamic file. Service 1 must be in the KDC's D7 list.

State: the quadlet mounts `~/openpak/deploy/x360-xpnfront/state` (presence data, `state.json`). The new-mail flag
needs `~/openpak/deploy/x360-xmessaging/internal.env` (`X360_XMESSAGING_INTERNAL_KEY`); the quadlet sets the URL.

## Account erasure

`DELETE /internal/accounts/{account_id}/data` with `Authorization: Bearer $ACCOUNT_ERASE_KEY` on a separate listener `ERASE_ADDR` (default `:9099`, container network only, never published; not served while the key is unset). Reply `{"service":"x360-xpnfront","rows":N}`; one `account erased` log line per erasure.

Website step name: `x360-xpnfront`.

Deletes the account's 60-byte presence blob from the `blobs` document. Friends, blocks and messages are account-core / xmessaging data. Run before x360-xuacs.
