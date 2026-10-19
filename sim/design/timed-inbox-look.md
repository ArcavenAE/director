# A timed look at director's inbox: read and surface, never act

Status: design for review, 2026-10-19. Owner: the architect role. Docs only;
no code lands until this is reviewed. The three operator decisions in
section 6 were ruled on 2026-10-19; section 6 records them, and section 3.4
carries the rulings and the operator's added request (3.4.1).

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
| The unread reader's durable path is non-consuming | `unread.go:344` (`ListConsumers`), `:391` and `:447` (`GetMsg` with `WithGetMsgSubject`) | READ: consumer info plus subject-filtered message gets, no ack. Its header says it "creates no consumer" |
| The unread reader's presence path creates a consumer | `unread.go:303` (`KeyValue`), then `kv.Keys`; nats.go v1.53.1 `jetstream/kv.go:1404` (`Keys` calls `WatchAll`) and `:1334` (`nats.OrderedConsumer()`) | READ in the client source: listing KV keys subscribes through an ordered consumer, so it creates one. The look therefore does not read presence |
| `GetMsg` sends a direct get on these streams | nats.go v1.53.1 `jetstream/stream.go:577-591`; the inbox streams' config | READ: with `AllowDirect` set, `GetMsg` requests `$JS.API.DIRECT.GET.<stream>`. MEASURED 2026-10-19 from the brokers' monitoring endpoints: `allow_direct` is true on `AGENT_INBOX` and on every hub `GLOBAL_TO_*` stream |
| GATE is not a performative | `envelope.go:59-63`; R-148 | READ: the envelope accepts 12 FIPA verbs, and GATE is not one. A GATE is a text convention: a body line, carrying a stage (draft or ready), per R-148 |
| launchd coalesces missed calendar runs | `launchd.plist(5)`, StartCalendarInterval | READ: "If multiple intervals transpire before the computer is woken, those events will be coalesced into one event upon wake from sleep." |
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
    missed ticks (`launchd.plist(5)`, quoted in section 2). So the digest
    never claims a window the host slept through: it says "last look at T".
- **No model turn in the first version.** A session timer (a /loop wakeup or
  a session cron) dies with its session, which is exactly when the look
  matters. A cue-started turn holds the seat's full tool set unless the
  harness scopes it, and no allowlist has a version-pinned test yet. The cue
  stays off for director.
  - A later version may add a cue turn on a peek-only tool allowlist, once
    that allowlist has a test that pins the harness version.

### 3.3 What it reads

`look` reuses the unread reader's subject-filtered walk (`pendingIDs`,
`unread.go:447`), not the whole reader. It skips presence, because listing KV
keys creates a consumer (section 2). It does not reuse the reader's lister
either: `readDurables` lists with `ListConsumers` (`unread.go:344`), which
sends `CONSUMER.LIST` and returns every seat's full consumer info. The look
sends `CONSUMER.NAMES` instead and asks `CONSUMER.INFO` only for the durables
it keeps, so it sees other seats' durable names and nothing else of them.

**Which durables.** Director runs one seat id, and each session makes its own
durables: `mcp_<seat>_<instance>` on the local `AGENT_INBOX`, and
`mcp_global_<seat>_<instance>` on the hub's `GLOBAL_TO_DIRECTOR`. The look
lists consumer names on each stream and keeps the ones with director's prefix.
The one with the latest `Created` is the current session, and its unread set
is the one shown. Older instances get one line each, carrying the name,
`Created` and the pending count, and they are not counted twice: the digest
dedupes by message id.

**What it reads, per kept durable:**
- `CONSUMER.INFO` for pending, ack-pending, the ack floor and the filter
  subjects;
- for each filter subject, direct gets with `next_by_subj` set to that
  subject, starting after the ack floor, stopping at the pending count or
  the scan cap. This is the same walk as `pendingIDs` (`unread.go:447`), and
  it never steps by bare sequence, so it reads only mail addressed to
  director's subjects.

From each message it keeps the envelope's performative, sender, message id,
stored time and `reply_by`, plus one classification of the body (section
3.4). It never writes a body anywhere, and nothing it reads reaches a model.

