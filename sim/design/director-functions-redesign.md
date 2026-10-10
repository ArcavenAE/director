# Director functions, redesigned

Status: design for review, 2026-10-09; the operator ruled every section 5
decision on 2026-10-10 (section 5, "Operator rulings"). Owner: the architect
role. Docs only.
Nothing is filed until the operator has seen this file. The common tickets
(section 6) need no design pick; everything else waits for the pick. The
party ratifies nothing: every recommendation below is the panel's, for the
operator to rule.

- Commission: the operations coordination plan, part 2. The operator's
  direction, verbatim: "improve the plan here, without losing capability,
  and recognizing that these are director functions, and each was hacked on
  over time based on need, but we can improve the design. further, the
  design should be informed from our prior art and research related to
  good work management, organization of work and information/work/leadership
  principles".
- Method: a six-round party of nine seats (refine, define, design, audit,
  plan, forced vote). The record is a gitignored party record in a private
  repository. This file is the result.
- Pins: director main 860f1aa. R-200 to R-204 are proposed in #273 (open,
  unmerged), and are cited here as proposed.
- Requirements: R-134, R-135, R-152, R-153, R-159, R-162, R-169, R-171,
  R-177, R-184, R-192, R-196, R-197, R-198, and R-200 to R-204 (proposed).

## 1. Why

Director grew one function at a time, each in answer to a failure, and the
functions now keep the same fact in several places. The clearest case is
the queue of what waits on the operator. It is kept by hand in sweep notes,
in the ledger's operator-blocked rows, in `board.md` prose and on the Desk,
and nothing reconciles them. R-202 (proposed) measured the cost: the ledger listed 18
rows as blocked on the operator, and 13 had been ruled hours earlier. Seven
of nine seats, working apart, named this queue as the most costly function.

A redesign has to keep every function, including those that exist only as
habit. The first inventory found 19 functions, and the capability audit
added five more, for 24 rows in all (section 3). Several have no written
definition on main: the Desk's card rules, the operator digest, the
close-or-kill pass and the merge exclusions.

## 2. What was found on the way

These hold whichever design is picked. Each was checked with one command at
the pin.

- **R-153 is ruled and has no code.** It says operator merge exclusions are
  "enforced before any merge" (requirements.md:1633). `merge-guard:29` says
  "Operator merge exclusions (R-153) are not checked here", and its PR read
  (`merge-guard:56-57`) does not request `labels`.
- **The ledger's writer table is honor-only.** "The actor is a claim, not an
  identity" (board-workstream-ledger.md:194-196).
- **The drain acks on read.** `drain.go:9-12` consumes and acks on receipt,
  and no non-test file in `probe/nats-phase-0/director-mcp/` records a
  handled state. R-169's handled ledger is unbuilt.
- **`idea` rows never flag.** `board-html:33-38`: "A stage missing here never
  flags: idea, accepted and parked." On one local ledger, 128 of 151 rows
  sat at `idea`, so a close-or-kill pass built on the unmoved thresholds
  would list none of them.
- **Bus mail expires.** AGENT_INBOX `max_age` is 72h, and only mail sent to
  `role://` survives a generation change (asks-die-unread.md:34-35). State
  must live in files, and a successor needs one by-role read of what it
  inherits.
- **The Desk page writes its own answers.** Any design that moves the store
  of asks off the Desk needs a sync writer for those answers.
- **This repository is public.** Exclusion data that names an organization
  or client stays in local or private state. Definitions may live here;
  their data may not.
- **The relay discipline stays open.** It was struck by ruling (SKILL.md:5,
  :173-177), so no design adds a check on relay wording.
- **The inventory had gaps.** R-115 (dispatch ledger) and R-170
  (review-request tracking) had no row; neither did the queue's surfacing
  filters (R-171, R-177, R-198, R-203 proposed), R-192 clock stamps, or the board's
  Uncaptured block. These five were added as rows 20 to 24 below. R-170 has
  no answer in any design yet, D included.

