# finding-director-97gz: director's reply delay has two halves, and subagents reach only one

- **Date:** 2026-11-05
- **Subject:** director: how quickly its own session answers, and how a directive reaches the front of another seat's queue
- **Asked by:** the operator (ask I-69), through director
- **Confidence:** read-only. Code read at director origin/main ff0d94c. Nothing was run against a live bus.

## Why this matters

The operator asked for director's actions to move into subagents so its session answers sooner, and for director's own directives to jump the queue "when appropriate". Read against the code, the first fixes one of two kinds of delay, and the second is only a boundary once director holds a credential no other seat holds. Saying so before building saves work on the wrong half.

## 1. Two delays

- **The operator's own prompt.** Typing into director's pane wakes director, so the only delay is a turn already running. A subagent shortens a busy turn. (INFERRED from how the harness takes a prompt.)
- **Mail and Desk answers.** They wait while director is idle with no wait open. A subagent runs only inside a turn, so it cannot help. That half needs a reader outside any turn and a wake.
- **Conjecture, to measure:** idle-with-no-wait accounts for more of the delay than busy turns. Nothing has measured the split on director's own records; ticket D-1 does.

## 2. What is built, and where each piece runs

#486 (expiry hint on an empty poll), #490 (read and expired-unread receipts), #491 (the cue on by default for supervisor casts), #492 (pending age over a bound is flagged), #493 (one wait per session), #494 (stale presence reaped), #495 (ack after the reply is written) and #499 are on main. Each runs only when some seat takes a turn: `list_roster`, `director-mcp unread` and freebusy are all read from inside a director turn, and no step of the tick order in `skills/director/SKILL.md` reads the #492 flag, the ask ledger's alarms or the rings.

## 3. Gaps, by class

- **A seat stopped before its first turn is invisible.** A cued seat waits at the development-channel consent prompt, which returns on every spawn and shift since #491. The shim writes presence before serving stdin (`cmd/director-mcp/main.go`), so a seat with no presence row at the prompt means the harness has not started its MCP servers yet (INFERRED). `holdsUnreadMail` counts live rows only (`cmd/director-mcp/unread.go:698`), and the seat's durable does not exist until connect, so #492 cannot see it. `--preflight` runs only from `sim/twin/cast-launch.sh:314`; the hand-start launcher `skills/director/scripts/director-session` never calls it.
- **The cue settles without a drain.** Any `inbox_summary`, any wait start (`cue.go:238`) and any decrease in pending on the tick (`cue.go:425`) settles the cue, so a wait that reads one of three cued messages settles all three. After two windows the outstanding cue is dropped (`cue.go:443-449`).
- **A subagent's wait silences the parent's cue.** New mail cues only while no wait is open (`cue.go:432-434`), and a subagent goes through the same shim.
- **The wait-and-turn wording invites a false belief.** The role library's supervisor text tells a seat never to end a turn "with no wait running". A wait ends with its turn, except that the harness may move one long wait to the background once. Twice in one hour a seat ended its turn believing a wait was still open; the cue.unanswered alarm caught both. Two director docs (`docs/getting-started.md:208`, `docs/shim-reference.md:68`) call the consent prompt one-time.
- **The priority lane has no boundary yet.** Sender, performative and headers are self-asserted (`cmd/director-mcp/tools.go:194`). Director's session uses "no second connection and no hub credential in the session" (`cmd/director-mcp/global.go:9`), so its mail reaches the hub over a leaf user other sessions share, and the hub template lets the mokuzai leaf publish its whole `global.mokuzai.>` tree.
- **A successor re-reads its predecessor's mail.** A new generation has a new agent id (`cmd/director-mcp/seatenv.go:57`), no earlier durable, and reads the supervisor inbox from the start of its retention (`global.go:572,640`). The FOR filter drops only mail for live seats of other teams.
- **Fan-out is counted as pending.** The cue and #492 count mail before the FOR filter runs (`pending.go:17`, `unread.go:405`).
- **Global mail has no receipts.** #486 and #490 read AGENT_INBOX only (`expiry.go:27`, `delivery.go:76`).

## 4. Recommended order

1. Measure (D-1): a latency band in `introspect` for operator prompt to first reply, split by whether a turn was open, and for Desk answer to relay.
2. Fix the idle half: a launchd watcher, outside any turn, whose product is a file the operator's dash and the tick read, with its own last-pass time checked by the seat doctor; it reminds and never acts. A cast record from `--preflight`, run by both launchers, so a seat stopped before its first turn is flagged. The cue settles only when its messages have left.
3. Build subagents (D-2) for the long non-judgement tick steps, only if the baseline shows busy turns are a real share of delay. A director subagent's tool list carries no director-mcp tool.
4. The lane: decide how director gets a credential of its own; then a directive subject only that credential can publish, ranked first by arrival subject, bounded to directives that carry a ref to an operator ask or ruling or name a MUST-wake item.

Every step reports or reorders. None types into a pane, preempts a turn or acts on silence, and none requires marvel. Director is never on another seat's critical path (operator ruling I-194).

## 5. Graveyard (harvested with this finding)

- `grv-tick-types-into-director-pane`: a tick that rings director by injecting into its pane is ruled out, because it is the pane the operator types in (finding-director-a53a). Reopener: a director seat with no operator pane.
- `grv-marvel-watchdog-reads-director-bus`: marvel's watchdog does not read director's bus, so that the components stay independent; director may read marvel, not the reverse (finding-director-gmah section 3). Reopener: an amended component-independence decision.
- `grv-escalation-acts-on-timeout`: escalation that acts on an ack timeout is ruled out; an unacked MUST-wake ask moves up as a message only (R-113 as narrowed 2026-10-15). Reopener: an operator ruling naming one action and its bound.

## 6. Gaps

- Nothing was measured on live records; every behavior claim is CODE or INFERRED.
- Whether the harness starts MCP servers before or after the consent dialog is inferred from a missing presence row, not observed.
- The live hub's configuration was not read; the template on main has drifted from it, so the lane's grants must be tested on the live hub.
