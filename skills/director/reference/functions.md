# Director functions: definitions

One entry per director function: where its state lives, who writes it, who
reads it, and whether its writer is enforced or runs on honor. This is K2 of
`sim/design/director-functions-redesign.md` (section 6), and its rows are that
design's section 3 inventory, in the same order and numbering. The rulings
kinds (K9) and the reconciler (K11) read this file to know which stores exist
and whose writes they can trust.

Checked at director main `d8adcec`. Each entry names the file or section it
was read from, so a reader can check it.

## The labels

Every writer carries one of these.

- **enforced**: code refuses a write that breaks the rule. The entry names the
  code.
- **honor-only**: the rule is written down, and nothing checks it. The writer
  is believed.
- **not defined**: the function runs as habit, and nothing on main says what
  it is. These entries record that fact and the requirement that asks for a
  definition. They do not invent one.

Paths under `$DIRECTOR_STATE` (default `~/.director/state`) and `sim/notes/`
are local and gitignored. This repository is public: a definition may live
here, and its data may not. Data that names an organization or client stays
in local or private state.

## Definitions

### 1. Decision Desk

- **Store:** the Decision Desk page, holding each operator ask as a card and
  the operator's answer on it.
- **Writers:** director writes the cards; the operator writes the answers in
  the page. R-202 asks that each card carry the question, each option's text,
  the recommendation, its expiry and a source pointer. Enforced: a card
  written through `card-ingest parse` carries every part, an expiry that is a
  date no earlier than today with a named re-checker, and a slug that is not
  already on the Desk; the file is refused whole, with every problem listed,
  and `card-ingest check` compares the written cards with the batch field by
  field. Honor-only: that the session sends the batch unchanged, and that the
  read file is a real Desk read. Direct writes made without the tool stay
  unchecked. Content stays the author's and the reviewer's: one decision per
  card, plain language, and that a seat's claim carries its source.
- **Readers:** the operator; director, in session. A script cannot read it
  (K5, finding-director-glst). A session reads the cards and hands them to
  `desk-snapshot write`, which validates them and writes
  `$DIRECTOR_STATE/desk-snapshot.json` with the ids whose status is exactly
  `open` and the read time. Honor-only: that a session really read the Desk.
  Enforced: the input's shape and completeness (a duplicate id, a missing
  status or a count that differs from the read's own report is refused).

### 2. Workstream ledger

- **Store:** `$DIRECTOR_STATE/workstreams.jsonl`, append-only events folded on
  read.
- **Writers:** `dws` (`skills/director/scripts/dws`). Enforced: one `flock`
  around fold, check and append, a stage move that names a stale `--from`
  is refused, and the writer table allows `director` every event and
  `refresh` only `observe` and two factual stage moves
  (`board-workstream-ledger.md` section 6). Honor-only: the actor itself.
  The table is an allowlist over `--actor`, and a caller that passes
  `--actor refresh` is believed (`board-workstream-ledger.md:194-196`).
  Enforced (K13): `--blocked operator` needs `--ask <card id>` and is
  refused unless that card is open in the Desk snapshot of section 1, read
  within 24 hours (`THRESHOLD_HOURS` in `dws`). With no usable snapshot it
  refuses and says UNREAD; until a session writes one, every operator-blocked
  row is refused. The card goes on the row as an `ask:` link.
  Enforced (ledger fill, #321): `dws refresh --local` moves rows from the
  relay log and the Desk snapshot, as `refresh`, appending `observe` events
  only, each carrying the `src` it consumed, so replaying the same sources
  writes nothing; it reads no network, holds one lock at a time and
  reads a relay entry only by an id its writer recorded (`relay-log --ws` or a
  `relay:R-<n>` link), never from message text. A director `set` moves last
  moved (R-197). `dws unlinked` lists what the fill could not tie to a row,
  with each source's last read time or UNREAD, and writes nothing. Honor-only:
  that director names the stream with `--ws` or links the entry; an entry
  with neither is listed and moves no row. Not built, awaiting a ruling:
  clearing an operator block when the linked card closes. Not built, no source
  file yet: the ask ledger as a source (`msg:` links are typed and wait).
- **Readers:** the sweep's "Blocked on you" (`dws show --json`, SKILL.md,
  Mode: sweep); `board-html`.

