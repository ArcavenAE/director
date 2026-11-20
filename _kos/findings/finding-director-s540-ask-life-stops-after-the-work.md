# finding-director-s540: most old asks stopped after the work, at a back half of the ask's life that the registers did not record until 2026-11-10

- **Date:** 2026-11-20
- **Session:** read-only probe for the operator, in response to "a mystery pattern of these old asks"
- **Subject:** the life of an operator ask in director, from intake to close
- **Confidence:** read-only and partial. 118 asks were traced, sampled from all eight batches of the open-ask triage, received from 2026-10-28 to 2026-11-20: 89 from before 2026-11-10 and 29 from after. The per-ask traces are in a private appendix.
- **Not in scope:** any design, any tool, and where this finding finally lands. The operator rules on what comes next.

## 0. The sentence

**Intake and relay worked for most asks, and most then did the work. Until
2026-11-10 the ask register recorded no close event and the relay log no
answer kind, so done work had nowhere to be recorded as done. The code for
both merged on 2026-11-09, and each was first recorded on 2026-11-10. Once those
records existed they were used: 199 of 293 later asks closed before the
triage, at a median of half an hour. The asks that still stay open stop in
two main places. Before 2026-11-10 the commonest stop was a residual piece
of a multi-part ask. After it, the commonest stop is a Desk ruling the
operator has not yet made. Answers can also stop after the operator: some
rulings were made on the Desk and never relayed back. Today's asks show a
further stop before the work, where an accepted ask can sit unread on an
idle seat until a supervisor rings it.**

## 1. What was measured

The ground truth is the open-ask triage of 2026-11-20. It classed the 399
asks still open that day (received 2026-10-28 to 2026-11-20) into five
classes:

- done: 202
- answered by a Desk card: 14
- still in flight: 127
- dropped: 10
- superseded, or not an ask: 46

Sample: 118 asks, drawn by a fixed stride over ask number so that no ask
was hand-picked:
- every ask in the card and dropped classes;
- every third in flight;
- every seventh done;
- every third superseded.

The strides ran over the first three batches together, then within each of
the five later batches. The sample is 32 done, 14 card, 44 in flight, 10
dropped and 18 superseded.

**A selection effect.** The triage holds only asks that were still open on
2026-11-20. A sampled done ask was therefore closed late by construction,
and the sample cannot say how many asks closed on time. Section 2a measures
that over the whole ask register instead.

Each ask was traced across the registers this seat can read:
- the ask register (intake and close events);
- the relay log (the structured intake and kind fields on each entry);
- the dws ledger;
- the Desk snapshot;
- board.md;
- bd.

The Reply Shelf was read from a full export taken at 00:42Z on
2026-11-20, which holds the rows written for the first three batches only.
Seat-to-director answers travel on the bus, and director keeps no store of
them beyond the relay log. The streams cannot be exported by ask, so the
bus is a measured gap: before 2026-11-10 an answer left no record except
the relay log's free text.

## 2. Where each open ask stopped

The 86 sampled asks outside the done class, by where the triage found them
stopped:

| where it stopped | before 2026-11-10 (62) | after (24) |
|---|---|---|
| A residual piece of a multi-part ask: a verification step, a last ticket, one PR of a set | 19 | 5 |
| Waiting on the operator: an open Desk card, a merge or an action at a host | 4 | 8 |
| Answered on the Desk, but the ruling was never relayed back | 3 | 0 |
| A card answered and relayed; only the close was left | 3 | 0 |
| The next phase never started or never ruled | 4 | 0 |
| A safety classifier refused the work, and nothing rerouted it | 6 | 1 |
| A seat or route never reported back | 4 | 2 |
| Never routed, or director owed an answer or a card | 5 | 3 |
| Recent and moving normally | 0 | 1 |
| A reply, a correction or an umbrella, logged as an ask and folded into another | 14 | 4 |

## 2a. The back half, over the whole ask register

| received | asks | closed before 2026-11-20 | closed only by the triage | still open |
|---|---|---|---|---|
| before 2026-11-10 02:35Z | 359 | 52 | 174 | 133 |
| after | 293 | 199 | 38 | 56 |

The split is at 2026-11-10 02:35Z, the first recorded close event. After it
the median time from intake to close was 0.5 hours, and
the 90th percentile 25 hours. The close record works when it exists. The
38 later asks closed only by the triage were done work whose close was not
written.

**Answers that stopped after the operator.** A Desk re-sync at 00:53Z on
2026-11-20 moved 133 answered cards off "open". In the same pass, seven
rulings the operator had made on 2026-11-13 were relayed for the first
time.

## 3. The failure points, counted against the candidates

Counts are out of the 118 traced asks unless stated. "Not testable" means
the asks predate the record the candidate needs.