**Exposure, stated.** The subject filter is in the look's code, not in any
grant. `AGENT_INBOX` and the hub streams are shared by every seat, and a
direct get on a stream cannot be confined to one subject by a broker
permission: the subject travels in the request payload. So the read-only
user of section 3.5 can, by its grant, read any seat's mail on those streams.
The look does not, and test 2 checks that it does not. Narrowing the grant
would need a stream per seat, which this design does not propose. A consumer
name listing also shows every seat's durable names, which carry seat ids and
instance ids, never bodies.

### 3.4 What it surfaces (votes D3 and E3, 5-0)

**What is urgent.** The look classifies each unread message into one of three
classes:
- **gate:** the body's text has a line starting with `GATE`, the R-148 form.
  The performative does not matter, because GATE is not one. The stage word
  (draft or ready) is kept with the line.
- **request:** the performative is `REQUEST`, and the message is not a gate.
  Its sell-by is the envelope's `reply_by` when one is set. A request with
  no `reply_by` has no sell-by and is never urgent.
- **other:** everything else, shown only as a count.

The urgent set is every gate not cleared (3.4.1), plus every request whose
`reply_by` has passed.
The body is read only to find that `GATE` line; nothing else of it is kept.

**The digest** is one file under the user's state directory, mode 0600,
overwritten each run. Each run writes it in full:

```
as of 2026-10-19T12:15:00Z (last look; host slept 11:40-12:10)
waiting: local 3, global 1; oldest 2h14m
GATE      01M59...  from agent://<team>/<seat>  age 2h14m
cleared   01M59...  from agent://<team>/<seat>  age 6h02m  desk <desk-id> at 2026-10-19T10:40:00Z
REQUEST   01M59...  from agent://<team>/<seat>  age 47m  sell-by 2026-10-20 (not passed)
other     2
reader: ok
```

- Lines carry the performative, the id, the sender, the age and the sell-by.
  They never carry a body.
- **A failure is a line, never a blank.** If the reader fails or the broker
  is down, the digest says so, so an empty digest can never be read as an
  empty inbox.
- A REQUEST past its `reply_by` is labelled "past sell-by, re-check". That is
  a label on a recommendation, never a trigger for an action.

**The notification** is a macOS user notification, posted by a separate
step (section 3.6), never by `look` itself. It carries the class, the count
and the oldest age: no body, and no command to run. It posts only when the
urgent set changes, that is a new gate or a request newly past its
`reply_by`. It never posts on a quiet run. A ping on a fixed cadence is what
trains a person to ignore it.

**The reminder** (vote E4, 5-0): a GATE that stays unread notifies again
every N hours.
- N is 4 h with a cap of 3 repeats, ruled by the operator (card 1,
  2026-10-19): 4 notifications in all for one gate, the first one included.
- **The cap, as ruled.** With a cap, a gate
  still unread after the last repeat stays in the digest and never notifies
  again, so it is silenced for good. The reason to cap anyway: a reminder
  that never stops trains a person to dismiss it, and then the next new gate
  is dismissed with it. The cost of no cap is that noise. The cost of a cap
  is one gate that waits silently, as every gate does today.
- The reminder state is a small file the job owns, keyed by tier, stream name
  and stream sequence, since sequences repeat across streams. A restart does
  not notify again.
- A repeat re-displays an existing item. It is a reminder, which the
  automation boundary allows, and it never escalates to a send, an ack or a
  merge.
- **Quiet hours and the cap.** Quiet hours never hold a gate, so they never
  touch a gate's reminder count: a gate notifies on its own schedule inside
  the window and counts toward its cap as usual. The cap applies only to
  gates. The one summary at the end of a quiet window is not a gate
  reminder, counts toward no cap, and posts at most once per window.
- **Quiet hours** (card 2, ruled 2026-10-19: a window) hold every
  notification except a gate. The window's times are on their own card,
  derived from the operator's least active hours on the Desk, as the
  operator asked. Until that card is ruled the job runs with no window. A held
  notification is not posted, and it is not queued one by one. At the end of
  the window, if the urgent set is still not empty, one notification posts
  with the counts as they stand then. Gate reminders are gates, so they
  pass through.

