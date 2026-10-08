# Per-seat global inboxes: closing the two-supervisor collision on the fabric

**Status:** design for review, 2026-10-07. Nothing here is built, cut over, or
reconfigured. Every step that touches a live broker stays operator-gated, as
brief 11 section 5 already requires.

**Ruling this note works from.** The operator, 2026-10-07, on the architect's
options for two seats sharing one global supervisor address: "global
supervision collision: B". B is per-seat global inboxes, in the direction of
R-94 as amended, delivered by the brief 11 cutover. Option A (a shim-side
filter as a stopgap) is not ruled. Until the cutover ships, the research seat
keeps forwarding the supervisor's mail to it verbatim.

**What this note adds to brief 11.** Brief 11 (`leaf-fabric-one-address-space.md`)
already defines the grammar, the streams and the cutover. It leaves two things
open that decide whether the collision actually closes: which role a seat
holds on the fabric (its section 8 question), and who reads the legacy global
stream between the two cluster windows. This note answers both, names the shim
change, proposes the R-94 text, and gives the forwarding seat an end
condition.

## 1. The collision, as built

Every seat cast `supervisor` or `research-supervisor` on a cluster reads the
same global subject.

- `sim/twin/cast-launch.sh:134` maps both cast roles to the one global
  role word: `supervisor|research-supervisor) GROLE=supervisor;;`. The comment
  above it cites R-94 as amended and the operator's 2026-09-26 reaffirmation.
- `probe/nats-phase-0/director-mcp/global.go:147`: every non-director
  global seat consumes `global.<cluster>.supervisor.inbox`. `selfAddress`
  (`:167`) gives each of them the same address,
  `global://<cluster>/supervisor`.
- `global.go:205`: the durable is per session,
  `mcp_global_<agent>_<instance>`, filtered on that one subject
  (`consumerConfig`, `:459`, `FilterSubject: g.cfg.inboxSubject()`).
- The `GLOBAL_TO_<cluster>` streams use limits retention (director's
  measurement below). Every durable on the subject is delivered every
  message, so the address is a fan-out, not a queue.

Director's measurement, 2026-10-07: `nats --js-domain global stream info
GLOBAL_TO_kinu` reports limits retention, 11 consumers and 677 messages. Some
follow-up reads returned "no responders". That is recorded as unexplained:
the shim's own hint (`global.go:579`, `hubHint`) names a down leaf link and
a credential that does not grant the stream as two causes that both surface
as no responders, and the reply cannot separate those two causes, or any
other. Nothing in this design depends on resolving it,
and nothing here should be read as explaining it.

The cost was measured before this note: finding-023 (91% of one seat's 300
held global messages were for another team's seats) and the frontier node
`question-global-supervisor-delivery-scope`. The collision this ruling
addresses is the narrower case inside one team: the supervisor and the research
supervisor share a team, so even a team-qualified global address would still
reach both.

## 2. Per-seat subject grammar

Brief 11 section 2.1, unchanged:

```
agent://<cluster>/<workspace>/<team>/<id>     agent.<cluster>.<workspace>.<team>.<id>.inbox
role://<cluster>/<workspace>/<team>/<role>    agent.<cluster>.<workspace>.<team>.role.<role>.inbox
director                                      director.inbox
```

Three rules this note adds, each needed for the collision to close:

1. **A seat's fabric role is its cast role.** The research seat's role address
   is `role://<cluster>/<ws>/<team>/research-supervisor`; the supervisor's is
   `.../supervisor`. The `GROLE` mapping exists because the original R-94
   allowed exactly two global role words, so a research seat needed the
   `supervisor` word to hold any global address at all. The amended R-94 gives
   every seat a fleet address, which removes that reason. If the research seat
   kept the `supervisor` role on the fabric, `role://.../<team>/supervisor`
   would be a one-taker queue with two holders, and the research seat could
   take the supervisor's mail: the same collision moved to the role form.
   This needs the operator's word, because it retires the 2026-09-26
   reaffirmation's mapping (section 6, ruling 1).