### 3. Board

- **Store:** `$DIRECTOR_STATE/board.md`, authored. `$DIRECTOR_STATE/board.html`
  is a render of it and the ledger, made only where the operator asked for
  the page.
- **Writers:** director edits `board.md` by hand. Honor-only: no check reads
  its prose. `board-html` writes `board.html` and nothing else. That is
  its design, not a refusal, so it is honor-only by the definition above.
- **Readers:** director, before the roster on every sweep (SKILL.md, Mode:
  sweep); the operator, through the page.

### 4. Relay log

- **Store:** `sim/notes/relay-log.md`, local.
- **Writers:** director, one entry per message sent: to, the verbatim text,
  the outcome, written with `relay-log append` (`skills/director/scripts/relay-log`;
  SKILL.md, Capture triggers). Enforced: the caller cannot set the time. The
  writer stamps each entry from the clock under one `flock`, its parser has no
  time argument and refuses one, and it refuses a log path that git would
  track. `--ws <slug>` (repeatable) writes one `**Workstream:**` field line
  after the heading, checked for shape only, so the entry can move that
  ledger row (section 2); the ledger is never opened, and a `**Workstream:**`
  line typed in the text stays quoted and links nothing. `verify-relay-log.sh` checks each of these (R-192; K6). Honor-only:
  calling the writer at all. An entry typed into the file by hand is not
  checked.
- **Readers:** director, in harvest; `reference/relay.md` cites it.

### 5. Bus reading

- **Store:** the seat's durable consumers on the bus. With
  `DIRECTOR_HANDLED=1`, also `handled.jsonl` beside the seat's state.
- **Writers:** the director-mcp shim (`cmd/director-mcp`). With the mode off,
  `wait_for_message` acks on read. With it on, `mark_handled` appends
  handled, forwarded or parked, fsyncs, and only then acks, and a failed
  append never acks (`handled.go`). Enforced in code. The mode is off by
  default, and turning it on for director's seat is a launch step.
- **Readers:** director, through `wait_for_message` and `inbox_summary`.

### 6. Merge guard

- **Store:** none; it reads GitHub and the rulings file (section 19) and prints
  one line.
- **Writers:** none. `merge-guard` is read-only and never merges.
- **Enforced:** the verdict itself, which fails closed: any read that errors,
  comes back empty or lacks a field prints `STOP` (`merge-guard:12-22`). The
  operator's merge exclusions (section 19) are read first, before any `gh`
  call, and fail closed: a missing or unreadable rulings file, a line that is
  not a JSON object, an exclusion without a repo and a label, and a view with
  no label list each print `STOP` (`merge-guard:27-43`, `:57-73`, `:98-112`). Once
  `--match-head-commit` is passed, GitHub refuses a merge whose head moved.
  An open PR based on the head is not a stop: after every other check passes the
  guard prints `PROCEED-KEEP-BRANCH` (exit 0), naming the children and the merge
  command without `--delete-branch`, since deleting the base branch closes a
  child for good. A child read that is not a list of PRs with numbers prints
  `STOP`. The cites above are to the guard at the commit pinned in this file's
  header and do not move with this verdict.
- **Honor-only:** everything after the verdict. `merge-guard` never merges
  (`merge-guard:42-43`); it prints the merge command (`:7-8`). Running the
  guard before a merge, obeying `STOP`, and passing the flag are SKILL.md
  instructions (Merging), and a merge made outside the guard is not checked.
- **Readers:** director, before any merge it makes or relays.

### 7. Harvest

- **Store:** `sim/requirements.md`, from the notes in `sim/notes/`.
- **Writers:** director, by hand (SKILL.md, Mode: harvest), with a source
  class on each entry and the next R number. Honor-only.