**Liveness.** Each run writes a heartbeat file with the time and the counts,
even when the counts are zero. A second check flags a heartbeat older than
two intervals (30 minutes), so a stuck job fails loud.

#### 3.4.1 A gate as a Desk item, so it can be cleared

The operator, ruling card 1: "it can also go to a deck item or a new class
of deck items so that we can clear it." Today a gate the cap has silenced
waits in the digest with no way to say "seen, leave it", and a gate the
operator has already handled keeps notifying until the cap.

- **Who writes the item.** Director, never the job. The job cannot reach the
  Desk: artifact data is reachable from a session, not from a script
  (director finding-director-glst). On its sweep, director reads
  `notify.json` and writes one Desk item for each gate that has none yet,
  under the existing rule that every operator decision goes to the Desk on
  first sight.
- **A new kind.** The item's `kind` is `gate`, beside `choice` and `action`.
  It carries the gate's id, sender, tier, stream and sequence, its age, and
  one answer, **clear**. It carries no body: the Desk item points at the
  message, as the digest does.
- **What clearing does.** Clearing tells the job to stop reminding. It is
  not an ack: the bus message stays unread, and the gate's own decision is
  still whatever the operator writes on its decision card. Director reads
  the answer on its next sweep and hands the clear answers it read to a
  script, `director-mcp look-clear` (3.4.2), which validates them and
  writes `cleared.json`. The session reads; the script writes the file,
  as with `desk-snapshot write`.
  The poster skips a cleared key, and the digest shows it under a "cleared"
  line instead of the urgent set.
- **The same pattern as the Desk snapshot.** A session reads the Desk and
  hands what it read to a script that writes a local file (director
  `desk-snapshot`, K13). The job only reads `cleared.json` and never writes
  it.
- **Rule: recording a clear writes nothing to the Desk.** The only party
  that can write the Desk is the session, and what a session does there is
  honor-only, as for every Desk answer. So this is a rule, not a test: the
  sweep's clear step reads the Desk and writes no Desk data, and it never
  closes or changes the gate's decision card. What can be tested is the
  script's side (test 7a2): `look-clear` has no Desk access at all, and its
  only effect is `cleared.json`.
- **Who may clear.** Only the operator. Director cannot check this: a Desk
  answer row carries the choice, a note and a time, and no author (read
  2026-10-19), and on main every Desk answer is honor-only ("Direct writes
  made without the tool stay unchecked", director
  `skills/director/reference/functions.md`). So a clear written by anyone
  else who can write the Desk would end that gate's reminders, and director
  would not know. This is stated, not solved. Two things bound it: a clear
  ends reminders only, and it leaves the gate visible (next item). If the
  Desk store later gives an answer's author, director records it and refuses
  a clear from anyone but the operator.
- **A cleared gate stays distinguishable from a passed one.** A passed gate
  was decided and acked: its message is read and it leaves the urgent set. A
  cleared gate's message stays unread, its decision card stays open, its key
  sits in `cleared.json`, and the digest lists it under "cleared" with the
  Desk id and the time. `cleared.json` also records the author as
  `"unverified"`, so no reader takes the clear for the operator's checked
  word. Nothing removes a key or rewrites a gate's reminder state.
- **Before director has swept,** nothing changes: the gate notifies on its
  ruled schedule. Clearing can only end reminders, never start or speed
  them up.

#### 3.4.2 `look-clear`, exactly

```
director-mcp look-clear --state <state> --items FILE --items-reported N
                        --answers FILE --answers-reported N [--limit 1000]
```

- **Input.** The same shape as `desk-snapshot write`. `--items` is what the
  session's Desk read returned for gate items: a JSON object of id to item,
  or a list of items that each carry `id` or `doc_id`. `--answers` is a JSON
  list of answer records, each naming its item in `card` or `card_id` (both
  only if they agree), with `choice`, `ready` and the Desk's `updatedAt`.
  Each `--*-reported` is the count the Desk read itself reported. The file
  must hold exactly that many, and a count at `--limit` may be truncated,
  so it is refused. The script also reads `<state>/notify.json` and
  `<state>/cleared.json`.
