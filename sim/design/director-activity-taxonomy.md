# Director's activity taxonomy for the token audits

Proposal, 2026-11-12. Nothing here is built. A token audit today gives director one row, "director sessions (coordination)", and counts all of it as overhead. That row hides very different work: routing the operator's asks, gating merges, keeping records, waiting on the bus. This design splits it into classes a classifier can apply to transcripts. A month-over-month trend then shows whether each kind of director work is getting cheaper or dearer, and a leader can see which management job each class stands in for.

Every number this produces is diagnostic. It is reported and never gated (`.claude/rules/diagnostic-not-gate.md` in the orchestrator; SOUL section 8).

## 0. Where it comes from

The operator asked for director's tokens to be broken down by activity, so leadership can understand them and the fleet can tell whether it is getting more or less efficient. The operator's point is that much of this work is what a project manager or a middle manager does, so one coordination-overhead figure set against a survey of human time is unfair to it.

A three-round design party with five panelists settled this design, with a forced vote on every contested item. The tally and the dissent are in section 7.

The classifier this targets is critic's `sim/tools/ledger-s10-axes.py`. Today a non-seat session counts as a director session when it calls the Skill tool with skill `director`, or when more than half its tool calls start with `mcp__director__` (that file's header). It reads only assistant records, and it counts a request whose tools are all bus waits as fleet "hub wait" before it looks at the session class. So today's director row holds none of director's waiting.

## 1. Unit of attribution

- **One class per assistant request.** The class comes from the request's `tool_use` blocks, so the classes partition the spend and nothing is counted twice. Per-turn attribution was ruled out, because the classifier never reads user records and so cannot see turn boundaries.
- **A text-only request** inherits the class of the next tool-bearing request in the same session stream, or of the previous one when there is no next (4-1).
- **Inheritance is visible.** The share of each class reached by inheritance is reported, split by direction, so a drift in it shows.

## 2. Classes and signatures

First match wins, over the set of `tool_use` blocks in one request.

- **Tool names** are as they appear in `tool_use.name`.
- **"Bash X"** means a Bash call whose command, after leading environment assignments and a path prefix, starts with the word X.
- **Command text is read, not kept.** The classifier reads the command only to match its first word; no command text leaves it.
- **The table is data.** It lives in critic's map file as a new kind, with a version that every audit prints, so a rename moves dollars between classes without a code change.

| # | class | signature | human role it stands in for |
|---|---|---|---|
| 1 | waiting | every tool in {`mcp__director__wait_for_message`, `mcp__director__inbox_summary`} | none; this is mechanical polling, with a known mechanical fix |
| 2 | merge gating | Bash `merge-guard`; a Bash command containing `gh pr merge` | release manager |
| 3 | decision routing | Bash `card-ingest`; Bash `desk-snapshot` with `--answers`; an Artifact write to the Desk's card collection | chief of staff |
| 4 | intake and relay | `mcp__director__send_message`, `broadcast`, `report_status`, `set_presence`; Bash `relay-log`, `route-ledger` | project manager |
| 5 | operator support | other Artifact tools; Bash `desk-snapshot`, `dash-writer`, `board-html`, `nav-sync`, `page-namespace`, `shelf-row`, `freebusy` | chief of staff |
| 6 | custody and records | Bash `bd`, `lane-ledger`, `draft-sweep`; `mcp__director__audit_lookup`; Write, Edit | scribe |
| 7 | fleet care | Bash `marvel inject`, `marvel capture`, `marvel events`; Bash `composer-check`, `idle-shadow`, `unattached`, `standing-hook`, `sweep-firings`, `schedule-ack` | engineering manager |
| 8 | fleet reads | `mcp__director__list_roster`; Bash `marvel get`, `marvel describe`; Bash `dsi`, `dsx`, `dws`, `dbi`, `vector-pin-drift` | engineering manager |
| 9 | director other | everything else, Read and Grep among it, with the Read and Grep share printed as a sub-line | none assigned |

The role names are a mapping aid for leadership, not a claim that the work was worth its cost. No salary or hours comparator is shown: the audit joins cost to no outcome, and a comparator would become the headline.

Three draft classes did not survive:

- **capture** folded into fleet care, because nobody could sign it apart from `marvel capture`;
- **investigation** folded into director other (3-2), because Read and Grep appear in every class;
- **scheduling** folded into fleet care (4-1).

## 3. Director's waiting

The director test runs before the hub-wait test (4-1), so director's own pure-wait requests move from the fleet hub-wait row into the waiting class above. That changes a published figure, so the audit prints:

- the old hub-wait and director rows unchanged, as memo lines;
- the new rows;
- a bridge row naming the dollars moved.