## 3. Designs considered

The three first designs were drafted against rows 1 to 19 and mapped every
one; none retired a function. The audit added rows 20 to 24 after A, B and
C were drafted. Where a draft had already named the function, its cell
says where; where none had, the cell says "open". That maps 7 of those 15
cells: A maps 2 of the 5 rows (22, 24), B maps 3 (22, 23, 24) and C maps 2
(23, 24). Rows 20 and 21 are open under all three. D, planned after the
audit, maps every row but 21, R-170, which it leaves open. A cell names where the function
lives in that design; "as today" means it stays where the today column
says. Today's column records whether a function is code, a requirement
only, or habit held in agent memory, as the first round sorted them.

| # | function | today | A one record | B small tools | C keep the stores | D recommended |
|---|---|---|---|---|---|---|
| 1 | Decision Desk | the Desk Artifact; card rules are habit | a view of the ask fold | a render of the `asks` tool | kept as the store | kept as the store of asks |
| 2 | workstream ledger | `dws` (code) | `stream` kind in the log | `streams` tool | kept; the reconciler checks it | kept (`dws`); blocked-on-operator needs a card id (K13) |
| 3 | board | `board.md`, authored; `board-html` (code) | a regenerated brief, plus notes citing ids | a generated page, plus notes | authored, with a generated state section first | generated state first, narrative second, ids resolve (K19) |
| 4 | relay log | a local log, by hand | `message` kind | `relay` tool, clock-stamped | kept, local | kept, local, clock-stamped (K6) |
| 5 | bus reading | director-mcp drain (code; acks on read) | one reader per tier writes receipts | one `inbox` tool, one cursor | one draining path with a handled ledger | as C (K3, K4) |
| 6 | merge-guard | `merge-guard` (code) | kept, reads exclusions | kept, reads exclusions | kept, reads exclusions | kept, reads exclusions (K1) |
| 7 | harvest | by hand | as today | `notes` tool | as today | as today; reconciler proposes a harvest due (R-179) |
| 8 | operator digests | habit; no definition on main | a view of the fold | a render of `queue` | reconciler output | reconciler render (K18) |
| 9 | close-or-kill | habit; no definition on main | a view of the fold, proposals only | weekly render | reconciler list | reconciler render, with an `idea` age line (K7, K18) |
| 10 | sweep (`dsi`, `dsx`) | `dsi`, `dsx` (code) | a query over the store | renders | as today; lists open cards | as today; state first; tick runs drain and reconcile (K20) |
| 11 | stansfield roll call | by hand | as today | `seats` snapshot | as today | as today |
| 12 | ask ledger | design, plus the `askledger.go` probe | ask-state events from the bus reader | part of `inbox` and `asks` | kept; shares the cursor | kept; shares the drain (K4) |
| 13 | ruling records | none (R-201, proposed) | `ruling` kind | `rulings` tool | `rulings.jsonl` or a Desk collection | `rulings.jsonl` (K9) |
| 14 | ring and ack | none (R-200, proposed) | `receipt` kind | `rings` tool | ring record in the ledger | `rings.jsonl` keyed by seat (K14) |
| 15 | successor inheritance | none (R-204, proposed) | a fold of receipts | grants and terms, by-role read | cards and rulings only | `inherit <role>` read, plus `term` kind (K15, K9) |
| 16 | replay | by hand | as today | `notes` tool | as today | as today |
| 17 | capture triggers | habit (the admission test) | as today | as today | as today | as today; written in the definitions file (K2) |
| 18 | relay authority rule | prose (R-184), open by ruling | as today (open by ruling) | as today (open by ruling) | as today (open by ruling) | as today; grants bound merges and terms only |
| 19 | merge exclusions | none (R-153 is ruled, no code) | `exclusion` grants | deny-grants | exclusion records | exclusion records, data kept local (K1, K9) |
| 20 | R-115 dispatch ledger | requirement only | open | open | open | per-seat depth view, partial (K17) |
| 21 | R-170 review tracking | requirement only | open | open | open | open: no answer yet |
| 22 | surfacing filters (R-171, R-177, R-198, R-203 proposed) | requirements | presentation check | in `queue` (one draft) | open | presentation check (K12) |
| 23 | R-192 clock stamps | requirement only | open | `relay` helper | clock stamps | clock stamps (K6) |
| 24 | the board's Uncaptured block | a block in `board.md` | `note` kind (one draft) | notes file | kept in the authored board | kept in the narrative |