- **What counts as a clear.** An answer whose item has `kind` exactly
  `gate`, whose `choice` is exactly `clear`, and whose `ready` is `true`.
  The item's tier, stream and sequence give the key.
- **Refused whole: exit 1, nothing written.** Anything that makes the input
  itself doubtful: a file that does not parse; a count that differs from
  its `--*-reported`, or sits at `--limit`; two items with the same id or
  the same gate key; two answers for the same item; an `updatedAt` that is
  missing or not a time; `notify.json` or `cleared.json` present and not
  parsing. As with `desk-snapshot`, a doubt about the read is never
  resolved by guessing which half to trust.
- **Skipped, alone: the rest are applied.** A single answer that is
  well-formed but does not apply. Each skip is one stdout line with the
  item id and the reason, never answer text:
  - its item is not in `--items` (`unknown desk id`);
  - its item is not `kind: gate` (`not a gate`);
  - its `choice` is not `clear`, or `ready` is not `true` (`not a clear`);
  - its gate is not open: the key is in neither `notify.json`'s `urgent`
    nor its `cleared`, so the gate was acked or never seen (`gate not
    open`). A clear never applies to a gate that has passed, so a passed
    gate cannot be written as cleared.
- **Against the stored file.** `cleared.json` is only ever added to.
  - A key already present with the same `desk_id`: kept as it is, and the
    stored `at` and `desk_updated_at` do not move.
  - A key already present with a different `desk_id`: the stored entry
    stays, and the answer is skipped (`already cleared by <stored desk
    id>`).
  - An answer whose `updatedAt` is earlier than the stored
    `desk_updated_at` for its key: skipped (`older than stored`). A clear
    cannot be undone by an older read.
- **Exit codes.** 0 when the file was written, or when nothing changed
  (every answer skipped or already stored); 1 when the input was refused
  whole or the state could not be read or written. A skip is never an error
  exit.
- **The write.** A temp file in `<state>`, created mode 0600, written,
  `fsync`ed and then renamed over `cleared.json`, as `desk-snapshot` does.
  A crash leaves the old file or the new one, never half of one.
- **Whoever runs it is a claim.** As with `desk-snapshot`, the script
  checks the input's shape and completeness, not that a session really read
  the Desk, nor who wrote the answer (3.4.1, "Who may clear").

### 3.5 Never acting, by mechanism (vote E2, 5-0)

A prompt line saying "do not act" is a request, not a control. "No publish"
is also the wrong property: every JetStream read is itself a request
published to a `$JS.API` subject. The property is an allowlist instead.

**No allowlisted request advances a consumer.** None of the subjects below
delivers a message through a consumer. `CONSUMER.INFO` and `CONSUMER.NAMES`
read consumer state. `DIRECT.GET` and `STREAM.MSG.GET` read the stream by
sequence or subject, outside any consumer. So a durable's delivered
sequence, ack floor, pending count and redelivery count cannot move because
of the look, and test 1 checks all four. Advancing a consumer needs
`CONSUMER.MSG.NEXT` or an ack, and both are outside the list.

**The allowlist.** The look may send a request to these subjects and no
others, on each tier:

| subject | why |
|---|---|
| `$JS.API.STREAM.INFO.<stream>` | the stream handle (`js.Stream`), which also says whether direct get is on |
| `$JS.API.CONSUMER.NAMES.<stream>` | find director's durables by name prefix |
| `$JS.API.CONSUMER.INFO.<stream>.<durable>` | pending, ack floor and filter subjects, for a kept durable only |
| `$JS.API.DIRECT.GET.<stream>` | the subject-filtered walk of section 3.3 |
| `$JS.API.STREAM.MSG.GET.<stream>` | the same walk, on a stream without direct get |

