// Package wire holds the header names the x360 services pass each other.
package wire

// AccountHeader carries the session's console account (the KDC account name,
// "<console>@xbox.com") from the security gateway to the service backends.
const AccountHeader = "X-X360-Account"

// UsersHeader lists the users signed in on the tunnel's ticket as
// "<online xuid hex>:<OpenPak account id>" separated by commas.
const UsersHeader = "X-X360-Users"

// GatewayKeyHeader proves a request came from the gateway: the gateway sends
// the shared key, and backends reachable by anyone (the public Traefik) drop
// AccountHeader/UsersHeader from requests that lack it.
const GatewayKeyHeader = "X-X360-Gateway-Key"