| candidate | count | note |
|---|---|---|
| A seat finished but never reported back | 6 of 86 open | 4 before 2026-11-10, 2 after. |
| Director got the answer but never closed it | before 2026-11-10: no close event was recorded; after: 38 of 293 | Section 2a. The close event's code merged on 2026-11-09; the first close was recorded at 2026-11-10 02:35:13Z. |
| The answer reached the operator only in scrollback | not testable for most | The answer kind was first recorded on 2026-11-10, its code having merged on 2026-11-09; 1 of 29 later sampled asks has an answer-kind relay. The Reply Shelf held 8 rows before the triage, all read by the operator within 2 to 89 minutes, then got no new row for four days. |
| The ask id did not travel with the work | 36 of 118 | No relay tagged with the id. The structured intake field was first recorded on 2026-10-28 19:23Z, its code having merged on 2026-10-27. The relay log holds 9, 4, 0 and 0 entries on 2026-11-05 to 2026-11-08, against hundreds a day around them, and 17 of the 29 untagged earlier asks fall in that window. |
| The ask was a reply with no forward edge | 174 of 399 classed | Reply-shaped by the text rule below: 124 of 307 before 2026-11-10, 50 of 92 after. |
| Director's compaction dropped a pending item | not testable | No register records it. The cases where director owed an answer or a card are consistent with it. |
| A bd task closed without the ask | not seen | 23 of 118 asks appear in bd at all. |
| A seat restarted and lost the thread | 1 consistent | A route to a seat that never received it. |

The text rule for reply-shaped asks, as Python:

```python
re.match(r"""^\s*(\d+[ .:)]|yes\b|no\b|operator\b|["'\u201c\u2018])""", text, re.I)
```

After leading space, the text opens with a number followed by a space,
period, colon or parenthesis, the word yes, no or Operator, a straight quote,
or a curly opening quote (U+201C or U+2018), case-insensitive.

Four causes not on the candidate list carry most of the open asks:

- **Residual parts.** 24 of 86 open asks are mostly done, with one part
  open. An ask is one row however many parts it has, so a done majority and
  an open remainder look the same as untouched work.
- **Waiting on the operator.** 8 of the 24 later open asks wait on a Desk
  card, a merge or a host action. After 2026-11-10 this is the commonest
  stop.
- **Rulings not relayed.** 3 sampled asks were answered on the Desk and
  never relayed back, and the re-sync in section 2a found seven more.
- **Refusals with no owner.** 7 of 86 open asks stopped at a safety
  classifier. Nothing records who reroutes refused work.

## 3a. A stop in transit, seen live on 2026-11-19

The triage sample shows where asks ended. One supervisor's ring log from
2026-11-19 shows a stop that applies to today's asks: an ask is accepted
onto a seat's inbox, the seat takes no turn, and only a supervisor's ring
moves it.

- From 20:20Z to 00:37Z: 10 ring events covering about 15 seat rings.
- Each rung seat was idle with an empty or dim composer, and its asks had
  been unread for 15 to 50 minutes.
- One of the rung seats, the one running this probe, was rung twice in that
  window. Each time a new ask had been waiting 15 to 16 minutes, after the
  seat had ended its turn idle under its role's end-of-turn rule. Its inbox
  summary reports its wake cue as unverified, with no push-only waits.

In the five-step life of an ask (intake, relay, the seat's turn, the work,
the answer and close), this is a stop at the seat's turn. An accepted send
is not a read one, as the bus itself says. finding-director-gmah found the
same wait from the reader's side: unread mail waits on a seat with no cue,
and nothing alarms on its age. The triage sample cannot show
this stop: an ask that waited 50 minutes and was then done reads as done.

## 4. Where one item lives

In the sample of 118:

| register | asks present |
|---|---|
| relay log, tagged with the ask id | 82 |
| relay log, tagged or mentioned | 93 |
| dws ledger, answered_at set | 13 |
| board.md | 15 |
| Desk | 20, of them 13 of the 14 card-class asks |
| bd | 23 |
| ask register close | 36, all written on 2026-11-20 by the triage |

The relay log is the only register that holds most asks. The others each
hold a different small slice. For asks older than 2026-11-10, no register
holds the step after the answer.

The Reply Shelf as a whole held 128 rows at the export. Eight were written
on 2026-11-16, and the operator read all eight within 2 to 89 minutes. No
further row was written until 2026-11-20, when director wrote 6 and the
triage 114. Where a row existed, the answer reached the operator. What
stopped was writing the rows.

## 5. What this does not yet show

- The later asks are 29 of the 118 traced, so their stop counts are small.
- The Reply Shelf export predates the rows the triage wrote for batches 4
  to 8, so the shelf counts cover the first three batches only.
- The reply heuristic is a text pattern, not a judgment. It may count a
  short new request as a reply.
- The bus was not read, because it cannot be exported by ask. "Never
  reported back" therefore counts what the files show, and a seat may have
  answered on the bus with no file record.
- The relay log's thin days, 2026-11-05 to 2026-11-08, were counted, not
  explained.

## 6. Frontier question

See `question-director-ask-life-after-the-work.yaml`: now that the answer
and close records exist, where do the asks that stay open stop, and is a
multi-part ask one item or several?