`<stream>` is `AGENT_INBOX` on the local broker and `GLOBAL_TO_DIRECTOR` on
the hub. On the hub the subjects take the domain form,
`$JS.<domain>.API.<rest>`, for example
`$JS.<domain>.API.DIRECT.GET.GLOBAL_TO_DIRECTOR`, because director-mcp reaches
the hub's JetStream through the leaf link with `jetstream.NewWithDomain`
(`global.go:386`). The local subjects use the plain `$JS.API.` prefix.

The look also subscribes to its own reply inbox, `_INBOX.>` or a named
prefix. Everything else is outside the list, and that includes each of
these, which a denylist could miss: ack, `Nak`, `Term`, in-progress, any
consumer create or delete, `STREAM.MSG.DELETE`, `STREAM.PURGE`, any KV put or
delete, any `CONSUMER.MSG.NEXT`, and any subject not in the table, in either
its local `$JS.API.` form or its hub `$JS.<domain>.API.` form.

**The grant does not confine the read to director's mail.** Both get verbs,
`DIRECT.GET` and `STREAM.MSG.GET`, carry the subject (or the sequence) in
the request payload, not in the request subject. So a user allowed this
table can read any seat's mail on that stream. The look's code keeps to
director's subjects, and test 2 checks it; no broker permission does. See
"Exposure, stated" in section 3.3.

The look is safe to run unattended for these reasons, in order of strength:
1. **It runs no model.** Nothing in it reasons about what it reads.
2. **Its code is checked against the allowlist, by the broker.** Test 2 runs
   `look` as a test-server user whose publish permission is exactly the
   table above. Any request outside it is a permissions violation, and the
   test fails on one. This checks the subjects the code actually sends, and
   does not rely on a list of forbidden calls.
3. **Its credential can send nothing else.** A dedicated read-only user with
   the same allowlist as its publish permission, on each broker.
   - The operator ruled the grant on 2026-10-19 (card 3, section 6). It
     waits only on the broker's authorization block (aae-orc-umw8p). The grant
     is the same table, so test 2 tests the grant too.
   - "Nothing else" is about writes. For reads, this user can read every
     seat's mail on the two shared streams, as section 3.3's "Exposure,
     stated" sets out. The card 3 ruling was made with that stated.

**The interim credential, stated plainly.** Until that user exists, `look`
connects as director-mcp does on that host, with the same connection
settings and the same credential. That credential can do whatever
director-mcp can: publish to any inbox, ack and consume mail, and create and
delete consumers. Nothing at the broker stops a changed `look` from doing any
of that. In the interim, read-only rests on facts 1 and 2: no model, and a
binary that test 2 showed sends only the allowlisted subjects.

### 3.6 The job, its files, and how the notification is posted

**The plist.** `~/Library/LaunchAgents/<label>.director-look.plist`, a user
agent, with:
- `ProgramArguments`: `/bin/sh`, `-c`, and the two steps below joined by `;`,
  so the second runs even when the first fails;
- `StartCalendarInterval`: an array of four dictionaries, `Minute` 0, 15, 30
  and 45;
- `RunAtLoad`: true;
- `StandardOutPath` and `StandardErrorPath`: `look.log` in the state directory.

**Step 1, the look.** `sandbox-exec -f <state>/look.sb director-mcp look
--state <state>`. The profile allows network to the broker and the hub only,
and writes only under `<state>`. `look` has no notification code and posts
nothing: it writes files and exits.

**Step 2, the poster.** `director-mcp look-notify --state <state>`, outside
the sandbox, because posting a notification talks to the user's
notification service, which the step-1 profile refuses. The poster reads
`notify.json`, compares it with `reminders.json`, and posts at most one
notification with `/usr/bin/osascript`. The text is built only from classes,
counts and ages, never from a body or a sender, but it is still passed as an
argument and never written into the script:

```sh
/usr/bin/osascript \
  -e 'on run argv' \
  -e 'display notification (item 1 of argv) with title (item 2 of argv)' \
  -e 'end run' \
  -- "$text" "director look"
```