- **A. One decision-and-work record.** One append-only event log; the Desk,
  ledger, board, digest and close-or-kill are views. Weakest point: one bad
  write feeds every view.
- **B. Small composed tools.** One record and one writer per tool, joined by
  bus events and receiver acks; no shared store. Weakest point: a lost event
  or a silent tool leaves a queue that looks complete.
- **C. Keep the stores, add the glue.** Today's stores, plus a definitions
  file, rulings and grants records, a reconciler that only proposes, and a
  handled ledger. Weakest point: drift is found after it happens.
- **D. The mix (recommended, 9 of 9).** C's stores and reconciler, with the
  existing `dws` fold reused for new local files, A's presentation check,
  and B's mirror-or-proposal tags and UNREAD rule. The three seats who
  planned D came from the A, B and C poles, and each made the same change to
  the starting mix: reuse the running `dws` fold, not a new central store.

## 4. Design D

**Stores.**
- The Desk stays the store of operator asks, with its cards and answers.
- The workstream ledger stays as `workstreams.jsonl`.
- `board.md` stays authored.
- The relay log stays local and append-only.
- New: `rulings.jsonl` beside the ledger, on the same fold and lock code.
  Its kinds are `ruling` (R-201, proposed), `grant` (draft or confirmed), `exclusion`
  (R-153), and `term` (R-204, proposed), each term with a `replaced` mark.
- New: `rings.jsonl`, keyed by seat, for a ring, its first turn, its ack and
  its blocker (R-200, proposed).

