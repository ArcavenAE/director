# finding-011: wait_for_message's 120 s maximum equals the Claude Code background threshold, so a full-length wait always turns into a background task

- **Date:** 2026-09-27
- **Session:** arcaven-builder-g5-0, placing the 2026-09-27 seat harvest
- **Subject:** director-mcp `wait_for_message` as a Claude Code seat experiences it (tooling friction)
- **Confidence:** reported by four seats on 2026-09-27 and seen by this seat the same day; the clamp read in `probe/nats-phase-0/director-mcp/tools.go` at 02a3188

## 0. The sentence

**A seat that asks for the longest wait gets a background task, not a wait:
the shim clamps `timeout_seconds` to 120, and Claude Code moves any MCP call
still running at 120 s to the background.**

## 1. What was observed

Four seats reported the same message, verbatim: `MCP tool
"director/wait_for_message" is still running after 120s. It was moved to the
background`. The call then completes later, usually empty, as a task
notification. Each silence wakes the seat for a turn. One seat stopped
re-arming after two empty polls to avoid a loop, and then missed a route until
a pane nudge reached it.

## 2. Mechanism

`toolWait` clamps `TimeoutSeconds` to at most 120 (tools.go, around line
213), and the tool description says "default 30, max 120". The background
move is the harness's, not the shim's. The two limits happen to be equal, so
the maximum the shim offers is exactly the point where the harness stops
waiting in the foreground.

## 3. Workaround and what it costs

Seats used shorter windows (under 120 s) to keep the wait in the foreground.
Either way, waiting costs a turn per window, because the server cannot push
into the model's context (the tool description says so). That is the
underlying cost; the equal limits only decide whether it arrives as a return
or as a notification.

## Related

- director#52 (a silence result does not name the tiers it polled)
- finding-007 (a deaf session reports silence as normal): a background wait that completes empty reads the same as a live one