The script is a fixed string, so nothing a seat writes can become
AppleScript. `osascript(1)` documents the `on run argv` handler, but not `--`
as the end of options; that `--` is safe here is measured, below, not
documented. MEASURED 2026-10-19 on macOS 26.5.2: with a payload of
`x" & (do shell script "touch <scratch file>") & "`, the interpolated form
`osascript -e "return \"$text\""` created the file, and the argv form
returned the payload as literal text and created nothing. The same held for
a payload carrying `"`, `\` and `" & do shell script "..." & "`. The poster opens no broker connection and
gets no credential: its environment is built empty apart from `PATH` and
`HOME`.

**Files,** all under `<state>` = `~/.director/look/`, directory 0700:

| file | written by | format |
|---|---|---|
| `digest.txt` | look | the text shown in 3.4, rewritten in full each run, 0600 |
| `notify.json` | look | `{"as_of", "urgent": [{"tier", "stream", "seq", "class", "id", "sender", "age_s", "stage"}], "cleared": [same fields plus "desk_id", "cleared_at"], "reader": "ok" or the failure}`, 0600. A cleared gate is in `cleared`, never in `urgent` |
| `reminders.json` | poster | `{"<tier>/<stream>/<seq>": {"first", "last", "count"}}`, 0600 |
| `cleared.json` | `look-clear`, from the Desk answers director's sweep read (3.4.1) | `{"<tier>/<stream>/<seq>": {"at", "desk_id", "desk_updated_at", "author": "unverified"}}`, 0600; the job only reads it |
| `heartbeat.json` | look | `{"at", "local": n, "global": n, "reader"}`, written every run, even with zero counts and even when the reader failed, 0600 |
| `look.sb` | installer | the sandbox profile |
| `look.log` | launchd | stdout and stderr of both steps |

**The heartbeat checker.** A third step, `director-mcp look-check --state
<state>`, run by the same plist after the poster. It reads `heartbeat.json`
and writes one line to `look.log`. If the file is older than two intervals
(30 minutes), it posts one notification, "timed look stale since T", and
repeats that no more than once every 4 hours. A stopped job cannot check
itself, so the same check also runs in `director-mcp`'s session start-up,
and shows the stale line to whoever opens director.

### 3.7 A view, not a fourth reader

The look, the ask ledger (#246) and a consumer-side silence check are one
reader rendered three ways: ledger rows, a watchdog alert, and the
operator's digest. Three pollers on one consumer would disagree about the
count, so they share the unread reader and its field names.

## 4. Tests (red first)

1. **No write, by state.** Run `look` against a broker seeded with a GATE
   whose body says "merge this". Afterwards:
   - each durable's delivered sequence, ack floor, ack-pending count,
     `num_pending` and redelivered count, and the consumer list, are
     unchanged;
   - the stream's message count, first and last sequence, and the seeded
     messages themselves, read back by sequence, are unchanged, so nothing
     was deleted or purged;
   - nothing was published.
2. **Only the allowlist, by the broker.** An embedded test server holds two
   streams shaped like `AGENT_INBOX` and `GLOBAL_TO_DIRECTOR`, with mail for
   director and for another seat. `look` connects as a user whose publish
   permission is exactly the table in 3.5.
   - It completes with no permissions violation in the server's log.
   - A planted control, a build of `look` with one ack added, fails with a
     violation, so the test can fail.
   - **Another seat's mail is not read.** The fixture puts a gate, addressed
     to another seat, on the same stream. Its id, its sender and a canary in
     its body are each absent from everything the look writes: the digest,
     `notify.json`, `heartbeat.json`, `reminders.json` and `look.log`. Since
     the look never outputs a body, the id and sender are what make this
     assertion able to fail.
   - **Only allowed `next_by_subj` values.** The test harness subscribes to
     both get verbs' request subjects, `DIRECT.GET.<stream>` and
     `STREAM.MSG.GET.<stream>`, in the local and the domain form, and records
     every request payload. Every recorded request carries `next_by_subj`,
     and every value is one of the filter subjects that `CONSUMER.INFO`
     reported for director's kept durables. A request with no `next_by_subj`
     fails, and so does a run that records zero get requests while director
     has unread mail, so the assertion cannot pass on nothing.
   - **Both stream shapes.** Test 2 runs twice: once with `allow_direct` set
     on the fixture streams, where the look sends `DIRECT.GET`, and once
     without it, where it sends `STREAM.MSG.GET`. Main sets `allow_direct`
     only in the probe scripts, while the live streams have it (MEASURED,
     section 2), so the look must be correct on either.
   - **Mutants, all of which must fail:**
     - a build that widens `next_by_subj` to `agent.>`. It fails both
       assertions above: the other seat's gate appears in the digest, and
       the recorded value is not allowed;
     - a build that walks by bare sequence. It fails the `next_by_subj`
       assertion, and the id assertion too, once the walk reaches the other
       seat's message;
     - a build that walks by bare sequence over `STREAM.MSG.GET` and drops
       other seats' messages in the client, on the stream without direct
       get. Its outputs are clean, so the id assertion passes; the recorded
       requests carry a sequence and no `next_by_subj`, so the
       `next_by_subj` assertion fails it. This is the case the request
       recording exists for.
3. **No write, by sandbox.** Step 1 runs under `look.sb`, which allows only
   the broker connections and writes to `<state>`.
   - A write elsewhere is refused, and the profile's refusal text goes in the
     report.
   - A write to `<state>` succeeds.
   - The poster runs outside it, with an empty environment apart from `PATH`
     and `HOME`. A check shows no NATS credential or connection setting in
     that environment.
4. **No body leaves.** A canary placed in a message body appears in neither
   the digest, the notification text nor the logs.
4a. **No injection through the poster.** The poster is given a `notify.json`
   whose class, sender and stage fields each carry `"`, `\`, and
   `x" & (do shell script "touch <canary path>") & "`. A recording stub
   stands in for `/usr/bin/osascript` and logs its argv.
   - The script arguments are byte-identical to the fixed string above.
   - Every field text appears only after `--`, as a literal argument.
   - Run once against the real `osascript`, with `display notification`
     replaced by `return`: the canary file does not exist afterwards. This
     step needs macOS and runs only on a developer's host; CI skips it and
     says so in the test output. The stub steps run everywhere.