2. **One durable per address, never per session.** Brief 11 section 2.5:
   work-queue retention, and the server refuses a second consumer on the same
   filter. A seat's successor binds the same durable, so a second live holder
   of one seat address is refused at the broker rather than silently sharing
   its mail.
3. **The legacy short form `global://<cluster>/supervisor` is an alias, and it
   refuses when ambiguous.** It resolves through the roster (R-92) to the one
   live seat holding the `supervisor` role on that cluster. When more than one
   does, as on every cluster with more than one team, it refuses before
   publish and names the candidates (R-78). It never fans out again. A sender
   that wants every supervisor uses broadcast, which keeps its fan-out
   semantics (brief 11 section 2.1).

The role queue (rule 2 applied to a role address) is the answer this note
recommends to brief 11's section 8 question: one taker per message, so two
replicas of one role on one team stop splitting each ask by PROPOSE, which is
the in-team case finding-023's addendum records.

## 3. The shim change

Against `global.go` and `cast-launch.sh` at director main `1e7e6c9`. It is
the brief 11 shim build (section 5.1 step 2), plus the pieces marked *added*.

| Today | After |
|---|---|
| `inboxSubject` returns `global.<cluster>.supervisor.inbox` for every global seat (`global.go:147`) | the seat reads its own `agent.<cluster>.<ws>.<team>.<id>.inbox` and its role's `...role.<role>.inbox`, in the cluster's `INBOX` stream |
| durable `mcp_global_<agent>_<instance>` (`:205`) | one durable per address, named from the address, with no instance token; a second binder is refused |
| `selfAddress` returns `global://<cluster>/supervisor` (`:167`) | the seat's one address, `agent://<cluster>/<ws>/<team>/<id>` |
| `parseGlobalAddress` accepts two forms and refuses everything else (`:217`) | *added:* `global://<cluster>/supervisor` resolves through the roster to one seat or refuses with the candidates named; `global://director` resolves to `director` |
| cross-cluster mail publishes straight to the hub stream (`publish`, `:743`) | publishes on `out.<dest>.` into the local `OUTBOX`, which the destination's `INBOX` sources (brief 11 section 2.3) |
| `cast-launch.sh:134` maps `research-supervisor` to `GROLE=supervisor` | *added:* the cast role is the fabric role; `DIRECTOR_GLOBAL_ROLE` retires with the global tier |
| a refused publish surfaces as no responders (`hubHint`, `:579`) | the async permission error becomes a named refusal (brief 11 section 2.4) |
| *added, transitional:* none | a `legacy-global` read of the cluster's old global subject, enabled only on the seat named in the cutover plan as the forwarder, off everywhere else (section 5.3) |
| `validate` allows only `supervisor` and `director` as `DIRECTOR_GLOBAL_ROLE` (`:137-140`) | unchanged for the legacy read. The forwarder's `legacy-global` config is stated, not derived: `DIRECTOR_GLOBAL_DOMAIN`, `DIRECTOR_CLUSTER` and `DIRECTOR_GLOBAL_ROLE=supervisor`, set by the cutover plan on that one seat, because its cast role no longer maps to a legacy role word and `research-supervisor` would be refused here |
| `presenceKey` (`:177`) and `globalPresencePrefix` (`:185`) key legacy presence as `presence.<cluster>.supervisor.<instance>`; `writePresence` records `role: g.cfg.Role` (`:673`) | the legacy row stays, written only by the forwarder, so uncut senders' liveness check still finds a reader. *added:* the alias never reads legacy presence. It resolves from the fabric presence row (brief 11 section 2.6), whose role field is the cast role, so the research seat is never a `supervisor` candidate even while its legacy row says `supervisor` |
| `publish` checks legacy presence before sending (`:748-752`) | unchanged on uncut clusters, which is what keeps their sends to `global://kinu/supervisor` landing while the forwarder holds the legacy row. On a cut cluster, `global://` sends take the alias path, with one transitional fallback: a destination cluster that the cutover record does not list as cut is reached by this legacy publish, unchanged, so the old tier carries that traffic as brief 11 section 5.2 says. *added:* the shim reads the cutover record (section 5.3) on each such send; a failed read refuses the send with the error named, and never reads as "not cut" |
| `seatFloor` finds a reconnecting seat's floor from `mcp_global_<agent>_` durables filtered on `inboxSubject` (`:390`) | unchanged for the legacy read: the forwarder's new instance resumes from its own departed instances' floor, so restarting it at the kinu window does not replay the legacy stream from the start. The supervisor runs no legacy read, so its old durables are left to `globalConsumerInactive` |

