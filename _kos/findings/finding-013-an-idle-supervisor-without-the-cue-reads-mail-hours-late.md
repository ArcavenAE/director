# finding-013: an idle supervisor seat without the channel cue reads its mail hours late, and the delay is invisible to the sender

- **Date:** 2026-10-02
- **Session:** the errand team's supervisor on mokuzai, errand harvest
- **Subject:** receive latency on an idle Claude seat that has no cue
- **Confidence:** three measured delays on one seat over one day; the mechanism is the one #166 already names

## 0. The sentence

**On a seat that is idle and not cued, a message waits until something else
wakes the seat, so the receive latency is set by the next unrelated wake, not by
the bus, and between 2026-10-01 and 2026-10-02 that was 4 to 21 hours.**

## 1. What was measured

My own seat, a supervisor on mokuzai, between 2026-10-01 and 2026-10-02. Each
row is a message addressed to me; "read" is when my seat drained it.

| message | sent (UTC) | read (UTC) | delay | what woke the seat |
|---|---|---|---|---|
| researcher: research PR done | 10-01 02:11:38 | 10-01 06:30 | 4h18m | a scheduled timer in the session |
| researcher: second PR done, proof torn down | 10-01 06:57:55 | 10-02 03:55 | about 21h | the operator typing into the pane |
| director relay: review result needing action | 10-01 12:53:36 | 10-02 03:55 | about 15h | the same |

Each send returned "accepted for delivery". The messages were not lost: all
three were waiting, in order, when the seat drained. Nothing on the sender's
side showed that they had not been read, so the director reported the work as
routed while it sat unread.

## 2. Mechanism

`wait_for_message` is a poll. A Claude seat that has finished its turn makes no
calls until a user turn or a scheduled prompt arrives. I had told my workers to
stop and not poll, which is the right instruction for their spend, and I had no
standing poll of my own. The P1 channel cue (#166, built in #171 and #175)
exists for exactly this case, but it is opt-in through `DIRECTOR_CUE=1` at cast
time, and none of the director-mcp processes on mokuzai carry it (checked with
`ps eww` per process on 2026-10-02: 0 of 18 running shims set `DIRECTOR_CUE`,
while all 18 show `DIRECTOR_AGENT_ID`, so the environment read worked).

## 3. What this adds to #166

#166 argued the need. This is a measured cost on a seat that runs without the
cue: the 4h delay came from an unrelated timer, and the two longer delays ended
only because a human looked. A seat with no timer and no human would not have
read them at all.

It also shows the sender's blind spot. R-08 (accepted is not delivered) makes
the sender say so, but in practice "routed" was read as "in hand". A cheap
signal would be the receipt the cue design already defines (`cue.unanswered`
after W), surfaced to the sender, or a per-recipient "oldest unread" age that
`inbox_summary` could return to the sender's side.

## 4. What I changed on my side

Nothing structural yet. Until the cue is turned on for mokuzai seats, a
supervisor that dispatches long work should arm its own scheduled check (a
session timer) for when the work is due, not rely on the worker's reply to wake
it.
