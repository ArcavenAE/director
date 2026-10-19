# The presence watch: absence and return per cluster, and held mail released on return

Status: design for review, 2026-10-19. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Issue: #319.
- Commission: the operator's "yes please execute on each" on director's
  improvement list. This is item 3: director watches global presence per
  cluster, reports a cluster or a supervisor going absent and coming back, and
  releases any held message when its recipient returns.
- Requirements: R-08, R-78, R-92, R-93, R-96, R-141, R-143, R-209, R-210,
  R-211, R-212 (`sim/requirements.md`).
- Prior art: `ask-ledger.md` (its reader already polls `GLOBAL_PRESENCE`),
  `asks-die-unread.md`, `shim-timer-heartbeat.md` (one timer), `channel-cue.md`
  (a shim-made notice to its own harness), brief 11
  `leaf-fabric-one-address-space.md` (the transport-level outbox, not built),
  finding-024 (held relays resent after an outage, one superseded while it
  waited), #88, #126, #289, #297.
- Checked against director `origin/main` 59f8512. Every `file:line` below is
  at that sha. A claim with no citation is marked inferred.

## 1. Why

Director finds out that a cluster left, or came back, only when something
happens to read the roster. On 2026-10-19 that cost hours twice. One cluster
was absent about six hours while director held an operator ask for it, and the
ask went out only when a status turn happened to list the roster. Separately,
every supervisor row for a second cluster was gone from 03:28Z to 04:01Z, and
director learned it from a peer team's supervisor, not from its own reading.
At about 04:3xZ a send to that cluster was refused with "nothing is registered
under presence.mokuzai." (these three are as reported by director; not
re-measured here).

A watch turns both into events director sees within a minute, and lets an ask
that could not be sent go out when its recipient returns, without a person or
a lucky turn remembering it. The cost of merge is one new opt-in loop on the
director seat's existing timer and its local files. The hold in section 4 is
behind the same flag, so with the watch off every send behaves as it does
today. No other seat's behavior changes until a separate ruling (section 5).

The watch is diagnostic. Its reports and counts block nothing (SOUL section 8,
ADR-007, `diagnostic-not-gate.md`).

## 2. What exists today (measured)

- **The bucket and its keys.** `GLOBAL_PRESENCE` (`global.go:44`). A key is
  `presence.<cluster>.<role>.<instance>`, or `presence.director.<instance>` for
  director (`global.go:184-196`). Only two roles exist at the global tier,
  `supervisor` and `director` (`global.go:35-41`). A worker seat never has a
  global row.
- **That holds only while the shim is the only writer.** The shim refuses any
  other role at start (`global.go:144-147`) and writes presence in one place,
  under `presenceKey` (`global.go:781`, `184-185`). The hub does not enforce
  it: the director credential may publish to `$KV.GLOBAL_PRESENCE.>`, and each
  leaf has the bucket's KV API (`probe/nats-global-tier/hub/nats-server.conf:31`,
  `56`, `82`). So any client holding one of those credentials can write a key
  of any shape. The watch states that assumption and narrows what it trusts
  (D4), but it cannot close it. A key of the right shape with a value that
  parses, written by a director or leaf credential, still counts as presence.
  That residual is #323's to close.
- **TTL and beat.** The probe hub creates the bucket with `--ttl 90s`
  (`probe/nats-global-tier/hub/provision.sh:24`); the live hub's setting is
  inferred to match and should be read once (`nats kv info GLOBAL_PRESENCE`).
  The shim renews both rows every 30s on its own timer (`main.go:166-168`,
  `bus.go:1337-1351`) and deletes them on an orderly exit
  (`bus.go:1363-1381`). A crash, a host sleep or a dropped leaf leaves the key
  to expire by TTL.
- **The roster read.** `list_roster` merges local and global rows
  (`bus.go:1435-1460`). It reads keys, then gets each one; it discards the
  scan, so an unreadable global row vanishes from the roster without a word
  (`bus.go:1451`). The send path does not discard it (`global.go:801-841`).
- **The refusal.** `publish` reads presence first and refuses with no live
  row (`global.go:849-856`). The text in the evidence is the
  `TeamMatched == 0` branch (`global.go:327-328`): no key at all under
  `presence.<cluster>.` (`teamScope`, `bus.go:777-783`), worded as "the
  cluster name is likely wrong". That wording is R-210's complaint and, for
  `global://director`, #88's.
