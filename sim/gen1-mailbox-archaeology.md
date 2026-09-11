# Gen-1 mailbox archaeology (multiclaude/internal/messages)

Reading of the store-and-forward predecessor that question-director-design
has named the design reference since session-008. The point is to mine a real
implementation for director requirements before the simulation collects more
of them by hand, because an implementation that shipped and was used is
stronger evidence than a simulation observation.

Source: `director/multiclaude/` (dlorenc upstream, third-party clone, read
only). Primary files: `internal/messages/messages.go`,
`internal/daemon/daemon.go`, `docs/ARCHITECTURE.md`, `docs/CRASH_RECOVERY.md`.
Nothing here is published upstream.

## What it actually implemented

**The mailbox is a filesystem tree of JSON files.** `Manager`
(`messages.go`) writes one file per message at
`<messagesRoot>/<repo>/<agentName>/<msgID>.json`. The `Message` struct carries
`ID`, `From`, `To`, `Timestamp`, `Body`, `Status`, and an optional `AckedAt`.
That is the whole envelope.

**Status is a four-state lifecycle:** `pending`, `delivered`, `read`, `acked`
(`messages.go` lines 15-20). The transitions are split across three actors:

- `Send` writes a message in `pending` (`messages.go` `Send`).
- The daemon's `routeMessages` loop (`daemon.go` line 372) picks up `pending`
  messages, types them into the recipient's tmux window with
  `SendKeysLiteralWithEnter`, and sets `delivered` (lines 407-415). Delivery is
  poll-based on a 2-minute timer (`messageRouterLoop`, line 367).
- `read` and `acked` are set only by explicit CLI commands the recipient runs
  (`cli.go` line 3871 sets `read`, line 3907 calls `Ack`). Nothing automatic
  ever advances a message past `delivered`. `Ack` stamps `AckedAt`
  (`messages.go` lines 104-116).

**Addressing is by name.** `agentDir` joins `repoName` and `agentName`
(`messages.go` line 162). The recipient is a directory path built from two
strings.

**Durability is real.** Messages are files, and `docs/ARCHITECTURE.md` and
`docs/CRASH_RECOVERY.md` both state the design intent: state lives on disk, the
daemon can crash and come back, and the last atomic write is preserved. A
daemon crash stops delivery but loses no queued message.

**Delivery liveness is inferred from tmux and PID.** `checkAgentHealth`
(`daemon.go` line 285) decides an agent is dead from three plumbing signals:
tmux session missing, tmux window missing, or `isProcessAlive(PID)` false. For
persistent agents it auto-restarts; for transient agents (workers, review) it
comments that they "complete and clean up" and does not restart, so a completed
worker and a crashed worker are handled by the same missing-window branch.

**Garbage collection is active.** `DeleteAcked` removes acked messages;
`CleanupOrphaned` (`messages.go` line 205) removes the entire message directory
of any agent not in the current valid-agent list.

**The human's own console is walled off from the bus.** Both `routeMessages`
(line 384) and `wakeAgents` (line 444) skip `AgentTypeWorkspace` with the note
that it "should only receive direct user input." A test asserts a message sent
to a workspace agent is never delivered (`daemon_test.go` line 901).

## Failure modes

**"Delivered" means keys were typed, not that anyone read them.** The
`delivered` transition fires when `SendKeysLiteralWithEnter` returns without
error, which happens whenever the tmux pane exists. An agent that is busy, in a
subshell, or not reading its pane gets a message marked `delivered` that it
never processed. The only signals that a human or coordinator did anything with
the message are `read` and `acked`, and both require the recipient to run a
command by hand.

**Transport errors are loud; agent-not-reading is silent.** On a tmux send
error `routeMessages` logs at Error level and leaves the message `pending`, so
it retries next cycle (lines 409-412). That half is loud. The other half, keys
delivered into a pane nobody is reading, returns success and is silent.

**Delivery order is filename order, not time order.** `List` reads the
directory with `os.ReadDir` (`messages.go` line 65), which returns lexical
order over `msg-<uuid-prefix>.json` names. `routeMessages` iterates that order.
The struct carries a `Timestamp` and delivery never sorts by it, so two
messages sent seconds apart can deliver in the reverse of send order.

**Orphan cleanup deletes a dead agent's undelivered messages.**
`CleanupOrphaned` removes the whole directory of any agent not currently valid.
An agent that died holding pending or delivered-but-unacked messages has those
messages erased on the next cleanup, along with any question it was waiting to
have answered.

**The wake loop injects a nudge every two minutes regardless of need.**
`wakeAgents` (line 437) sends a fixed "Status check" string to every
non-workspace agent unless it was nudged in the last two minutes. The nudge is
unconditional; it does not check whether anything changed.

**No correlation, no reply, no duplicate detection.** The `Message` struct has
no correlation id, no in-reply-to, no thread field, and no dedup key. There is
no `reply` operation. Send is strictly one-to-one, from a name to a name.

## What it deliberately did not attempt

No presence beyond alive-or-dead. No heartbeat. No capability negotiation. No
message chunking (the body is one string typed into a pane). No cross-host,
cross-account, or cross-harness anything: every path assumes one machine, tmux
windows, and PIDs the daemon can signal. No authority or principal model beyond
the `From` string. No broadcast or fan-out.

## Mapping against the register

### CONFIRMS

- **R-08 (ack comes from the receiver, not the send call).** Gen-1 got the
  shape right: `delivered` is set by the transport, and `read`/`acked` are set
  only by the recipient. The four states finding-151 asked for are exactly
  gen-1's four states. The gap is that nothing requires or expects the ack, so
  a real ack and a never-answered message are distinguishable only by reading
  the status field, which no loop does.
