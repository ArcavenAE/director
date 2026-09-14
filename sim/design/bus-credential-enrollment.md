# Design brief 9: marvel as the enrollment and distribution plane for the global bus credential

Status: shape for the operator's review, 2026-09-14. Not a build; tickets
follow approval. Second half of one credential story with brief 8 section
4.1 (R-95, the grant table): that half says which subjects a cluster's
credential may use; this half says how the credential reaches the cluster
without a hand-carried seed. The interim hand-carry in
`probe/nats-global-tier/recipe-mokuzai.md` stays until this ships.

## 1. The shape in one paragraph

The operator and the cluster owner exchange marvel client keys by hand once
(public keys, not secrets); that is the trust bootstrap. From then on the
operator's marvel CLI reaches the cluster's daemon over `mrvl://`, which is
SSH with key authentication and wire encryption that marvel already runs,
and pushes the hub-minted NKey seed into that daemon's in-memory Store as a
`Credential` resource that is never written to disk. The cluster's local
broker gets the seed from its own daemon at start, through the environment
of the broker process, and dials the hub with it. Revocation is one command
at the hub and one at the daemon; re-mint is the same push again. Marvel
brokers a bus credential it can revoke; it never holds authority at a third
party.

## 2. Primitives it builds on (present today, verified in the marvel tree)

- `mrvl://` remote administration: embedded SSH on :6785, kubeconfig-style
  clusters in `~/.marvel/config.yaml` (`--cluster <name>`), remote verbs
  already run over it (`marvel --cluster <name> daemon logs`). The tunnel
  terminates on the daemon's unix socket (`dialSSHTunnel` in
  `internal/daemon/daemon.go`).
- `marvel keys`: `generate`, `show` (client side); `authorize <file>`,
  `authorized`, `revoke <fp>`, `host-fingerprint` (daemon side). Keys are
  all-or-nothing daemon admin today; there is no per-key scope.
- The daemon Store (`internal/api/store.go`): in-memory by default, optional
  bolt L2 with `persistPut` and `persistDelete` per bucket; resource types
  Workspace, Team, Session, Endpoint, Policy, Budget. Policy is the nearest
  sibling: an operator-supplied document marvel holds and projects.
- The heartbeat token: minted per session, stamped into the pane
  environment, validated on the heartbeat RPC (`internal/api/heartbeat.go`,
  `internal/session/manager.go`). Distribution of a bus credential is that
  issuance pattern with a different audience, not a new custody model.

## 3. The flow

**E0. Mint (hub operator, kinu).** `nats auth nkey gen user` for
`leaf-<cluster>`; the public key goes into the hub config and the hub
reloads. Unchanged from brief 8; the hub is not marvel-managed by decision.

**E1. Enroll (both parties, by hand, once).** The cluster owner runs
`marvel keys authorize <operator-pubkey>` on their daemon and sends back
`marvel keys host-fingerprint`; the operator adds a cluster entry
`{name: <cluster>, server: mrvl://<host>:6785, identity: <client key>}` and
accepts the host fingerprint on first connect (TOFU, existing behavior).
Symmetric consent: the owner can `marvel keys revoke <fp>` at any time.
Nothing secret moves in this step.

**E2. Push (operator, kinu).**
`marvel --cluster <cluster> credential put bus/leaf --kind nats-nkey-seed --stdin < leaf-<cluster>.nk`.
The seed travels inside the SSH session, lands in the remote daemon's Store
as a `Credential` with `Persist: false`, and is acknowledged with its
metadata only. The daemon emits `credential.put` on the events ring with the
caller's key fingerprint and the credential name, never the value.

**E3. Consume (cluster daemon, at broker start).** The daemon supervises the
local broker (aae-orc-xy1dh) and renders its conf (aae-orc-e9g8i) with the
leaf remote as `nkey: $DIRECTOR_LEAF_NKEY`; when it starts the broker
process it sets that variable from the Store value. The seed exists in the
daemon's memory and the broker's process environment, nowhere on disk.
Interim before broker supervision exists: the owner starts the broker with
`DIRECTOR_LEAF_NKEY="$(marvel credential get bus/leaf --reveal)" nats-server -c ...`,
where `--reveal` is honored only on the daemon's local unix socket, never
through the `mrvl://` tunnel (seam S3).

**E4. Revoke and re-mint.** Hub side: remove the public key, reload; the leaf
link fails authentication on its next reconnect (one second) and the cluster
drops off the global roster within one presence TTL. Daemon side:
`marvel --cluster <cluster> credential delete bus/leaf` clears memory; the
broker keeps its copy in its environment until restarted, which is why the
hub-side revoke is the authoritative one. Re-mint is E0 then E2 then a
broker reload. Rotation without a gap: the hub carries both public keys
during the overlap, the new seed is pushed, the broker restarts, the old key
is removed.

