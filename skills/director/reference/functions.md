# Director functions: definitions

One entry per director function: where its state lives, who writes it, who
reads it, and whether its writer is enforced or runs on honor. This is K2 of
`sim/design/director-functions-redesign.md` (section 6), and its rows are that
design's section 3 inventory, in the same order and numbering. The rulings
kinds (K9) and the reconciler (K11) read this file to know which stores exist
and whose writes they can trust.

Checked at director main `39ec338`. Each entry names the file or section it
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
  the page. Honor-only: nothing checks a card's shape. The card rules are
  not defined on main (design section 1). R-202 asks that each card carry
  the question, each option's text, the recommendation, its expiry and a
  source pointer.
- **Readers:** the operator; director, in session. Whether a script can read
  it is open (K5).

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
- **Readers:** the sweep's "Blocked on you" (`dws show --json`, SKILL.md,
  Mode: sweep); `board-html`.

### 3. Board

- **Store:** `$DIRECTOR_STATE/board.md`, authored. `$DIRECTOR_STATE/board.html`
  is a render of it and the ledger, made only where the operator asked for
  the page.
- **Writers:** director edits `board.md` by hand. Honor-only: no check reads
  its prose. `board-html` writes `board.html` (enforced: it renders, and
  writes nothing else).
- **Readers:** director, before the roster on every sweep (SKILL.md, Mode:
  sweep); the operator, through the page.

### 4. Relay log

- **Store:** `sim/notes/relay-log.md`, local.
- **Writers:** director, by hand, one entry per message sent: to, the verbatim
  text, the outcome (SKILL.md, Relaying). Honor-only. Entry times are typed,
  not taken from the clock (R-192; K6).
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

- **Store:** none; it reads GitHub and prints one line.
- **Writers:** none. `merge-guard` is read-only and never merges.
- **Enforced:** a merge run through it, which proceeds only on `PROCEED` and
  merges with `--match-head-commit`. Honor-only: whether a merge runs it at
  all. SKILL.md (Merging) says it runs before every merge, and a merge made
  outside it is not checked. Operator merge exclusions are not read yet
  (`merge-guard:29`; K1, #282).
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
  Enforced: each writes only its own files. Honor-only: the presented shape,
  one line per item (SKILL.md, Mode: sweep).
- **Readers:** director and the operator.

### 11. Stansfield roll call

- **Store:** none kept; the result is reported in session.
- **Writers:** director, by hand, following `skills/stansfield/SKILL.md`.
  Honor-only.
- **Readers:** the operator.

### 12. Ask ledger

- **Store:** the `ASK_LEDGER` bucket and a JSON file the ask reader rewrites
  after each pass.
- **Writers:** the ask reader only (`askreader.go`, feeding `askledger.go`). It
  reads AGENT_AUDIT by sequence and creates no consumer, so it never moves an
  ack floor. Enforced in code. Until it has its own principal it runs under
  director's user. Diagnostic: nothing waits on it.
- **Readers:** the on-demand `asks` read.

### 13. Ruling records

- **Store:** none. Rulings live in design files (for example the "Operator
  rulings" table in the redesign) and in the relay log.
- **Writers:** whoever records the ruling, by hand. Honor-only. R-201 asks
  for records keyed by subject; K9 adds the `ruling` kind to `rulings.jsonl`.
- **Readers:** director, by search.

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
  director represents itself as carrying no authority it was not given. The
  rest is open by ruling, and no check is added on relay wording. R-184
  governs a relayed grant refused at the target seat.
- **Writers:** honor-only.

### 19. Merge exclusions

- **Store:** none yet. R-153 is ruled: exclusions are held as data, by repo
  and label, and checked before every merge. The data stays in local or
  private state, never in this repository.
- **Writers:** none yet. K1 (#282) makes `merge-guard` read them and fail
  closed; K9 adds the `exclusion` kind.
- **Honor-only** until K1 lands, and afterwards for any merge made outside
  the guard.

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

- Requirement only (R-192). K6 stamps relay-log entries from the clock.

### 24. The board's Uncaptured block

- **Store:** a block in `board.md`; `board-html` renders it.
- **Writers:** director, by hand, for any session output that would otherwise
  be lost (SKILL.md, Capture triggers). Honor-only.
- **Readers:** director and the operator.

## Thresholds

Design D puts every threshold in one operator-editable table, out of any
required check (section 4, the automation boundary). Today there are two:

- `board-html` carries `UNMOVED_HOURS` in the script, the hours a row may sit
  in a stage before the page says unmoved. `idea`, `accepted` and `parked`
  have no row, so they never flag (K7).
- the director-mcp shim reads `thresholds.json` beside the seat's state, with
  one row, `handled_ack_wait` (30 minutes by default; `handled.go`).

Joining them is not part of K2.
