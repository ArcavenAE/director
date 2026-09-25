# finding-008: the leaf fabric holds on scratch brokers; a link cut delays mail and loses none, and a permission refusal still reaches the caller as a timeout

- **Date:** 2026-09-24
- **Session:** arcaven-architect-g5-0, on the operator's ruling of director#77
- **Subject:** design brief 11 (`sim/design/leaf-fabric-one-address-space.md`), probe P0 to P6
- **Confidence:** measured on nats-server 2.14.6 scratch brokers; P0 verified on all three live servers (the mokuzai read was made on mokuzai and relayed, 2026-09-24)
- **Rig:** `probe/leaf-fabric/` (`rig.sh`, `p2-linkcut.sh`, `p4-presence.sh`, `fabtool/`). A scratch hub (domain `ghub`) and two scratch leaf clusters (domains `pa`, `pb`), plus two standalone scratch servers for P3 and P5, all on random ports above 20000 with their own store dirs. The live brokers were read through their monitoring ports only; their process ids were the same before and after the run.
- **Subject root:** first run on `mail.`; the operator then ruled for `agent.<cluster>.` with a flag-day cutover, and P1 to P7 were rerun on that root (section 4). `out.` stays as the internal outbox transport subject.

## 0. The two sentences

**Mail to another cluster held in the sender's outbox through a ten-minute link
cut and arrived exactly once, in order, when the link returned.** 500 of 500
distinct messages on the leaf-to-leaf leg, 20 of 20 on the director leg, zero
duplicates from 50 deliberate repeats, the outbox drained to zero.

**A per-seat credential refuses exactly the right subjects, but the refusal
reaches a JetStream publisher as a 3-second timeout; the named error arrives
only on the connection's async error handler.** The shim has to capture that
error and fail the send on it, or R-109's "denial wearing a timeout's clothes"
survives the move to per-seat credentials.

## 1. Results

| step | result | measured |
|---|---|---|
| P0 versions | **pass** | kinu local broker 2.14.6; hub 2.14.6; scratch servers 2.14.6. mokuzai, read on mokuzai by a seat there and relayed through the director: one nats-server, 2.14.6 (go1.27.0), on-disk binary matches the running one, JetStream domain `mokuzai`, one leaf remote to the hub authenticated by NKey, local account `$G`, link up. From kinu the mokuzai leaf shows only its server id, and `/leafz` carries no version. Work-queue sourcing with a durable arrived in 2.14, so every server qualifies. |
| P1 wiring | **pass** | 10 published at `pa` on `out.pb.w.t.x.inbox` arrived in `pb` `INBOX` as `mail.pb.w.t.x.inbox`, `Nats-Msg-Id` preserved; 3 on `out.director.inbox` arrived in the hub's `DIRECTOR_INBOX`; `pa` `OUTBOX` 0 afterwards. Sourcing **leaf to hub to leaf** works (brief 11 had this UNVERIFIED). Two sources with the same stream name on different external APIs are accepted on one stream. |
| P1 isolation | **pass** | A raw publish at `pa` to `mail.pb.w.t.x.inbox` failed with "no response from stream" and stored nothing: the leaf deny lists keep raw mail subjects off the link. |
| P2 link cut | **pass** | Hub killed for 600s. During the outage `pa` acked all 500 plus 20 locally and reported the 50 repeats as duplicates at publish; `pb` `INBOX` held none of them. After the hub returned: 500 distinct in `pb` `INBOX`, 0 duplicate ids, 0 out of order; 20 in `DIRECTOR_INBOX`; `pa` `OUTBOX` 0. |
| P2 timing | **note** | The leaf-side source (`pb` from `pa`) caught up within seconds of the link returning. The hub-side sources (the director leg) stayed inactive for about 44 seconds after the hub itself restarted, then drained all 20. A hub restart adds a source retry delay; it delays and does not lose. |
| P3 permissions | **pass, with the refusal finding above** | Operator mode, one scoped signing key, one template with tags in the middle of the subject (`mail.{{tag(cluster)}}.{{tag(ws)}}.{{tag(team)}}.>` and three more). A worker stored to its own team on its cluster, its own team on another cluster (outbox), its supervisor, and the director. It was refused for another team on its cluster, another team on another cluster, and a different seat in its supervisor's team; nothing refused was stored. A second seat on the same key expanded the same template to its own team. Every refusal: `context deadline exceeded` after 3001 to 3002 ms from `Publish`, with `Permissions Violation for Publish to "<subject>"` on the async error handler within that window. |
| P4 presence | **pass** | Five writes of one seat key with five instances left one key on the cluster and one in the hub's sourced fleet view, holding the latest instance. Compare-and-set: the holder's update at its revision succeeded; a rival at the same stale revision was refused (`wrong last sequence`). A delete on a cluster removed the key from the fleet view. With `pa` killed and `pb` heartbeating, `pa`'s row aged out of the fleet view within its 20s age while `pb`'s stayed. |
| P5 no silent expiry | **pass** | Inbox capped at 3 messages with `discard: new`: the 4th publish was refused, `maximum messages exceeded` (10077). With `max_age` 150s and notices at 50s and 100s, the sweeper sent every message's sender two notices (6 in the sender's inbox) before the messages left at about 152s. A subscriber on `$JS.EVENT.>` for the whole window saw only API audit advisories and **no advisory for the age removal**, which confirms the brief's decision not to depend on one. |
| P6 one consumer per address | **pass** | A second durable on the same address was refused (`filtered consumer not unique on workqueue stream`, 10100). Re-creating the same durable three times left the consumer count at 1. A fetch and ack removed the message. |

