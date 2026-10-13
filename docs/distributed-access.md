# Distributed access with existing WireGuard clients

This fork preserves the upstream fanout architecture and adds local periodic
reconciliation against the shared database. Stock WireGuard clients can switch
between gateways by changing only Endpoint, provided every gateway has the same
server identity and authorizes the same client public key, PSK and address.

## Recovery of missed events

Configure every node with the same database encryption passphrase and a unique
`core.cluster_node_id`. Enable `core.fanout.enabled`, configure its authenticated
peer URLs, and set `core.peer_reconcile_interval`, which defaults to `30s`.
Setting the interval to `0` disables periodic reconciliation.

Fanout still applies online create/update/delete events immediately. The local
reconciliation loop compares the shared DB with existing local kernel peers.
It repairs missing peers, changed AllowedIPs/PSKs/keepalive and revocations,
including peers deleted while fanout was disconnected. No incoming fanout or
additional event is required for recovery. The loop starts after initialization
and does not recreate interfaces, alter routes or send new fanout requests.

The loop reuses the existing interface peer sync handler. It applies the stored
peer configuration without replacing the interface. Leave client peer endpoints
unset in the shared DB to retain each node's authenticated roaming endpoint. A database or kernel read failure is an error,
never an empty peer set; the current kernel state is retained and the next cycle
retries. Expired-peer cleanup remains a separate upstream feature.

The expected catch-up bound is one reconciliation interval after the DB becomes
readable, plus query/application time. Existing kernel authorization continues
during DB failure; revocations cannot reach a disconnected node until it can
read the authoritative DB again. This is eventual consistency, not synchronous
revocation across a partition.

## Existing public-key-only peers

The v1 peer API accepts an empty client `PrivateKey` and requires a valid public
key. Keep `Identifier` equal to `PublicKey`; submit the original PSK and address.
Client private keys remain on their devices and are not regenerated during
import. Such peers cannot produce a new downloadable client private-key profile.

With `force_client_ip_as_allowed_ip: true`, put only the client's own address in
`Addresses`; put routed LAN prefixes in `ExtraAllowedIPs`. This preserves gateway
peers without granting the entire VPN pool to one peer. Do not import learned
client endpoints as fixed configuration across gateways.

Clearing a configured PSK or keepalive now supplies explicit zero values to
wgctrl. Nil would mean "keep the existing value" and silently retain old settings.

## Example

```yaml
core:
  import_existing: false
  restore_state: true
  sync_on_startup: true
  manage_dns: false
  ignore_main_default_route: true
  cluster_node_id: gateway-a
  peer_reconcile_interval: 30s
  force_client_ip_as_allowed_ip: true
  fanout:
    enabled: true
    self_url: http://gateway-a:8888
    peers: [http://gateway-b:8888]
    auth_header: Authorization
    auth_value: "Basic <base64 of admin_user:admin_api_token>"
    timeout: 5s
    topics: ["peer:created:sync", "peer:updated:sync", "peer:deleted:sync"]
```

Set `WG_PORTAL_CONFIG` to the configuration filename. Fanout belongs under
`core`, not at the YAML root. API credentials use HTTP Basic, not a Bearer token.
Transport the control API over an authenticated private network or HTTPS.

The gateway routes, NAT, DNS and application reachability still require explicit
configuration. Sharing a DB does not provide them. When another native manager
owns an existing interface, leave startup interface restoration disabled there;
the recovery loop manages its peer authorization without replacing the interface.
Cold startup without a reachable DB requires a separately designed native local
configuration; this patch does not introduce an offline database replica.

## Validation

Run on Linux (the project's netlink adapter is Linux-specific):

```sh
cd frontend
npm ci --no-audit --no-fund
npm run build
cd ..
go test -race ./...
```

The regression tests cover missed creation/deletion, disabled peers, preserving
roaming endpoints and unchanged handshakes, changed authorization, query failures,
explicit PSK/keepalive removal and public-key-only import validation.
Multi-node kernel tests should additionally block only fanout, then separately
block DB access, and verify automatic convergence without manual sync/restarts.
