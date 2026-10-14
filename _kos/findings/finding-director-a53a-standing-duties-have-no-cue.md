# finding-director-a53a: director's standing duties have no cue of their own, so a fast run of operator questions crowds them out

Date: 2026-10-14. Subject: director, the hand-played skill and the software it
must become. Status: cause agreed 3-0 by a three-round design party; fixes
ranked, none applied. The operator rules on the fixes; this finding recommends.

## What happened

On 2026-10-14 the session playing director went 2 hours 19 minutes without
draining its inbox while it answered a fast run of operator questions. 55
messages piled up, among them a merge-breach report, an escalation about a
half-applied change, and six merge recommendations. The same day an answered
decision card was found about 20 hours after the operator answered it, a
published artifact and the work-stream ledger were found to have had no owner
for several days, and a ruling was relayed only after the pull requests it was
about had merged. Separately, under the same load, director sent a stub to its
own address, claimed review authority the operator had not given, and filed two
notes in the wrong place.

The evidence is the director's session notes (gitignored, five observations
dated 2026-10-14 and one more the same day), its relay log, and the operator's
own question asking why this happens.

## The cause

**Standing duties have no cue of their own.** Draining the inbox, keeping the
ledger, re-checking state and reading answered cards all run only inside turns
the operator starts. When questions come fast, every turn goes to an answer, and
the duties with no cue fall away. Three facts back this. The first two can be checked from this repo with one command each; the third is an observation from the session notes:

1. **The skill never names the drain.** `grep -ci drain` over
   `skills/director/SKILL.md` and `reference/` returns 0. Its standing-mode
   triggers depend on the human asking or on judged elapsed time
   (`SKILL.md:96-102`: "Re-sweep when the human asks what is in flight, and on
   your own after any stretch ..."). The one duty that held under the same load
   is the one tied to an act: "the guard runs before every merge"
   (`SKILL.md:105-108`).
2. **The receive path is a poll.** The director inbox tool's own description
   reads: "This is a poll: the model must call it. The server cannot push a
   message into context on its own." R-106 keeps messages durable, but a message
   that is kept is not one that is read.
3. **Director has never run a standing loop** (observed in the gitignored session notes and relay log; not reproducible from this repo). Every loop it ran was a watch on
   one task, started and cancelled by hand. The first was a session-only cron
   that expired after seven days and was built to stop itself after two quiet
   hours (2026-09-18). Later loops were stood down on the operator's order on
   2026-10-09 and again on 2026-10-14. None of them drained the inbox.

Context pressure is an accelerant, inferred and not measured.

## The nine misses, their class, and which fix cuts each

The eight misses above (the artifact and the ledger counted as one unowned record, the two misfiled notes as two), plus one near miss, make nine:

| # | miss | class | cut by fix |
|---|---|---|---|
| 1 | the inbox went unread for 2 h 19 min under load | omission | 1, 2, 3 |
| 2 | an answered decision card was found about 20 h late | omission | 1, 2, 3 |
| 3 | a published artifact and the work-stream ledger had no owner for days | omission | 2 |
| 4 | a ruling was relayed after the pull requests it concerned had merged | omission | 2, 3 |
| 5 | a stub with placeholder text was sent to director's own address | slip under rush | 5 |
| 6 | director claimed review authority the operator had not given | slip under rush | 1 |
| 7 | observations were written into the relay log | slip under rush | 1, 4 |
| 8 | a relayed note was attributed to the wrong place | slip under rush | 1, 4 |
| 9 | a seat's composer held a dimmed suggestion one Enter would have submitted; caught by hand | near miss | 4 |

Omissions need a cue outside the answer; slips need a check inside it. Fix 1 cuts row 2 through line 1 of the checklist (answered cards read), and rows 6 to 8 through lines 2 and 3.

No single fix covers both classes, which is why a loop alone, or a checklist
alone, would leave half of them open.

## Fixes, ranked by misses cut (out of nine)

| rank | fix | kind | misses cut |
|---|---|---|---|
| 1 | a hook on every operator prompt that puts two things in front of the answer: the drain age, read from a heartbeat line the session writes after each drain, and the checklist below | now: skill plus a hook script | 5 |
| 2 | K20 with K11 and K5: drain and reconcile on the tick, with a Desk reader feeding the reconciler (aae-orc-d1xwd, aae-orc-o4z6u, aae-orc-ivvpm) | build: director software | 4 |
| 3 | a tick outside the session that never types into the director pane: it writes a "drain due" marker the hook reads, and raises a missed-beat alarm | now: a scheduled job, only on an operator ruling | 3, overlapping 1 and 2 |
| 4 | a relay template (verbatim text, the standing ruling it touches, the file it goes to) and an empty-composer check before any inject with Enter, both in the skill | now: skill | 3 (rows 7, 8, 9); 1 not cut by another fix |
| 5 | R-213's compose step, so a stub never publishes | build: director software | 1 |

Counted from the table: fix 1 cuts rows 1, 2, 6, 7, 8; fix 2 rows 1 to 4; fix 3 rows 1, 2, 4; fix 4 rows 7, 8, 9; fix 5 row 5. The fixes that need no build (1, 3, 4) cover rows 1, 2, 4 and 6 to 9, which is 7 of the 9; with the build items, all 9.

**The checklist** (fix 1), as adopted 2-1:

1. Drained, and answered Desk cards read, since my last answer? If not, do both first.
2. Does this answer or relay touch a standing ruling, or claim authority? Quote the ruling.
3. Where does this text land, and does that file or seat own it?

**Rejected:** a tick that rings the director pane by typing into it. That is
the pane the operator types in, and an inject with Enter there is the same
hazard as the dimmed suggestion above.

**Not yet checked:** whether a hook can read the inbox's pending count from the
hub's read-only monitor, as R-106's evidence did. Until a seat allowed to read
the hub checks it, the hook shows drain age only, because an unchecked number in
front of every answer teaches trust in it.

## Dissent

- If the operator does not allow a standing tick (fix 3), the hook cannot fire
  when nobody types, so the overnight missed-card case stays open until K20
  ships.
- The checklist is itself uncued. It holds only while the hook puts it in front
  of the answer; on its own it would fail the way the drain rule did.
- One seat preferred the checklist without the Desk clause in line 1.

## Candidate requirements (provisional, for director's own harvest)

- **Provisional R:** a standing duty is bound to a cue that fires without an
  operator turn, and the skill names each duty with its cue. Source: this
  finding. Cross-refs R-196, and R-205 (proposed in director#290).
- **Provisional R:** director reports its own drain age, and a drain age past
  its bound is raised as an event, not found by the operator. Source: this
  finding. Cross-refs R-107, and R-216 (proposed in director#291).

## Decisions for the operator

1. Whether director may run a standing drain tick that never stops on silence,
   distinct from the per-task watches stood down on 2026-10-09 and 2026-10-14,
   with the 2026-09-18 pause-on-silence rule kept for watches.
2. Whether to adopt fixes 1, 3 and 4 into the skill.

Nothing takes effect on this finding. Its recommendations are valid until
2026-10-21; the architect seat re-checks them then.

Cross-refs: R-106, R-107, R-196; R-205 as proposed in director#290; R-213 and
R-216 as proposed in director#291; K5, K11, K20 in
`sim/design/director-functions-redesign.md` section 6.