- **R-14 (liveness must be produced by the receiver after processing, not
  inferred from the plumbing).** Gen-1's automatic pipeline stops at
  `delivered`, which is a plumbing signal (keys typed). It has the
  receiver-produced signals (`read`, `acked`) but never waits on them. This is
  the state-from-proxy defect, in a shipped implementation.
- **R-16 (status is not liveness) and R-17 (one terminal signal distinguishes
  finished from died).** `checkAgentHealth` cannot tell a clean completion from
  a crash: both are the missing-window branch, and gen-1 resolves the ambiguity
  with a type guess (persistent restarts, transient does not). It encoded the
  missing terminal signal as a policy rather than measuring it.
- **R-06 (a restart changes identity; key on durable identity).** Gen-1 keys
  the mailbox on `agentName`. A restarted agent that keeps its name inherits
  the mailbox by luck; a renamed one loses it. Gen-1 keyed on the fragile thing
  R-06 says not to key on.
- **R-41 (the receiver's context budget is the coordinator's to spend
  deliberately).** The wake loop spends every agent's context on a status-check
  nudge every two minutes by construction, whether or not there is anything to
  report.

### SUPPLIES (proposed new requirements)

- Message-store durability across a coordinator restart. Gen-1 got this right
  and it is the direct counterexample to the Claude Code borrowed substrate
  that silently dropped messages (finding-151). The register has R-22 for a
  dead session's asks surviving, but nothing for the transport itself surviving
  a coordinator crash with the queue intact.
- Delivery ordering must be defined. Gen-1 delivered in filename order, which
  is effectively random against send time despite carrying a timestamp.
- Message lifecycle and cleanup must not destroy unanswered asks. Gen-1's
  orphan cleanup does exactly that (see the contradiction below), which is the
  evidence that the requirement is needed.

### CONTRADICTS (flagged loudest)

- **The cited "correlation_id cascade-loop" is not in this code.** The
  `Message` struct in this checkout has no correlation id and no reply
  threading, and there is no reply operation. question-director-design and the
  aae-orc-dft6 ticket both name multiclaude's `internal/messages` as the
  store-and-forward reference and cite a "correlation_id cascade-loop" as its
  lesson. That mechanism is not present in the reference as checked out here.
  Either the lesson came from a different or older fossil, or from a design
  document rather than this code. The node premise should be corrected to say
  where the cascade-loop actually lives, or the claim retired. Not confirmed
  from source: whether an earlier version carried the field (checking would
  need git history, which this pass did not run).
- **Orphan cleanup does the opposite of R-22.** `CleanupOrphaned` deletes the
  message directory of any agent no longer valid, so a dead agent's pending and
  unacked messages are erased rather than preserved. This does not make R-22
  wrong; it is a shipped implementation doing the exact thing R-22 forbids,
  which is strong evidence for R-22 and a specific warning not to copy gen-1's
  garbage collection.
- **Gen-1 walls the human's console off from the bus; R-32 puts it on.**
  Gen-1 excludes the workspace agent (the human's interactive session) from
  both delivery and nudges, on the rule that it takes only direct user input.
  R-32 says director addresses local sessions directly as an intended use case.
  These are opposing design decisions. Gen-1's choice is worth weighing rather
  than dismissing: it kept agent traffic out of the human's own console on
  purpose. The conflict should be resolved deliberately, not by default.

## Proposed register changes

Integrate these into `sim/requirements.md`; drafted here to avoid colliding
with concurrent edits to that file. Source class noted for the tagging work
(aae-orc-lb9hj): these are OBSERVED from a shipped implementation.

- **R-42 (new). The message store survives a coordinator restart with the
  queue intact.** A crash of the routing process must not lose queued or
  in-flight messages. *Earned by: gen-1 files-on-disk mailbox; ARCHITECTURE.md
  and CRASH_RECOVERY.md state the intent, and the daemon-crash path preserves
  the last atomic write.*
- **R-43 (new). Delivery order is defined, not incidental.** A message carries
  a send time and the transport must deliver in an order it states, rather than
  in whatever order the store enumerates. *Earned by: gen-1 delivered in
  `os.ReadDir` filename order over uuids while carrying an unused Timestamp
  (`messages.go` List; `daemon.go` routeMessages).*
- **R-44 (new). Message lifecycle and cleanup must not destroy an unanswered
  ask.** Garbage collection of a dead endpoint's mailbox must preserve messages
  that were never delivered or never acked, or hand them somewhere durable.
  *Earned by: gen-1 `CleanupOrphaned` removing dead agents' directories
  wholesale, which is the failure R-22 names, observed in shipped code.*
- **Amendment to R-08.** Add: an acknowledgement that is modeled but never
  required is not an acknowledgement. Gen-1 has `read` and `acked` states and
  no loop ever waits for them, so a never-answered message and an answered one
  differ only by a field nothing reads. *Earned by: gen-1 routeMessages stops
  at delivered; read/acked are manual CLI only.*
- **Flag on R-32 (do not renumber).** Record the gen-1 counter-decision: the
  predecessor deliberately kept the human's console off the agent message bus.
  R-32's direction (director addresses local sessions directly) is in direct
  tension with a shipped choice and should be resolved on purpose.
- **Correction owed to question-director-design (not a register change).** The
  "correlation_id cascade-loop" attributed to this reference is not present in
  the checked-out code. Correct or retire the citation.
