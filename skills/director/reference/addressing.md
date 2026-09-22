# Addressing and reach — the model to orient against BEFORE reaching a seat

Read this first when you need to reach an agent. It exists because the
reach model was learned the hard way, mid-operation, more than once: every
prop existed in the graph and the agent still misaddressed seats because no
single backdrop held the model together (the F25/F19 failure). This is that
backdrop. When it conflicts with what a command actually does, believe the
command and fix this file.

## Two tiers

- **Local tier** — the bus on this hub. Seats on the same host as you.
  Address: `agent://{team}/{agent-id}` with the `workspace` param set.
  Bidirectional: a local seat's reply reaches your inbox.
- **Global tier** — cross-host, via the global hub. Only two address
  shapes exist today:
  - `global://director` — the human's director (you).
  - `global://{cluster}/supervisor` — the supervisors on a cluster.

## The five things that bite

1. **There is no per-seat global address.** `global://{cluster}/supervisor`
   fans out to EVERY supervisor on that cluster (five on mokuzai, so a
   single send hits errand ×2, migrated, ops, reviewer). This is the q9mtd
   gap. When you must use it to reach one seat, scope the recipient in the
   FIRST line of the body ("FOR migrated-supervisor; others stand down").

2. **`agent://` does not cross hosts.** An `agent://{team}/{id}` send aimed
   at a remote seat is delivered to your LOCAL inbox instead, silently.
   Cross-host must go `global://`. (If per-seat cross-host addressing is
   what you need, it does not exist yet; it is a director requirement, not
   a thing you can do.)

3. **marvel pane verbs key on `<workspace>/<agent-name>`, not the bare
   name.** `marvel capture`, `inject`, and `describe` reject the AGENT NAME
   that `marvel get sessions` prints. The accepted key is
   `<workspace>/<agent-name>`, plus `--cluster` for a remote cluster:
   - local: `marvel capture aae/arcaven-supervisor-g1-0`
   - remote: `marvel inject aae/migrated-supervisor-g1-0 "text" -e --cluster skippy`
   The bare `arcaven-supervisor-g1-0` errors `resource not found` and dumps
   usage, which reads like a syntax error but is a lookup miss. Tracked as
   marvel#337 / aae-orc-bd78j; until it is fixed the workspace prefix is
   mandatory and there is no `-o json`/`-o wide` to discover it — read the
   WORKSPACE column of `get sessions` and prepend it.

4. **`marvel inject` submits with `-e`.** `marvel inject <key> "text" -e`
   sends the literal text then a separate Enter (the doorbell fix, PR #323).
   Without `-e` it stages a draft that never submits.

5. **Accepted is not delivered or read (R-08).** `send_message` returning
   "accepted for delivery" means the bus took it, nothing more. Confirm via
   the recipient reporting back, not the send result.

## Reach precedence (highest layer that can actually reach the seat)

1. `mcp__director__send_message` — structured, presence-aware, cross-host
   over the global tier. Preferred when the seat holds a live bus presence.
2. `marvel capture` / `marvel inject` — pane read + keystroke doorbell,
   executive privilege, keyed `<workspace>/<agent-name>` [+ `--cluster`].
3. marvel native tmux ops.
4. direct `tmux capture-pane` / `send-keys` for a local unmanaged pane.
5. `ListAgents` + SendMessage — interactive/Remote Control Claude sessions
   the others cannot reach; no presence guarantee.

## Cluster geography

`cluster name == hostname`. kinu = this laptop; mokuzai = skippy's host
(192.168.100.196); desk = desk.local. From kinu, reach skippy's fleet with
`--cluster skippy`; ON skippy the same daemon is the local socket. Skippy's
own marvel config names that cluster "mokuzai" — same fleet, two names.

## Current condition (dated — this part decays)

**2026-09-21: the mokuzai -> kinu return path is DOWN.** Replies to
`global://director` are accepted onto GLOBAL_TO_DIRECTOR but never reach the
director inbox, which binds the local tier, not GLOBAL_TO_DIRECTOR
(consumer-side; O-23). While it holds: cross-host seats report status on
GitHub (PR/issue comments), which is the authoritative channel; do not read
a bus "accepted" as "heard." Fix in flight: l15b5 + 7gnvo. Re-run the
round-trip test when they land, and delete this paragraph once the return
path is confirmed up.
