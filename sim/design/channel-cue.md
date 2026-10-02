# Channel cue: wake an idle Claude seat on arrival (P1 design)

> **Status:** design for #166, draft. P1 of `receive-without-polling.md`
> (#163), using the P0 result recorded in #165. Implementation follows in a
> separate red/green PR once this design is approved.

## 1. What changes, in one paragraph

When a message arrives for a session, director-mcp sends that session's
Claude Code one `notifications/claude/channel` notice: "new message in your
director inbox", with the sender, performative and message id in `meta`,
never the body. P0 showed such a notice starts a turn on an idle seat in
about 1 second. The model then drains with `wait_for_message` exactly as
today, so FIFO order, acks and R-08 are unchanged; the cue only replaces the
human who types "read your inbox" into the pane. It is off unless a seat
opts in, and every way it can fail degrades to today's polling.

## 2. What the shim does today (checked on main, e5ed906)

| fact | where | consequence for P1 |
|---|---|---|
| Requests are read and dispatched one at a time, on one goroutine | `probe/nats-phase-0/director-mcp/mcp.go:63-79` | while a `wait_for_message` blocks (up to 120 s), no other request is read; the cue must come from a second goroutine |
| Every write takes `outMu` | `mcp.go:142-148` | a notification from another goroutine cannot interleave a response line |
| Unknown methods with an id get `-32601` | `mcp.go:97-100` | `server/discover` already gets `-32601`, which is what made Claude Code fall back to the legacy `initialize` in P0 |
| `initialize` answers `protocolVersion: 2025-06-18`, whatever the client sent, and declares only `tools` | `mcp.go:83-88` | P0's server echoed `2025-11-25`; the cue is **unmeasured** at `2025-06-18` (section 4) |
| Each session has a durable on `AGENT_INBOX` filtered to its own inbox and role subjects, and a second durable on the hub stream when the global tier is on | `bus.go:185-220`, `bus.go:232-238`, `global.go:115-132` | arrival can be read from each durable's `NumPending` without consuming anything; the shim already reads it (`bus.go:245`) |
| `cast-launch.sh` writes the shim's MCP JSON and execs claude with `--strict-mcp-config --mcp-config` | `sim/twin/cast-launch.sh:158-159`, `:261-265` | the wrapper is where a wrapped seat opts in |

## 3. Design

### 3.1 Opt-in, per seat

- **Shim side.** `DIRECTOR_CUE=1` in the shim's environment turns P1 on.
  Without it the shim is byte-for-byte today's: no capability, no watcher, no
  change to `initialize`.
- **Harness side.** Claude Code binds a non-plugin channel only with
  `--dangerously-load-development-channels server:<name>` (P0: the channel
  did not bind without it). Putting that flag on a fleet launch is the
  operator's decision (no-control-bypass), so it is per seat, never a
  default.
- **Wrapped seats (every arcaven role today).** `cast-launch.sh` gains one
  switch, `DIRECTOR_CUE` read from the role's environment. When it is `1`,
  the wrapper adds `"DIRECTOR_CUE":"1"` to the shim's `env` in the MCP JSON it
  already writes, and appends the development-channels flag naming the
  `director` server. The seat's manifest is the one place the opt-in is
  visible.
- **marvel#422's projection.** The rule there is "a role-declared or wrapper
  MCP config wins whole": marvel projects nothing for a wrapped role, and
  nothing for a role that declares its own `--mcp-config`. So for every
  current seat the wrapper owns the cue, and marvel is not involved. For a
  bare-claude seat that marvel projects into, the projection file carries
  `command` and `args` only; the shim would see `DIRECTOR_CUE` only through
  environment inheritance (marvel#422 SB-0 line 11, unmeasured), and the flag
  would have to be in the role's args. P1 therefore supports wrapped seats
  only, and a projected seat opts in after SB-0 passes.
- **Org gate.** Channels need `channelsEnabled` for the account's
  organization and are unavailable on Bedrock, Vertex and Foundry. P0 found
  the gate open on the fleet's subscription account with no managed
  settings. The shim cannot read this gate; the self-test (3.3) observes its
  effect.

### 3.2 Handshake

- With the cue on, `initialize` declares
  `capabilities.experimental["claude/channel"] = {}` beside `tools`.
- **Pin the legacy handshake.** `server/discover` keeps getting `-32601`
  (today's default, made explicit with its own test), so Claude Code falls
  back to `initialize`, the path P0 measured. The binary carries "negotiated
  a modern protocol revision with no unsolicited notification path", so a
  modern session may have no route for the cue at all; P1 does not implement
  `server/discover`.
- **Protocol version.** With the cue on, the shim echoes the client's
  `initialize` version when it is one P1 was measured on (initially
  `2025-11-25`, the P0 value), and otherwise answers as today and reports
  `cue: off, protocol <v> not verified`. C-0 measures whether `2025-06-18`
  also carries the notice; if it does, it joins the list.
- **Upgrades.** A Claude Code release that stops falling back, or sends a
  version outside the list, turns the cue off with a reason at startup. It
  never turns into a silent drop.

### 3.3 Positive health check: the startup self-test

The banner `server:<name> · no MCP server configured with that name` showed
on P0's working channel, so it proves nothing, and the shim cannot see
whether Claude Code registered the channel. The only positive proof is the
round trip:

1. After `notifications/initialized`, the shim draws a random nonce for
   this start (16 bytes, hex) and sends one cue with
   `meta.kind = "self-test"`, `meta.nonce = <nonce>`, and content "director
   cue self-test: call inbox_summary with cue_ack set to the nonce in this
   notice". The first notice can go out before Claude Code registers the
   channel and be lost (C-0, finding-016), and nothing in the MCP traffic
   marks registration. So while no echo has come, the shim sends the same
   notice again at 10, 30 and 70 s (three retries, backoff doubling from
   10 s; director#188).
2. `inbox_summary` gains an optional `cue_ack` argument. Only a call whose
   `cue_ack` equals this start's nonce, within the self-test window (default
   120 s, counted from the latest send), records `cue: live`. An inbox call alone proves nothing: seats
   drain their inbox at start whether or not a cue arrived, which is the
   false positive this PR's first review named. The nonce can only be known by a model
   that received the notice.
3. Otherwise it records `cue: unverified` with the reason it can name
   (`no nonce echo after self-test`, set only once the last send's window
   has closed, or `wrong nonce`). A nonce is valid for
   one start; a shim restart draws a new one. The cause could be a flag not bound, an
   org gate shut, the seat held by an onboarding dialog (3.6), or a busy
   first turn; the shim does not guess which.
4. The state is published beside presence (a `cue` field) and returned in
   every `inbox_summary`, so director and the supervisor can list seats
   whose cue is not live. `unverified` does not stop cues, and only a nonce
   echo changes the cue state. While a seat is `unverified`, every cue it
   is sent carries a fresh `meta.nonce` and asks for `cue_ack`; an
   `inbox_summary` echoing that nonce within W promotes the seat to `live`.
   A drain, a `wait_for_message`, or an `inbox_summary` without the nonce
   never promotes it. A restart runs the self-test again.

The self-test costs one short turn per seat start, and up to four notices
when none is echoed. That is ruling 2. The
nonce is not a secret and carries no authority; it only proves the notice
reached the model. It never appears in presence or on the bus, only the
resulting state does.

### 3.4 The watcher

- One goroutine per tier polls its durable's `NumPending` every 2 s (default,
  configurable). Consumer info is an API call the seat's broker user already
  makes (`noteResumed`, `bus.go:245`), so no new subscribe permission is
  needed, and the global tier works across the leaf the same way the drain
  does. Nothing is fetched or acked.
- **Arrival** is `NumPending` rising above the value last seen after the
  model's most recent drain.
- **No cue into an open wait.** The shim counts in-flight
  `wait_for_message` calls. If one is open when an arrival is seen, the open
  wait will deliver the message, so no cue is sent, and that delivery counts
  as the receipt (3.5).
- **Coalescing.** The first arrival after a quiet period cues at once.
  Arrivals within the floor after a cue (default 5 s) fold into one cue at
  the end of the floor, with `meta.count`. A roll call of thirty replies is
  at most one cue per floor.
- **Metadata only.** `meta` carries `sender`, `performative`, `message_id`
  and `count` for the oldest unread message, read from the stream by
  sequence without consuming it. Never the body, never any text a sender
  chose (INJ-A..C, `authority-never-in-content.md`).
- **Reconnect.** After a broker reconnect the watcher re-reads `NumPending`
  and cues once if anything is waiting.

### 3.5 Receipt, without false failures

This "answered" rule governs receipt and re-cue suppression only. It never
changes the cue state; only a nonce echo does (3.3).

A cue is **answered** when, within the window W after it, any of these
happens: the model calls `wait_for_message` or `inbox_summary`; an open wait
delivers the message the cue named; or the cued messages leave the durable
by any other drain. W defaults to 300 s (the fleet's five-minute health
check).

An unanswered cue is **not** a failure. The shim cannot see the model's
turn: a seat woken mid-turn may be busy for longer than W and is healthy.
So:

- after W with the cued messages still pending, the shim emits
  `cue.unanswered` (a warning naming the message ids and the seat's cue
  state) to director, and re-cues once;
- after a second W it emits `cue.unanswered` again and stops cueing those
  messages; the seat is back on today's manual wake for them;
- it never reports the seat dead or the message lost. R-89's fail-loud half
  is this warning; liveness stays with marvel's pane checks.

C-0 measures what a notice does mid-turn. If Claude Code queues it until the
turn ends, W can be tightened for seats known idle; if it drops it, the
re-cue is the remedy and the doc records the loss case.

### 3.6 The onboarding dialog

After P0's woken turn, a "Teach auto mode about your environment?" dialog
appeared. A fresh seat held on a dialog answers no cue. director cannot
dismiss it. The fix belongs to seat bootstrap: marvel#422's seed of
onboarding state for Claude Code homes should include whatever key marks
this dialog seen (the key is not yet identified; C-0 finds it). Until then
the self-test reports such a seat as `cue: unverified`, which is the
visible form of the problem.

## 4. Measured before rollout beyond one seat (C-0)

On one opted-in wrapped seat, recorded as pass or fail per line:

1. Idle wake with the shim's real handshake: at `2025-06-18` as today, and at
   the echoed `2025-11-25`.
2. A notice mid-turn: queued to turn end, delivered into the turn, or
   dropped.
3. Plan mode, and whether the woken turn meets the `wait_for_message`
   permission prompt (aae-orc-r675b).
4. A burst of 30 sends: cues sent, turns started, messages drained in order.
5. The self-test result on a fresh home, and the dialog key for 3.6.
6. An open `wait_for_message` receiving a message: no cue sent.
7. A shim restart mid-session: the cue state is re-established.

Then one team (arcaven) for one working day, with `cue.unanswered` counts
read as a diagnostic (never a gate), before any other team opts in.

## 5. Edits, in order (none made by this PR)

| # | Edit | Depends on |
|---|---|---|
| C-0 | The measurement in section 4, on one seat | the operator's go for the flag on that seat |
| C-1 | `DIRECTOR_CUE`; capability; version echo list; the explicit `server/discover` pin | none |
| C-2 | The watcher: `NumPending` poll per tier, open-wait suppression, coalescing, reconnect | C-1 |
| C-3 | Receipt window, `cue.unanswered`, one re-cue | C-2 |
| C-4 | Startup self-test with a per-start nonce; `cue_ack` on `inbox_summary`; the `cue` field in presence and `inbox_summary` | C-1 |
| C-5 | `cast-launch.sh`: `DIRECTOR_CUE` from the role env, the shim env entry, the flag | C-1, ruling 1 |

The builder writes C-1 to C-4 red first against these tests: the cue-off
path is unchanged (golden `initialize` and `tools/list`); `server/discover`
gets `-32601`; a rise in `NumPending` with no open wait emits one cue with
metadata only and no body; with an open wait, none; three arrivals inside the
floor emit one cue with `count = 3`; an unanswered cue emits
`cue.unanswered` after W, re-cues once, then stops; a drain inside W emits
nothing; the self-test sets `live` only on an `inbox_summary` whose `cue_ack`
equals this start's nonce; a drain with no `cue_ack` (both `inbox_summary`
and `wait_for_message`) inside the window leaves `unverified`; a `cue_ack`
carrying a wrong nonce, or the previous start's nonce, leaves `unverified`
with reason `wrong nonce`; while `unverified`, a cue followed by a
`wait_for_message` drain with no `cue_ack` leaves the state `unverified`
(and counts as answered for receipt); a later cue's nonce echoed through
`inbox_summary` promotes to `live`; a client version outside the list reports `cue: off`.

## 6. Rulings

Ruled by the operator on 2026-10-01, all as the defaults proposed here:

1. **The development flag on fleet seats.** Adopted per seat, arcaven team
   first, after C-0 passes.
2. **The self-test turn at every seat start.** Yes, at every seat start.
3. **W = 300 s and the floor = 5 s.** Yes, revisited after the one-day team
   run.

Rollout is one seat at a time (default (a)).

Implemented by C-5, and not part of the rulings: `DIRECTOR_CUE=1` in a seat's
environment opts it in, and the spawn line names a cue that is on. The
current code has no switch to skip the self-test.