4b. **Only classes, counts and ages reach the poster's argv.** A fixture
   inbox holds a gate and an overdue request whose subject, sender id, body
   and stage word each carry a distinct canary string. `look` then the
   poster run end to end, with the recording stub for `osascript`. No canary
   appears anywhere in the recorded argv, and the notification text matches
   the allowed shape: a class word, a count and an age. The test fails if a
   later change lets any field written by a seat reach the notification.
5. **A failure is a line.** With the broker down, the digest has a reader
   line naming the failure, and the counts are not shown as zero.
6. **Notify on change only.** Two runs with the same mail post one
   notification. A quiet run posts none.
7. **Reminder.** With N set to 1 h, a cap of 3 repeats and a fake clock, an
   unread gate notifies at 0 h, 1 h, 2 h and 3 h, 4 notifications, and then
   never again. With no cap, it keeps notifying every hour. A restart between
   runs does not notify again. Two gates at the same sequence on the two
   tiers each get their own count.
7a. **Quiet hours.** A request newly past its `reply_by` inside the window
   posts nothing. At the window's end, one notification posts if the urgent
   set is still not empty. A gate inside the window posts as usual, and its
   repeats inside the window count toward its cap: with a cap of 3 and a
   window covering repeats 2 and 3, the gate still stops after 4
   notifications. The end-of-window summary leaves every gate's count
   unchanged.
