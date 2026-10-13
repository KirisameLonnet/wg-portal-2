# Distributed access with existing WireGuard clients

This fork preserves the upstream fanout architecture. Stock WireGuard clients
can switch gateways by changing only Endpoint, provided the gateways share the
server identity, client public keys, PSKs and addresses.

## Recovery of missed notifications

Online changes send an immediate peer notification. The receiving node reads
the current shared DB state and acknowledges only after applying the local
WireGuard configuration. A DB or kernel failure returns an error, so the sender
keeps that notification pending and retries after 1, 2, 4, 8, 16, then 30 seconds
until it succeeds or the application stops. Concurrent notifications for the
same destination and peer share one worker. Notifications carry an identifier,
not a stale create/update/delete command; a missing DB peer is removed locally.

Configure every node with the same DB encryption passphrase and a unique
`core.cluster_node_id`. Enable `core.fanout.enabled` and configure authenticated
peer URLs. `core.peer_reconcile_interval` defaults to `30s`; it performs local
configuration comparison as an additional recovery path. `0` disables this
comparison without disabling notification retries. Comparison runs once after
initialization, then at the configured interval. A low-frequency interval such
as `60s` is suitable when notifications provide normal propagation.

The periodic comparison covers notification loss across sender crashes: pending
notifications are in memory, not a durable event log. It adds missing peers,
removes revoked/disabled peers and writes only changed configuration. Unchanged
peers generate no WireGuard writes. AllowedIPs are compared as sets; handshake
times and traffic counters are ignored. An unset DB endpoint leaves each node's
authenticated roaming endpoint intact. Interface identity and routes are not
recreated by peer comparison.

A failed DB read is an error, not an empty peer set. Existing kernel authorization
continues during DB failure; revocations cannot reach a disconnected node until
it can read the authoritative DB again. This is eventual consistency, not
synchronous revocation across a partition. All nodes need this fork for the new
acknowledgement semantics. Expired-peer cleanup remains a separate feature.

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
  peer_reconcile_interval: 60s
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
the peer comparison manages authorization without replacing the interface.
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

Regression tests cover notification retry without a subsequent event,
notification coalescing, application failure responses, unchanged-peer write
suppression, removal/disable, roaming endpoint preservation, configuration-only
comparison, DB failures, explicit PSK/keepalive clearing and public-key-only
import. Multi-node kernel tests additionally block fanout and DB separately and
verify recovery without manual sync/restarts. Disable periodic comparison in a
fanout retry test to establish which recovery mechanism fixed the state.