The legacy read is the one piece that is not in brief 11. It exists only for
the gap between two cluster windows and is removed in the same change that
retires the `GLOBAL_TO_*` streams.

## 4. R-94 amendment text (proposed, provisional)

To be appended to R-94 after the ruled 2026-09-24 text. Nothing in the ruled
text is replaced.

> A seat's role on the fabric is the role it was cast with, and a role address
> names one team's role: `role://<cluster>/<workspace>/<team>/<role>` is a
> queue, and each message is taken by one holder. The legacy
> `global://<cluster>/supervisor` is an alias resolved through the roster
> (R-92) to the one live seat holding that role on the cluster, and refuses
> before publish, naming the candidates, when more than one does (R-78); it
> never fans out. A message meant for every holder of a role is a broadcast.
> Replaced wording: the `cast-launch.sh` mapping of `research-supervisor` to
> the `supervisor` global role word, reaffirmed 2026-09-26, which existed
> because only two global role words could hold an address.

Source class: JUDGMENT (the alias rule and the queue semantics are design),
with the collision mechanism OBSERVED in the code at the lines in section 1
and the fan-out OBSERVED in director's stream measurement.

## 5. Cutover plan

### 5.1 Order

Brief 11 section 5, with nothing reordered:

1. Builds ready and pinned: the new shim with section 3's changes, the
   migration tool, and the current shim pinned as the rollback build.
2. `DIRECTOR_INBOX` created on the hub first, and the empty `FABRIC_CUTOVER`
   record beside it (section 5.3). The hub's leaf allow-list grants
   `GLOBAL_PRESENCE` by name (`probe/nats-global-tier/hub/nats-server.conf:56-62`),
   so the same step adds read grants for the record to each cluster's leaf
   user: `$JS.<domain>.API.STREAM.INFO.KV_FABRIC_CUTOVER`,
   `$JS.<domain>.API.DIRECT.GET.KV_FABRIC_CUTOVER.>` and
   `$JS.<domain>.API.STREAM.MSG.GET.KV_FABRIC_CUTOVER`. Read only: no leaf
   gets a write grant, because only the operator writes the record. Without
   the grants every read answers no responders and every gap send is refused,
   so the step's check is one read of the record from each cluster's leaf.
3. The kinu window (brief 11 section 5.3, steps 1 to 10). Before step 8,
   `cast-launch.sh` on kinu carries the cast-role change, so every seat starts
   on its own address.
4. The mokuzai window, in the same session if possible.
5. `GLOBAL_TO_*` retire after the last cluster is cut and their pending counts
   read zero. That rule also catches unread mail already on
   `GLOBAL_TO_mokuzai` at the last window, which has no reader afterwards: a
   nonzero pending count holds the retirement until it is carried across or
   reported.

The cutover plan goes to director before any of it is scheduled. No window
opens on this note.

### 5.2 Checks that the collision is closed, run at step 8 of each window

- Send one message to the supervisor's seat address and one to its role
  address. The supervisor receives both; the research seat receives neither.
- Send one to the research seat's role address. Only the research seat
  receives it.
- Send to `global://<cluster>/supervisor` on a cluster with more than one
  supervisor. It is refused before publish, and the refusal names the
  candidates.
- A second shim binding a live seat's address is refused at the broker.

The cutover record is one operator write per cluster, and a wrong or missing
write misroutes mail. So at each window's step 8, and again before the next
window opens, the operator checks it:

```
nats --js-domain global kv ls FABRIC_CUTOVER
```

The listed keys must equal the set of clusters past step 7. `kv ls` opens a
consumer, so the check runs under the operator credential, not a leaf user,
whose grants are read-only. A listed cluster
without fabric presence, or an unlisted cluster with fabric presence, is a
stop.

