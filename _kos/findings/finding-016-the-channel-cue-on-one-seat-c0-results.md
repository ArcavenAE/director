# finding-016: the channel cue on one seat: idle wake works, a mid-turn cue waits for the next tool boundary, and the startup self-test can lose a race

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
| 3. Plan mode and the permission prompt | Not in the harvest record. |
| 4. A burst of 30 | One cue with `count=30`, not 30 turns. |
| 5. Self-test on a fresh home, dialog key | The self-test was lost once in two starts, when it went out before channel registration; the nonce on the next cue recovered it. The development-channels consent dialog appears at every start; no key that marks it seen was found. |
| 6. An open `wait_for_message` | No cue sent. Pass. |
| 7. A shim restart mid-session | Not in the harvest record. |

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
- Section 3.6's dialog is a per-start consent, not a one-time onboarding
  screen, so marvel#422's seeded state may not cover it. Every restart of an
  opted-in seat can stop on it.

## 3. What this does not establish

Items 3 and 7, the older protocol in item 1, and whether the self-test race
depends on host load. The one-working-day team run in section 4 has not
started.

## Related

- sim/design/channel-cue.md sections 3.3, 3.5, 3.6 and 4; #166 (P1), #175 (C-5),
  #179 (the rulings record).
- question-seat-startup-notice-capture: the consent dialog is a startup
  notice that blocks.