**E5. Restart semantics.** A daemon restart empties the Store's transient
resources by design; the operator pushes again (one command) or, when the
operator later ratifies disk persistence, the resource gains an encrypted
bolt bucket keyed by the daemon's host key. Runtime-only is the ruling for
now and the brief does not pre-decide the later step.

## 4. The `Credential` resource

```
Credential {
  Name       "bus/leaf"                 # one per purpose; the key in the Store
  Kind       "nats-nkey-seed"           # closed set; others arrive with their audience
  Audience   "nats://global-hub"        # who the value means something to
  Binding    { cluster: "<cluster>", subjects: "global.director.inbox, global.<cluster>.>" }  # display of the hub grant, not enforced here
  IssuedAt   ts;  IssuedBy  "<operator key fingerprint>"
  Persist    false                      # the bolt layer skips it
  Value      []byte, memory only, zeroed on delete; never in list or get output
}
```

`marvel get credentials` prints metadata. `describe credential` prints
metadata. Only `credential get --reveal` on the local socket prints the
value. The Store's bolt layer treats `Persist: false` as "never write";
`marvel daemon reexec` (same process image, state re-read from bolt) keeps
the in-memory map alive only if the reexec handoff carries it, which is a
seam (S4), and otherwise the operator pushes again.

## 5. Custody argument (ADR-009)

The test is audience, not format. The seed means nothing to anyone but the
hub's nats-server, which the operator runs and can reconfigure; the hub
operator mints it, can revoke it in one reload, and can mint another. That
is issuance inside the operator's trust domain, the same class as the
heartbeat token, and marvel brokering it is the permitted shape. It is not
bearer authority at a third party: no vendor, no OAuth grant, no token that
outlives the operator's ability to revoke it. The most durable artifact in
the chain is the operator's own marvel client key, which is the operator's
identity toward daemons they administer, not a credential marvel holds.

## 6. The multi-user boundary (SOUL section 3)

Skippy's harness credentials never move. What moves is a bus credential over
marvel's own authenticated transport, and the only enrollment secret is a
key pair each party generates and keeps. The boundary holds on three counts:
his OAuth stays on his host and in his harness; the bus credential grants
only the R-95 leaf row (write to the director and its own subtree); and his
consent is revocable (`marvel keys revoke` on his daemon ends the operator's
reach). One honest cost: marvel keys are all-or-nothing today, so the
operator's enrolled key holds full daemon admin on his machine (logs,
inject, delete). He accepts that or seam S5 lands first.

## 7. Seams for marvel-builder (build items, after approval)

- S1. `Credential` resource type with `Persist: false` and a bolt layer that
  skips transient resources; Store CRUD; `get credentials` and `describe`
  print metadata only.
- S2. Daemon methods `credential.put`, `credential.get`, `credential.delete`,
  `credential.list`; CLI verbs under `marvel credential`; events
  `credential.put` and `credential.delete` with caller fingerprint.
- S3. Local-versus-remote caller distinction for `--reveal`: the tunnel
  terminates on the same unix socket today, so the daemon cannot tell them
  apart; simplest shape is a second socket path for tunnelled connections on
  which reveal is refused, or a connection tag set by the SSH server.
- S4. Reexec handoff of transient resources, or the documented "push again"
  rule.
- S5 (optional, before a second-party cluster enrolls): per-key scope on
  marvel keys (`admin` versus `credential-push`), so an operator's key on
  another person's daemon holds only what enrollment needs.
- S6. Broker supervision consumes the credential at start (rides
  aae-orc-xy1dh and aae-orc-e9g8i); until then the interim `--reveal` start
  line.

## 8. What this does not change

The hub's public-key registration stays a config edit plus reload on kinu.
The R-95 grant table is unchanged; this brief moves the seed, not the
grant. The phase-0 envelope, the two-tier topology, and the interim LAN
posture (brief 8 section 9) are unchanged. Sessions never hold the cluster
credential; the daemon and the broker process do.

## 9. Decisions for the operator

1. Approve the shape (sections 3 to 7) so tickets can be filed.
2. Runtime-only confirmed: a daemon restart means one push command again.
3. Whether S5 (key scopes) must land before skippy enrolls, or whether
   full-admin trust on his daemon is acceptable for the interim.
4. Whether the interim `--reveal` start line is acceptable until broker
   supervision exists, or whether E3 waits for aae-orc-xy1dh.
