# NATS mechanics for a one-channel global tier

Research date: 2026-09-14. Sources are docs.nats.io (2.14 is the latest line; 2.12 maintained; 2.11 end of life since 2026-04-27, see https://docs.nats.io/release-notes), the archived pre-2.14 docs in the nats-io/nats.docs `master` branch, nats-server `main` source, and nats-server GitHub releases. Anything not confirmed by one of those is marked UNVERIFIED.

## Summary and recommendation

Use leaf nodes. Each host's local broker adds one `leafnodes.remotes` entry that dials the global broker with a per-host credential; the global broker runs JetStream in its own domain (`global`), each local broker keeps its own domain, and the system account is not shared across the link. Only subject interest crosses the link, so team traffic on `agent.<workspace>.<team>.>` never leaves the host unless something on the hub subscribes to it, and the hub-side user permissions plus `deny_exports`/`deny_imports` on the remote make that impossible rather than merely unlikely. Leaf-side clients reach the hub's JetStream stream and KV bucket by addressing the `global` domain (client Domain option, `$JS.global.API` prefix). When the hub is unreachable the local broker keeps serving local clients and reconnects on its own; nothing is buffered for core NATS, so put anything that must survive a WAN gap in a leaf-local stream and have the hub `source` it. Gateways are the wrong shape: both sides must be clusters that can dial each other, and interest handling is heavier. One shared cluster with accounts would isolate traffic correctly but forces every local message across the WAN and removes the local-when-disconnected property.

## Mechanism 1: leaf nodes

Facts verified against current docs and source.

- Direction and interest. A leaf is a server that opens an outbound connection to a hub and bridges subject interest; "the leaf carries only subjects that have interest on the other side. This is the same interest propagation clusters use internally". Wire ops are `LS+`/`LS-` (interest) and `LMSG` (data). https://docs.nats.io/learn/topologies/leaf-nodes and https://docs.nats.io/reference/protocols/leafnode
- Local clients stay hidden. The hub sees one leaf link, not the leaf's clients. Subject boundaries are an account decision, not a property of the link: "Bound to its own account, only the subjects that account imports and exports cross the link". Same page.
- Restricting what crosses, three layers:
  1. Hub-side user permissions. The archived docs state the rule directly: "Subjects that the user is allowed to publish are exported to the cluster. Subjects the user is allowed to subscribe to, are imported into the leaf node." https://github.com/nats-io/nats.docs/blob/master/running-a-nats-service/configuration/leafnodes/README.md. Source confirms the hub reverses pub/sub on the accepted connection because "data is flowing in the opposite direction" and sends the resulting import/export sets back to the leaf for local enforcement (`sendPermsAndAccountInfo`, server/leafnode.go on main).
  2. The `leafnodes.authorization` block on the hub accepts `username`, `password`, `nkey`, `account`, `users[]` (username, password, account, proxy_required) and `timeout`. It has no `permissions` field (reference plus `parseLeafAuthorization` in server/opts.go). To bind subject permissions, authenticate the leaf as a user in the hub's `accounts { X { users: [...] } }` block, which does carry `permissions`; the archived docs say a leaf "can authenticate against any user account on the hub ... including those defined in the accounts themselves". https://docs.nats.io/reference/config/leafnodes/authorization and https://github.com/nats-io/nats.docs/blob/master/running-a-nats-service/configuration/leafnodes/leafnode_conf.md
  3. Remote-side `deny_imports` / `deny_exports` (string or list). Archived wording: deny_imports are "subjects which will not be imported over this leaf node connection. Subscriptions to those subjects will not be propagated to the hub"; deny_exports the mirror. Reload caveat: "On 2.11/2.12 the reload returns success; the new deny_exports take effect only after a restart." https://docs.nats.io/reference/config/leafnodes/remotes/deny_exports. There is no `deny_publish`/`deny_subscribe` key on the remote block; those names do not exist in the reference. Account exports/imports are a fourth lever when the hub keeps the channel in a separate account (see Mechanism 3).
- Account binding on the leaf. `remotes.account` names the LOCAL account whose traffic is forwarded; `credentials` is a creds file "when decentralized auth is used on the remote"; `nkey` is a user seed; user/password go in the URL (`nats-leaf://user:pass@host:7422`). https://docs.nats.io/reference/config/leafnodes/remotes
- 2.12 additions. `isolate_leafnode_interest` (hub and per-remote) stops interest learned from one leaf propagating to other leaves, replacing the "same cluster name" workaround; `request_isolation` asks the hub to do it; `disabled: true` drops a remote on reload. 2.14 adds remotes add/remove on reload. https://docs.nats.io/release-notes/upgrade-to-2.12 and https://docs.nats.io/release-notes/upgrade-to-2.14
- JetStream across the link (the load-bearing part):
  - When the leaf and hub have different domains, or the system account is not shared, the server logs "JetStream using domains: local X, remote Y" and merges a deny of `$JS.API.>`, `$KV.>`, `$OBJ.>` in both directions on the link (`denyAllClientJs`, server/jetstream.go and `leafnode.go` on main). So bare `$JS.API` requests and bare `$KV.bucket.key` publishes never cross.
  - For every non-system account on the link the hub adds mappings `$JS.<hubdomain>.API.>` to `$JS.API.>` and `$JS.<hubdomain>.API.$KV.>` to `$KV.>` (same code; visible as "Adding JetStream Domain Mapping" debug lines in https://github.com/nats-io/nats-server/issues/6293, where the maintainer calls it auto-clamping: "By default the domain you are connected to is used. If you want to interact with a different domain across a leafnode you need to have the domain in the JS API subject").
  - Client side: nats.go `jetstream.NewWithDomain(nc, "global")` sends API requests as `$JS.global.API.…`; the KV `Put` path prepends the API prefix to `$KV.<bucket>.<key>` when the prefix is not the default (`useJSPfx` in jetstream/kv.go on nats.go main). The nats CLI equivalent is `--js-domain global` (archived leaf JetStream doc). So a leaf-side client can create consumers on, publish into, and read from a hub stream, and read/write a hub KV bucket, provided the hub-side permissions allow `$JS.global.API.>` publish and `_INBOX.>` subscribe. Reply and ack subjects: `_INBOX` replies need subscribe permission; `$JS.ACK.` subjects are forwarded without permission checks (`leafMsgAllowed`, server/leafnode.go).
  - Domain rules: "Every server in a cluster and super cluster needs to have the same domain name ... domain names can only change between two servers if they are connected via a leaf node connection." Domain names must be unique. https://github.com/nats-io/nats.docs/blob/master/running-a-nats-service/configuration/leafnodes/jetstream_leafnodes.md
- Hub unreachable. The archived doc names the use case: a leaf provides "a local NATS network even when the connection to a hub or the cloud is down", and JetStream domains exist "to support such a disconnected use case". Local clients keep working; the leaf retries the remote at `leafnodes.reconnect` (server default `DEFAULT_LEAF_NODE_RECONNECT = time.Second`, server/const.go). Core messages with hub-side interest are dropped while the link is down (core NATS is not buffered; a JetStream publish addressed to the hub domain returns no ack and the client sees a timeout). Durable pattern: keep a leaf-local stream and have the hub `source` it, since "Once copied, accessing the data is independent of the leaf node connection being online ... This is the recommended way to exchange persistent data across domains." Same archived doc. Loop detection or a permission violation forces a 30 s reconnect delay (server/leafnode.go constants).
- TLS and mTLS. The hub's `leafnodes.tls` block is separate from client TLS; `verify: true` requires and verifies client certificates; `verify_and_map` maps the cert to a user and requires restart. The remote's `tls` block carries `ca_file`, `cert_file`, `key_file` "for connecting/authenticating with the remote if mutual TLS is required"; `verify_and_map` is "Silently ignored for leafnode remotes entirely". On 2.11/2.12 remote TLS material only changes after a restart. https://docs.nats.io/reference/config/leafnodes/tls/verify, https://docs.nats.io/reference/config/leafnodes/remotes/tls/verify_and_map, https://docs.nats.io/reference/config/leafnodes/remotes/tls. `handshake_first` (TLS before INFO) arrived for leaf links in 2.11 and must be set on both sides. https://github.com/nats-io/nats.docs/blob/master/release_notes/whats_new_211.md

## Mechanism 2: gateways and super-clusters

- A gateway "joins one cluster to another cluster"; the `gateway.name` must equal the `cluster.name` or the server refuses to start; every server in the cluster carries the same gateway block, each server opens one gateway connection to each other cluster, and gateways gossip so the list need not be complete. https://docs.nats.io/learn/topologies/super-clusters
- Both sides must accept connections: "A gateway joins two clusters that can both accept connections from each other. The factory can't accept anything." That sentence is the docs' own leaf-versus-gateway rule. https://docs.nats.io/learn/topologies/leaf-nodes
- Interest handling is two-mode: gateways start in optimistic mode (forward, learn `RS-` no-interest), then switch per account to interest-only mode after too many `RS-`. TLS `verify` is always on for gateways, and certificates need both serverAuth and clientAuth key usages. Reload covers only cert material. https://docs.nats.io/reference/protocols/gateway, https://docs.nats.io/reference/config/gateway, https://docs.nats.io/learn/security/encryption
- Gateways add no boundary: "Neither [routes nor gateways] partitions anything ... The one layer that can draw a boundary is the leaf, and only when you bind it to its own account." https://docs.nats.io/learn/topologies/putting-it-together
- Verdict: wrong fit. Local brokers are single servers, not clusters, and hosts behind NAT cannot accept a dial-in. A gateway would also require the same JetStream domain everywhere.

## Mechanism 3: one shared cluster with accounts

- Accounts are the unit of isolation, "a flat space of subjects separate from every other"; users inside an account get allow/deny publish and subscribe lists, and "an allow-list closes everything else" (an empty list is not a lock-down; use `deny: [">"]`). https://docs.nats.io/learn/security/authorization
- Cross-account sharing is an explicit export/import pair per subject: stream exports for one-way flow, service exports for request/reply; an import with no matching export fails at startup. https://docs.nats.io/learn/security/cross-account
- Cost: every host's team traffic (inbox stream, AGENT_STATE presence writes at a 90 s cadence, all `agent.*` publishes) traverses the WAN to the cluster, and a WAN outage stops the local team, which contradicts the local-when-disconnected requirement. Clustering also assumes low latency: "A full mesh assumes the members are close together." https://docs.nats.io/learn/topologies/super-clusters
- The account model is still useful inside Mechanism 1 on the hub: put the director channel in its own hub account and bind each host's leaf user to it, so nothing else on the hub can see `global.>` without an export.

## Credentials bound to a subject prefix

- Static users with permissions (config mode). Three credential styles: user/password (bcrypt via `nats server passwd`), NKey (config holds only the public key; the client signs a server nonce, "nothing secret crosses the wire"), and token. `accounts.*.users` is hot-reloadable, so a new host is a config edit plus `nats-server --signal reload`. Revocation is deleting the entry and reloading. https://docs.nats.io/learn/security/authentication-basics, https://docs.nats.io/reference/config/accounts
- Operator/JWT mode (`nats auth`, formerly nsc). The server needs a resolver (recommended: the full NATS resolver with a directory) and account JWTs are pushed over NATS per account; revocation writes a revocations entry into the account JWT and the next push disconnects the user; creds carry their own expiry. Heavier to run, but adding a host never touches server config. https://docs.nats.io/learn/security/operator-mode, https://docs.nats.io/learn/security/decentralized-auth
- Templates. `{{name()}}` and `{{tag(key)}}` in permission subjects exist only for scoped signing keys in JWT mode: "Templates to scoped signing key user permissions" shipped in nats-server v2.9.0 (2022-09-09, https://github.com/nats-io/nats-server/releases/tag/v2.9.0). Current source also expands `{{account-name()}}`, `{{account-subject()}}`, `{{account-tag(k)}}` in `processUserPermissionsTemplate`, which is called only from the JWT scoped-signer path (server/auth.go). Config-file users do not get templates; version that added the account-* forms is UNVERIFIED. For a static-config fleet the equivalent is one explicit user per host with `global.<host>.>` spelled out.
- Which one here. For a handful of hosts, static NKey users in the hub's `accounts` block: no shared secret on the wire, per-user permissions, hot reload. Move to operator mode when hosts are added often enough that editing the hub config is the bottleneck.

## TLS

- Server TLS: `cert_file`, `key_file`, `ca_file`; each connection type (client, cluster, gateway, leafnode, websocket, mqtt, monitoring) has its own `tls {}` block and turning on one leaves the others plaintext. Certs are read at startup and swapped on reload for new connections only. https://docs.nats.io/learn/security/encryption
- mTLS: `verify: true` requires a client cert signed by `ca_file`; `verify_and_map: true` additionally matches the cert subject (DN-aware, or SAN email/URI) to a `user` entry and authenticates without a password; the docs advise pasting the DN from `openssl x509 -noout -subject`. Same page and https://docs.nats.io/reference/config/tls/verify_and_map
- On leaf links: `verify_and_map` on the hub's `leafnodes.tls` works and maps the leaf's cert either to a user in `leafnodes.authorization.users` or, when that block sets no credentials at all, to the general accounts users table (`isLeafNodeAuthorized` and the TLSMap condition in server/auth.go). It requires restart to change. It is ignored on the remote side, where the cert is just presented.

## TTL and scheduling features relevant to presence and escalation

- 2.11 per-message TTL: `Nats-TTL` header, stream must set `AllowMsgTTL` (irreversible); `SubjectDeleteMarkerTTL` leaves markers when MaxAge deletes a subject's last message. Relevance: escalation messages in the global stream can expire on their own without a purge job. https://docs.nats.io/learn/jetstream/message-ttl, https://github.com/nats-io/nats.docs/blob/master/release_notes/whats_new_211.md
- KV per-key TTL rides on bucket limit markers (`nats kv edit BUCKET --marker-ttl 1h`, then create with a TTL); TTL is set only at create. Relevance: presence keys can carry their own lifetime instead of the bucket-wide 90 s max age. https://docs.nats.io/learn/key-value/ttl-and-limits
- 2.12 delayed scheduling: `AllowMsgSchedules` plus `Nats-Schedule: @at <RFC3339>` with `Nats-Schedule-Target` (a subject in the same stream); one schedule per subject. 2.14 adds recurring `@every`/cron schedules with time zones and `Nats-Schedule-Rollup`. Relevance: a "remind me if not acknowledged by T" escalation timer without an external scheduler. https://github.com/nats-io/nats-architecture-and-design/blob/main/adr/ADR-51.md, https://docs.nats.io/release-notes/upgrade-to-2.14
- 2.11 consumer pausing (`PauseUntil`) and 2.12 atomic batch publish are available but not needed for this channel. 2.12 also turns on strict JetStream API by default (invalid requests are rejected, not just logged).
- 2.14 warning that touches ACLs: v2 ack/flow-control subjects `$JS.ACK.<domain>.<account hash>.…` become the default in 2.15; permissions or imports written as `$JS.ACK.<stream>.>` must be widened to `$JS.ACK.>` before then. https://docs.nats.io/release-notes/upgrade-to-2.14

## JetStream-over-leaf gotchas

- Same domain plus shared system account means the leaf extends the hub's JetStream instead of running its own: "a stream you create on the factory floor may land on the hub, not locally. Give the leaf its own JetStream domain when you want a distinct local store." https://docs.nats.io/learn/topologies/leaf-nodes. Source: with the system account connected and domains equal, the leaf's meta controller is put into observer mode ("Extending JetStream domain"); with domains differing it logs "JetStream not extended, domains differ" and denies all JS traffic on the system-account link. Extension also requires the central JetStream to be clustered (archived known issue).
- Do not share the system account across the link for this design; it is what makes extension possible and it exposes hub-wide monitoring to every host.
- Same subject in two domains in one account: "each stream will store the message" if both have interest while connected. Keep `global.>` out of every local stream's subject list.
- Mirrors and sources across domains use the `external` block with `api: "$JS.<domain>.API"`; across accounts the consumer API and flow-control subjects must be service exports and the delivery subject a stream export, and "Get a type wrong and replication doesn't fail with an error; the mirror never catches up." Across accounts, push consumers are unsupported (archived known issue). https://docs.nats.io/learn/jetstream/mirrors-and-sources
- 2.14 supports sourcing from WorkQueue and Interest streams with a durable consumer and `AckFlowControl`; earlier servers use an ephemeral, less reliable path. https://docs.nats.io/release-notes/upgrade-to-2.14
- Stream placement: a stream lives in the domain whose API created it; a leaf-side client with the default prefix creates streams on the leaf, with the `global` prefix on the hub. Placement tags cannot move a stream across domains (implied by the domain rules; explicit statement UNVERIFIED).
- If the hub later becomes a cluster: "if one server in a cluster accepts leaf node connections, all servers need to", and a remote's URL list should point at every hub server (archived README).
- Same domain name on hub and leaf is a misconfiguration the server only partly guards against (the outgoing `$JS.<domain>.API.>` deny in leafnode.go covers "a hub and a spoke having the same domain name. But not two spokes having the same one").
- Per-account leaf cap: `accounts.<name>.limits.max_leafnodes` exists if a host should be allowed exactly one link. https://docs.nats.io/reference/config/accounts/limits/max_leafnodes

## Config sketches (illustrative, untested; verify the ACLs on a scratch pair first)

Global hub, static NKey users, one account for the channel, its own domain, mTLS on the leaf listener:

```
server_name: global-hub
listen: 0.0.0.0:4222
jetstream { store_dir: /var/lib/nats/js, domain: global }
accounts {
  SYS: { users: [ { user: sys, password: "$2a$11$..." } ] }
  DIRECTOR: {
    jetstream: enabled
    users: [
      { nkey: UDIRECTOR...,   # the director process
        permissions: { publish: ["global.>", "$JS.API.>"],
                       subscribe: ["global.>", "_INBOX.>", "$JS.API.>"] } }
      { nkey: UHOSTKINU...,   # leaf link from host kinu; pub = what kinu may send in,
                              # sub = what kinu may receive (server reverses on the hub)
        permissions: { publish:   { allow: ["global.kinu.>", "global.director.inbox",
                                            "$JS.global.API.>"] },
                       subscribe: { allow: ["global.director.>", "global.kinu.>",
                                            "_INBOX.>"] } } }
    ]
  }
}
system_account: SYS
leafnodes {
  listen: 0.0.0.0:7422
  tls { cert_file: hub.crt, key_file: hub.key, ca_file: fleet-ca.crt, verify: true }
}
```

Leaf (one local broker), keeping its own domain and its existing local accounts:

```
server_name: kinu
listen: 127.0.0.1:4222
jetstream { store_dir: /var/lib/nats/js, domain: kinu }
accounts { FLEET: { jetstream: enabled, users: [ ...existing static users... ] } }
leafnodes {
  remotes: [ {
    urls: ["nats-leaf://hub.example:7422"]
    account: FLEET            # local account whose interest is bridged
    nkey: "SUHOSTKINU..."     # seed for UHOSTKINU (or credentials: kinu.creds in JWT mode)
    tls { ca_file: fleet-ca.crt, cert_file: kinu.crt, key_file: kinu.key }
    deny_exports: ["agent.>", "$KV.>"]   # never send team traffic up
    deny_imports: ["agent.>"]            # never accept team subjects down
  } ]
}
```

Client on the leaf addressing the hub stream and KV: `jetstream.NewWithDomain(nc, "global")` in nats.go, or `nats --js-domain global stream info GLOBAL_CHANNEL`. Local streams keep the default context.