- **Something already polls the bucket.** The ask reader lists
  `GLOBAL_PRESENCE` keys each 30s pass (`askreader.go:216-238`,
  `askledger.go:21`) and keeps first-seen, last-seen and live per (cluster,
  role) (`askledger.go:106-119`, `419-438`). It records its own downtime as a
  gap when two passes are more than 90s apart (`askledger.go:466-471`).
- **No watch.** No file in the shim calls a KV `Watch` (`git grep -n
  "Watch(" origin/main -- cmd/director-mcp`, no hits).
- **No held store.** Nothing in `cmd/` or `skills/` holds a refused message.
  R-141 rules that mail for an absent seat is held and delivered on return by
  default (`requirements.md:1500-1506`), and the code refuses instead. So
  "held" today means director keeps the ask in its own session or board and
  resends by hand (inferred from the evidence and from finding-024 section 1.1).
- **Nothing subtracts director's own sleep.** R-209 asks for it
  (`requirements.md:2354-2365`); `git grep -i "slept\|pmset"` finds nothing in
  `cmd/` or `skills/`.

## 3. Choices, with a recommendation each

**D1. Watch or poll.**

| | option | gives | costs |
|---|---|---|---|
| a | a KV watch on `GLOBAL_PRESENCE` | puts and orderly deletes at once | sees no TTL expiry (below), so it needs a timer anyway; an ordered consumer across the leaf must be re-created after every link drop |
| b | poll keys and values every 30s | one code path for every way a row disappears | up to 30s late; one keys call and N gets per pass |

A bucket made with a plain `--ttl` ages messages out by stream max age, and a
watcher gets no delete marker for that (inferred from NATS behaviour; per-key
markers need `LimitMarkerTTL`, which the probe's provisioning does not set,
`provision.sh:24`). The cases that matter here, sleep, crash and a dropped
leaf, all end by expiry. **Recommended: (b), the poll.** A watch can be added
later for the orderly-exit edge if 30s proves too slow. The hub grants
consumer create on `KV_GLOBAL_PRESENCE` (`probe/nats-global-tier/hub/nats-server.conf:60,86`),
so (a) stays open.

**D2. Which process runs it.**

| | option | gives | costs |
|---|---|---|---|
| a | director's own shim, on the existing 30s heartbeat tick | the same process that holds director's identity and publish path does the release; no second principal | watches only while director's session runs |
| b | a separate loop, `director-mcp presence-watch`, like `ask-reader` | history while director is down | a second process that must either send as director (R-78 risk) or hand each release back to the shim |
| c | a sweep step | no new code path | the failure observed: it sees only when a turn runs |

**Recommended: (a), opt-in with `DIRECTOR_PRESENCE_WATCH=1`, and only on a
shim whose global role is `director`.** The release in section 6 must go out
under director's address through the normal publish, with its audit mirror and
dedupe header (`global.go:862-863`); doing that from a second process is the
cost (b) cannot avoid. A sweep still reads the watch's file (section 5), so
(c) remains as the fallback reader, not the watcher. The alternative is (b),
and it is right if the operator wants absence history recorded while no
director session runs.

**D3. Surviving restart and sleep.**

- **State on disk.** The watch keeps `presence-watch.json` (current state per
  cluster and supervisor instance, last pass time) and appends events to
  `presence-events.jsonl`, both mode 0600 beside the seat's other state, the
  way `handled.jsonl` is (`handled.go:172`, `290-300`). A new director
  instance reads them on its first tick.
- **Its own downtime is a gap, never a transition.** On start, and on any
  pass more than 90s after the last, it records `watch down <from> to <to>`,
  the ask reader's rule (`askledger.go:466-471`). It never infers that a
  cluster left and returned inside a gap; it reports "absent before the gap"
  or "present after it" as observed at each end.
- **Director's own sleep.** Each pass records wall time and Go's monotonic
  reading. When wall time advanced more than 30s beyond the monotonic clock
  between passes, the host slept; the gap is recorded as `watcher host slept
  <from> to <to>`, and every absence duration subtracts it, as R-209 asks.
  That Go's monotonic clock stops during sleep on darwin is inferred, not
  measured, and the verify script (build item 7) checks it on a real host
  before anyone relies on it.