7a2. **Cleared on the Desk.** With a gate's key in `cleared.json`:
   - the poster posts no further reminder for it;
   - the bus message is still unread (an unread count before and after is
     the same);
   - **it is out of the urgent set and still visible:** the gate is absent
     from `notify.json`'s `urgent` and present in its `cleared`, and the
     digest lists it under "cleared" only, with the Desk id and time, never
     in the urgent lines;
   - **it never reads as passed:** a fixture with one passed gate (acked, so
     absent from every list) and one cleared gate gives two different
     outputs. The cleared one appears in `cleared`, and the passed one in no
     list. The test fails if the cleared gate is missing from every list,
     or if the passed gate appears in any list;
   - **`look-clear` writes only `cleared.json`:** fed a recorded clear
     answer, it changes no file but `cleared.json`. The check is a listing
     of every file under the scratch `HOME` and the scratch `TMPDIR`, with
     each file's SHA-256, taken before and after, so a rewrite of the same
     size in the same second still shows. The only difference allowed is
     `cleared.json`'s, and no temp file is left behind. It takes no Desk url
     and no credential, so it has no Desk to write to. That the session
     writes nothing to the Desk is the rule in 3.4.1, not this test;
   - **`look-clear`'s refusals and skips** (3.4.2), one fixture each:
     - a count that differs from `--answers-reported`, two answers for one
       item, and a `cleared.json` that does not parse each exit 1 and leave
       `cleared.json` byte-identical;
     - an unknown desk id, a non-gate item, a `choice` other than `clear`,
       a gate not open, a key cleared by another desk id, and an older
       `updatedAt` are each skipped with their reason. A valid clear in the
       same input is still written, and the run exits 0;
     - the same input run twice leaves `cleared.json` byte-identical the
       second time;
   - each key carries `"author": "unverified"`.
   A key in `cleared.json` that matches no gate changes nothing. A malformed
   `cleared.json` is a reader line in the digest and leaves every gate on
   its schedule, so a bad file cannot silence one.
7b. **Classification.** A `REQUEST` whose body starts with `GATE (ready)` is a
   gate. An `INFORM` whose body has a `GATE` line is a gate. A `REQUEST` with
   no `reply_by` is never urgent. One past its `reply_by` is urgent.
8. **Sleep.** A coalesced run after a simulated sleep says "last look at T"
   and claims no window it did not see.
9. **Heartbeat.** With the job stopped, `look-check` flags it after two
   intervals and posts once. A director-mcp start-up shows the stale line.
   A heartbeat written by a run whose reader failed is still a heartbeat, and
   its `reader` field names the failure.
10. **On the host, before the floor is called done.** Check that a launchd
    user agent runs, and the notification posts, while the screen is locked
    and while the lid is closed. If it does not, the digest's "as of" line
    says so. This is not measured yet.
11. **After umw8p.** With the read-only user on the real brokers, a publish
    to an inbox, an ack, a consumer create and a KV put are each refused. A
    positive control shows the user can read `CONSUMER.INFO`.

## 5. Candidate requirements (provisional)

- **R-TBD-1 (JUDGMENT, provisional).** A timed look at a seat's inbox
  consumes nothing. Draining stays an act the inbox's owner starts.
- **R-TBD-2 (JUDGMENT, provisional).** A timed look runs no model. What it
  surfaces carries ids, classes and ages, never message bodies.
- **R-TBD-3 (OBSERVED, provisional).** A look that cannot read says so in
  its output. An unreadable inbox never renders as an empty one.

## 6. Decisions for the operator

All three are ruled. The one card still open is the quiet-hours times,
valid until 2026-11-16; the architect role re-checks it then:
1. The gate reminder interval N, and whether it has a cap. **Ruled
   2026-10-19: every 4 hours, at most 3 repeats**, with the operator's
   request for a Desk item that clears a gate (3.4.1).
2. Quiet hours: whether there is a window, and when it is. GATE passes
   through either way. **Ruled 2026-10-19: a window**, its times derived
   from the operator's least active hours on the Desk; the times are on a
   follow-up card.
3. **Ruled 2026-10-19: grant, when the broker's authorization block lands.**
   Granting the read-only NATS user once the broker's authorization block
   is live. The user can send only the read requests in the 3.5 table, and
   cannot ack, consume, create or delete consumers, delete messages or write
   presence. It can, by its grant, read any seat's mail on the shared inbox
   streams, because both get verbs carry the subject inside the request and
   the broker cannot confine them to director's subjects. The look reads only
   director's subjects, by code and by test 2, not by the grant. Confining
   the grant would need a stream per seat, which this design does not
   propose.

## 7. Not covered

- A cue-started model turn (deferred; section 3.2).
- Notification delivery when the host is asleep (test 10 decides how the
  digest says so).
- Mail addressed to seats other than director. The same reader serves them
  through the ask ledger, which has its own design.
