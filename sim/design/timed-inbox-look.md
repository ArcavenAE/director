# A timed look at director's inbox: read and surface, never act

Status: design for review, 2026-10-19. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Commission: the operator's queue, item B4: "a timed inbox drain for
  director. Today director drains only when the operator speaks." The
  constraint, as given: a timed drain reads and surfaces, it never acts on
  what it reads, and it does not violate "nothing executes on silence or a
  clock".
- Related: #246 and `ask-ledger.md` (one row per ask), #126 and
  `unread-slice-m.md` (the read-only unread reader), #166 and
  `channel-cue.md` (the opt-in cue that starts a turn), `asks-die-unread.md`
  (#220, #222).
- Checked against director `origin/main` 3f54b56.
- Panel: five seats (operator, bus, harness, boundary, sre), two rounds,
  every item voted 5-0. The party record is kept outside this repo.

## 1. Why

Mail to director waits until the operator types. A verdict, a GATE or a
blocked ask can sit for hours, and nothing tells the operator it is there. A
look on a timer fixes the waiting. The danger is that a timer which reads
mail turns into a timer that acts on mail, and the rule here is that nothing
executes on silence or a clock. This design keeps the look and removes every
way it could act.

## 2. The premises, verified

| premise | where | result |
|---|---|---|
| `inbox_summary` reads without consuming | `cmd/director-mcp/tools.go:352` (`toolSummary`) | MEASURED: its result says "nothing was acked; drain in order with wait_for_message max=N" |
| `wait_for_message` consumes | `bus.go:1051`, `global.go:742` (`m.Ack()`) | MEASURED: it acks, and the durable's ack floor moves. There is no un-ack |
| A full peek writes server state | `drain.go` (`peekWaiting`) | MEASURED: it creates and deletes an ephemeral `AckNone` consumer. That touches no seat state, but it is a write, and it would need consumer-create rights |
| The unread reader writes nothing | `unread.go` header | MEASURED: "it creates no consumer, acks nothing and writes no presence row". It reads durable info (pending, ack floor) and stored timestamps |
| The cue starts a turn that then drains | `cue.go:3-8` | MEASURED: the cue notice carries sender, performative and id, never the body, and the turn then calls `wait_for_message`, which consumes |
| Director's seat runs without the cue | the director-mcp server config the operator's director session loads | MEASURED, key names only: there is no `DIRECTOR_CUE` key. INFERRED: the cue is off for director |
| A per-user read-only grant needs the broker's authorization block | aae-orc-umw8p | that block is not live yet, so a read-only credential cannot be enforced before it lands |
| Director drains only when the operator speaks | the operator's queue | as relayed, not re-measured |

## 3. The design

### 3.1 A look, not a drain (vote D1, 5-0)

The timer may look and may not consume. Consuming on a clock removes mail
the operator has not seen and moves the ack floor for good, so it changes
what the operator can decide. A look leaves the queue exactly as the
operator would find it. A late look loses nothing; the mail is still there
at the next one.

Draining stays an act the operator starts.

### 3.2 Where it runs (votes D2, 5-0, and E1, 5-0)

The floor is an OS job outside any session, and it runs no model.

- **A launchd user agent** runs `director-mcp look` every 15 minutes (vote
  E5, 5-0), using `StartCalendarInterval`.
  - After sleep, launchd runs one coalesced catch-up and does not replay the
    missed ticks (INFERRED). So the digest never claims a window the host
    slept through: it says "last look at T".
- **No model turn in the first version.** A session timer (a /loop wakeup or
  a session cron) dies with its session, which is exactly when the look
  matters. A cue-started turn holds the seat's full tool set unless the
  harness scopes it, and no allowlist has a version-pinned test yet. The cue
  stays off for director.
  - A later version may add a cue turn on a peek-only tool allowlist, once
    that allowlist has a test that pins the harness version.

### 3.3 What it reads

`look` is a mode of the existing unread reader, pointed at director's own
durables on the local and global tiers:
- `CONSUMER.INFO` for pending, ack-pending and the ack floor;
- one `STREAM.MSG.GET` by sequence for each message above the floor. That
  gives the performative, the sender, the message id, the stored time and,
  for a REQUEST, its sell-by.

It never reads a body into anything a model sees. It never calls
`peekWaiting`, and it never creates a consumer.

### 3.4 What it surfaces (votes D3 and E3, 5-0)

**The digest** is one file under the user's state directory, mode 0600,
overwritten each run. Each run writes it in full:

```
as of 2026-10-19T12:15:00Z (last look; host slept 11:40-12:10)
waiting: local 3, global 1; oldest 2h14m
GATE      01M59...  from agent://<team>/<seat>  age 2h14m
REQUEST   01M59...  from agent://<team>/<seat>  age 47m  sell-by 2026-10-20 (not passed)
other     2
reader: ok
```

- Lines carry the performative, the id, the sender, the age and the sell-by.
  They never carry a body.
- **A failure is a line, never a blank.** If the reader fails or the broker
  is down, the digest says so, so an empty digest can never be read as an
  empty inbox.
- A REQUEST past its sell-by is labelled "past sell-by, re-check". That is a
  label on a recommendation, never a trigger for an action.

**The notification** is a macOS user notification posted by the job. It
carries the class, the count and the oldest age: no body, and no command to
run. It posts only when the urgent set changes, that is a new GATE or a
REQUEST newly past its sell-by. It never posts on a quiet run. A ping on a
fixed cadence is what trains a person to ignore it.

**The reminder** (vote E4, 5-0): a GATE that stays unread notifies again
every N hours.
- N is the operator's: card 1. The panel's suggestion is 4 h, never under
  1 h, with a cap of 3 repeats. After that the item stays in the digest and
  the notification stops.
- The reminder state is a small file the job owns, keyed by stream sequence,
  so a restart does not notify again.
- A repeat re-displays an existing item. It is a reminder, which the
  automation boundary allows, and it never escalates to a send, an ack or a
  merge.
- Quiet hours hold every notification except a GATE: card 2.

**Liveness.** Each run writes a heartbeat file with the time and the counts,
even when the counts are zero. A second check flags a heartbeat older than
two intervals (30 minutes), so a stuck job fails loud.

### 3.5 Never acting, by mechanism (vote E2, 5-0)

A prompt line saying "do not act" is a request, not a control. The look
cannot act because of three facts about it:
1. **It runs no model.** Nothing in it reasons about what it reads.
2. **Its code has no write path.** It has no publish call, no ack and no
   consumer create. A build test fails if one appears.
3. **Its credential cannot write.** It runs as a dedicated read-only NATS
   user: consumer and stream INFO plus MSG.GET on director's inbox streams,
   local and global, with no publish, ack or consumer-create subject.
   - This needs the broker's authorization block (aae-orc-umw8p), and the
     grant is the operator's: card 3.
   - **Until then, stated plainly:** no broker grant enforces it, so
     read-only rests on facts 1 and 2 alone.

### 3.6 A view, not a fourth reader

The look, the ask ledger (#246) and a consumer-side silence check are one
reader rendered three ways: ledger rows, a watchdog alert, and the
operator's digest. Three pollers on one consumer would disagree about the
count, so they share the unread reader and its field names.

## 4. Tests (red first)

1. **No write, by state.** Run `look` against a broker seeded with a GATE
   whose body says "merge this". Afterwards:
   - the durable's ack floor, its ack-pending count and the consumer list are
     unchanged;
   - nothing was published.
2. **No write, by code.** A source check fails if `look`'s package reaches a
   publish, an `Ack`, a consumer create or `peekWaiting`.
3. **No write, by sandbox.** `look` runs under a `sandbox-exec` profile that
   allows only the broker connection and writes to its own state directory.
   - A write elsewhere is refused, and the profile's refusal text goes in the
     report.
   - A write to the state directory succeeds.
4. **No body leaves.** A canary placed in a message body appears in neither
   the digest, the notification text nor the logs.
5. **A failure is a line.** With the broker down, the digest has a reader
   line naming the failure, and the counts are not shown as zero.
6. **Notify on change only.** Two runs with the same mail post one
   notification. A quiet run posts none.
7. **Reminder.** With N set to 1 h and a fake clock, an unread GATE notifies
   at 0 h, 1 h and 2 h, and then not again after the cap. A restart between
   runs does not notify again.
8. **Sleep.** A coalesced run after a simulated sleep says "last look at T"
   and claims no window it did not see.
9. **Heartbeat.** With the job stopped, the age check flags it after two
   intervals.
10. **On the host, before the floor is called done.** Check that a launchd
    user agent runs, and the notification posts, while the screen is locked
    and while the lid is closed. If it does not, the digest's "as of" line
    says so. This is not measured yet.
11. **After umw8p.** With the read-only user, a publish, an ack and a
    consumer create are each refused by the broker. A positive control shows
    the user can read INFO.

## 5. Candidate requirements (provisional)

- **R-TBD-1 (JUDGMENT, provisional).** A timed look at a seat's inbox
  consumes nothing. Draining stays an act the inbox's owner starts.
- **R-TBD-2 (JUDGMENT, provisional).** A timed look runs no model. What it
  surfaces carries ids, classes and ages, never message bodies.
- **R-TBD-3 (OBSERVED, provisional).** A look that cannot read says so in
  its output. An unreadable inbox never renders as an empty one.

## 6. Decisions for the operator

Each goes on its own card, valid until 2026-10-30, and the architect role
re-checks it then:
1. The GATE reminder interval N and its cap. Suggested: 4 h, never under
   1 h, cap 3.
2. Quiet hours: whether there is a window, and when it is. GATE passes
   through either way.
3. Granting the read-only NATS user once the broker's authorization block
   is live.

## 7. Not covered

- A cue-started model turn (deferred; section 3.2).
- Notification delivery when the host is asleep (test 10 decides how the
  digest says so).
- Mail addressed to seats other than director. The same reader serves them
  through the ask ledger, which has its own design.