**D4. What "absent" and "back" mean.**

- **Units.** A *supervisor* is each instance under
  `presence.<cluster>.supervisor.`; a *cluster* is the set of its supervisor
  instances. Director's own rows are not watched.
- **Only shim-shaped keys count.** A key counts toward a unit only when it has
  the shape the shim writes, `presence.<cluster>.supervisor.<instance>` with
  one instance segment, and its value parses as a presence record. Any other
  key under `presence.<cluster>.` is *foreign*: it never makes a unit present
  or keeps it present, and the watch lists each foreign key once in the event
  file and the next `presence_notice`. This stops malformed and wrong-role
  rows only. A forged row of the right shape is indistinguishable from the
  shim's and counts; section 5 names who closes that.
- **Absent:** no readable key for the unit on two consecutive passes. With a
  90s TTL and a 30s beat, that is about two and a half minutes after the last
  beat (inferred arithmetic). One missed pass is not reported.
- **Back:** a readable key on two consecutive passes. A row that also shows
  R-143's confirmation is not required for "back", because R-143 is about a
  seat's first turn, which a supervisor's presence row does not carry.
- **Unknown, never absent:** a pass where the keys call fails, or a matched
  key cannot be read, marks those units `unknown` (finding-188, the scan
  `Unreadable` count, `global.go:331-336`). A failed keys call marks every
  unit unknown and reports "watch blind: hub unreadable from this host" once,
  so director's own dropped leaf never reads as every cluster leaving.
- **Flapping:** the watch counts transitions per unit per rolling hour (R-210).
  Four or more marks the unit `flapping`; it is reported once, then quiet
  until 15 minutes without a transition. These two numbers are JUDGMENT and
  go in `thresholds.json` beside `handled_ack_wait`
  (`skills/director/reference/functions.md:273-282`).
- **Declared sleep.** `set_presence` takes a free-text state. The convention
  is `asleep` or `asleep until <RFC3339>`. The watch keeps the last state each
  unit wrote; a unit whose last row said `asleep` reads `asleep since <t>`,
  not absent, and is not reported as a loss until its `until` plus 15 minutes
  has passed. Reading a host's power log, R-209's other half, is out of scope.

**D5. Where reports go, and how often.**

| surface | what | when |
|---|---|---|
| the shim's tool results | a `presence_notice` field on `wait_for_message` and `inbox_summary`, beside `global_warning` (`tools.go:297-302`) | each unreported event, once |
| the channel cue, if `DIRECTOR_CUE=1` | one shim-written line, no sender text (`cue.go:1-10`) | an absent or back event with held mail |
| the sweep | one line `Away: <cluster> absent since <t> (N held)` | every sweep while a unit is absent |
| `presence-events.jsonl` | every transition, gap and flap | always |
| the relay log | each release (section 6) | per release |

The operator hears of presence through director's sweep reply, not a new page
and not a Desk card, since absence is not a decision. Peers are not broadcast
to. The one peer told is the returning supervisor, by the release itself,
which is R-212's re-ring for held mail. Noise limit: at most one notice per
unit per transition, flapping collapsed to one per hour.

These recommendations are valid until 2026-11-02, or until a builder starts
build item 1, whichever comes first. The architect re-checks them then.

## 4. Held messages

**Behind the watch's flag.** The hold exists only on a shim with
`DIRECTOR_PRESENCE_WATCH=1` and global role `director`, the same gate as the
watch (D2), because release runs only on the watch's back event. With the flag
off, a send that presence refuses is refused exactly as today, and nothing is
written to `held.jsonl`. A shim started with the flag off while held records
exist sends none of them. It lists them in each `presence_notice` as `N held
while the watch is off`, so turning the watch off cannot strand mail silently.

**What one is.** A director `send_message` to a `global://` address that the
shim did not publish because presence said no live recipient, kept by the
shim for release. Kept only for these outcomes:

- the four no-live-presence branches of `noGlobalPresenceErr`, including
  `Unreadable` (liveness not established) (`global.go:321-339`);
- a presence read that failed because the hub did not answer
  (`global.go:850-853`), the finding-024 shape.

