# Design brief 8: the global bus tier (R-86)

Status: design with a running MVP hub, 2026-09-14. Probe brief:
`probe/nats-global-tier/brief.md`. Artifacts: `probe/nats-global-tier/`
(hub config, provisioning, start and verify scripts, the leaf example, the
mokuzai recipe). Commissioned by the director seat under the operator's
forward program; placement ruled by the operator the same day (kinu now, a
cloud host with DNS and TLS later). Topology per R-86 is ratified and not
reopened here: one local NATS per marvel cluster for team traffic, events,
and heartbeats; one global NATS for the director-supervisor channel across
hosts. This brief answers how, and records what is proven.

Built on: R-77 (the bus enforces credential-to-subject binding), R-85
(marvel supervises NATS as a declared workload), R-86, R-92 (recipient-derived
routing), the director#4 authorization block, the R-56 shim timer, marvel's
`identity-at-launch-and-managed-nats.md` (section 4 and its open question on
the global credential story), and marvel-builder's R-85 answers of this date.
NATS facts were verified against the 2.10 to 2.14 documentation and server
source (`probe/nats-global-tier/nats-mechanics.md`, with a URL per fact and
the unverified items marked; the load-bearing ones are cited inline).

## 0. What is running

Hub on kinu (this is the operator's always-on host; the phase-0 local broker
stays on 127.0.0.1:4222): `server_name global-hub`, client 0.0.0.0:4242, leaf
0.0.0.0:7442, monitoring 127.0.0.1:8242, JetStream domain `global`, store
under `~/.director/nats-global`. nats-server 2.14.6. Account `FLEET` with four
NKey users: `admin` (provisioning, `>`), `director` (direct client for the
seat), `leaf-kinu`, `leaf-mokuzai` (one per cluster, bound to the cluster's
prefix). Provisioned: streams `GLOBAL_TO_DIRECTOR` (`global.director.>`),
`GLOBAL_TO_kinu`, `GLOBAL_TO_mokuzai` (`global.<cluster>.>`), bucket
`GLOBAL_PRESENCE` (ttl 90s). `verify-global.sh` passes 8 of 8 against it with
two throwaway leaves on this host (section 8).

## 1. Mechanism: leaf nodes (recommended and built)

Each cluster's local broker dials the hub with one `leafnodes.remotes`
entry and its own JetStream domain. Only subject interest crosses the link,
so `agent.<workspace>.<team>.>` traffic never leaves a host unless something
on the hub subscribes to it, and the hub user's permissions make that
impossible rather than unlikely. The hub sees one link per cluster, not the
cluster's clients. Leaf-side clients reach the hub's stream and bucket by
addressing the `global` domain (`jetstream.NewWithDomain`, `--js-domain
global`); bare `$JS.API` and `$KV` traffic is denied across the link by the
server itself when domains differ. When the hub is unreachable the local
broker keeps serving and the leaf retries every second; a global publish
fails loud (no responders) rather than queueing. Measured: distinct domains
logged on link-up, local publishes add nothing at the hub, local traffic
continues through a hub outage, both leaves relink and both streams keep
their messages after a hub restart, and adding a domain to an existing broker
keeps its streams, bucket, and default-prefix clients working.

