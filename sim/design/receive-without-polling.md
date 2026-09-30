# Receive without polling: feasibility and effort

Status: estimate for the operator, not a build plan. No code changes. Written
2026-09-30 by arcaven-architect-g5-0 on a commission relayed by
arcaven-supervisor.

Operator, verbatim: "we have plans to modernize the director-mcp to support
push, let's route the planned work to get an estimate on feasibility and level
of effort to adopt changes that would allow director to not require us to
execute/poll it, because that makes this seat act as the central cog in every
step/process/iteration and we want things to function smoother and not suffer
so badly from conways law".

## 1. The answer in brief

- **Feasible for Claude Code seats; P0 is green.** Claude Code 2.1.285
  ships an MCP server-to-client push path ("channels"), which the Phase 0
  settlement predates. P0 (section 5a) measured an idle seat woken by a
  channel notice, with a turn started in about 1 second and no human input.
  director-mcp can adopt it with a small change.
- **For every other harness, marvel is the push path.** It holds the panes
  and the leaf, so it can ring a doorbell on arrival. That is the automated
  form of what the supervisor does by hand today.
- **Smallest first phase:** probe P0 (half a seat-day), then P1, a channel cue
  in director-mcp (about three seat-days). After P1, no Claude seat, director
  included, waits for someone to wake it, **provided two gates are open**: the
  account's organization allows channels (`channelsEnabled`; channels are not
  available on Bedrock, Vertex or Foundry), and every seat to be woken either
  launches with the development-channels flag or loads director-mcp as a
  plugin on the approved channels allowlist. If either gate is shut, P1 does
  nothing and P2 is the first phase.

## 2. Is there a written plan?

No written plan covers delivering a message into a running turn. What exists:

| Item | What it covers | Push into a turn? |
|---|---|---|
| `probe/nats-phase-0/PROGRESS.md` sub-probe 3 (finding-159, finding-160) | "PUSH-VS-POLL, SETTLED IN CODE": MCP is request and response, so the shim cannot speak first; a hook bridge "remains unbuilt" | no, and section 3 shows the premise is now incomplete for Claude Code |
| `docs/architecture.md` "receive is a poll, not a push"; `docs/shim-reference.md` "It does not push" | the same position, as documentation | no |
| R-89 (`sim/requirements.md`) | every dispatch needs an out-of-band doorbell, and a doorbell must land or fail loud | requirement only |
| aae-orc-xg9yd | one message per call; a woken seat should drain its backlog in one call | no; the batch drain has since shipped (`max`, director#79, commit 2a089bc), so the ticket may be closable |
| aae-orc-fln6p | dispatch goes through the inbox, and `marvel inject` is kept for waking a parked seat | names the doorbell, does not build it |
| aae-orc-7vw44, `docs/design/utility-event-plane.md` in the orc | `subscribe_events` and `set_timer` in the shim | events still arrive by poll |
| aae-orc-zsd5 | measure the token cost of polling against push | measurement, open |

The plan the operator remembers is most likely xg9yd with fln6p and R-89 taken
together. The receive side of xg9yd is built: `wait_for_message` takes `max`
and returns up to 50 waiting messages in one call (director#79), so a woken
seat already drains in one turn. This estimate is written to be that plan.

## 3. What each harness can receive

| Harness | Today (verified here) | Only planned or unverified |
|---|---|---|
| Claude Code, interactive | The binary carries the MCP capability `experimental: {'claude/channel': {}}`, the notification `notifications/claude/channel`, the flags `--channels` and `--dangerously-load-development-channels` (hidden from `--help`), the message "server: entries need --dangerously-load-development-channels", and policy keys `channelsEnabled` and `allowedChannelPlugins` (`strings` on 2.1.285) | **Not measured:** whether a channel notification wakes an idle session and starts a turn, how it behaves mid-turn, and whether the account's policy allows it. The binary labels the feature "Channels (experimental)" and carries three refusals: "Channels are not enabled for your org" (`channelsEnabled` in managed settings), "Channels are not available on Bedrock, Vertex, or Foundry", and "not available on third-party providers"; and a server's capability registers only when its plugin is on the approved channels allowlist or the development flag is set (strings quoted in the #163 review). P0 settles the rest |
| Claude Code, headless (`-p`) | a one-shot run; nothing to wake | n/a |
| codex | MCP client only; no channel equivalent found in `codex --help` | an experimental `app-server` with `remote-control` exists; not assessed |
| opencode, others | no push path known | not assessed |
| any harness in a marvel pane | `marvel inject` types into the pane, which is how the supervisor wakes seats by hand after each send | automated in P2 |

The shim can already send a notification: it writes JSON-RPC to stdout under a
mutex (`outMu`, `probe/nats-phase-0/director-mcp/mcp.go`), so a background
goroutine can write a notification line safely.

## 4. What marvel can do

marvel knows each seat's bus id (it sets `DIRECTOR_AGENT_ID`, marvel
`internal/runtime/adapter.go:323-331`), its pane, and whether the pane is
alive. A **doorbell** in marvel:

- watches its seats' inbox subjects on the leaf with a non-consuming consumer
  that reads subjects and headers, never bodies, so custody stays with the
  seat's own `wait_for_message`;
- on arrival, if the seat is idle, injects one fixed cue ("new message in your
  director inbox"), never the message text;
- emits `seat.doorbell` with `rang` or `failed` (R-89: a wake lands or fails
  loud) and rings once per arrival burst, not once per message.

Knowing when a seat is idle is the hard part. Typing into a busy Claude Code
seat queues a draft in the composer, which is the hazard finding-186 and fln6p
describe. Claude Code has `Stop` and `Notification` (`idle_prompt`) hooks that
could report idle to marvel the way `ctx-forward` reports context. That is not
built, and not verified to fire reliably.

## 5. Phases and effort

Sizes are in seat-days for one builder with review; S under 1, M 2 to 4, L 5
or more.

| Phase | Work | Size | Takes director out of |
|---|---|---|---|
| P0 | First, at no seat cost: read whether the fleet's accounts allow channels (`channelsEnabled`, and the provider: subscription, not Bedrock, Vertex or Foundry). If they do not, stop: P1 is dead and P2 goes first. Otherwise probe: director-mcp declares `claude/channel`; one test seat loaded with the development-channels flag (operator-run, since the flag is named "dangerously"); send to it while idle, while mid-turn, and under plan and auto modes; record wake, latency, and any prompt | S (0.5) | nothing yet; decides P1 |
| P1 | director-mcp: a background inbox watcher; on arrival, one `notifications/claude/channel` cue carrying the sender, performative and message id, never the body; the model then calls `wait_for_message` as today, so FIFO order and R-08 are unchanged; dedupe and rate limit; a receipt check (section 6) so an unanswered cue fails loud; the startup bind check and handshake pin from section 5a; tests including the P0 cases still unmeasured | M (about 3) | waking Claude seats, itself included |
| P2 | marvel doorbell (section 4) for non-Claude seats and as a fallback when channels are off; idle signal from hooks for Claude, `turn.started` and `turn.ended` for codex and opencode | M to L (4 to 6) | waking codex and other seats; the manual inject after each send |
| P3 | Route seat to seat without director: supervisors and builders address each other by `role://` on the bus (fln6p), with director copied, not relaying | M (2 to 3), mostly process text in wardrobe | the relay hop (the Conway cost) |

## 5a. P0 result (2026-09-30)

Measured by a builder seat on kinu with Claude Code 2.1.285, one test seat,
a minimal stdio server declaring `experimental: {'claude/channel': {}}`, and
the development-channels flag. Evidence: the server's JSON-RPC log and three
pane captures (before the notice, 8 seconds after, 35 seconds after), kept in
that seat's scratch directory.

- **Wake: yes.** One `notifications/claude/channel` with `content` and
  `meta` (`sender`, `performative`, `message_id`) woke the idle session and
  started a turn in about 1 second, with no human input. The model's reply
  named the sender from `meta`, so it sees the metadata, not only the content.
- **The org gate is open** for this subscription account, which has no
  managed settings. The development flag was required.
- **The banner is not a health signal.** The session showed
  `server:<name> · no MCP server configured with that name` at start, and the
  channel bound anyway. A real misconfiguration shows the same banner, so a
  seat cannot tell from it whether its cue path is live.
- **The handshake era carried it.** On the later launch the client first sent
  `server/discover` at protocol version `2026-07-28`; the server answered
  `-32601`, and the client fell back to `initialize` at `2025-11-25`. The
  notice travelled on that legacy session. (An earlier launch the same day
  went straight to `initialize` at `2025-11-25`.) The binary also carries
  "negotiated a modern protocol revision with no unsolicited notification
  path", so on a modern session the notice may have nowhere to go.
- **A dialog followed the turn.** After the woken turn, an onboarding dialog
  ("Teach auto mode about your environment?") appeared. A fresh seat could
  sit on it, which is a seat-bootstrap concern (marvel#422).

**Still unmeasured:** behaviour mid-turn; plan mode; whether the woken seat
meets the `wait_for_message` permission prompt (aae-orc-r675b); coalescing of
several notices.

**What P1 takes from it:**

1. **A positive bind check.** At startup the shim confirms its own cue path
   rather than trusting the banner: the client advertised or accepted the
   channel capability on the session it holds, and the negotiated protocol
   version is one P1 was verified on. It reports the result on the bus
   (`cue: live` or `cue: off, <reason>`), and the section 6 receipt check
   still runs per cue.
2. **Pin the handshake, or verify the modern one.** P1 answers
   `server/discover` with `-32601` so the client falls back to the
   `2025-11-25` `initialize` that carried the notice, or P1 is measured on a
   modern session before it relies on one. Either way a Claude Code upgrade
   that changes the negotiation shows up as `cue: off` at startup, not as a
   silent drop.
3. **The flag stays an operator decision** (section 6), now with a measured
   reason: without it the channel did not bind.
4. **P1's tests add** the remaining P0 cases (mid-turn, plan mode, the
   permission prompt, a burst of notices) before the cue is enabled for any
   fleet seat.

**The smallest first phase that takes director out of the polling loop is P0
plus P1**, about three and a half seat-days, if the org gate is open and P0 is
green. If either fails, P2 becomes the first phase and the estimate is about
five seat-days. The org check comes first because it changes that answer
before any seat time is spent.

## 6. Risks

- **Experimental.** The channel surface is labelled experimental and hidden
  from `--help`; it can change between Claude Code releases. P1 keeps the cue
  optional, and `wait_for_message` stays the only receive, so a regression
  degrades to today's polling rather than to loss.
- **The development flag.** A non-plugin server needs
  `--dangerously-load-development-channels`. Putting that in every fleet
  launch is the operator's decision (no-control-bypass). Packaging director-mcp
  as a plugin may remove the need; unverified.
- **Account policy.** An organization can switch channels off
  (`channelsEnabled`), and they are unavailable on Bedrock, Vertex and
  Foundry. The fleet's accounts are not checked; P0 checks them first.
- **A blocked cue drops silently, so a bare cue does not meet R-89.** For a
  policy-blocked channel the binary says "Inbound messages will be silently
  dropped". The shim writes the notification and gets no acknowledgement, so
  a cue that never landed looks like one that did. P1 therefore includes a
  receipt check the shim can make on its own: it sees its model's tool calls,
  so after a cue it waits a bounded window for `wait_for_message`, and if none
  comes it emits a wake failure on the bus (to director, and to the sender
  where the envelope names one). That is the fail-loud half of R-89. Without
  it, P1's cue is fire-and-forget and the fail-loud half stays with P2's
  doorbell.
- **Injection surface.** A channel event lands in the model's context. The cue
  therefore carries only metadata the bus already authenticates, never a
  sender's text (INJ-A..C, `authority-never-in-content.md`).
- **Wake storms.** Thirty roll-call replies would mean thirty wakes. Coalesce
  per arrival burst, with a floor between cues.

## 7. Dependencies

- **aae-orc-t77rr (24h inbox retention).** A cue for a message that retention
  later deletes is still a loss. Push shortens the wait but does not fix
  retention, so t77rr stays P1 on its own.
- **aae-orc-4unyk (phantom presence).** A doorbell keyed on presence would
  ring a dead shim. P1 does not depend on presence (the live shim rings its
  own model); P2 should key on marvel's pane liveness, not on the roster.
- **aae-orc-r675b (plan-mode receive).** A plan-mode seat woken by a cue still
  meets the permission prompt on `wait_for_message`. P0 records whether it
  does.
- **aae-orc-zsd5.** P0 and P1 give the push side of its measurement.

## 8. Asks

1. ~~Run P0.~~ Done 2026-09-30: green (section 5a).
2. Choose P1 (Claude channel cue) or P2 (marvel doorbell) as the first build
   if P0 is green. Default: P1 first, P2 next for codex seats.