### 5.3 The gap between windows, and the forwarding seat

Between the kinu window and the mokuzai window, an uncut mokuzai seat that
sends to `global://kinu/supervisor` still publishes onto the legacy kinu
global subject (brief 11 section 5.2: the old tier carries supervisor traffic
in the gap). After kinu is cut, no kinu supervisor shim reads that subject
unless one is told to.

So the research seat's shim keeps the `legacy-global` read (section 3) on
kinu, and no other kinu seat does. During the gap the research seat is the
only reader of the shared subject, and it keeps forwarding verbatim to the
supervisor, now addressed by the supervisor's seat address. The supervisor no
longer reads the shared subject at all, so the collision is closed for the
supervisor from the kinu window on; the research seat carries the legacy
traffic until it stops.

The other direction needs no forwarder, and it is keyed on cutover state,
never on presence. The cutover plan keeps a **cutover record**: a hub
key-value bucket, `FABRIC_CUTOVER`, with no TTL and one key per cut cluster.
The operator writes a cluster's key after its window's step 7 count matches
and before step 8 starts the new shims. Writing it earlier would lose mail:
`OUTBOX` is work-queue, so once the key sends other clusters' mail to the
fabric, sourcing moves each message into this cluster's `INBOX` and removes
it from the sender's `OUTBOX`, and a step-7 abort deletes that `INBOX`
(brief 11 section 5.3 step 7) after the senders were told it was accepted.
With the key written after the count, a step-7 abort has no key to undo and
no sourced mail to lose.

Before the write, other clusters' sends take the legacy publish and are
refused loudly, because nothing reads the legacy subject any more. That
depends on one stated check at step 1: after the cluster's shims stop, the
operator confirms no legacy presence row for the cluster remains. Rows expire
after 90s (`probe/nats-global-tier/hub/provision.sh:24`), so a killed shim's
row can outlive it, and while one remains a legacy send passes the liveness
check and strands on the legacy stream. Legacy keys have four tokens
(`presence.<cluster>.supervisor.<instance>`), so the check matches
`presence.<cluster>.>`, under the operator credential:

```
nats --js-domain global kv ls GLOBAL_PRESENCE > presence.txt; echo rc=$?
grep -c 'presence\.director\.' presence.txt
grep 'presence\.<cluster>\.' presence.txt
```

Pass: `rc=0`, the director's row is counted at least once (a positive control
that the listing holds keys at all, in whatever layout the CLI prints), and
the last line prints nothing. A failed or empty listing is not a clean one. In a rollback the key is deleted first, before brief 11
section 5.4 step 1 stops the new shims. A cut
kinu seat sending to `global://mokuzai/supervisor` reads the record:

- **mokuzai not listed** (the gap): the send takes the legacy publish
  (section 3, the `publish` row), and mokuzai's uncut supervisors read it as
  they do today.
- **mokuzai listed**: the send goes to the fabric through `OUTBOX`
  (brief 11 section 2.3), and waits there through a link outage. Presence on
  the destination does not change this. A fabric presence row that expired
  during an outage (section 2.6 gives `FLEET_PRESENCE` a short TTL) must not
  read as "uncut": mokuzai's legacy rows were deleted at its step 1
  (`deletePresence`, `:691`), so a legacy publish would be refused, or, if a
  legacy row outlived its session, stranded on `GLOBAL_TO_mokuzai` with no
  reader.
- **the record cannot be read**: the send is refused, naming the error. A
  failed read is never taken as absence. A key-not-found answer, including
  from an empty bucket, means "not listed"; any other error refuses.

Alias resolution never reads legacy presence, in either direction. The
fallback is chosen by the cutover record alone, never by a legacy row or by
missing fabric presence, so the research seat's legacy `supervisor` row can
never make it a resolved candidate. Once the fallback is chosen, the legacy
publish keeps its own liveness check on legacy presence, unchanged; that
check decides only whether anyone reads the legacy subject, not who.

**The forwarding seat's end condition.** The research seat stops forwarding
when all three hold, and reports each to director with the command it ran and
its output:

