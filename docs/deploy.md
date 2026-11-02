# Deploy

Primary host, quadlet `deploy/openpak-x360-xpnfront.container` (IP pinned in openpak.network), Traefik routes from
`deploy/traefik.yml` merged into the shared dynamic file. Service 1 must be in the KDC's D7 list.

State: the quadlet mounts `~/openpak/deploy/x360-xpnfront/state` (presence data, `state.json`). The new-mail flag
needs `~/openpak/deploy/x360-xmessaging/internal.env` (`X360_XMESSAGING_INTERNAL_KEY`); the quadlet sets the URL.
