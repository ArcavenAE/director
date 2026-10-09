# Director functions, redesigned

Status: design for review, 2026-10-09. Owner: the architect role. Docs only;
nothing is built or filed until the operator picks a design. The party
ratifies nothing: every recommendation below is the panel's, for the
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
- Pins: director main 860f1aa. R-200 to R-204 are cited from #273 (open,
  unmerged).
- Requirements: R-134, R-135, R-152, R-153, R-159, R-162, R-169, R-171,
  R-177, R-184, R-192, R-196, R-197, R-198, R-201, R-202, R-203, R-204.

## 1. Why

Director grew one function at a time, each in answer to a failure, and the
functions now keep the same fact in several places. The clearest case is
the queue of what waits on the operator. It is kept by hand in sweep notes,
in the ledger's operator-blocked rows, in `board.md` prose and on the Desk,
and nothing reconciles them. R-202 measured the cost: the ledger listed 18
rows as blocked on the operator, and 13 had been ruled hours earlier. Seven
of nine seats, working apart, named this queue as the most costly function.

A redesign has to keep every function, including those that exist only as
habit. The inventory found 19 functions. Several have no written definition
on main: the Desk's card rules, the operator digest, the close-or-kill pass
and the merge exclusions.

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
  filters (R-171, R-177, R-198, R-203), R-192 clock stamps, or the board's
  Uncaptured block.

## 3. Designs considered

Each maps all 19 functions, and none retires one.

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
  Its kinds are `ruling` (R-201), `grant` (draft or confirmed), `exclusion`
  (R-153), and `term` (R-204), each term with a `replaced` mark.
- New: `rings.jsonl`, keyed by seat, for a ring, its first turn, its ack and
  its blocker (R-200).

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

**Stop lines.**
- If the Desk snapshot reads UNREAD twice in a week, ask for Q4 the other
  way, and move the store of asks to a local file.
- If most events end up tagged judgment, drop the tags and keep UNREAD and
  the presentation check.

## 5. Decisions for the operator

Each recommendation is valid until 2026-10-23; the architect role re-checks
it then. Nothing executes on silence. Dissent is named by seat.

| # | question | options | recommendation | vote |
|---|---|---|---|---|
| Q0 | Which design? | A, B, C, D | D | 9-0 |
| Q5 | A merged or closed linked PR against an open ask (amends R-197, OBSERVED) | (a) it clears the block; (b) it only proposes closing, and the card shows it; (c) the ledger flag clears by mirror, the ask stays open, and the queue hides it until director closes it | (c) | 7-2; automation and organization prefer (b) |
| Q4 | Where do decision records live? (R-202 names no store) | (a) the Desk stays the store; director reads it in session; scripts get a read path if one exists; (b) a local file, with the Desk as a render and a sync step | (a) | 9-0 |
| Q1 | Which items interrupt you? (R-134, JUDGMENT) | (a) none by default, you name the list; (b) a default list for you to confirm: an outage, a console step or recommendation expiring before the next digest, a seat blocked on your ask | (b) | 7-2; architect and automation prefer (a) |
| X7 | How do grants take effect? | (a) director drafts, you confirm on a card showing the bounds; (b) director writes from your quoted words, and the reconciler flags any grant with no source | (a) | 9-0 |
| Q2 | A cap on open asks? (R-135, no owner) | shown, never blocking, N named by you; or none | shown, N yours | 9-0 |
| Q3 | Report grant use after the fact? | one digest line per use; or none | one line per use | 9-0 |
| Q6 | Ring window and cap? | set by you; past the cap, report the blocker and stop | as stated | 9-0 |
| Q7 | Digest cadence? | twice daily as a start; or other | twice daily | 9-0 |

A further ruling is optional. The board's prose check (V4) carried 8-1;
practitioner preferred no check on prose.

## 6. Candidate tickets (not filed)

Flat, with edges. "Common" lands under any design and needs no pick. Even
so, nothing is filed before the operator has seen this file.

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

Order: K1, K2 and K4 start on day one; K4 is the long pole. Before any
ticket cites R-201 or R-202, those requirements are split so that each
R-id owns one behavior. Both sit in #273 and are unmerged.

## 7. Open

- Which hosts run a merge guard. Exclusion records held on one host bind
  only that host's guard. This is unchecked, and is a fact to report, not a
  ruling.
- Whether a script can read the Desk (K5).
- The value of the `idea` threshold. It is an operator-tunable table entry.

Candidate DFR-A to DFR-C, provisional, for harvest once the operator picks:
- DFR-A: every operator-facing list is a query over records, and none is
  hand-kept (R-202).
- DFR-B: every source a director view reads prints its last-read time, or
  UNREAD, and a count reads "at least n" while a source is unread.
- DFR-C: a grant takes effect only on the operator's confirm of a card that
  shows its bounds.