## 2. What this changes in brief 11

1. **Section 2.4 overclaims.** "A refused publish returns a named permissions
   refusal at the shim" is true only if the shim makes it true. The server
   sends the violation asynchronously; the JetStream publish call just waits
   for an ack that never comes. The shim must register an error handler and
   fail an in-flight send when a permissions violation names its subject, well
   before the publish timeout. Brief 8's "no responders at once" was the leaf
   case, where the leaf enforces the hub's pushed permissions; a client
   refused by its own broker's user permissions does not get that.
2. **Section 2.7 constraint.** On a work-queue inbox the server refuses any
   consumer whose filter overlaps another's, including a team-wide or
   cluster-wide watcher (P6). The notifier cannot be a consumer on `INBOX`. It
   reads stream state and messages by sequence, as `fabtool sweep` does, or
   watches a separate limits-retention copy of the inbox. That is also why P5's sweeper uses
   direct reads.
3. **Section 2.3 timing.** A hub restart delays the hub-side sources by tens of
   seconds. The director's inbox lives on the hub, so the director leg is the
   one that sees it.
4. **Section 2.6 holds as written,** including delete propagation through the
   source.

## 3. Not covered by this run

- The leaves and hub ran with no authorization. The leaf allow lists that
  permit only the sourcing API, delivery, and flow-control subjects are
  specified in brief 11 and were not exercised; sourcing under restricted
  leaf users is the next step before any live change.
- Per-seat JWT permissions were measured on a standalone server, not on a
  leaf in operator mode.
- The subject delete marker (`SubjectDeleteMarkerTTL`) was not tried.

## 4. Rerun on the ruled root, and the cutover probe (P7)

The operator ruled the same day: no `mail.` root; stay on
`agent.<cluster>.<ws>.<team>.<id>` and accept a coordinated cutover from
`AGENT_INBOX`. Every step above was rerun on that root on fresh scratch
brokers, and P7 was added.

- **P1 to P6 on `agent.<cluster>.`:** same results as the first run. The
  cluster `INBOX` lists `agent.<c>.*.*.*.inbox` and `agent.<c>.*.*.role.*.inbox`
  explicitly; a role message arrives on the role form; a broadcast publish is
  captured by no inbox. In P2 both legs resumed 40 to 44 seconds after the
  hub restart this time (the first run saw the leaf-to-leaf leg resume in
  seconds), so the resume delay belongs to any source whose path ran through
  the restarted hub, not only the hub's own sources.
- **P7 overlap, measured:** with a legacy `AGENT_INBOX` holding
  `agent.*.*.*.inbox` and `agent.*.*.role.*.inbox`, the server refuses a new
  stream on `agent.kc.>`, on the two explicit patterns, and on the seat pattern
  alone (10065, subjects overlap). The role pattern alone (seven tokens) is
  accepted. The six-token seat pattern overlapping the six-token legacy role
  pattern is what forces the flag day.
- **P7 cutover:** publishers stopped, legacy subjects parked to
  `legacy.parked.agent_inbox` (messages and durables kept), `INBOX` created,
  then `fabtool migrate` moved exactly the unread set, 10 of 17 messages, by
  the highest-ack-floor rule (three addresses, one of them cold; the role
  address fully read). A rerun stored nothing new (dedupe on the original
  `Nats-Msg-Id`).
- **P7 rollback:** after one migrated message was read and three new ones
  arrived, parking `INBOX`, restoring the legacy subjects, and
  `fabtool rollback` left the legacy durables' pending counts at exactly the
  expected 4, 5, 3, 0.
- **A tooling defect found and fixed in the run:** republishing a message read
  back from a stream must drop the metadata headers the read adds
  (`Nats-Subject`, `Nats-Sequence`, `Nats-Time-Stamp`, `Nats-Stream`). Copying
  them made a later read report the old subject, which read as a wrong
  rollback until the stream's own subject counts showed it was correct. The
  production migration tool needs the same filter.

The plan these results support is brief 11 section 5.
