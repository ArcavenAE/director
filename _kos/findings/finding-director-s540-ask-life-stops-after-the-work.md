# finding-director-s540: most old asks stopped after the work, at a back half of the ask's life that the registers did not record until 2026-11-10

- **Date:** 2026-11-20
- **Session:** read-only probe for the operator, in response to "a mystery pattern of these old asks"
- **Subject:** the life of an operator ask in director, from intake to close
- **Confidence:** read-only and partial. 41 asks were traced, all received from 2026-10-28 to 2026-11-02, against the open-ask triage of the same period. The asks received after 2026-11-10 are not yet in the sample (section 5). The per-ask traces are in a private appendix.
- **Not in scope:** any design, any tool, and where this finding finally lands. The operator rules on what comes next.

## 0. The sentence

**In the oldest open asks, intake and relay worked: almost every sampled ask
reached a seat under its own id. Most then did the work. What failed is
everything after the work. Until 2026-11-10 the ask register had no close
event, and the relay log had no answer kind, so done work had nowhere to be
recorded as done. Of the asks still open, most stopped at a residual piece
of a multi-part ask, or at a reply the operator gave to an earlier
question. Today's asks show a further stop before the work: an accepted
ask can sit unread on an idle seat until a supervisor rings it.**

## 1. What was measured

The ground truth is the open-ask triage of 2026-11-20. It sorted the 150
oldest open asks (received 2026-10-28 to 2026-11-02) into five classes:

- done: 85
- answered by a Desk card: 4
- still in flight: 36
- dropped: 5
- superseded, or not an ask: 20

Sample: 41 asks, drawn by a fixed stride over ask number so that no ask was
hand-picked:
- every ask in the card and dropped classes;
- every third in flight;
- every seventh done;
- every third superseded.

Each ask was traced across the registers this seat can read:
- the ask register (intake and close events);
- the relay log (the structured intake and kind fields on each entry);
- the dws ledger;
- the Desk snapshot;
- board.md;
- bd.

The Reply Shelf was read from a full export taken at 00:42Z on
2026-11-20. Seat-to-director answers travel on the bus, and director keeps
no store of them beyond the relay log. The streams cannot be exported by
ask, so the bus is a measured gap: before 2026-11-10 an answer left no
record except the relay log's free text.

## 2. Where each ask stopped

| class | n | where it stopped |
|---|---|---|
| done | 13 | Work finished; no answer leg recorded; closed only by the triage, 18 to 23 days after intake. |
| card | 4 | Two wait on the operator's ruling. One is marked superseded, with no record of what replaced it. One was answered, but the build after the ruling was never checked. |
| in flight | 12 | Nine have a residual piece of a multi-part ask open: a verification step, a last ticket, one PR of a set. Two have a next phase never started or never ruled. One waits on an operator action. |
| dropped | 5 | Director owed the operator an answer (1). A safety classifier refused the work and nothing rerouted it (2). The work went to a seat that never received it (1). A seat never reported back (1). |
| superseded | 7 | Five were the operator's replies or corrections, logged as asks. Two were overtaken by later asks, with no link from the old one to the new. |

## 3. The failure points, counted against the candidates

Counts are out of the 41 traced asks. "Not testable" means these asks
predate the record the candidate needs.

| candidate | count | note |
|---|---|---|
| A seat finished but never reported back | 1 seen | Not testable for most: before 2026-11-10 no answer leg was recorded. |
| Director got the answer but never closed it | 13 done asks open until the triage | The close event did not exist until 2026-11-10 and was not backfilled. |
| The answer reached the operator only in scrollback | not testable for the sample; see section 4 | The answer kind starts 2026-11-10. The Reply Shelf held 8 rows before the triage, all read by the operator within 2 to 89 minutes, then got no new row for four days. |
| The ask id did not travel with the work | 6 of 41 | No relay tagged with the id; the structured intake field starts 2026-10-28 19:23Z. The cross-host case (the register lives on one host) is not seen in this sample. |
| The ask was a reply with no forward edge | 76 of 150 | Reply-shaped by a text heuristic: the text opens with a numbered item, yes, no, a quote, or "Operator". Covers all 5 dropped, 22 of 36 in flight, 12 of 20 superseded. |
| Director's compaction dropped a pending item | not testable | No register records it. 1 drop (director owed an answer) is consistent with it. |
| A bd task closed without the ask | 0 | Only 4 of 41 asks appear in bd at all. |
| A seat restarted and lost the thread | 1 consistent | The routed-but-never-received case. |

Two causes not on the candidate list carry most of the open asks:

- **Residual parts.** 9 of 12 in-flight asks are mostly done, with one part
  open. An ask is one row however many parts it has, so a done majority and
  an open remainder look the same as untouched work.
- **Refusals with no owner.** 2 of 5 drops stopped at a safety classifier.
  Nothing records who reroutes refused work.

## 3a. A stop in transit, seen live on 2026-11-19

The triage sample shows where old asks ended. One supervisor's ring log
from 2026-11-19 shows a second stop that applies to today's asks: an ask is
accepted onto a seat's inbox, the seat takes no turn, and only a
supervisor's ring moves it.

- From 20:20Z to 00:37Z: 10 ring events covering about 15 seat rings.
- Each rung seat was idle with an empty or dim composer, and its asks had
  been unread for 15 to 50 minutes.
- One of the rung seats, the one running this probe, was rung twice in that
  window. Each time a new ask had been waiting 15 to 16 minutes, after the
  seat had ended its turn idle under its role's end-of-turn rule. Its inbox
  summary reports its wake cue as unverified, with no push-only waits.

In the five-step life of an ask (intake, relay, the seat's turn, the work,
the answer and close), this is a stop at the seat's turn. An accepted send
is not a read one, as the bus itself says. The old-ask sample cannot show
this stop: an ask that waited 50 minutes and was then done reads as done.

## 4. Where one item lives

In the sample:

| register | asks present (of 41) |
|---|---|
| relay log, tagged with the ask id | 35 |
| relay log, tagged or mentioned | 36 |
| dws ledger row | 3 |
| board.md | 7 |
| Desk | 4, exactly the card class |
| bd | 4 |
| ask register close | 13, all written by the triage itself |
| Reply Shelf | 13, the same 13, all written by the triage on 2026-11-20 |

The relay log is the only register that holds most asks. The others each
hold a different small slice, and no register holds the step after the
answer for asks older than 2026-11-10.

The Reply Shelf as a whole has 128 rows. Eight were written on 2026-11-16,
and the operator read all eight within 2 to 89 minutes. No further row was
written until 2026-11-20, when director wrote 6 and the triage 114. Where a row existed, the
answer reached the operator. What stopped was writing the rows.

## 5. What this does not yet show

- These are the oldest asks, all from before 2026-11-10. They cannot tell
  whether the back half works now that it exists. Since 2026-11-10, 201 of
  290 new asks are closed, against 138 of 359 before. Most of those 138
  were closed by the triage on 2026-11-20. The later triage batches will
  test the post-2026-11-10 path, and this finding will be re-counted when
  they land.
- The reply heuristic is a text pattern, not a judgment. It may count a
  short new request as a reply.
- The bus was not read, because it cannot be exported by ask. "Never
  reported back" therefore counts what the files show, and a seat may have
  answered on the bus with no file record.

## 6. Frontier question

See `question-director-ask-life-after-the-work.yaml`: once the answer and
close records exist, where do asks stop now, and is a multi-part ask one
item or several?
