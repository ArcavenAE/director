# finding-director-gmah: unread mail waits on seats that have no cue, and nothing reads its age

- **Date:** 2026-11-05
- **Subject:** director: how a seat is woken by mail, and how a stalled inbox is surfaced
- **Asked by:** the operator, through director, 2026-11-05
- **Confidence:** read-only. Code read at director origin/main 7c8ef64. Seats observed on one host (corporate), from the roster, process command lines and pane captures; seats on kinu were seen only through the roster. No seat was rung, restarted or recast.

## 0. The sentence

**Six supervisor seats held 29 to 99 hours of unread mail because none of them was launched with the channel cue, so mail could not start a turn. The one reading that could see it, `oldest_pending_s`, is published on the roster but has no reader with a threshold. The `stalled` judgement that does have a threshold is computed only for seats with the cue on.**

## 1. What was observed

At 02:52Z on 2026-11-05, the roster showed six supervisor seats with `oldest_pending_s` between 110130 and 360930 seconds: four on corporate and two on kinu. Every one of them was idle, with no `cue` field in its row. Every seat on corporate whose row carried `cue` was at or near zero, except one envoy.

On corporate the four seats were read directly:

| Class | Seats | Evidence |
|---|---|---|
| No cue; the last turn ended after a prompted drain or report, with no wait open | 3 | The pane shows an ended turn ("Say if you want me to drain them now"; "the other 24 messages ... were for other seats"), compacted while idle, and an empty prompt |
| No cue; never got a first prompt | 1 | The pane shows only the start banner and the placeholder prompt. The heartbeat has never run a turn |
| Cue on; the turn ended on a question asked in the pane | 1 (the envoy, the operator's case 6) | The row reads `cue: stalled`, "no wait open", 42067 s |

None of the four corporate seats had `--dangerously-load-development-channels server:director` on its claude command line, and none had `DIRECTOR_CUE` in its shim environment. The director MCP server was running under each of them as a tool server, so the seats could send and read mail when asked, but mail could not start a turn.

Three of the four belong to a team whose seats appear superseded by seats on other hosts. One of them had told its supervisor it was being retired, but the manifest was never changed, so those seats still receive `global://corporate/supervisor` fan-out.

Ruled out on corporate:
- a wrong cwd (each matches its manifest);
- a logout;
- a dialog or dev-channels prompt;
- the "Update installed" banner, which shows on stuck and working seats alike.

The two kinu seats were not read beyond the roster, and their class is open.

## 2. Why the seats were not woken

The cue is opt-in per seat, by the operator's ruling of 2026-10-01. `cast-launch` adds the channel flag only when the role's environment has `DIRECTOR_CUE=1` (the block that begins "The channel cue (sim/design/channel-cue.md, C-5)"). On corporate, only two of the host's manifests set it. A supervisor cast from any other manifest gets the bus as tools, a global supervisor address and fan-out mail, but no way for that mail to start a turn. It reads only when someone types into its pane.

## 3. Why nothing surfaced it

- **The roster's own reading has no judge.** `presenceView` (cmd/director-mcp/bus.go, from line 1459) always writes `oldest_pending_s`, renewed on the shim's timer even when the model is stuck. It computes `cue` and the `stalled` reason only when `cueState` is set, which happens only with the cue on. `list_roster` flags a missing age (`pending-age-unreported`), never a large one.
- **freebusy reads the local tier only, prints a count, and runs only in a sweep.** It calls `director-mcp unread --json --older-than` without `--global` (skills/director/scripts/freebusy, near line 552), so mail on the hub streams is not counted. Its output is one number with no seat names, and it runs only as step 3 of a director sweep, inside a director turn.
- **introspect has no pending-age band.** Its `asks` band counts REQUESTs on the local audit that are still "sent", and those leave "sent" after 72 h, so the oldest case here had aged out. Its drain lag is computed from mail already drained, which leaves undrained mail out by construction.
- **director#486** adds `expired_unread` to an empty poll, but only the stuck seat itself would see it, and a seat that takes no turn never polls.
- **marvel's watchdog** does not read director's bus, by design, to keep the components independent. Its quiet-pane test does not see an idle claude seat (marvel#801).
- **A supervisor that does notice cannot always ring.** In case 6, the envoy's supervisor tried a ring by inject, granted by director, and its own classifier denied it.

finding-director-a53a and finding-director-gnr2 named the parent cause: a standing duty with no cue of its own falls away, and a seat cannot check itself from a state where it takes no turn. This is the same pair, seen from the receiving side. a53a's fix 2 (the drain on the tick, aae-orc-d1xwd) and gnr2's seat doctor (aae-orc-wria0) are both still open. aae-orc-0nniu is about idle compaction, not about seats holding unread mail, and nothing else tracks this.

## 4. Recommendation

**Give director one outside reading of pending age that depends on no stuck seat.** The director-mcp developer owns it.

- `list_roster` and `director-mcp unread` flag any live row, local or global, whose `oldest_pending_s` is over a bound. A starting value is twice the cue's stall window, so it matches `stalled`.
- freebusy passes `--global` and names each flagged seat with its age, not a count.
- The reading runs in the sweep now, and on the K20 tick once a53a decision 1 is ruled.

It is a message to director and the seat's supervisor, never an action, as R-113 requires: automation reminds and proposes, and people judge.

Why this one: it covers all six seats and case 6, whatever their wake path, because the data is already on the roster and renewed by the shim. Turning the cue on for every supervisor would have covered four of the six at most. It would not have covered the envoy, which had the cue and stalled anyway. And it would change an operator ruling, so it goes to the operator as a separate question, not as this recommendation.

Two operator questions come out of this, and neither is part of the recommendation:
1. Should supervisor-type seats get the cue by default, overturning the opt-in ruling of 2026-10-01?
2. Should the superseded corporate seats be retired, ending their fan-out?

This recommendation is valid until 2026-11-12. The research-supervisor seat re-checks it then, or sooner if the two kinu seats turn out to be a class it does not cover.

## 5. Gaps

- `sim/notes/friction.md`, cited for the 2026-11-04 dev-channels case, is not on director origin/main, so it was not read.
- The kinu seats were not captured. That is the kinu supervisor's read, or director's.
- Whether mail past 72 h expired unread on these seats was not checked (director#486 notes AGENT_INBOX max age as 72 h).

Evidence is held by the research-supervisor seat and is not published: pane captures, the roster read and process command lines.