1. The last cluster window has completed its step 9 (legacy quiet), from that
   window's own record.
2. Its own legacy global durable shows nothing unprocessed and nothing
   ack-pending. The durable is named per instance,
   `mcp_global_<agent>_<instance>` (`global.go:205`), so the reader takes its
   agent id and its current instance from its own presence row and reads that
   one name:
   ```
   nats --js-domain global consumer info GLOBAL_TO_kinu mcp_global_<agent>_<instance> --json
   ```
   Pass: `num_pending` is 0 and `num_ack_pending` is 0. Departed instances'
   durables do not count toward the pass and are not read: the current
   durable was created from the highest of their ack floors (`seatFloor`,
   `:388`, which falls back to `floorByName`, `:415`, because a cluster
   credential gets no responders on consumer listing, finding-006), so its
   pending count already covers everything above them. The step lists no hub
   consumers for the same reason.
3. The legacy kinu global stream has not grown for 24 hours:
   ```
   nats --js-domain global stream info GLOBAL_TO_kinu --json
   ```
   Pass: `state.last_seq` is the same on two reads at least 24 hours apart,
   and `state.last_ts` is more than 24 hours old at the second read.

A "no responders" reply, a timeout or any other error on either command is
not a pass. It is a failed check, reported as it came back.

Then its shim restarts without the `legacy-global` read, and `GLOBAL_TO_kinu`
is eligible for retirement under brief 11 section 5.2. If the mokuzai window
does not happen, the end condition does not arrive and forwarding continues;
that is the honest state, not a failure to clear.

### 5.4 Rollback

Brief 11 section 5.4 applies as written, for both clusters together (the
outbox limit there is the reason). Two additions:

- The pinned old shim brings back the `GROLE=supervisor` mapping, so after a
  rollback both seats share the legacy address again, and the research seat
  resumes forwarding on the shared subject exactly as it does today.
- A rollback after the kinu window but before the mokuzai window needs only
  kinu rolled back; mokuzai never left the legacy tier.
- Each cluster's rollback deletes its `FABRIC_CUTOVER` key first, before its
  new shims stop, so no other cluster sends to its fabric inbox while it is
  being parked. Mail already sent waits in the sender's `OUTBOX`, the limit
  brief 11 section 5.4 states.

The R-94 text in section 4 is not rolled back by a cutover rollback. It
describes the target, and the old build simply does not implement it yet.

## 6. Rulings needed

Each goes to the operator through director. Each recommendation is valid
until 2026-10-21 or until the kinu window is scheduled, whichever comes first;
the architect re-checks it then.

1. **Does the research seat hold `research-supervisor` on the fabric, ending
   the 2026-09-26 mapping?** Recommended: yes (section 2, rule 1). The
   alternative, keeping `supervisor`, reproduces the collision at the role
   queue.
2. **Role address semantics (brief 11 section 8).** Recommended: a one-taker
   queue per team role, and the cluster-wide legacy alias refuses when
   ambiguous. The alternative, refusing a role address with more than one
   holder, would refuse every send to a role run with two replicas.
3. **The forwarding seat for the gap.** Recommended: the research seat, as
   ruled for today, with the end condition in section 5.3.

## 7. What this does not cover

- The "no responders" replies in director's measurement. Unexplained, and a
  separate read.
- Whether the 677 messages on the legacy kinu stream include unread mail for
  any seat. Brief 11 section 5.3 step 3 counts it per address at the window;
  this note does not count it ahead of time.
- Restricted leaf users, per-seat JWT on a leaf in operator mode, and the
  delete marker, which brief 11 section 10 lists as not yet probed.
- Clusters other than kinu and mokuzai.

## 8. Candidate requirements (provisional)

- **GSI-A** (amends R-94, section 4 text): a seat's fabric role is its cast
  role; a role address is a one-taker queue per team role.
- **GSI-B** (amends R-94): the legacy cluster-wide supervisor alias resolves
  through the roster to one seat or refuses with the candidates named; it never
  fans out.
- **GSI-C**: a cutover that leaves an uncut cluster names one reader for each
  legacy subject still receiving mail, and that reader carries a written end
  condition checked by command.