Never held: a publish that failed after presence passed (a permission or
violation error, `global.go:865-873`), an R-78 refusal for two directors
(`global.go:857-861`), or a send with `fail_fast: true`, which keeps today's
refusal (R-141's per-send option).

**The tool result** says `held, not delivered`, with the hold id and the
reason text, and never "accepted for delivery" (R-08).

**Store.** `held.jsonl`, append-only, mode 0600, beside the seat's state,
written under a lock with fsync as `handled.jsonl` is (`handled.go:260-300`).
The shim is its only writer. One line per state change; the current state of
a record is its last line.

| field | meaning |
|---|---|
| `id` | the envelope `message_id`, minted at hold time |
| `to` | the address as sent |
| `envelope` | the full envelope as it would have been published |
| `held_at`, `reason` | when, and the refusal text verbatim |
| `sell_by` | the send's `reply_by`, or `held_at` plus 8h (JUDGMENT, in `thresholds.json`) |
| `state` | `held`, `released`, `proposed`, `cancelled`, `failed` |
| `state_at`, `stream`, `seq` | when, and the hub's PubAck on release |
| `superseded_by` | a later hold id that replaces this one, when director says so |

A `cancel_held` tool takes an id, and an optional replacement, so director can
withdraw or supersede a held ask, the finding-024 case.

**Release after the watch turns on.** A restart or a turn-on is a gap, never
a transition (D3), so a unit that is already present when the watch starts has
no back event. For release only, the first sighting of a unit after a gap
counts as back once one of the unit's rows shows a fresh heartbeat: that
row's `ts` (`global.go:778`) is later on a following pass than on the pass
where the watch first saw that row. Freshness is measured per row, from the
row's own first sighting, so an instance that restarts or hands off around
turn-on and whose key first appears on a later pass is confirmed by its own
next beat. A row that merely exists is not enough. A supervisor that crashed
just before turn-on leaves its row inside the TTL for up to 90s (section 2),
and a release to it would sit unread on the hub stream for up to 72h
(`global.go:48-56`). There is no pass cap: a row whose `ts` never moves is not
being renewed, so it expires by TTL into absent, and a row that keeps beating
is confirmed on its next beat. No new judgment number is needed, so nothing is
added to `thresholds.json` for this rule.
Once the heartbeat is fresh, every `held` record for the unit is released
within its `sell_by`, as below. Without this rule, a record held before a
restart for a cluster that never leaves again would wait until `sell_by` and
be proposed, not sent. The event file still records `present after the gap`,
not a return, so no false transition is reported.

Tests:
- `TestHeldReleasedOnFirstSightingAfterTurnOn`: hold with the watch on, stop
  the shim, keep the cluster's row beating, start the shim, run the passes,
  and see one stream message.
- `TestReleaseToKeyFirstSeenLateAfterTurnOn`: the old instance's row is
  gone and a new instance's key first appears on the third pass after the
  restart; its `ts` moves on its next beat and the held record is released
  once, not proposed at `sell_by`.
- `TestNoReleaseToStaleRowAfterTurnOn`: the same as the first, but the row stops beating
  before the restart and stays inside the TTL; no stream message, the record
  stays `held`, and the unit reads absent once the row expires.
- `TestFirstSightingIsNotReportedAsBack`: after the restart, the event file
  holds exactly one `present after the gap` line for the unit and no
  `back` line, and `presence_notice` reports no return. This fails on main,
  where no event file exists.

**Release.** On each tick after a unit goes back, the shim re-runs the normal
`publish` for each `held` record addressed to it, oldest first. So the R-92
and R-78 checks run again at release time, the audit mirror runs, and the
dedupe header is the record's own `id` (`global.go:863`).

- **Idempotent.** Publish first, then append `released` with stream and seq.
  A crash between the two re-publishes on the next tick, and the hub's dedupe
  window drops the copy (the window's length on the live hub is inferred to
  be the NATS default of 2 minutes and should be read; a tick is 30s).
- **Refused again** (the unit flapped out): the record stays `held`, and the
  attempt is counted, not reported each time.
- **Failed for any other reason:** `failed`, reported once, never retried by
  the shim.
- **Relay log:** each release is one `relay-log append` entry from the
  director session on its next turn, carrying the hold id, the address, the
  held duration and the seq. The log's writer stamps the time
  (`functions.md:79-87`). The shim does not write the relay log; that stays
  director's act through its writer.

**Automatic or proposed.** Recommended: **automatic within `sell_by`,
proposed after it.** Director decided the message when it composed it; putting
it on the wire when the recipient can receive it is clerical, the same act
store-and-forward does in transport, and R-141 rules that as the default. Past
`sell_by` the conditions that produced the message may no longer hold
(finding-024: one held relay was superseded while it waited), so a stale
record goes to `proposed` and appears in the next `presence_notice` and sweep
as "N held for <cluster> past sell-by: release, revise or cancel". Nothing is
sent on a clock or on silence (`expiry-is-sell-by.md`). The alternative is
**propose always**, which keeps every release a session act and costs exactly
the miss in the evidence: a six-hour ask goes out only when a turn happens to
look.

## 5. What it does not do, and its neighbours

- It does not watch local seats. The shim writes global presence for
  supervisors and director only (`global.go:35-41`; section 2 on other
  writers), so "every seat on a cluster absent" is
  visible only as that cluster's supervisor rows gone. A cluster's workers
  live in its own broker's `AGENT_STATE`, which director on another host does
  not read (inferred from the per-cluster broker layout).
- It does not harden the hub, and the residual is real. The hub lets the
  director credential and each leaf write any key in `GLOBAL_PRESENCE`
  (section 2). D4's shape rule stops malformed rows. A row of the right shape
  with a parseable value, written by any holder of those credentials, still
  makes a unit present, and the watch cannot tell it from the shim's. Narrowing
  subjects on today's shared credentials does not close this: everything on a
  cluster's local bus shares its leaf credential (R-77). A credential per
  writing principal would close it: each supervisor's shim writes only its own
  key prefix, and director's credential writes no cluster's keys. That is
  #323, a separate item. Its form is a design plus a hub config change, owned
  by the global tier's design (the architect role) and built by a builder like
  this one. The watch ships without it and says so.
- It does not hold mail for any seat but director. R-141 is fleet-wide; making
  every shim hold by default changes send semantics for every seat and is a
  separate ruling. Brief 11's outbox is the transport-level answer and
  replaces this store at its cutover.
- It does not say a returned supervisor read anything. Release is delivery to
  the hub stream (72h max age, `global.go:48-56`), not acknowledgement (R-08).
  The ask ledger tracks the reply.
- **#289** (no host address in presence): the watch says that and when a
  cluster left, not where it went. Its `absent since` is an input to a repoint,
  nothing more.
- **#297** (cue shows live after two unanswered cues): the watch reads
  presence and inherits that blindness. A present supervisor may not drain.
- **R-211:** held records are one of the lists R-211 wants. Reviews and
  doorbells waiting on an absent cluster are not in this store.
- **R-212:** the release is the re-ring for held mail, and the persisted last
  pass is the look-back. Re-ringing other stranded work stays with director.
- **R-210 and #88:** with the watch on, the refusal can say "no live presence
  for <cluster>, last seen <t>" from the watch's state. Build item 6 does that
  for the director seat only; other seats keep today's text until #88 lands.

## 6. Build items, in order, each small

All tests run against injected clocks and recorded scans, or the scratch
brokers in `leafrig_test.go` (`startLeafRig`, `provision`, `restartHub`,
`leafrig_test.go:43-150`) and `drain_broker_test.go:24`. None touches a live
bus. Each refusal assertion has a positive control first, as
`presence_scan_test.go:25-36` does.

1. `presencewatch.go`, the pure core: per-unit state from a sequence of scans
   and times. Tests: `TestWatchOneMissedPassIsNotAbsent`,
   `TestWatchTwoMissedPassesIsAbsent`, `TestWatchUnreadableIsUnknownNotAbsent`,
   `TestWatchKeysErrorMarksAllUnknownOnce`, `TestWatchBackNeedsTwoPasses`,
   `TestWatchFourTransitionsInAnHourIsFlapping`,
   `TestWatchAsleepReadsAsleepUntilPastUntil`.
2. Gaps and sleep in the core: `TestWatchGapIsNotATransition`,
   `TestWatchWallAheadOfMonotonicRecordsHostSleep`,
   `TestWatchAbsenceSubtractsHostSleep`.
3. State and events files: atomic rewrite, append with fsync, reload.
   Tests: `TestWatchStateSurvivesRestart`, `TestWatchRestartRecordsDownGap`.
4. Wire the core to the heartbeat tick behind `DIRECTOR_PRESENCE_WATCH=1` and
   role `director`, with `presence_notice` on the two tool results. Tests
   (leaf rig): `TestWatchSeesSupervisorExpireAndReturn` (stop beating a
   scratch row; see absent; write it again; see back),
   `TestWatchOnWritesStateOffWritesNothing`: a positive control first (the
   flag on, one pass, and the state file and one event exist), then the same
   pass with the flag off, in a fresh state dir that already holds the seat's
   other files, leaves that dir byte for byte as it was. The control fails on
   main, where no watch exists. Also `TestWatchHubRestartIsBlindNotAbsent` (`restartHub`) and
   `TestWatchForeignKeyNeverCountsAsPresent` (a scratch client writes
   `presence.<cluster>.worker.x` and a malformed supervisor value; the unit
   stays absent, and both keys are listed as foreign).
5. `held.go` and the hold path in `publish` for director's seat, behind the
   watch's flag, with `fail_fast` and `cancel_held`. Tests:
   `TestHoldOffKeepsTodaysRefusal` (the flag off: the refusal text as on main,
   and no `held.jsonl`; a regression pin that passes on main, paired in the
   same file with the red `TestHoldOnNoPresenceSaysHeldNotDelivered` on the
   same fixture), `TestHeldWhileWatchOffIsListedNotSent`,
   `TestHoldOnNoPresenceSaysHeldNotDelivered`,
   `TestNoHoldWhenPresencePassedAndPublishFailed`,
   `TestNoHoldOnTwoDirectors`, `TestFailFastKeepsRefusal`,
   `TestReleaseOnBackPublishesOnceWithHoldID`,
   `TestReleaseCrashBeforeRecordIsDeduped` (leaf rig: publish, skip the
   append, publish again, one stream message),
   `TestPastSellByIsProposedNotSent`, `TestCancelledIsNeverSent`.
6. The R-210 text on director's seat: `TestRefusalNamesLastSeenWhenWatchKnowsIt`,
   with the control that an unknown cluster keeps today's wording.
7. `scripts/verify-presence-watch.sh`: a scratch hub and leaf on loopback
   ports in a temp dir with a cleanup trap and a scratch `HOME`; a positive
   control (one send to a live scratch supervisor passes) before the absent
   trial; then hold, expire, return, release, and one stream message. A
   second, manual section reads wall and monotonic time across a real
   `pmset sleepnow` on the operator's host and prints both, to settle D3's
   inferred clock claim. It writes nothing outside its temp dir. Then the
   skill line: SKILL.md sweep step 4 gains the `Away:` line, and
   `functions.md` gains the held store as a function.

## 7. Candidate requirements (provisional tags)

- **PW-A (OBSERVED)** · director learns that a cluster or a supervisor left or
  returned from its own watch within a bounded time, not from a peer or a
  lucky roster read. Earned by the 2026-10-19 six-hour hold and the
  03:28Z-04:01Z absence learned from a peer.
- **PW-B (JUDGMENT)** · absence is two missed passes, return is two seen
  passes, an unreadable row or an unreadable hub is unknown, never absent, and
  only a key of the shape the shim writes counts toward presence.
- **PW-C (OBSERVED)** · the watcher's own downtime and its host's sleep are
  gaps it records and subtracts, never transitions it infers. Extends R-209.
- **PW-D (JUDGMENT)** · a unit with four or more transitions in an hour is
  shown as flapping and reported once per hour. Extends R-210.
- **PW-E (OBSERVED)** · with the watch on, a director send refused for absence is held, says
  "held, not delivered", and is released through the normal publish when its
  recipient returns, under its own message id. Earned by the six-hour hold and
  the 04:3xZ refusal; implements R-141 for director's seat.
- **PW-F (JUDGMENT)** · release is automatic within the hold's sell-by and
  proposed after it; nothing is sent on a clock past sell-by. Earned by
  finding-024's superseded relay.
- **PW-G (JUDGMENT)** · each release is recorded in the relay log by director,
  and presence reports reach the operator through the sweep, one line per
  absent cluster.

The open decision for the operator: D2 (a) or (b), which is whether absence
history must be kept while no director session runs.