- **Readers:** every design and ticket that cites an R number.

### 8. Operator digest

- Not defined on main (design section 1). Director writes it as habit. R-134
  and R-135 ask for decisions that queue without interrupting and an
  attention budget held as settings. Q7 ruled twice daily, and K18 renders it
  from the reconciler.

### 9. Close-or-kill pass

- Not defined on main (design section 1). Held as habit. K7 gives `idea`
  rows an age line, and K18 renders the pass from the reconciler, proposals
  only.

### 10. Sweep

- **Store:** `$DIRECTOR_STATE/sessions.json` and `roster.md`, written by `dsi`
  (atomically, through a temp file and `os.replace`), and
  `$DIRECTOR_STATE/external.md`, written by `dsx`.
- **Writers:** `dsi` and `dsx`, both read-only against what they inspect.
  Each writes only its own files by design; nothing refuses
  another writer, so this is honor-only by the definition above, as is the
  presented shape, one line per item (SKILL.md, Mode: sweep).
- **Readers:** director and the operator.

### 11. Stansfield roll call

- **Store:** none kept; the result is reported in session.
- **Writers:** director, by hand, following `skills/stansfield/SKILL.md`.
  Honor-only.
- **Readers:** the operator.

### 12. Ask ledger

- **Store:** the `ASK_LEDGER` bucket and a JSON file the ask reader rewrites
  after each pass.
- **Writers:** the ask reader (`askreader.go`, feeding `askledger.go`). It
  reads AGENT_AUDIT by sequence and creates no consumer, so it never moves an
  ack floor, and it writes only its own store (`askreader.go:6-8`). Those are
  its design, not a refusal, so they are honor-only by the definition above.
  So is being the only writer: until the reader has its own principal it runs
  under director's user (`:8-10`), so any process running as that user can
  write the store. Diagnostic:
  nothing waits on it.
- **Readers:** the on-demand `asks` read.

### 13. Ruling records

- **Store:** `rulings.jsonl` (see 19), kind `ruling`, written by
  `dws record ruling` (K9): subject, the operator's words verbatim, by,
  ruled_at and a source, with an optional `replaces` id pointer. Older rulings
  live in design files and the relay log.
- **Writers:** director, through `dws record`. It never creates the file, and
  nothing stops a hand edit, so a line written by hand is honor-only.
- **Readers:** `dws records`, which marks a replaced target and counts a pointer
  to a missing id or another kind; director, by search.

### 14. Ring and acknowledgement

- **Store:** none. The channel cue (`DIRECTOR_CUE=1`, `cue.go`) tells a Claude
  Code seat that mail arrived, and records nothing.
- R-200 asks that a ring be followed to a first turn and an ack. K14 adds
  `rings.jsonl`, keyed by seat, with Q6's numbers.

### 15. Successor inheritance

- **Store:** none. R-204 asks that a successor inherit its predecessor's open
  asks. K15 adds an `inherit <role>` read, and K9 the `term` kind.

### 16. Replay

- **Store:** the session corpus already on disk, read only.
- **Writers:** director, by hand, to the notes (`reference/replay.md`).
  Honor-only.
- **Readers:** director, in harvest.

### 17. Capture triggers

- **Store:** the notes files in `sim/notes/`, and `board.md` under Uncaptured.
- **Writers:** director, on each struggle the trigger table names, with the
  admission test applied before anything enters the register (SKILL.md,
  Capture triggers and The admission test). Honor-only.
- **Readers:** director, in harvest.

### 18. Relay authority rule

- **Store:** none; it is prose. SKILL.md (Relaying) rules out one thing:
  director must not represent itself as carrying authority it was not given. The
  rest is open by ruling, and no check is added on relay wording. R-184
  governs a relayed grant refused at the target seat.
- **Writers:** honor-only.

### 19. Merge exclusions

