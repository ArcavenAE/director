# Design brief 11: one fleet address space over the leaf fabric

Status: amendments ruled 2026-09-24 (R-50, R-94, R-95, R-109 accepted on
director#77); subject root ruled the same day: `agent.<cluster>.`, with a
coordinated flag-day cutover from today's `AGENT_INBOX` (section 5). Probe P0
to P7 run on scratch brokers on that root, results in
`_kos/findings/finding-008-leaf-fabric-probe.md` and section 10. Commissioned by the
operator through the director seat. Nothing here is built. The live brokers
were read, not changed (section 1). Where a mechanism rests on a NATS fact this
sitting did not execute, it is marked UNVERIFIED and the probe plan (section 9)
names the step that settles it.

Built on: brief 8 (`global-bus-tier.md`), the leaf-attach direction
(`global-nats-leaf-attach.md`), brief 9 (enrollment), brief 10 (local broker
supervision), `probe/nats-global-tier/nats-mechanics.md` (researched
2026-09-14, URL per fact), finding-004 (JetStream across the leaf partition),
and requirements R-08, R-09, R-10, R-50, R-78, R-86, R-89, R-92, R-94, R-95,
R-96, R-97, R-104 to R-112.

## 0. Why

Cross-host messaging is special-cased. There are two address spaces: a local
`agent://{team}/{id}` that means nothing off its own broker, and a global tier
with exactly two role words (`global://director`, `global://{cluster}/supervisor`)
and no per-seat address. A worker cannot reach another cluster at all. The
hierarchy is enforced by missing addresses, so "not permitted" and "broken" look
the same, and the send tool returns "accepted" in both cases.

On 2026-09-24 that shape produced a run of silent losses:

- Six reports from a remote architect seat went to `agent://ops/director`, a
  mailbox no one reads. Every send returned "accepted".
- A FAILURE went to `role://<team>/director`, which does not exist.
- The old shim's consumer queued messages and never handed them out.
- `AGENT_INBOX` ages mail out at 24h, so unread mail to a live seat is deleted
  and the seat reports "silence, not failure" (bd `aae-orc-t77rr`).
- A restarted shim leaves a phantom presence row, so a singleton supervisor
  sees a rival that is itself (bd `aae-orc-4unyk`).
- Durable consumers accumulate, one per shim start, and nothing reaps them
  (bd `aae-orc-iejcx`).
- `wait_for_message` is pull-only, so an idle seat never reads its mail.

Each of those is a place where the fabric is quiet about something it knows.
This brief replaces the two-tier address split with one address per seat, stores
mail where the recipient lives, carries cross-cluster mail through a store that
survives a link outage, and moves the hierarchy from missing addresses to
credentials that refuse loudly.

## 1. Ground truth (read-only, 2026-09-24, from kinu)

Read through the monitoring ports (`/varz`, `/leafz`, `/jsz`, `/accountz`) and
the config files on disk. No config change, restart, stream edit, or consumer
deletion was made.

| broker | version | JetStream domain | auth | leaf |
|---|---|---|---|---|
| kinu local (`director-phase0`, 4222) | 2.14.6 | **none set** | **none** (`$G` only, `auth_required` null) | one remote, to the hub over loopback |
| hub (`global-hub`, 4242 / 7442) | 2.14.6 | `global` | account `FLEET`, NKey users per brief 8 | two leaves in `FLEET`: `director-phase0` and one unnamed server |
| mokuzai local | **UNVERIFIED from kinu** | UNVERIFIED | marvel-managed; team-scoped users per R-109's evidence | linked to the hub, rtt about 3 ms |

The mokuzai leaf appears at the hub under its server id, not a server name,
because its `server_name` is unset; `/leafz` does not report a peer's version.
Its version must be read on mokuzai before anything in section 3 that depends
on 2.14 behavior is relied on (probe P0).

Two facts differ from what brief 8 section 11 recorded as the plan. The kinu
local broker never took `domain: kinu` (build step 2), and it runs with no
authorization block, so the director#4 grants are not in force on kinu.

Streams and consumers as they stand:

| stream | where | retention | max age | messages | consumers |
|---|---|---|---|---|---|
| `AGENT_INBOX` (`agent.*.*.*.inbox`, `agent.*.*.role.*.inbox`) | kinu | limits, discard old | 24h | 53 | **185**, none with an inactive threshold, 89 holding pending mail (720 pending in total), none with a waiting pull |
| `AGENT_AUDIT` | kinu | limits | 30d | 473 | 0 |
| `KV_AGENT_STATE` | kinu | limits | 90s | 20 | 0 |
| `GLOBAL_TO_DIRECTOR` | hub | limits, discard old | 24h | 14 | 2 director durables, 6 and 14 pending (one belongs to an earlier director instance) |
| `GLOBAL_TO_mokuzai` | hub | limits, discard old | 24h | 6 | 8; every one holds the same 6 pending (five supervisor durables plus three hand-made ones with no inactive threshold) |
| `GLOBAL_TO_kinu` | hub | limits | 24h | 0 | 0 |
| `KV_GLOBAL_PRESENCE` | hub | limits | 90s | 5 | 0 |

Three readings. The 185 consumers are the `iejcx` leak, measured again, and the
cause is visible in config: the local durable is created with no
`InactiveThreshold` (`bus.go` `connect`), while the global one carries 25h.
Every `GLOBAL_TO_mokuzai` durable holding the same six messages is the role
fan-out R-97 and R-104 describe: a role address is a copy per holder, not a
queue. And every inbox stream in the fleet is `limits` retention with a 24h
age, so every inbox has the `t77rr` failure, not only the local one.

## 2. Target design

### 2.1 One address space, cluster-qualified

Every seat has exactly one address, and it works from any cluster:

```
agent://<cluster>/<workspace>/<team>/<id>     subject  agent.<cluster>.<workspace>.<team>.<id>.inbox
role://<cluster>/<workspace>/<team>/<role>    subject  agent.<cluster>.<workspace>.<team>.role.<role>.inbox
director                                      subject  director.inbox
```

`<cluster>` keeps R-94's token class and its reject-not-rewrite rule. The two
role words stop being special: a supervisor is a seat with an ordinary address
and the `supervisor` role, the director is the one fleet seat with the reserved
`director.inbox`. The short forms (`agent://<team>/<id>`) remain as aliases
during migration and resolve through the roster (R-92) to the full form; an
ambiguous or unresolvable alias refuses before publish (R-78).

A role address is a queue, not a fan-out (section 2.5): one holder takes each
message. Broadcast keeps its current fan-out semantics and is not an inbox.

**Subject root: `agent.<cluster>.`, ruled 2026-09-24.** The operator declined a
separate `mail.` root and accepted a coordinated flag-day cutover instead
(section 5). The reason a flag day is needed is measured, not assumed (P7): the
server refuses the new inbox stream while the legacy `AGENT_INBOX` holds its
subjects, because `agent.<cluster>.*.*.*.inbox` (six tokens) overlaps the
legacy role pattern `agent.*.*.role.*.inbox` (six tokens), and
`agent.<cluster>.>` overlaps both legacy patterns. The new role pattern
(seven tokens) does not overlap anything.

Two rules follow from the shared root:

- A cluster's `INBOX` lists the two inbox forms explicitly,
  `agent.<cluster>.*.*.*.inbox` and `agent.<cluster>.*.*.role.*.inbox`, never
  `agent.<cluster>.>`, so broadcast (`agent.<cluster>.<ws>[.<team>].broadcast`)
  and `agent.audit` are never captured by an inbox. Measured in P1: a
  broadcast publish stores nothing.
- `role` is reserved and cannot be a seat id, because
  `agent.<c>.<ws>.<team>.role.<r>.inbox` is the role form. `audit` and
  `broadcast` are reserved as cluster and workspace tokens for the same reason.

`out.<dest>.` is the internal outbox transport subject (section 2.3). It is
never an address and never appears in the `agent://` grammar.

### 2.2 Mail is stored where the recipient lives

Each cluster runs its own JetStream domain (its cluster token) and one inbox
stream for its own seats:

```
INBOX       subjects agent.<self>.*.*.*.inbox, agent.<self>.*.*.role.*.inbox    domain <self>
OUTBOX      subjects out.>                                                   domain <self>
```

A seat's durable consumer reads `INBOX` on its own broker. It never crosses a
link to read its mail, so a seat can drain its inbox while the hub is down.

The director's inbox lives in the hub domain, `DIRECTOR_INBOX` on
`director.inbox`, because the director seat is one fleet seat that may move
hosts and the hub is the always-on member of the fabric (the leaf-attach
direction moves it onto shared infrastructure).

### 2.3 Cross-cluster mail rides an outbox, and nothing is lost to an outage

A publish to a recipient on the sender's own cluster goes straight to
`agent.<self>.<workspace>.<team>.<id>.inbox` and lands in the local `INBOX`. A publish to a recipient on
another cluster goes to the sender's local `OUTBOX` as
`out.<dest>.<workspace>.<team>.<id>.inbox` (or `out.director.inbox`). The shim
chooses, because it already derives the subject from the recipient's resolved
identity (R-92) and knows its own cluster; the sender still sees one address.

Every cluster's `INBOX` sources from every other cluster's `OUTBOX`, filtered to
its own token and transformed back into its own subject space:

```
INBOX (domain kinu) sources:
  - name: OUTBOX, external api $JS.mokuzai.API,
    subject_transforms: [ { src: "out.kinu.>", dest: "agent.kinu.>" } ]
DIRECTOR_INBOX (domain global) sources, one per cluster:
  - name: OUTBOX, external api $JS.<cluster>.API,
    subject_transforms: [ { src: "out.director.inbox", dest: "director.inbox" } ]
```

`OUTBOX` is work-queue retention: the sourcing consumer's ack removes the
message, so the outbox holds exactly the mail not yet taken by its destination.
During a link outage the sender's publish still succeeds against its own
broker, the outbox grows, and the destination's source resumes when the link
returns. An outage delays mail and loses none, and the outbox depth per
destination is the queue state R-10 asks the sender to be able to see.

Raw `agent.>` and `out.>` subjects never cross the link. The leaf remote carries
`deny_exports` and `deny_imports` on both, so the only traffic crossing is the
sourcing consumer's API, delivery, and flow-control subjects, presence, and the
director inbox. This is also what keeps one message from being stored twice:
the mechanics note records that one subject captured by streams in two domains
is stored by both.

NATS basis, with status:

- Leaf interest propagation, domains per leaf, the `$JS.<domain>.API` mapping,
  and the remote deny lists: cited in `nats-mechanics.md` Mechanism 1; the
  domain mechanics are proven on 2.14.6 in brief 8 section 8.
- A source "can set `filter_subject` or `subject_transforms`, but not both" on
  one entry (https://docs.nats.io/nats-concepts/jetstream/source_and_mirror,
  read 2026-09-24). The design uses a transform only.
- Cross-domain sourcing through the `external` block: documented
  (mechanics note; https://docs.nats.io/learn/jetstream/mirrors-and-sources).
  **Leaf to hub to leaf** (kinu sourcing from mokuzai, with the hub in the
  middle) is UNVERIFIED; brief 8 proved leaf to hub only. Probe P1.
- Sourcing from a work-queue stream with a durable consumer and
  `AckFlowControl` arrived in 2.14; earlier servers use a less reliable
  ephemeral path (https://docs.nats.io/release-notes/upgrade-to-2.14, via the
  mechanics note). The kinu brokers are 2.14.6; mokuzai is UNVERIFIED. Probe P0.
- Resume after an outage: finding-004 proved a leaf-side **mirror** resumes by
  stored sequence with no gaps or duplicates on 2.14.6. A **source** resuming
  the same way is UNVERIFIED. Probe P2.
- Duplicate suppression rides `Nats-Msg-Id` = envelope `message_id`, as today
  (R-13). Whether the dedupe window holds across the source hop is UNVERIFIED.
  Probe P2.

### 2.4 Hierarchy by permission, not by missing address

Every seat holds its own credential, and the credential, not the address book,
decides where it may publish. The shape the operator named: a worker may send
to its own team and to its supervisor, on its own cluster or another; a
supervisor may send within its cluster, to other supervisors, and to the
director; the director may send anywhere and reads only its own inbox. A
refused publish returns a named permissions refusal at the shim, never a
timeout (R-109's defect) and never "accepted" (R-09). P3 measured that the
server delivers the refusal asynchronously and the JetStream publish call times
out, so the shim must catch the async permissions error and fail the send on
it (finding-008).

Mechanism, per layer:

1. **Per-seat credentials.** Operator/JWT mode with scoped signing keys, so a
   user JWT carries the seat's cluster, workspace, team, id, and supervisor as
   tags, and one permission template expands per seat, for example publish
   allow `agent.{{tag(cluster)}}.{{tag(ws)}}.{{tag(team)}}.>` and
   `out.*.{{tag(ws)}}.{{tag(team)}}.>`. Templates exist only for scoped signing
   keys in JWT mode (nats-server v2.9.0; mechanics note, "Credentials bound to a
   subject prefix"). Static per-user NKeys, brief 8's model, would need a hub
   and broker config edit per seat; that is the scaling line the mechanics note
   already drew. Whether `tag()` expansion accepts a tag in the middle of a
   subject with the values above is UNVERIFIED. Probe P3.
2. **Leaf allow and deny lists** bound what crosses the link at all: the
   sourcing API for the other clusters' `OUTBOX`, its delivery and flow-control
   subjects, presence, and `director.inbox`. Nothing else (brief 8 section 4,
   narrowed to the new subjects).
3. **Minting stays marvel's** (brief 9; R-85): marvel mints the seat's user JWT
   at spawn from the cluster's scoped signing key and injects it the way it
   injects the heartbeat token. The custody test in SOUL section 3 holds: the
   operator's trust domain issues and revokes every credential.

What this supersedes: R-95's rule that the hierarchy is "by topology" and R-109's
clause that relay through the director is "by topology". The asymmetry stays a
policy the operator sets; it stops being an accident of which addresses exist.

### 2.5 Retention that never deletes unread mail

- `INBOX` and `DIRECTOR_INBOX` use work-queue retention: a message is removed
  when its consumer acks it. The server enforces one consumer per filter on a
  work-queue stream and "rejects overlapping consumers outright"
  (https://docs.nats.io/learn/jetstream/retention-policies, read 2026-09-24).
- Consequence, and a deliberate one: **one durable per address, not per shim
  instance.** A shim restart rebinds the same durable instead of minting a new
  one, so nothing accumulates (`iejcx`). A second live session claiming the same
  address gets a refusal from the server, which is R-78's loud failure for the
  collision R-50 turned into silent duplication.
- `max_age` is a backstop measured in weeks (28 days proposed), with
  `discard: new` and a byte cap, so a full inbox refuses the next publish
  loudly instead of evicting the oldest mail. Limits still apply under
  work-queue retention (same page).
- No expiry is silent. A per-cluster sweeper (section 2.7 hosts it) reads each
  durable's pending count and oldest pending timestamp, and at 7 days sends the
  **sender** an INFORM naming the message id and "undelivered 7 days", copies
  the director, and repeats at 21 days. A message that reaches the backstop has
  therefore produced two notices first. NATS does not publish a per-message
  advisory when `max_age` removes a message, as far as the advisory list I have
  read shows; that absence is UNVERIFIED, and the design does not depend on
  it either way. The 2.11 subject delete marker (`SubjectDeleteMarkerTTL`)
  would leave a marker when age removes a subject's last message, which gives
  the receiver evidence that mail expired (`t77rr` ask 1); optional, and
  UNVERIFIED on a sourced stream.
- The poll result carries consumer state: pending, oldest pending, and whether
  the durable exists, so "no mail", "mail waiting", and "no consumer" are three
  answers (R-107).

### 2.6 Presence aggregated across clusters

Each cluster keeps a presence bucket keyed on the **seat**, not the shim:
`<cluster>.<workspace>.<team>.<id>`, with the instance, pid, harness session id,
and timestamp in the value. A restarted shim overwrites its own row, so a
phantom row cannot exist (`4unyk`). A second live instance is detected by
compare-and-set: the writer updates only against the revision it last wrote,
and a revision it did not write means another live writer, which is reported as
a collision rather than rendered as two rows.

The hub keeps `FLEET_PRESENCE`, sourced from every cluster's bucket, with a
short TTL of its own so a cluster whose link is down ages out of the fleet view
instead of freezing there. KV buckets are streams and accept sources; the exact
server version for sourced KV and how delete markers propagate through a source
are UNVERIFIED. Probe P4.

### 2.7 Receipt that does not depend on polling

The durable inbox is the delivery path; the notifier only wakes. Each cluster
runs one notifier (the marvel doorbell, or a shim sidecar where no marvel runs)
that watches its own `INBOX` for new mail and knows each seat's last poll time.
It cannot be a consumer on `INBOX`: a work-queue stream refuses any
consumer whose filter overlaps a seat's (P6), so it reads stream state and
messages by sequence instead. When mail lands for a seat that has not polled
within a bound, it rings the
seat's doorbell with a short pointer whose load-bearing token is last (R-111),
through a channel independent of the composer where one exists (R-112). The
ring is itself loud (R-89): the notifier records each attempt, and if the mail
is still pending after a second bound it escalates to the director. The same
process hosts the aging sweeper in section 2.5.

## 3. What changes versus today

| today | target |
|---|---|
| two address spaces; workers have no cross-cluster address | one address per seat, any cluster; short forms are aliases |
| role address fans out to every holder | role address is a queue; one holder takes each message |
| hierarchy by missing address; refusal looks like a timeout | hierarchy by per-seat credential; refusal is named |
| cross-cluster traffic is core NATS to the hub, fails when the link is down | outbox plus sourcing; an outage delays and loses nothing |
| inbox retention `limits`, 24h, discard old | work-queue, weeks, discard new, notices at 7 and 21 days |
| one durable per shim instance, no inactive threshold locally | one durable per address; restart rebinds |
| presence keyed on shim instance | presence keyed on seat; compare-and-set |
| receipt depends on the seat choosing to poll | notifier rings idle seats; unanswered rings escalate |
| kinu broker: no domain, no auth | every cluster: its own domain, JWT-scoped users |

## 4. Failure modes, mapped

| loss or risk | element that addresses it |
|---|---|
| `t77rr`: mail aged out unread at 24h | 2.5 work-queue retention, weeks backstop, sender notices at 7 and 21 days; 2.7 notifier wakes the seat long before |
| `4unyk`: phantom presence row after a shim restart | 2.6 presence keyed on seat, overwrite on restart, compare-and-set for real rivals |
| `iejcx`: 185 orphaned durables | 2.5 one durable per address; the server refuses a second; migration step 6 sweeps the existing set |
| reports to an unread mailbox (`agent://ops/director`) | 2.1 the director has one address, `director`; alias resolution refuses an address no seat holds |
| FAILURE to `role://<team>/director` | 2.1 role words are validated against the roster; a role no seat holds refuses before publish |
| consumer queued mail and never handed it out | 2.5 poll result carries consumer state (R-107); 2.7 notifier sees pending mail with no poll |
| idle seat never reads | 2.7 notifier |
| link outage between clusters | 2.3 outbox and sourcing |
| a worker's cross-cluster send refused as a timeout | 2.4 named permission refusal |
| two sessions under one address both act on one REQUEST (R-78) | 2.5 work-queue refuses the second consumer |
| outbox grows without bound during a long outage | byte cap with `discard: new` on `OUTBOX`: the sender's publish refuses loudly when full, and the outbox depth is on the roster (R-10) |
| sourcing silently stalls (a wrong export type "never catches up") | P1 proves the wiring; the sweeper reports outbox age per destination, so a stalled source shows as aging mail |

## 5. Cutover from the two-tier design (flag day, ruled 2026-09-24)

The operator ruled for the `agent.<cluster>.` root and a coordinated cutover.
The server will not let the new inbox and the legacy `AGENT_INBOX` capture
overlapping subjects on one broker (P7 measured the refusal), so on each
cluster there is one window in which publishers stop, the legacy stream stops
capturing, the new one starts, and unread mail is carried across. Every step
that touches a live broker is operator-gated. The whole procedure, including
the rollback, ran end to end on a scratch server (P7).

### 5.1 Before any window

1. **Probe passes** on scratch brokers (sections 9 and 10), and P0 is read on
   mokuzai.
2. **Builds ready and pinned.** The new shim (cluster-qualified subjects, one
   durable per address, the async permission error turned into a named
   failure) and the migration tool (`fabtool unread`, `migrate`, `rollback`,
   promoted out of the probe). The current shim binary is pinned as the
   rollback build.
3. **Optional, independent, and worth doing now:** raise `AGENT_INBOX` and the
   `GLOBAL_TO_*` streams from 24h to 14 days and give the local durable an
   `InactiveThreshold`. This stops `t77rr` and the growth of `iejcx` until the
   window, and it does not change the cutover.
4. **Hub first.** Create `DIRECTOR_INBOX` on `director.inbox` in the hub
   domain. It overlaps nothing (`global.director.>` is a different root), so
   it can exist ahead of every cluster; its sources are added as each cluster
   gains an `OUTBOX`. The director reads the old global inbox and the new one
   until the last cluster is cut.

### 5.2 Order

kinu first (the director's host, where the operator watches), then mokuzai, in
the same session if possible. Between the two windows a cut cluster and an
uncut one have no fabric path between their seats; the old global tier still
carries supervisor and director traffic, so keep the gap short. The
`GLOBAL_TO_*` streams retire after the last cluster is cut and their pending
counts read zero.

### 5.3 The window on one cluster

1. **Stop publishers.** Stop every director shim on the cluster. Confirm
   `AGENT_INBOX`'s last sequence is unchanged for 60 seconds.
2. **Back up.** `nats stream backup AGENT_INBOX`.
3. **Count what must move.** `unread`: per address, every message above the
   highest ack floor of any durable filtering that address. The highest floor
   is the right rule because the legacy shim gave every instance its own
   durable under `DeliverAll`: a message any instance acked was read, and dead
   instances' low floors must not resurrect it. An address with no durable
   moves everything it holds. Messages delivered but not yet acked move too;
   none were ack-pending in the section 1 survey.
4. **Domain.** If the broker still has no JetStream domain, add
   `domain: <cluster>` and restart it here (adding a domain keeps streams and
   clients, brief 8 section 1).
5. **Park the legacy stream.** Edit `AGENT_INBOX`'s subjects to
   `legacy.parked.agent_inbox`. From this instant it captures nothing and
   keeps every message and durable for rollback. **This is what prevents
   double capture:** the new inbox does not exist until the legacy stream has
   released the subjects, and the server refuses the reverse order anyway.
6. **Create the fabric streams.** `INBOX` with the two explicit patterns
   (work-queue, 28-day backstop, `discard: new`, byte cap, 20-minute dedupe
   window), `OUTBOX` on `out.>`, this cluster's sources from the other
   clusters' outboxes, and this cluster's source on the hub's
   `DIRECTOR_INBOX`.
7. **Drain by migration.** `migrate` republishes each unread message onto
   `agent.<cluster>.<ws>.<team>.<id>.inbox` (or the role form) with its
   original `Nats-Msg-Id` and a header naming its legacy sequence, and writes
   the id-to-sequence map. The count must equal step 3. A rerun inside the
   dedupe window stores nothing new (P7). On a mismatch, abort: delete
   `INBOX`, restore the legacy subjects, restart the old shims; nothing new has
   happened yet.
8. **Start the new shims.** Each binds one durable per address. Check one
   round trip per seat and a director roll call.
9. **Confirm the legacy stream is quiet.** Its last sequence has not moved
   since step 1.
10. **Hold.** The parked legacy stream stays for 14 days as the rollback
    source. Deleting it after sign-off also deletes its 185 orphaned durables,
    which closes the `iejcx` sweep with no separate step.

### 5.4 Rollback (P7, measured)

Available while the legacy stream is parked:

1. Stop the new shims.
2. Park `INBOX` (edit its subjects to `legacy.parked.inbox`).
3. Restore `AGENT_INBOX`'s two original subject patterns.
4. `rollback` reconciles. It deletes the legacy original of every migrated
   message that was read after cutover, so it is not delivered twice. It also
   republishes every message that arrived after cutover and is still unread
   onto its legacy subject. Migrated messages still unread need nothing,
   because their legacy originals are still unread above the old durables'
   ack floors.
5. Start the pinned old shim build. The old durables resume from their ack
   floors.

P7 result: after a cutover, one migrated message read, three new messages, and
a rollback, the legacy durables' pending counts were exactly the expected
unread sets (4, 5, 3, 0). Two limits are stated rather than hidden:

- **Mail waiting in `OUTBOX` for another cluster** at rollback has not left
  the cluster. Roll back both clusters together, and treat the outbox
  contents as undelivered mail to report to the senders.
- **Messages read after cutover** leave no trace in the legacy stream beyond
  the deletion. A seat that acted on one before rollback keeps its own record
  of having done so. The legacy stream does not.

## 6. What this supersedes and what it keeps

Supersedes, on approval:

- Brief 8 section 2 (two address spaces, two role words, the global subject
  grammar) and its per-direction `GLOBAL_TO_<cluster>` hub streams, replaced by
  per-cluster `INBOX` and `OUTBOX`.
- Brief 8 section 4.1's "by topology" asymmetry and R-109's topology clause:
  the asymmetry becomes credential policy.
- R-94's "a worker never holds a global address". Every seat holds one fleet
  address; what a worker may send is decided by R-95 as amended here. **This is
  an amendment to a ratified requirement and is the operator's call.**
- R-50's per-instance durable, replaced by one durable per address with the
  server refusing a second (which answers R-78).
- The 24h hub retention and the `globalConsumerInactive` rationale in
  `global.go`, which exists only because messages expire at 24h.

Keeps:

- Leaf nodes as the edge link, and gateways as the future tier-to-tier layer
  (brief 8 section 1; leaf-attach).
- One JetStream domain per cluster plus the hub domain; R-94's token class for
  the cluster name.
- Enrollment and seed custody (brief 9); marvel supervising the local broker
  (brief 10); the leaf-attach move of the hub to shared infrastructure.
- The envelope, `Nats-Msg-Id` dedupe, the audit mirror, and the A2A direction
  (brief 8 section 6).
- R-92: the subject is derived from the recipient's resolved identity.

## 7. Candidate requirements (provisional tags)

- **FAB-A.** Every seat has one fleet address that resolves from any cluster.
- **FAB-B.** Mail is stored on the recipient's cluster; mail to another cluster
  is held on the sender's cluster until the recipient's cluster takes it.
- **FAB-C.** An inbox never deletes unread mail without first notifying the
  sender, and a full inbox refuses rather than evicts.
- **FAB-D.** One consumer per address; a second claimant is refused.
- **FAB-E.** Presence is keyed on the seat and written by compare-and-set.
- **FAB-F.** A permission refusal is named at the sender.
- **FAB-G.** Pending mail for a seat that is not polling produces a wake, and an
  unanswered wake escalates.

## 8. Open questions

- Role inbox as a queue changes what "send to the supervisor role" means on a
  cluster with five supervisors. Is one-taker the wanted semantics, or should a
  role address refuse when more than one seat holds it?
- Backstop length and the notice ages (28, 7, 21 days) are proposals.
- Where the director's inbox lives once the hub moves to shared infrastructure
  and there is one tier per person (leaf-attach, Mode D).
- Whether the notifier is marvel's from the start or a shim sidecar first.

## 9. Probe plan: two clusters, a link cut, no loss

Scratch brokers only, on one host, in the style of `verify-global.sh`: a hub
(domain `ghub`) and two leaves (domains `pa`, `pb`), throwaway store dirs,
random ports, cleanup trap. The live brokers are not touched.

- **P0 versions.** Record `nats-server --version` on every live broker,
  including mokuzai (read there). Pass: every broker that will source a
  work-queue stream is 2.14 or later.
- **P1 wiring.** `pb` `INBOX` sources `pa` `OUTBOX` through the hub with the
  transform in 2.3. Publish 10 on `out.pb.w.t.x.inbox` at `pa`. Pass: 10 in
  `pb` `INBOX` on `agent.pb.w.t.x.inbox`, 0 left in `pa` `OUTBOX`.
- **P2 link cut.** Kill the hub. Publish 500 at `pa` with unique
  `Nats-Msg-Id`, then 50 repeats of earlier ids. Wait 10 minutes. Restart
  the hub. Pass: exactly 500 distinct messages in `pb` `INBOX`, in order, none
  duplicated, `pa` `OUTBOX` empty, and every publish at `pa` acked by `pa`
  during the outage.
- **P3 permissions.** JWT mode with a scoped signing key and the template in
  2.4. Pass: a worker publishing to its own team, to its supervisor, and to
  `director.inbox` succeeds; to another team or another cluster's team it is
  refused with a permissions error, not a timeout; nothing is stored.
- **P4 presence.** Restart a shim five times. Pass: one row for the seat in
  both the cluster bucket and the hub's sourced view; a second concurrent
  writer is reported as a collision; a cluster whose leaf is cut ages out of
  the hub view within its TTL.
- **P5 no silent expiry.** Scratch ages scaled to minutes. Pass: the sender
  receives both notices before the backstop removes the message; a full inbox
  refuses the next publish.
- **P6 one consumer per address.** Pass: a second durable on the same address
  is refused by the server; a shim restart rebinds the existing durable and
  the stream's consumer count does not grow.

- **P7 cutover and rollback.** On a standalone scratch server holding a
  legacy `AGENT_INBOX` configured as the live one is, with several durables per
  address at different ack floors, a cold address, and a role address. Pass:
  the new `INBOX` is refused while the legacy subjects are held; after
  parking, the migration moves exactly the unread set and a rerun moves
  nothing; after new-era reads and writes, rollback leaves the legacy
  durables' pending counts equal to the expected unread sets.

Each step either passes or turns its UNVERIFIED mark into a finding before the
cutover in section 5 begins. All steps run on the `agent.<cluster>.` root.

## 10. Probe results (2026-09-24, finding-008)

All steps rerun on the ruled `agent.<cluster>.` root; the first run on a
`mail.` root gave the same results and is kept in finding-008.

| step | result |
|---|---|
| P0 | partial: kinu local and hub 2.14.6; mokuzai must be read on mokuzai |
| P1 | pass: leaf to hub to leaf sourcing with the transform onto `agent.<c>.`, seat and role forms, outbox drained; a raw cross-cluster publish is refused at the link; a broadcast publish is captured by no inbox |
| P2 | pass: 10-minute hub outage, 500 of 500 and 20 of 20 delivered once and in order, 50 repeats dropped at the outbox; both legs resumed 40 to 44 seconds after the hub restart |
| P3 | pass on the template; the refusal reaches the publisher as a timeout plus an async named error, which the shim must turn into a named failure |
| P4 | pass: one row per seat through five restarts, stale compare-and-set refused, delete propagated, a cut cluster aged out of the fleet view |
| P5 | pass: full inbox refuses; two sender notices before age removal; no server advisory for the removal |
| P6 | pass: second consumer on an address refused, and so is a team-wide watcher; rebinding keeps the count at 1 |
| P7 | pass: new `INBOX` refused while the legacy subjects are held (the seat pattern overlaps the legacy role pattern); park, create, migrate moved exactly the 10 unread of 17; a rerun moved nothing; rollback deleted the 1 migrated message read after cutover, republished the 3 new ones, and left the legacy durables at 4, 5, 3, 0 pending as expected |

Not yet covered: sourcing under restricted leaf users, per-seat JWT on a leaf
in operator mode, and the subject delete marker.