Rejected. Gateways and superclusters: both sides must be clusters that can
dial each other ("the factory can't accept anything" is the docs' own rule),
the JetStream domain must be the same everywhere, and gateways draw no
account boundary; a host behind NAT cannot be a gateway peer. One shared
cluster with accounts: isolation would be correct, but every local message
would cross the WAN and a WAN outage would stop the team, which contradicts
the local-when-disconnected property R-86 exists for. The account model
still applies inside the hub (section 4).

## 2. Partition and subject grammar

Crosses the global bus, and nothing else:

| flow | subject | stream |
|---|---|---|
| director to a cluster's supervisor | `global.<cluster>.supervisor.inbox` | `GLOBAL_TO_<cluster>` |
| a supervisor to the director | `global.director.inbox` | `GLOBAL_TO_DIRECTOR` |
| presence of global principals | bucket `GLOBAL_PRESENCE`, key `presence.<cluster>.<role>.<instance>` and `presence.director.<instance>` | (KV) |
| escalations (future) | `global.<cluster>.escalation`, `global.director.escalation` | same streams |

Stays local: `agent.<workspace>.<team>.<id>.inbox`, role inboxes, team
broadcast, `agent.audit`, `AGENT_STATE` worker presence, marvel events and
heartbeats. Two role words exist at the global tier, `supervisor` and
`director`; a worker never holds a global address. `<cluster>` is a subject
token in the `[A-Za-z0-9_-]` class, operator-assigned, one per marvel
cluster; the hostname is the natural choice (`kinu`, `mokuzai`). One stream
per direction per cluster is deliberate: it is what lets the hub bind a leaf
to its own stream by name (section 4), the residual director#4 could not
close on a shared stream.

Identity without rename (R-06, R-79, R-86): a supervisor keeps its local id
(`fleet-supervisor-g1-0` at `agent://fleet/...` in its workspace); at the
global tier it is addressed as `global://<cluster>/supervisor` and its
presence record carries the local id, workspace, team, and instance, so both
tiers name the same session and nothing is renamed. R-92 at this tier is the
liveness half only: the subject is fully determined by the address, and the
sender refuses when `presence.<cluster>.supervisor.*` has no live record.

## 3. Placement and operation

Ruled: kinu now, cloud later. Ports chosen after checking what listens on
kinu (dolt on 3307, the phase-0 broker on loopback 4222 and 8222, nothing on
the LAN interface): 4242 client, 7442 leaf, 8242 monitoring on loopback only.
Discovery is the LAN address in each cluster's leaf remote today; the cloud
move is a URL, a CA, and a TLS block on the hub's client and leaf listeners,
with the same users, streams, and subjects, so it is a reconfiguration.
Operation: `hub/start.sh` (pidfile, log), `hub/provision.sh` (idempotent
streams and bucket; a bare hub is the finding-166 failure at the global
tier), a launchd unit is a ticket. Monitoring: `/leafz` on 8242 lists live
cluster links; marvel-builder's recommendation stands that a cluster surfaces
its own link state on the events ring, never as a health gate.

## 4. Credential-to-subject binding across hosts (R-77)

Two layers, composed. At the hub: each cluster's leaf authenticates as an
NKey user in the `FLEET` account, and for a leaf link the server reads the
user's publish list as what the cluster may send in and its subscribe list as
what the hub forwards down (verified in the docs and in
`server/leafnode.go`); the hub also pushes the permission set down so the
leaf enforces it locally, which is why a refused publish returns "no
responders" at once instead of a timeout. `leaf-mokuzai` may publish
`global.director.inbox` and `global.mokuzai.>`, the JetStream API only for
`GLOBAL_TO_mokuzai` and `KV_GLOBAL_PRESENCE` (consumer create, info, next,
delete; stream info; the KV put, get, and watch subjects), and acks; it may
subscribe `global.mokuzai.>` and `_INBOX.>`. `leaf-kinu` (the director's
host) mirrors that against `global.*.supervisor.inbox`, `global.director.>`,
and `GLOBAL_TO_DIRECTOR`. `director` is the same binding for a direct client
connection, kept for the window before the phase-0 broker relaunches as a
leaf. Proven: a mokuzai publish to another cluster's prefix, to a subject
outside its allow, a consumer on the director's stream, and a pull from it
all refuse and store nothing; anonymous connections get an authorization
violation. At the local broker: the director#4 block decides which local
session may publish the global prefix at all (the supervisor role, nobody
else); a worker publishing `global.>` is refused before the link. The
envelope's `sender.principal` (reserved) will carry cluster and role once the
identity lane lands; until then the name on a message stays display text
(R-82) and the credential is the authority.

Credential lifecycle (ADR-009: issuance within the operator's trust domain,
revocable, not third-party custody): the hub operator mints one NKey per
cluster (`nats auth nkey gen user`), pastes the public key into the hub
config, and hands the seed to the cluster's owner privately; the seed lives
0600 under that cluster's marvel or director home and reaches the broker
through the environment at start. Revocation is removing the public key and
reloading the hub (the `accounts.users` block is hot-reloadable); rotation is
mint, paste, reload, hand over. Static NKey users are the right size for a
handful of clusters; the operator/JWT model is the step when adding a cluster
becomes frequent enough that editing the hub config is the bottleneck.

## 5. The multi-user boundary (SOUL section 3)

The global bus is shared transport, not shared identity. skippy's supervisor
runs under skippy's own harness credentials on his host, connects only to his
own local broker, and reaches the director because his broker's leaf link
holds a cluster credential the operator minted and can revoke. No consumer
OAuth crosses hosts; no agent session holds the hub credential; the director
holds its own. What the binding enforces across the principal boundary is
exactly section 4: his cluster can write to the director and to its own
subtree, read its own subtree, and nothing else; the director can write to
any cluster's supervisor inbox and read its own. A forged sender name buys
nothing because routing and authority both come from the credential.

## 6. Envelope and direction anchors (not reopened)

The phase-0 envelope rides the global tier unchanged inside NATS messages
with `Nats-Msg-Id` for dedupe; the address grammar gains the `global://`
forms, which is a shim change and not a schema change. A2A v1.0 remains the
target envelope with codegen; the global tier is where it earns its place
(cross-organization boundary), and switching the payload is independent of
the topology built here. NATS stays the transport. SLIM stays watched: its
end-to-end encryption is the one property this tier lacks, and it becomes
material at the cloud placement, where the hub host is not the operator's
own machine; the interim LAN posture is stated in section 9. Matrix remains
under examination as a director substrate; nothing here forecloses it, since
the global tier carries one channel that a Matrix room could also carry.

## 7. marvel's role (R-85; marvel-builder's answers, this date)

Nothing of the supervise-an-external-component pattern is built in marvel
today (`internal/otel` is a stub; no workload kind fits a non-agent daemon;
the generic adapter with process-alive health is the only mechanical
interim). The build items, in marvel's court: a non-agent workload kind, or
an explicit interim ruling to run the local broker through the generic
adapter; a `bus` section on `Cluster` in `~/.marvel/config.yaml`
(`internal/config/config.go:173`) carrying hub URL, CA path, leaf credential
path, and the cluster's subject token, with marvel rendering the local
`nats-server.conf` (leaf remote, local domain) under `~/.marvel` the way
policy projection writes a file; `Cluster.Name` validated to the subject
token class at add and load (today unvalidated, "my.cluster" is accepted);
leaf-link state on the events ring, not a health state; the local broker as
a hard pre-agent dependency and the hub as best-effort. marvel does not run
the hub; the hub is infrastructure each cluster connects up to. The leaf
credential is per cluster and held by the broker process, never injected into
a session; R-85's per-session credential stays local-broker-scoped and only
the supervisor role is granted the global prefix by the local authorization
block. Topology stays out of the workspace manifest, the same portability
principle as R-92 and the workdir default (marvel#255).

## 8. Proof (verify-global.sh, 2026-09-14 16:27 local, 8 of 8)

1. Leaf link up with distinct domains (`local "verifymokuzai", remote
   "global"`).
2. Presence written from the mokuzai leaf, read from the kinu leaf.
3. A JetStream publish from the kinu leaf to
   `global.mokuzai.supervisor.inbox` stored in `GLOBAL_TO_mokuzai` and pulled
   by a durable created from the mokuzai leaf through the domain.
4. The reverse on `global.director.inbox` into `GLOBAL_TO_DIRECTOR`.
5. A mokuzai publish to `global.kinu.supervisor.inbox` refused, nothing stored.
6. A mokuzai consumer on `GLOBAL_TO_DIRECTOR` refused.
7. A mokuzai publish to a subject outside its allow refused.
8. A local `agent.>` publish adds nothing at the hub.

By hand, also this sitting: with the hub killed, local publishes continue
and a global publish fails loud; after restart both leaves relink and both
streams hold their messages; anonymous connections to 4242 get an
authorization violation; the `director` NKey works over the LAN address.
Not yet proven, because the shim's global mode is unbuilt: receipts with
`in_reply_to` across the link, presence from a real supervisor session, and
the R-92 liveness refusal in the shim. Those are the shim ticket's tests.

## 9. What the interim LAN posture leaves unprotected (operator ratifies)

No TLS on 4242 or 7442. A device on the LAN can read every envelope on the
wire and can attempt connections. It cannot publish, subscribe, or forge a
cluster without a minted NKey (the seed never crosses the wire; the client
signs a server nonce). Consequences accepted for the interim: message bodies
are readable on the LAN, so no secret ever rides content (already the rule);
the leaf port is reachable by any LAN device; there is no rate limit beyond
the 64 KiB message cap and the 24h stream age. Deferred to the cloud phase:
TLS on both listeners with a fleet CA, mutual TLS on the leaf listener
(`verify: true`), DNS, and public reachability.

## 10. Candidate requirements (for the operator; not self-ratified)

- **R-94 (candidate).** A cluster name is a subject token in the identity
  class and a namespace at the global tier; exactly two role words exist
  there, `supervisor` and `director`; a worker never holds a global address.
- **R-95 (candidate).** The global tier binds one credential per cluster to
  that cluster's prefix and streams at the hub, and the local broker binds
  the global prefix to the supervisor role alone; a session never holds the
  cluster credential.

## 11. Build sequence and tickets

Filed at close; ids in the closing note of the probe brief.

1. Shim global mode (director-mcp): domain-qualified JetStream context,
   global inbox durable, presence into `GLOBAL_PRESENCE`, `global://`
   addresses with liveness refusal, preflight extension. Builder:
   marvel-builder, after director#27 merges.
2. Phase-0 broker relaunch as the kinu leaf (coordinated: config plus
   restart, shims reconnect; add `domain: kinu` and the `leaf-kinu` remote),
   with the director#4 authorization activated in the same window or the next.
3. Cross-host stage: mokuzai joins per the recipe; the director reaches
   skippy's supervisor; the checks in section 8 rerun with real sessions.
4. Hub as a service: launchd unit, log rotation, a provisioning step in the
   runbook (finding-166 shape 3).
5. marvel items (section 7), five tickets in marvel's court.
6. Cloud phase: TLS, CA, DNS, placement move; SLIM reassessed at that point.
