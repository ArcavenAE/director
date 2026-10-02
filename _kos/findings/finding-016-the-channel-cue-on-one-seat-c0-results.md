# finding-016: the channel cue on one seat: idle wake works, a mid-turn cue waits for the next tool boundary, plan mode cannot drain, and the startup self-test can lose a race

- **Date:** 2026-10-02
- **Session:** the arcaven builder seat, placing the 2026-10-02 team harvest
- **Subject:** C-0 of sim/design/channel-cue.md, measured on opted-in arcaven seats
- **Confidence:** measured on live seats by the team supervisor, plus one seat's own report; small samples (the self-test race was seen once in two starts)

## 0. The sentence

**The cue wakes an idle Claude seat in about a second, and nothing it sends
is lost; but a cue sent mid-turn is held until the next tool boundary, and
the startup self-test can fire before the harness has registered the
channel.**

## 1. What was measured, against the section 4 list

| C-0 item | Result |
|---|---|
| 1. Idle wake, real handshake | Pass at protocol `2025-11-25`: about 1.2 s from send to cue. `2025-06-18` was not reported. |
| 2. A notice mid-turn | Held, not dropped. It arrives attached to the next tool result; one seat saw it about 69 s after the send, during a long tool call, then a new turn. |
| 3. Plan mode and the permission prompt | Fail to drain. The cue wakes the seat, but `inbox_summary` meets an MCP permission prompt and the model declines `wait_for_message`. |
| 4. A burst of 30 | One cue with `count=30`, not 30 turns. Whether the 30 drained in order is not reported. |
| 5. Self-test on a fresh home, dialog key | The self-test was lost once in two starts, when it went out before channel registration; the nonce on the next cue recovered it. Section 3.6's "Teach auto mode about your environment?" dialog did not appear at these starts, so its key is still unfound. A different dialog, the development-channels warning below, appeared at every start. |
| 6. An open `wait_for_message` | No cue sent. Pass. |
| 7. A shim restart mid-session | Not tested: the auto-mode classifier refused the shim kill (`[Interfere With Workloads]`). |

The development-channels warning, as it showed in the panes at every start
of a cue seat (line breaks shown as `/`):

> WARNING: Loading development channels / --dangerously-load-development-channels
> is for local channel development only. Do not use this option to run
> channels you have downloaded off the internet. / Please use --channels to
> run a list of approved channels. / Channels: server:director / 1. I am
> using this for local development / 2. Exit

It comes from the flag only cue seats pass (C-5), so it is a second blocking
notice, separate from section 3.6's.

Two facts outside the list:

- `--channels server:<name>` does not bind a server that is not a plugin, so
  only `--dangerously-load-development-channels` reaches director-mcp
  (the flag C-5 added).
- Before the shared launcher checkout moved (finding-017), both seats showed
  `cue: unverified`: their shims had `DIRECTOR_CUE=1` by inheritance, and
  their claude processes had no channels flag.

## 2. What it changes in the design

- Section 3.5 asked whether a mid-turn notice is queued, delivered into the
  turn, or dropped. It is held to the tool boundary, so the receipt window W
  must allow for a long tool call, and a re-cue is not needed for that case.
- Section 3.3 assumes the self-test at start proves the channel. A lost
  self-test reads as `unverified` on a working channel; the recovery by the
  next cue's nonce is what makes this safe, so that path deserves a test.
- Section 3.6 names one dialog; there are two. Its "Teach auto mode" dialog
  did not appear here, and its key is still unfound. The development-channels
  warning is the other, and it appears at every start of an opted-in seat,
  so seeded onboarding state (marvel#422) would not dismiss it. Every restart
  of a cue seat can stop on it.
- Plan mode wakes but cannot drain, so a cue seat in plan mode is woken for
  nothing until the permission prompt is resolved (aae-orc-r675b).

## 3. What this does not establish

Item 7, the older protocol in item 1, the drain order in item 4, and
whether the self-test race depends on host load. The one-working-day team run in section 4 has not
started.

## Related

- sim/design/channel-cue.md sections 3.3, 3.5, 3.6 and 4; #166 (P1), #175 (C-5),
  #179 (the rulings record).
- question-seat-startup-notice-capture: the development-channels warning is
  a startup notice that blocks.
