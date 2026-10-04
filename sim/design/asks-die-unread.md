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
- Checked against director `origin/main` faed978 and the live kinu broker on
  2026-10-04 at about 03:40Z.

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
| A lineage is already supplied on shifts | marvel `CLAUDE.md:215`, `docs/design/shift-trigger-list.md:169`; this seat's env | marvel sets `MARVEL_PREDECESSOR` (`<workspace>/<old seat key>`) on every shift successor, as it did for the architect seat that wrote this. The shim does not read it |
| How much mail is orphaned now | `nats stream subjects AGENT_INBOX`, `kv ls AGENT_STATE`, `consumer info` on each departed id's newest durable | 1306 of 3625 stored messages sit on 16 subjects (14 agent ids) with no live presence. Past each id's newest ack floor, **47 are unread**, spread over 7 ids (two builder replicas hold 20 and 21). `num_pending` on a durable that also filters its role subject counts role mail too, so 47 is an upper bound for instance mail |

## 2a. Relation to brief 11 (the leaf fabric)

`leaf-fabric-one-address-space.md` (brief 11, amendments ruled 2026-09-24,
nothing built) already rules the long-term answer to P1: FAB-C's per-cluster
sweeper sends the sender an INFORM at 7 and 21 days unread, under a 28-day
`max_age` backstop with `discard: new` (section 2.5). It arrives with a
flag-day cutover off `AGENT_INBOX`. This design works on today's stream until
then, and is shaped to converge:

- **D3 is FAB-C on today's stream**, with ages scaled to the 72h limit. When
  FAB-C ships, its sweeper replaces U3 and U3 is deleted.
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

**The read.** At connect, when `DIRECTOR_PREDECESSOR` is set and that id has
no live presence row, the shim creates one more durable,
`mcp_<self>_inherit_<instance>`, filtering only the predecessor's instance
subject `agent.<ws>.<team>.<predecessor>.inbox`. It starts at the
predecessor's ack floor plus one, computed by the existing `seatAckFloor`
with the predecessor's prefix. If the predecessor has no durable left, it
starts at the beginning of what the stream holds, and the resume note says so,
as the existing notes do (bus.go:268-276).

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
at send time today when no workspace is given (bus.go:527-558), so D1 reads
what was waiting at death plus what was sent with an explicit workspace. It
does not redirect new mail. That stays with senders addressing `role://`,
which already survives a generation (section 2).

## 4. D2: a catching-up presence (closes P3)

At connect the shim writes presence state `catching-up`, not `idle`, and keeps
writing it on its heartbeat until both hold:

1. every source (local, inherited, global) has reported zero waiting once, and
2. the seat has called `wait_for_message` at least once since connect.

Then it writes `idle` and accepts the seat's own `set_presence` from then on.
Until then, a `set_presence` call from the seat is recorded but presence stays
`catching-up`, and the tool result says why and how many are waiting.

- **Sends still resolve.** resolveSubject reads any presence row, so mail to
  a catching-up seat is queued as today. Nothing is refused.
- **Director counts it as not live** for routing and for "reports live"
  (R-143, R-178). The roster shows `catching-up (N waiting)`.
- **Compatibility.** An older director reading the new state sees one more
  free-text value and counts the row as live, which is today's behavior. No
  reader breaks.
- **Stuck.** A seat that never drains stays `catching-up`. That is the
  correct report, not a bug: it is the seat R-117's doorbell is for.

## 5. D3: an expiry notice before the drop (closes P1)

Retention cannot tell a sender about a message it has already deleted, so the
notice comes before the drop. The reader that already exists does the work:
`director-mcp unread --threshold` (#126, `unread-slice-m.md`) knows each
address's oldest unread age from consumer ack floors.

- **Who runs it.** The director seat's sweep runs it on its usual cadence with
  a threshold of `max_age` minus 24h (48h on today's 72h). This reminds and
  proposes; it decides nothing (ADR-007).
- **What it sends.** For each message past the threshold: to the sender, one
  FAILURE-free INFORM `unread at 50h, expires at 72h, to <address>`; to the
  recipient, one doorbell naming the count. Each notice once per message id,
  recorded in `$DIRECTOR_STATE` so a sweep does not repeat it.
- **What it does not do.** It does not raise `max_age`, re-send, or move mail.
  Raising the age is the operator's call (ruling B2-R3); it delays the drop and
  closes nothing.

## 6. Parts, in order

| Part | What | Depends on | PR |
|---|---|---|---|
| U1 | `DIRECTOR_PREDECESSOR` input, the inherited durable, `inherited_from`, its end rules; `cast-launch.sh` mapping | none | 1 |
| U2 | `catching-up` presence and its exit rule; the roster's display and "not live" count | none | 2 |
| U3 | sweep step: expiry notices from `unread --threshold`, once per id | #126 slice M on main | 3 |
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
3. U1: a predecessor with a live presence row is not inherited; the resume
   note says why.
4. U1: the inherited durable is deleted after it drains with the predecessor
   still absent.
5. U2: right after connect, presence reads `catching-up`; with 2 waiting, a
   seat `set_presence idle` leaves it `catching-up` and the result names 2.
6. U2: after a drain to zero and one `wait_for_message`, presence reads `idle`
   and later `set_presence` calls apply.
7. U2: a send to a `catching-up` seat resolves and is stored (no refusal).
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

## 9. Rulings needed (operator, via director)

- **B2-R1, the lineage source.** Default: `DIRECTOR_PREDECESSOR`, set by the
  launcher from marvel's `MARVEL_PREDECESSOR`, or by hand. Alternative: the
  shim finds the predecessor itself from the newest departed presence row with
  the same team and role. The alternative needs no launcher change but picks
  wrongly when two replicas of a role die together.
- **B2-R2, does catching-up hold routing?** Default: director does not count a
  catching-up seat as live, and does not route new asks to it, while mail to it
  still queues. Alternative: display only.
- **B2-R3, max age.** Default: keep 72h and fix the stale 24h text (U4)
  until brief 11's cutover brings its ruled 28-day backstop. Alternative:
  raise today's stream to 28 days now; that delays the drop, closes nothing on
  its own, and grows the replay a respawned seat reads (finding-015).

Expiry: these defaults hold until U1's build starts; with no ruling by then,
the builder builds the defaults.
