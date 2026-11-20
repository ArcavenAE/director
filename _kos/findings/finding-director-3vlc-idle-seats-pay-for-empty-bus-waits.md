# finding-director-3vlc: a seat with nothing to do can keep paying for turns, about 55 an hour, while it waits for mail that does not come

- **Date:** 2026-11-20
- **Session:** read-only probe of one operator ask (the ask id is kept in the private appendix)
- **Subject:** the end-of-turn bus wait in director-mcp, and how it combines with the role rules that end every turn on a wait
- **Objects:** the wardrobe role texts (the end-of-turn rule), marvel (seat lifecycle), the Claude Code harness (a long tool call moves to the background)
- **Confidence:** measured from transcript structure on one host (kinu), 2026-10-25 to 2026-11-20, plus one other host's counts relayed by its supervisor. No seat was started, no loop was run, nothing was published. The id is minted at filing (`kos id finding`).

## 0. The sentence

**The "runaway loop" that prompted this probe was an idle seat behaving as designed. Its role told it to end every turn on a bus wait. Each empty wait cost about two requests, so a seat with nothing to do spent about 55 requests an hour, around the clock, for days. Before 2026-11-11 this idle pattern took about 14% of the host's cache-read, and about 12% over the whole period measured (the table in section 2). Since the rulings of 2026-11-11 it is down about 92%, so it is no longer a large cost. What is still missing is any instrument that names an idle seat within a day.**

## 1. The problem

A seat that ends each turn on `wait_for_message` and keeps waiting while the bus stays empty takes turns that do nothing. With a 120 s wait the harness moves the call to the background. The seat then pays twice per idle cycle: once for the call and once for the background-completion notice. On a large context each turn reads the whole cached prompt, so idle turns are cheap per call but run all day.

The case behind the ask appears first in a dispatch brief as "a hand-started session, 55 turns an hour, 103M cache-read in 6 hours". It was a brief to seats, not a report, and no record holds a table behind it, so the figures were an unverified claim until now. Matched against the transcripts:

- One session, in a project directory of another orchestrator, matches it in the recorded window, with 54 to 56 requests an hour from 21Z to 01Z. Each of those hours has about 28 waits, all empty, about 27 of them moved to the background, no mail and no other tool call. It read 14.6M to 18.1M cache-read an hour, about 100M per 6 hours.
- That session was a marvel seat, not a hand-started one. The marvel daemon log records it as cast on 2026-09-26 and deleted on 2026-11-05. So the brief's "hand-started" was wrong. Its rate and its 6-hour total match this seat's measured hours.
- It and a sibling seat idled this way from the start of the data (2026-10-25) until both were deleted. Together that was 223 idle hours and 3.55B cache-read. The deletion came 3.5 days after the report.

## 2. What separates an idle loop from a busy seat

Each session-hour was put in one of four classes. All hours with at least 10 requests:

- **idle-wait:** at least 10 waits, at most 2 waits that brought mail, at most 10 other tool calls.
- **coordinating:** at least 10 waits, and either more mail or more tool calls than idle-wait.
- **working:** fewer than 10 waits.
- **wait-plus-tools:** at most 2 waits with mail, but more than 10 other tool calls.

| class | session-hours | requests/hour | waits that brought mail/hour | other tools/hour | requests per waits that brought mail | share of cache-read |
|---|---|---|---|---|---|---|
| coordinating | 2,432 | 86.9 | 25.29 | 54.0 | 3.4 | 70.3% |
| working | 1,996 | 30.7 | 2.40 | 28.4 | 12.8 | 15.2% |
| idle-wait | 658 | 55.7 | 0.23 | 1.6 | 244 | 11.9% |

(wait-plus-tools, 97 hours, 1.9%, and hours under 10 requests, 0.7%, are left out.)

Requests per hour alone does not separate the classes: a coordinating seat runs faster than an idle one. Two measures do, by about two orders of magnitude:

- **requests per waits that brought mail:** 244 for idle against 3.4 for coordinating;
- **non-wait tool calls per hour:** 1.6 for idle against 54.0 for coordinating.

Of the idle hours' 20,398 waits, 20,129 returned empty. A third useful signal is the run length: the number of consecutive idle-wait hours in a session.

## 3. Base rate and cost

Kinu, from transcript structure:

| window | idle-wait hours | requests per day | cache-read per day | cost per day at $0.063 to $0.070 per poll |
|---|---|---|---|---|
| 2026-10-25 to 11-10 (17 days) | 613 | 2,030 | 577M | $128 to $142 |
| 2026-11-11 to 11-19 (9 days) | 45 | 235 | 44M | $15 to $16 |