- **Store:** `rulings.jsonl` in director's local state directory
  (`$DIRECTOR_STATE`, or `MERGE_GUARD_RULINGS` to name the file), one JSON
  object per line, never committed. R-153 is ruled: exclusions are held as
  data, by repo and label, and checked before every merge. Only
  `{"kind":"exclusion","repo":R,"label":L}` is read; the ruling, grant and term
  kinds are ignored, and a line with no kind or another kind STOPs the guard.
- **Writers:** the operator creates the file and enters the exclusions
  (`merge-guard:61`); director does not write exclusions. `dws record` (K9)
  appends the ruling, grant and term kinds to the same file and refuses the
  exclusion kind. Honor-only. A seat that meets the missing-file `STOP`
  reports it and does not create the file.
- **Enforced:** `merge-guard` is the reader and fails closed on every
  unreadable case (`merge-guard:57-73`, `:98-112`).
- **Honor-only** for any merge made outside the guard.

### 20. Dispatch ledger

- Requirement only (R-115). K17 adds a per-seat depth view, which covers part
  of it.

### 21. Review-request tracking

- Requirement only (R-170). The design leaves it open.

### 22. Surfacing filters

- Requirements only: R-171 (no draft reaches the operator), R-177, R-198 and
  R-203 (clerical relay is never an operator ask). Honor-only, applied by
  director when it writes a card. K12 applies them as a presentation check on
  the queue.

### 23. Clock stamps

- **Store:** the stamp in each relay-log entry's heading.
- **Writers:** `relay-log` (K6, #287). Enforced: the time comes from the
  clock and the caller cannot set it. Honor-only: that every entry goes
  through the writer (see 4, Relay log).
- **Readers:** director, and R-192's check, `verify-relay-log.sh`.

### 24. The board's Uncaptured block

- **Store:** a block in `board.md`; `board-html` renders it.
- **Writers:** director, by hand, for any session output that would otherwise
  be lost (SKILL.md, Capture triggers). Honor-only.
- **Readers:** director and the operator.

### 25. Presence watch and the held store

- **Store:** under `$DIRECTOR_STATE`, `presence-watch.json` (the current state per
  cluster and supervisor instance, rewritten whole), `presence-events.jsonl`
  (append-only: every transition, gap, flap and foreign key) and `held.jsonl`
  (append-only: one line per state change of a held send; its current state is
  the last line).
- **Writers:** the director shim, on its 30 second heartbeat, only when
  `DIRECTOR_PRESENCE_WATCH=1` and the session holds the global director role
  (`presencewatch_shim.go`). The held store is written by the same process
  (`held.go`) under a file lock with an fsync. Enforced: with the flag off the
  shim writes none of these, and a send that presence refuses is refused as before.
  Honor-only: that nothing else writes them.
- **Readers:** director, through `presence_notice` on `wait_for_message` and
  `inbox_summary`, the sweep's `Away:` line (SKILL.md, Mode: sweep), and the
  verifier `./scripts/verify-presence-watch.sh` (in the director checkout; it is
  not installed). A shim started with the flag off while held records exist
  lists them as held, and sends none.
- **What it is not:** a gate. It reports and releases; it blocks nothing
  (SOUL section 8, ADR-007). A held send goes out only when its recipient has
  returned and its presence row is beating, and only within its sell-by; past
  that it is proposed for review, never sent on a clock.

## Thresholds

Design D puts every threshold in one operator-editable table, out of any
required check (section 4, the automation boundary). Today there are two places:

- `board-html` carries `UNMOVED_HOURS` in the script, the hours a row may sit
  in a stage before the page says unmoved. `idea`, `accepted` and `parked`
  have no row, so they never flag (K7).
- the director-mcp shim reads `thresholds.json` beside the seat's state, with
  four rows: `handled_ack_wait` (30 minutes by default; `handled.go`) and the
  presence watch's `presence_flap_window` (1 hour), `presence_flap_quiet` (15
  minutes) and `presence_hold_sell_by` (8 hours), all in `handled.go`. The flap
  count, 4, is fixed in `presencewatch.go` until a count row is wanted.

Joining them is not part of K2.