**Writers.** Each event carries a tag: mirror (a cited external fact),
proposal (needs a confirm) or judgment (director's own call). Writers that
are honor-only are labelled as such in the definitions file.

**One source for "blocked on the operator".** It is read from open Desk
cards. `dws set --blocked operator` refuses unless it names an open card
id.

**The reconciler.** `dws reconcile` reads the Desk, the ledger,
`rulings.jsonl`, `rings.jsonl` and linked PR state. It writes proposals to a
file and never writes a record, and director applies or drops each one. It
proposes:
- a blocked row with no open card;
- a card whose PR merged;
- two open cards on one subject;
- a recommendation past its sell-by date;
- an ask unacked past its window;
- a grant past its expiry;
- unmoved rows, as close-or-kill input;
- a harvest due (R-179).

Every source prints its last-read time, or UNREAD, and a queue count reads
"at least n" while any source is UNREAD. The standing tick runs the drain
and the reconciler.

**The queue.** The operator reads one list: open Desk cards, grouped as
merges recommended, rulings needed and console steps. A presentation check
keeps out drafts, items already merged or closed, clerical relays, and
claims with no source. It also hides an ask whose linked PR has merged
while the close is pending.

**Bus.** Mail addressed to director is read in full on every tier, through
one drain that acks after handling. Traffic between supervisors is read as
headers or counts only (R-159).

**Board.** A generated state section comes first; authored narrative
follows, and free judgment stays, including the Uncaptured block. Any ask
id or state line in the prose must resolve to a record. That check is
structural.

**Grants.** Director drafts a grant, and the operator confirms it on a card
that shows its bounds. It has no effect until confirmed. Acting inside a
confirmed grant is not an ask, and each use is one line in the next digest.

**R-153.** merge-guard adds `labels` to its PR read, reads exclusion records
before its other checks, and stops on a match, or when the records or the
labels cannot be read (R-152). A merge made outside merge-guard stays
honor-only, and the definitions file says so.

**The automation boundary** (SOUL section 8, ADR-007):
- every threshold lives in one operator-editable table and in no required
  check;
- the reconciler's applied-versus-dropped count is diagnostic only;
- nothing executes on a clock or on silence;
- merge-guard is the only gate.

**Stop lines.** Each is an observation that sends a recommendation back to
the operator. Nothing changes on the observation alone.
- If the Desk snapshot reads UNREAD twice in a week, the architect role
  re-presents Q4 with that evidence, recommending (b), a local store of
  asks. The store moves only if the operator rules (b).
- If most events end up tagged judgment, the architect role re-presents the
  tags with that evidence, recommending that they be dropped while UNREAD
  and the presentation check are kept. They are dropped only if the
  operator agrees.

## 5. Decisions for the operator

Each recommendation is valid until 2026-10-23; the architect role re-checks
it then. Nothing executes on silence. Dissent is named by seat.

| # | question | options | recommendation | vote |
|---|---|---|---|---|
| Q0 | Which design? | A, B, C, D | D | 9-0 |
| Q5 | A merged or closed linked PR against an open ask (amends R-197, OBSERVED) | (a) it clears the block; (b) it only proposes closing, and the card shows it; (c) the ledger flag clears by mirror, the ask stays open, and the queue hides it until director closes it | (c) | 7-2; automation and organization prefer (b) |
| Q4 | Where do decision records live? (R-202, proposed, names no store) | (a) the Desk stays the store; director reads it in session; scripts get a read path if one exists; (b) a local file, with the Desk as a render and a sync step | (a) | 9-0 |
| Q1 | Which items interrupt you? (R-134, JUDGMENT) | (a) none by default, you name the list; (b) a default list for you to confirm: an outage, a console step or recommendation expiring before the next digest, a seat blocked on your ask | (b) | 7-2; architect and automation prefer (a) |
| X7 | How do grants take effect? | (a) director drafts, you confirm on a card showing the bounds; (b) director writes from your quoted words, and the reconciler flags any grant with no source | (a) | 9-0 |
| Q2 | A cap on open asks? (R-135, no owner) | shown, never blocking, N named by you; or none | shown, N yours | 9-0 |
| Q3 | Report grant use after the fact? | one digest line per use; or none | one line per use | 9-0 |
| Q6 | Ring window and cap? | set by you; past the cap, report the blocker and stop | as stated | 9-0 |
| Q7 | Digest cadence? | twice daily as a start; or other | twice daily | 9-0 |

A further ruling is optional. The board's prose check (V4) carried 8-1;
practitioner preferred no check on prose.

### Operator rulings (2026-10-10)

Relayed by director from the operator's decision desk, and recorded by the
architect role that chaired the panel. Each ruling quotes the option the
operator chose, as it was written on the card.

| # | ruling | the chosen option, as written |
|---|---|---|
| Q0 | **D**, as recommended | "The mix (9 of 9 seats): keep today's separate stores (desk, board, ledger) with a reconciler that only proposes changes, built on the existing workstream ledger; add a check that every open ask is shown to you somewhere; tag each record as either a copy of outside state or a proposal; and treat any message nobody has read as a problem to report." |
| Q1 | **(b)**, the default list, as recommended | "Use that list." |
| Q2 | **none**, against the recommendation | "No cap; show everything." |
| Q3 | **one line per use**, as recommended | "Yes, one line per use." |
| Q4 | **(a)**, as recommended | "Yes, the Desk stays the store." |
| Q5 | **(c)**, as recommended | "The ledger's 'waiting on PR' marker clears by itself, but your ask stays open until director checks the result and closes it." |
| Q6 | the operator's numbers | "Ring after 10 minutes idle with mail; at most 3 rings per seat per hour, then report the seat as stuck." |
| Q7 | **twice daily**, as recommended | "Twice a day." |
| X7 | **(a)**, as recommended | "Yes, director drafts and you confirm each one on a card." |

Consequences for section 6:
- K8 to K20 no longer wait on Q0. Their other edges stand.
- Q2's ruling removes the cap: the presentation check (K12) shows every open
  ask, and no N is configured.
- K14 takes Q6's numbers: a 10-minute idle-with-mail window, three rings per
  seat per hour, then a stuck report.
- The rulings are design inputs. Nothing is filed or built from them until
  the tickets are filed in their own step.

## 6. Candidate tickets (not filed)

Flat, with edges. R-200 to R-204 in the serves column are proposed
(#273). "Common" lands under any design and needs no pick. The Q and X
entries in the "needs first" column are ruled (section 5); filing is its own
step.

| id | title | needs first | serves | common |
|---|---|---|---|---|
| K1 | Make merge-guard read labels and exclusion records, fail closed | none | R-153, R-152 | yes |
| K2 | Write the function definitions file; label honor-only writers | none | R-134, R-135, R-202 | yes |
| K3 | Promote the director-mcp drain out of probe/ | none | R-196 | yes |
| K4 | Record handled, forwarded and parked; ack after handled | K3 | R-169, R-08 | yes |
| K5 | Spike: can a script read the Desk cards and answers? | none | R-202 | yes |
| K6 | Stamp relay-log entries from the clock | none | R-192 | yes |
| K7 | Give `idea` rows an age line in the threshold table | none | R-197 | yes |
| K8 | Tag dws events as mirror, proposal or judgment | Q0 | R-186 | |
| K9 | Add rulings.jsonl kinds: ruling, grant, exclusion, term | K2, Q0 | R-201, R-204, R-184, R-153 | |
| K10 | Add the grant confirm card on the Desk | K9, K5 | R-184 | |
| K11 | Add `dws reconcile`, proposals only, UNREAD per source | K8, K9, K5 | R-197, R-201, R-202, R-179 | |
| K12 | Apply the presentation check to the operator queue | K11, Q5 | R-171, R-177, R-198, R-203 | |
| K13 | Refuse `--blocked operator` without an open card id | K5, Q4 | R-202 | |
| K14 | Add rings.jsonl keyed by seat | K9, Q6 | R-200, R-194, R-185 | |
| K15 | Add an `inherit <role>` read | K9, K4 | R-204 | |
| K16 | Record owed forwards with their arrival time | K4, K6 | R-162 | |
| K17 | Add a per-seat depth view | K4 | R-115, R-117 | |
| K18 | Render the digest and close-or-kill from reconcile | K11, K7, Q1, Q7 | R-134, R-135 | |
| K19 | Put a generated state section first on the board | K11 | R-36 | |
| K20 | Change the sweep order; run drain and reconcile on the tick | K4, K11, K19 | R-196 | |

Order: K1, K2 and K3 start on day one, and K4 follows K3 at once. K3 and K4
together are the long pole. Before any
ticket cites R-201 or R-202, those requirements are split so that each
R-id owns one behavior. Both sit in #273 and are unmerged.

### K4 in detail

Rows 5 and 12 say only "one draining path with a handled ledger". The
builder asked three questions before starting; this is the answer, read
from section 4 ("Mail addressed to director is read in full on every tier,
through one drain that acks after handling") and R-169. It adds no ruling:
every point below sits inside Q0 and Q5 as ruled.

**Who records a message as handled, and for whom.**
- Only the session can know that it acted, so the session records it, with
  a new `mark_handled` tool. It takes the envelope's `message_id`, the tier
  and a disposition: `handled`, `forwarded` (with the forward's message id) or
  `parked` (with a one-line reason).
- The change is scoped to director's own drain, which is what section 4 and
  R-169 cover. The shim gets a per-seat handled mode, on for director and off
  by default. With it off, `wait_for_message` acks on read exactly as today,
  on both tiers (`cmd/director-mcp/bus.go:1004` local, `global.go:656`
  global), and no other seat's drain changes. With it on, both tiers follow
  the rules below. If the
  build finds the mode cannot be kept per seat, that is a stop line: it goes
  back to director as a decision, because it would change every draining
  seat.
- With the mode on, `wait_for_message` delivers without acking, so the
  message stays ack-pending on the seat's durable. `mark_handled` appends the
  disposition to the ledger, fsyncs it, and only then acks. A failed append
  or fsync never acks; the tool returns the error and the message stays
  pending. A crash between the write and the ack
  leaves the message pending, and it is redelivered; the drain then finds
  the disposition already in the ledger, acks the message and does not
  return it, so there is no second record.
- Every disposition acks, `parked` included. A parked message stays visible
  through the ledger, not through redelivery.
- The ack wait for the handled mode is longer than the default 30 s (the
  consumers set none today), so a read message is not redelivered while the
  session is working on it. Its value is an entry in the operator-editable
  threshold table (section 4, the automation boundary), not a constant.
  While the session is live, the shim sends `InProgress` on each delivered,
  unrecorded message before its ack wait runs out, so long handling does not
  cause a redelivery; the ack wait then bounds only a session that died.

**Where the ledger lives.**
- A per-seat append-only JSONL file beside the seat's other director state,
  `handled.jsonl`, folded on read by the envelope's `message_id`. Last event
  per key wins. Each event also records the tier, stream and sequence, for
  the reader only. The key is not the sequence because a stream that is
  deleted, recreated or restored restarts its sequences: a new message that
  reused a ledgered sequence would be acked and never returned, a silent
  loss. `message_id` is minted by the sender and is already the bus dedupe
  id (`Nats-Msg-Id`, `bus.go:823`), so it survives a stream reset. The
  shim's own sends always carry one (`envelope.go:71`). An envelope from
  another publisher with no `message_id` cannot be keyed or marked, so the
  drain reports it (a `rejected` line in the ledger with its tier, stream and
  sequence, counted by `inbox_summary`) and then Terms it, as the receive
  path already does for undecodable mail (`bus.go:997`, `global.go:650`).
  Holding it would redeliver it without bound, since `MaxDeliver` is -1.
  Each event also records a hash of the envelope. A ledgered `message_id`
  that arrives with a different hash is a reuse by a sender outside the
  shim; the drain reports it and returns it to the session rather than
  acking it as handled. There is no cursor and no high-water mark, which is the
  failure R-169 names.
- It follows the `dws` store shape (append-only, folded on read, one flock
  around fold, check and append) so that `dws reconcile` can read it. Design
  D reuses local files on that fold; a JetStream KV would be a new central
  store, which D ruled out.

**Read but not handled, without redelivery storms.**
- `inbox_summary` gets its own section for messages delivered to this
  session and not yet in the ledger, oldest first, with their age. These
  come from the ledger and the consumer's pending state, not from
  redelivery.
- An unread message is still reported as UNREAD (Q0). A read-unhandled one
  past its window is a reconciler proposal ("an ask unacked past its
  window", section 4), never an automatic action.
- K4 does not carry the waiting-on-PR marker. Q5 amends R-197, a workstream
  ledger requirement, so the marker is on the `workstreams.jsonl` row, where
  "the ledger" means throughout this doc. A merged PR clears it there as a
  mirror event, and the ask stays open until director closes it (Q5 (c));
  that work sits with K11 and K12. The handled ledger and the ask ledger
  (`askledger.go`, the bus REQUEST ledger) leave that row alone.

## 7. Open

- Which hosts run a merge guard. Exclusion records held on one host bind
  only that host's guard. This is unchecked, and is a fact to report, not a
  ruling.
- Whether a script can read the Desk (K5).
- The value of the `idea` threshold. It is an operator-tunable table entry.

Candidate DFR-A to DFR-C, provisional, for harvest once the operator picks:
- DFR-A: every operator-facing list is a query over records, and none is
  hand-kept (R-202, proposed).
- DFR-B: every source a director view reads prints its last-read time, or
  UNREAD, and a count reads "at least n" while a source is unread.
- DFR-C: a grant takes effect only on the operator's confirm of a card that
  shows its bounds.