- Across the whole period there were 169 idle-wait runs: 77 of 3 hours or more, 33 of 6 or more, 9 of 12 or more, and 2 of 24 or more. The longest was 41 hours.
- The drop starts on 2026-11-11, the day the end-of-turn wait went to 110 s and the cue rule replaced the blocking wait. The timing points to those rulings as the cause. It is not proven, because seats also moved between hosts in the same weeks.
- After 2026-11-11, three sessions account for all of the idle-wait hours, 45 in total.
- The other host's relayed counts (35 seats, 24 hours) show the same shape on two seats:
  - one seat had 229 empty returns in 649 turns, and 31.6M of its 99.4M cache-read came on the turn right after an empty return;
  - another had 121 empty returns, and 32.4M of its 595.8M came right after one.
  - Most of the other seats had none.

## 4. Causes, as candidates

| candidate | evidence for | evidence against or open |
|---|---|---|
| A role rule that ends every turn on a wait, with no cap on empty returns (the supervisor rule's "call it again and keep a count") | The relayed seat names that rule as its reason for waiting again. The idle hours are supervisor and architect seats. | The count feeds an idle-planner query, not a stop. Roles that end idle after a capped number of empty waits (this probe's own seat: five, about 10 minutes) show 1 to 2 idle hours in the window. |
| A 120 s wait moved to the background, costing two requests per cycle | 14,502 of the 20,129 empty waits in the idle hours were backgrounded. 55.7 requests an hour is about two requests per 2-minute cycle. | The 110 s ruling removed the background step. After it, the idle requests per day fell 88%. |
| A seat left running with no work and no owner | The matched seat idled for 11 days or more until it was deleted. | This is about seat lifecycle, not the bus. marvel saw the seat as running and healthy throughout. |
| A cue that does not wake, so the seat falls back to blocking waits | On 2026-11-18 and again on 2026-11-20 at 00:37Z, this probe's own seat's `inbox_summary` reported its cue as unverified ("no nonce echo after self-test"). | Not measured per seat. The contrast case (below) is the opposite failure. |
| A cue storm, or mail that never comes | Not seen: the idle hours show 0.23 waits with mail per hour. | |
| A restart that loses its state | Not seen in this data. | |

**The contrast case: seats that do not wake.** The same evening's supervisor notes record 10 ring events (20:20Z to 00:37Z, about 15 seat rings). In each, a seat was idle with unread asks for up to about 50 minutes. That is the cost the other way. A rule that stops idle turns has to keep a way to wake the seat, or work waits instead.

## 5. Detection map: which instrument would have named the matched seat, and when

| instrument | reads | would it name the seat | how late |
|---|---|---|---|
| director-mcp (the shim) | every wait it serves | No. It counts only cue push-only waits, in memory per process, and never saves or publishes the count. | never |
| introspect (director sweep, daily; weekly at harvest) | transcripts in its configured projects | No. It has no wait or turns-per-hour tally; it reads the right data but asks a different question. | never as built |
| critic's token ledger (by hand, monthly) | all transcripts, including sessions marvel does not manage; it has a "hub wait (bus polling)" class | Yes, as a class total and per session in dollars. | at month end, so up to 30 days |
| token-topn (by hand, one day) | all transcripts; classes each wait result as delivered, empty or backgrounded | Partly. It counts empties day-wide and ranks sessions by tokens, but not empties per session. | a day, if someone runs it |
| freebusy, idle-shadow (director sweep) | marvel session state, bus presence, ready work | No. An idle-waiting seat shows as free or busy. | never |
| `marvel get budgets` | marvel's usage meter, teams only | No. It shows team spend against a limit, not idle spend, and it does not cover unmanaged sessions. | never |

The matched seat was found by a hand-run inventory about 8 days after the start of the data, and it ran 3.5 days more after that.

## 6. Frontier question

**question-director-idle-wait-spend:** Now that idle-wait spend is down to about $15 a day on kinu, is a daily, after-the-fact tally of consecutive idle-wait hours per session worth adding? It would count requests per waits that brought mail and non-wait tools per hour, run in a script that already reads transcripts, and spend no model tokens. And does the supervisor rule's uncapped re-wait need a cap like the other roles have?

Options are not designed here; they come after this finding is reviewed. Each option will state its standing token cost per day (target zero), its expected saving (base rate times the cost of one idle run before it is named) and its break-even against doing nothing. On this data, doing nothing now costs about $15 a day. One idle run of the matched kind, left for a week or more before anyone saw it, cost about $84 to $94 a day per seat (about 1,335 requests a day).