The new rows plus the bridge equal the old rows exactly.

## 4. Unit costs

Each unit cost is published only when its denominator has a recorded source. A missing source prints "n/a" with the source named, so "no source" never reads as zero.

| unit | denominator source | first re-run |
|---|---|---|
| per relay | `relay-log` entries (R-n, clock-stamped) | ships |
| per merge | the audit's GitHub-side merged-PR list, with the source named; `merge-guard` keeps no log of its own | ships, window matched to the audit's (5-0) |
| per operator decision | `desk-snapshot` answered cards and their `answered_at` (and `desk-reads.jsonl`) | ships with the decision-routing class (see 7, open item) |
| per ask closed | `asks.jsonl` (`ask_id`, `received_at`) joined to the workstream ledger's `closed_at` by ask id; asks.jsonl has no closure field of its own | ships only if the join is built in the same change, else "pending" (5-0) |
| per stall cleared | none; nothing records a stall cleared | "n/a" |

Each unit is reported per class and month over month, never as a target and never as a gate.

## 5. Presentation in the audit

- Director stays inside coordination, split by the classes above.
- The old director-row and coordination-overhead figures stay printed beside the split, so the reframing changes no total.
- The October audit is re-run on this taxonomy, so October and November compare on one definition. A later change to the table re-runs the earlier month the same way.
- The panel recommends, to the audit's author, that the comparison with surveys of human working time be labelled an analogy, not a measurement: it sets a share of time against a share of token spend. The report is not this design's to edit.

## 6. Slices

These are proposed as flat tickets with blocks edges. None is filed. Each red test is named here and written in its slice's PR.

| # | slice | red test (fails today because) | blocked by |
|---|---|---|---|
| 1 | critic: a `director_sig` kind in the map file, the first-match table, the version printed | a fixture request per row lands in its class; reordering the table changes the assertion (no such kind) | none |
| 2 | critic: text-only inheritance, next else previous, with the inherited share by direction | a text-only request between `merge-guard` and `send_message` takes merge gating; a leading text-only request takes the previous class or the residual (no inheritance) | 1 |
| 3 | critic: the director test before the hub-wait test, with the memo and bridge rows | new rows plus bridge equal old rows to the micro-dollar (director waits sit in hub wait) | 1 |
| 4 | critic: the unit-cost table with named sources and "n/a" or "pending" | a zero or missing denominator prints "n/a" with its source, never 0 (no table) | 1 |
| 5 | critic: the asks-to-ledger join for per ask closed | the join fixture yields the closed count; without it the cell prints "pending" (no join) | 4 |

The audit re-runs on both months follow slices 1 to 4. They are the research seat's, not this design's.

## 7. Votes, dissent and open items

| item | outcome | tally | dissent |
|---|---|---|---|
| investigation | folded into director other | 3-2 | two panelists wanted it as a low-confidence class |
| decision routing | its own class with a per-decision unit | 4-1 | one panelist would fold it into operator support |
| scheduling | folded into fleet care | 4-1 | one panelist wanted it as a class |
| inheritance | next, else previous | 4-1 | one panelist: previous only |
| `audit_lookup` | custody and records | 4-1 | one panelist: fleet reads |
| `marvel events` | fleet care | 3-2 | two panelists: fleet reads |
| director's waiting | moves into director's breakdown, with a bridge row | 4-1 | one panelist: stays in the fleet row, since moving it shrinks a published churn figure |
| merge unit | ships from the GitHub-side list | 5-0 | none |
| ask-closed unit | ships only with the join | 5-0 | none |

Open item: in round 2, before the Desk snapshot's `answered_at` was checked, the panel settled that the per-decision unit prints "n/a". The round 3 vote for a decision-routing class with that unit came after the check. This design reads the later vote as superseding the earlier one for that unit, and says so here rather than deciding it quietly.

The signatures are pending a check that both months' transcripts carry what the rules read (the Bash command, the script names as written, the Artifact tool names). The research seat is running that check, and a row it finds unreadable will be named before the classifier slice is built.

## 8. Recommendation

Build slices 1 to 4, then re-run October and November on the new taxonomy. This recommendation is valid until 2026-11-26, or until the applicability check reports, whichever comes first. The arcaven architect seat that chaired the party re-checks it then. Nothing here takes effect on silence.

## 9. Prior art

- critic `sim/tools/ledger-s10-axes.py`: the classifier and today's director rule.
- `skills/director/SKILL.md`: the standing sweep, whose steps these classes cover.
- `skills/director/scripts/relay-log`, `desk-snapshot`, `merge-guard`, `dws`: the denominator sources.
