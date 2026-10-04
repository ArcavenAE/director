# Asks die unread: inherited mail, a catching-up presence, and an expiry notice

Status: design for review, 2026-10-04. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Issues: #222 (inbox max age), #220 (a respawned seat reports live before
  reading its predecessor's replies). Tracks aae-orc-t77rr.
- Requirements: R-130, R-143, R-144, R-178 (`sim/requirements.md`).
- Related: #126 and `unread-slice-m.md` (unread age per address), #83 and
  finding-015 (replay on reconnect), `continuous-custody-succession.md`
  (custody, not mail), `session-identity-and-succession-options.md` (options
  A to C).
- Checked against director `origin/main` faed978, marvel `origin/main`
  c99ce98, and the live kinu broker on 2026-10-04: the stream config at about
  03:19Z, subjects and presence at 03:24Z, consumer floors at 03:26Z to 03:27Z.

## 1. One class, three paths

An ask dies unread when no reader is reading the mailbox it was sent to. #222
and #220 are two of the three ways this happens, and they share one fix
surface (the shim's startup and its consumers), so they are designed
together.

| Path | What happens | Issue |
|---|---|---|
| P1, expiry | A live seat that does not poll lets mail age past `max_age`; the stream drops it and both sides read the quiet as nothing to do | #222 |
| P2, orphaned mail | Mail sent to an instance id that then dies is never read by its successor, which has a new id and a new subject | #220, R-144 |
| P3, early live | A successor announces presence before reading anything, so senders and director route to it as ready while it holds an unread backlog | #220, R-143, R-178 |

## 2. The premises, verified

| Premise | Where | Result |
|---|---|---|
| The inbox max age is 24h (#222, t77rr) | `nats stream info AGENT_INBOX` on kinu | **False today: 72h** (`max_age` 259200s). The 24h figure survives in `probe/nats-phase-0/verify-auth.sh:87` and `PROGRESS.md:11-12`; docs and the shim's `localConsumerInactive` (bus.go:76) say 72h. Raising it again does not close P1: a seat that never reads loses mail at any age |
| A successor never reads its predecessor's instance mail | bus.go:223 (`seatAckFloor` over prefix `"mcp_"+self.AgentID+"_"`), resolveSubject bus.go:446-478 | The resume floor and the subject are both keyed to the agent id. Only mail sent to `role://` survives a generation change |
| A seat reports live before reading | main.go:128-129 | `setPresence(ctx, "idle")` right after connect, "so a roster lists us immediately" |
| No catching-up presence state exists | tools.go:83-89, bus.go:1212-1237 | state is free text with the hint `idle | busy | away`; nothing enforces it |
| A lineage is supplied on some respawns | marvel `internal/team/controller.go:2209-2229` (c99ce98); this seat's env | marvel sets `MARVEL_PREDECESSOR` (`<workspace>/<old seat key>`) only in a shift's launch, and only when the predecessor is alive (`aliveSessions`, :2209). The successor launches **before** the drain (:2249-2259), so at its connect the predecessor is still live. A crash repair outside a shift gets no lineage; a single-replica role's repair reuses the same key, while a multi-replica role's crashed slot returns under a new index (marvel `CLAUDE.md`, Process Management). The shim reads none of it today |
| How much mail is orphaned now | `nats stream subjects AGENT_INBOX`, `kv ls AGENT_STATE`, `consumer info` on each departed id's newest durable | 1306 of 3625 stored messages sit on 16 subjects (14 agent ids) with no live presence. Past each id's newest ack floor, **47 are unread**, spread over 7 ids (two builder replicas hold 20 and 21). `num_pending` on a durable that also filters its role subject counts role mail too, so 47 is an upper bound for instance mail |

## 2a. Relation to brief 11 (the leaf fabric)

`leaf-fabric-one-address-space.md` (brief 11, nothing built) proposes the
long-term answer to P1. Its ruled parts are the amendments to R-50, R-94,
R-95 and R-109, the `agent.<cluster>.` subject root and the flag-day cutover
(its status line). FAB-C ("an inbox never deletes unread mail without first
notifying the sender", brief 11 section 7) is a **candidate** requirement, not
in `requirements.md`, and its figures (a sender INFORM at 7 and 21 days under
a 28-day `max_age` with `discard: new`, section 2.5) are **proposals** (its
section 8). This design works on today's stream and is shaped to converge if
FAB-C is accepted:

- **D3 is FAB-C's notice on today's stream**, with ages scaled to the 72h
  limit. If FAB-C is accepted and ships, its sweeper replaces U3.
- **D1 and D2 are not in brief 11.** FAB-D and FAB-E key one durable and
  presence on the seat's address, which fixes replay on restart (`iejcx`) but
  says nothing about a successor with a new id. Inheritance and catching-up
  carry over to the fabric unchanged: the inherited durable filters the
  predecessor's fabric subject instead.

## 3. D1: inherited mail (closes P2)

**Lineage input.** The shim reads an optional `DIRECTOR_PREDECESSOR`, an
agent id in the seat's own team. A launcher sets it; the shim does not parse
instance names to guess one (instance ids are not identity). `cast-launch.sh`
maps marvel's `MARVEL_PREDECESSOR` (`<workspace>/<id>`) to it, the same way it
maps the manifest role to `DIRECTOR_ROLE`. Without marvel, an operator sets it
by hand or leaves it unset; unset means today's behavior.

**When it starts.** The predecessor of a shift is still live when its
successor connects (section 2), so inheritance is armed at connect and starts
later, when the predecessor is gone. Presence is timing only, never the
lineage source (ruling B2-R1, section 9). With `DIRECTOR_PREDECESSOR` set, the
shim's existing 30s heartbeat (main.go:134-137) reads the
`presence.<team>.<predecessor>.*` keys on each tick, and each read has one of
three results:

| Read | Means | Effect |
|---|---|---|
| a row exists | the predecessor is live | the count of empty reads resets to 0 |
| the read succeeds and finds no row | the predecessor looks gone | the count goes up by 1 |
| the read fails (KV error, timeout, disconnect) | **unknown** | the count resets to 0; unknown never counts toward the trigger, and it breaks the run |

Inheritance starts when the count reaches 3: three successful empty reads in
a row, with no row and no failed read between them, about 60 to 90 seconds.
So empty, empty, failed, empty does not trigger, and the run starts again
after the failure. A predecessor whose shim restarts in place
writes its new instance's row within its own connect, well inside that span,
so the restart resets the count and does not trigger. The same three-read rule
applies when no row was ever seen (a predecessor that died before the
successor connected); there is no separate time-only fallback. The arm
expires after the stream's `max_age`.

**The delivery contract is at-least-once, not exactly-once.** A predecessor
whose shim loses the broker for longer than three ticks looks gone and is
inherited while it may still be alive, and it reads its own mail again when it
reconnects. The design accepts that and states it instead of claiming
otherwise. Every message carries its `message_id`. The inherited copy is
marked `inherited_from`, so the successor checks its predecessor's handoff
and replies before acting on it, which is R-144's "does not re-execute what
its predecessor acknowledged". A predecessor row that reappears stops the
inherited durable at once (below).

**Crash repairs.** A single-replica role's repair reuses its key, so it reads
its own floor today and needs nothing. A multi-replica slot that returns under
a new index has no lineage. The fix is marvel's: set `MARVEL_PREDECESSOR` on a
repair spawn to the crashed row it replaces (part U5, marvel#541). The
shim does not guess.

**The read.** Once started, the shim creates one more durable,
`mcp_<self>_<instance>-inherit`, filtering only the predecessor's instance
subject `agent.<ws>.<team>.<predecessor>.inbox`. It starts at the
predecessor's floor for that subject plus one, by the containment rule below.
If the predecessor has no durable left, it starts at the beginning of what the
stream holds, and the resume note says so, as the existing notes do
(bus.go:268-276).

**Floors by subject containment, not by identical filters.** Today
`seatAckFloor` skips any durable whose filter set is not identical to the
caller's (`sameSubjects`, bus.go:311 and :344). A role-holding predecessor on
today's shim has one durable over [instance, role], so an instance-only
durable would find floor 0 and replay all 72h of acked mail: the rollout case
under section 8's one-seat pin. The same mismatch would make every seat
replay its own inbox once on its first restart onto the split durables of
3a. The floor for a subject is therefore:

> the highest `AckFloor.Stream` among the matched, departed durables whose
> filter set **contains** that subject.

"Contains" is exact string membership: the subject appears verbatim in the
consumer's `FilterSubject` or `FilterSubjects`. No wildcard matching, so a
`agent.>` filter never counts as containing a role subject.

**Which consumers count, in every scope.** Only a seat durable: a durable
consumer (`Durable` set), with `AckPolicy` explicit, whose name parses as
`mcp_<id>_<ULID>` with an optional `-role` or `-inherit` suffix. Anything
else is skipped. That excludes the ephemeral `peek_<instance>` consumers (`"peek_" + newInstanceID()`, drain.go:469) that
`inbox_summary` creates (`peekWaiting`, drain.go:465-481): they copy a
holder's filters, use `AckNone` and start at its floor plus one, so their ack
floor moves with delivery, not with a read. Counted, a peek running while a
new seat starts would hand it a floor past mail nobody acked.

A durable's ack floor is the stream sequence at or below which it has acked
everything it reads, so it is a safe floor for each subject it filters. One
function, `subjectFloor`, replaces the identical-set test, with two scopes:

| Caller | Durables scanned | Why |
|---|---|---|
| own-subject resume, inherited durable | one id's durables only (the prefix `mcp_<id>_` and the per-id guard below), departed only | an instance subject belongs to one id, and a live durable of the same id is the collision, not a floor |
| role-subject start (3a) | **every seat durable** on the stream whose filter set contains the role subject, any id, live or departed; no prefix and no per-id guard | the floor that matters is what any holder of the role has acked. A new id has no durable of its own, so the per-id scope finds nothing and replays all role mail, which is the S12-8 case |

The role scope lists every consumer on the stream and reads `consumer info` for the seat durables; it
runs once per role durable at connect, not per message. Names gain a suffix after the instance ULID (`-role`,
`-inherit`). **The parse is right-anchored**, because the role scope has no
prefix to split on and an id may itself contain `_`: strip an optional known
suffix (`-role`, `-inherit`) from the right; the 26 characters before it must
be a ULID and the character before those must be `_`; what remains must start
with the tier's fixed head, `mcp_` on the local stream or `mcp_global_` on the
hub stream (`globalDurable`, global.go:176-177), and the text between that
head and the `_` is the id. Each stream is parsed with its own head, so a
local id that begins `global_` is never read as a global durable. In the
per-id scopes the id must also equal the caller's, which keeps the existing
guard against one id prefixing another (bus.go:315-317).

**The invariant this depends on.** A durable outlives its session: it is not
deleted at exit, and it expires only after `InactiveThreshold`, 73h on both
tiers (`localConsumerInactive`, bus.go:76; `globalConsumerInactive`,
global.go:55). That is longer than the 72h `max_age` of AGENT_INBOX and the
global streams, so while any of a departed seat's mail is still stored, its
floor is still readable.

**How it reads.** `wait_for_message` and `inbox_summary` treat the inherited
durable as a third source after local and before global, and every message
from it carries `inherited_from: <predecessor>` in the result. The successor
answers with `in_reply_to` as usual; the sender sees a reply from a new id,
which is the truth.

**When it ends.** The inherited durable is deleted once it reports zero
pending and the predecessor is still not live, or after the stream's max age,
whichever comes first. A predecessor that comes back to life (a live
presence row appears) stops the inheritance at once: two readers of one
mailbox is the collision `session-identity-and-succession-options.md` option C
warns about.

**Stated limit.** Mail sent to the predecessor's id after it died is refused
at send time today when no workspace is given (bus.go:774, `pickWorkspace`), so D1 reads
what was waiting at death plus what was sent with an explicit workspace. It
does not redirect new mail. That stays with senders addressing `role://`,
which already survives a generation (section 2).

### 3a. The mirror case: a new instance with no lineage (S12-8)

**What happens today.** A seat's one local durable filters its own instance
subject and, when it holds a role, the role subject (bus.go:206-216,
`selfSubjects` :244-250). A brand-new id has no departed durable of its own,
so the durable starts with `DeliverAllPolicy` over both subjects and replays
every role message the stream still holds, up to 72h of mail shared by every
holder of that role. aae-orc#461 S12-8 saw it on a new supervisor in the
corporate cluster. Director's judgment (not a ruling): that replay is not
intended. It is the mirror of D1, which is about reading too little.

**The two subjects get different starts.**

| Subject | Start | Why |
|---|---|---|
| The seat's own instance subject | all the stream holds (unchanged) | every message on it is addressed to this id, including any sent with an explicit workspace before it connected, so none is history to skip |
| The role subject | one past the highest ack floor of any seat durable on the stream, any id, live or departed, whose filter set contains the role subject (section 3, the role scope); all the stream holds when no such durable exists | mail at or below that floor was delivered to and acked by some holder of the role. Mail past it may be unread by every holder, so the new seat still reads it. That is "start at the tail of what the role has already handled", one step safer than the raw stream tail, which would drop role mail sent while no holder was reading |

A durable has one start, so a seat holding a role gets two local durables,
`mcp_<self>_<instance>` (own subject) and `mcp_<self>_<instance>-role` (role
subject), read as one source by `wait_for_message` and `inbox_summary`. Both
floors come from the containment rule in section 3, so a combined durable left
by today's shim still counts for each subject it filtered. The global tier's
role subject follows the same rule on its own durable.

Role mail can wait for a holder only if it was sent with an explicit
workspace: a role send with no live holder and no workspace hint is refused at
send time (bus.go:596-597). So the "all the stream holds" start for a first
holder reads only mail that a sender addressed to that role's mailbox on
purpose.

**Telling "new, no lineage" from "successor not yet detected".** The shim
decides by its input, never by timing. `DIRECTOR_PREDECESSOR` unset means no
lineage: the start rule above applies at connect, and nothing waits on a
presence row. Set means a successor: D1 arms, and the three-read trigger
applies only then. A presence row is never read as evidence of lineage, so a new seat that
happens to connect near another seat's death inherits nothing from it.

**What this does not drop.** A successor launched without lineage (a crash
repair under a new index, before U5) is treated as new. Its own subject is
read in full, since nothing on a new id is history. Role mail its crashed
predecessor never acked is past every holder's floor, so it is still read.
What it misses is the crashed id's own instance mail, which is U5's case.

## 4. D2: a catching-up presence (closes P3)

At connect the shim writes presence state `catching-up`, not `idle`, and keeps
writing it on its heartbeat until both hold:

1. every source (local, inherited, global) has reported zero waiting once,
   and an armed inheritance (section 3) has started and drained, and
2. the seat has called `wait_for_message` at least once since connect.

Then it writes `idle` and accepts the seat's own `set_presence` from then on.
Until then, a `set_presence` call from the seat is recorded but presence stays
`catching-up`, and the tool result says why and how many are waiting.

- **Sends still resolve.** resolveSubject reads any presence row, so mail to
  a catching-up seat is queued as today. Nothing is refused.
- **Ruled (B2-R2):** director counts it as not live for routing and for
  "reports live" until its catch-up is complete (R-143, R-178). The roster shows
  `catching-up (N waiting)` either way.
- **Compatibility.** An older director reading the new state sees one more
  free-text value and counts the row as live, which is today's behavior. No
  reader breaks.
- **Stuck with mail.** A seat that never drains stays `catching-up`. That is
  the correct report: it is the seat R-117's doorbell is for.
- **Stuck with nothing waiting.** A seat that connects with zero waiting and
  never calls `wait_for_message` also stays `catching-up`, and no doorbell
  fires, because nothing is unread. The presence row carries its connect time,
  and the director sweep lists any seat `catching-up` for more than 10 minutes
  with zero waiting as "connected, never polled" in its Uncaptured block. This
  is the deaf-seat class (#66); the sweep surfaces it, it does not fix it.

## 5. D3: an expiry notice before the drop (closes P1)

Retention cannot tell a sender about a message it has already deleted, so the
notice comes before the drop. The reader that already exists does the work:
`director-mcp unread --threshold` (#126, `unread-slice-m.md`) knows each
address's oldest unread age from consumer ack floors.

- **Who runs it.** The director seat's sweep runs it on its usual cadence with
  a threshold of `max_age` minus 24h (48h on today's 72h). This reminds and
  proposes; it decides nothing (ADR-007).
- **What it sends.** For each message past the threshold: to the sender, one
  INFORM `unread at 50h, expires at 72h, to <address>`; to the
  recipient, one doorbell naming the count. Each notice once per message id,
  recorded in `$DIRECTOR_STATE` so a sweep does not repeat it.
- **What it does not do.** It does not raise `max_age`, re-send, or move mail.
  Raising the age is the operator's call (ruling B2-R3); it delays the drop and
  closes nothing.

## 6. Parts, in order

| Part | What | Depends on | PR |
|---|---|---|---|
| U1 | `DIRECTOR_PREDECESSOR` input, the inherited durable, `inherited_from`, its end rules; `cast-launch.sh` mapping; the split role durable and its start rule (section 3a), local and global; `subjectFloor` by containment (section 3) | none | 1 |
| U2 | `catching-up` presence and its exit rule; the roster's display and "not live" count | none | 2 |
| U3 | sweep step: expiry notices from `unread --threshold`, once per id | #126 slice M on main | 3 |
| U5 | marvel: set `MARVEL_PREDECESSOR` on a repair spawn that replaces a crashed row under a new index (marvel#541, not a director PR) | none | marvel |
| U4 | fix the stale 24h in `verify-auth.sh:87`, `PROGRESS.md:11-12`, `probe/nats-global-tier/verify-global-tls.sh:247-250` and `verify-global-shim.sh:93-96`, and correct #222's title | none | 3 |

Three PRs, not one: U1 and U2 touch different code paths and each can be
reverted alone; U3 and U4 are sweep text and scripts. U2 is the smallest and
most visible; U1 closes the loss.

## 7. Tests (red first on faed978)

1. U1: a predecessor id with 3 unread past its floor; the successor, started
   with `DIRECTOR_PREDECESSOR`, receives exactly those 3, each with
   `inherited_from`, and none of the predecessor's already-acked mail.
2. U1: unset `DIRECTOR_PREDECESSOR` creates no inherited durable (today's
   behavior, a guard).
3. U1, the shift order: the successor connects while the predecessor's
   presence row is live; no inherited durable exists and nothing is read. The
   predecessor acks 2 more and exits; the inherited durable starts on the third
   successful empty read after its row expires (fake clock), at the new floor,
   and delivers only what is past it.
3b. U1, unknown reads: with the KV read failing on every tick (stubbed), no
   inherited durable is ever created; two empty reads, one failure and one
   empty read do not trigger; three empty reads in a row do.
3c. U1, in-place restart: the predecessor's shim restarts (old instance row
   expires, new instance row written 10 seconds later); the empty-read count
   reaches at most 1 and resets, and nothing is inherited.
3d. U1, the rollout case: a role-holding predecessor on today's shim has one
   combined [instance, role] durable acked through sequence 120; the
   successor's instance-only inherited durable starts at 121, not 1.
3e. U1, the first restart onto split durables: a seat whose departed durable
   is a combined [instance, role] acked through 200 restarts on the new shim;
   both its own durable and its `-role` durable start at 201, and nothing at
   or below 200 is delivered again.
3a. U1: a predecessor row that reappears after inheritance started stops the
   inherited durable at once, and the resume note says why.
4. U1: the inherited durable is deleted after it drains with the predecessor
   still absent.
4a. U1 (section 3a): a new id with no departed durable and no
   `DIRECTOR_PREDECESSOR` connects holding role `supervisor`. The stream holds
   40 role messages, of which a live holder has acked through the 37th, and 2
   messages on the new id's own subject. The live holder's durable is
   `mcp_sup-a_<ULID>` and the new id is `sup-b`, so no durable shares the new
   id's prefix. The seat receives exactly the 2 own messages and role messages
   38 to 40, not 1 to 37. Repeated with the holder departed and its durable
   still stored: the same result.
4d. U1, the parser: with ids `sup` and `sup_T1` both holding durables
   (`mcp_sup_<ULID>`, `mcp_sup_T1_<ULID>`, `mcp_sup_T1_<ULID>-role`), the
   own-subject floor for `sup` reads only `mcp_sup_<ULID>`, and the one for
   `sup_T1` reads only its own two. A name whose text after the prefix is not
   a 26-character ULID plus an optional known suffix is skipped.
4f. U1, the right-anchored parse in the role scope (no prefix): on the hub
   stream, `mcp_global_sup_T1_<ULID>-role` parses as id `sup_T1`, instance
   `<ULID>`, suffix `-role`; a left split on the first `_` after the head
   would give id `sup` and fail. On the local stream, `mcp_sup_T1_<ULID>`
   parses as id `sup_T1`, and `mcp_global_x_<ULID>` parses as local id
   `global_x`, never as a global durable. `mcp_sup_<25 chars>` and
   `mcp_sup_<ULID>-other` are skipped.
4b. U1: the same with no durable that ever filtered the role subject: all 40
   role messages are delivered (first holder).
4e. U1, a concurrent peek: the 4a stream, with the live holder acked through
   37 and its `inbox_summary` peek (`peek_<instance>`, `AckNone`, started at 38)
   having delivered 38 to 40 when the new seat's role durable is created. The
   new seat still receives role messages 38 to 40. A consumer named like a
   seat durable but with `AckNone`, or with a filter `agent.>` only, is also
   skipped.
4c. U1: the same seat started with `DIRECTOR_PREDECESSOR` set takes D1's
   path instead (test 3), and a departed sibling's presence row expiring
   during its first three ticks changes nothing for a seat without the
   variable.
5. U2: right after connect, presence reads `catching-up`; with 2 waiting, a
   seat `set_presence idle` leaves it `catching-up` and the result names 2.
6. U2: after a drain to zero and one `wait_for_message`, presence reads `idle`
   and later `set_presence` calls apply.
7. U2: a send to a `catching-up` seat resolves and is stored (no refusal).
7a. U2: with an armed inheritance whose predecessor is still live, presence
   stays `catching-up` after the seat drains its own inbox, and turns `idle`
   only after the inherited mail drains.
7b. Sweep: a seat `catching-up` for 11 minutes with zero waiting and no
   `wait_for_message` is listed once as "connected, never polled".
7c. U5 (marvel): a multi-replica role whose middle replica crashes respawns
   under a new index with `MARVEL_PREDECESSOR` naming the crashed row; a
   single-replica repair carries none (same key).
8. U3: a message unread past the threshold produces one notice to its sender
   and one doorbell to its recipient; a second sweep produces none.

## 8. Rehearsal and rollback

This changes the shim every seat runs, so it is a change to a live system.

- **Rehearsal.** Each part's tests run against a throwaway `nats-server` in a
  temp dir, as `verify-auth.sh` already does. Before install, the
  shim-release flow (`shim-release-and-pinned-install.md`) pins the new build
  to one seat first; that seat's presence, inherited reads and roster line are
  checked by hand before the fleet takes it.
- **Rollback.** Reinstall the previous pinned shim. U1's inherited durable is
  one extra consumer: deleting it loses nothing, since the stream keeps the
  messages. U2's state is a free-text value that older shims never write.
  U3's notices are messages; stopping the sweep step stops them.

## 9. Rulings (operator, 2026-10-04, relayed by director)

Verbatim:

> "R1 predecessor lineage comes from the launcher's environment, R2 it's not
> live until it's considered to have taken over for it's predecisor, the
> handoff is compelete; We don't have the details on that exactly, for now
> it's when the catch up is complete, but we should do more research on how
> this handoff/shiftchange should be completed, possibly put elegable
> originals into a secondary guarentee/support role ready for questions for
> some period of time (if they are being rotated out for a non-critical or
> non-fault reason) B2-R3 yes, definitely 72h and fix the stale 24h
> referneces"

What it settles in this design:

- **B2-R1:** the lineage is `DIRECTOR_PREDECESSOR`, set by the launcher from
  marvel's `MARVEL_PREDECESSOR`, or by hand. The presence-gone trigger
  (section 3) is timing only, never a lineage source. U5, marvel setting
  `MARVEL_PREDECESSOR` on a crash repair, is now load-bearing, tracked as
  marvel#541.
- **B2-R2:** a seat is not live until it has taken over for its predecessor.
  For now that means its catch-up is complete, which is section 4's exit rule.
  How a handoff or shift change should finish, including keeping an eligible
  predecessor on as a support seat for questions after a non-fault rotation,
  is a research item for director's graph, filed as a kos idea by its writer.
  This design cites it once it exists and does not design it.
- **B2-R3:** 72h stays, and the stale 24h references are fixed (U4).
