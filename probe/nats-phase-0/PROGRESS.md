# director NATS Phase 0 probe: progress

Ticket aae-orc-spbc. Brief: aae-orc/_kos/probes/brief-director-nats-phase-0.md.
Envelope: aae-orc/docs/design/director-envelope-and-adapter-events.md.
Run 2026-09-10. Capability snapshot (F16): nats-server v2.14.6, nats CLI 0.4.0,
Go 1.26.5, Claude Code 2.1.267, macOS.

## Sub-probe 1: broker up. PASS.
nats-server -js on 127.0.0.1:4222 (monitor :8222), config in this dir, data in
~/.director/nats. Streams created:
- AGENT_INBOX: subjects agent.*.*.*.inbox and agent.*.*.role.*.inbox, file
  storage, limits retention, 24h max-age, 64KiB max-msg, 2m dedupe window.
- AGENT_AUDIT: subject agent.audit, file, append-only, 30d.
- KV AGENT_STATE: presence. Note: this build reports per-key TTL unsupported,
  so the bucket carries a 90s TTL; a heartbeat rewrite resets key age, absence
  after 90s of silence. Adequate for presence; revisit if per-key TTL needed.

Not yet persisted to launchd (deliberate, per staging: prove before persist).
Restart: nats-server -c probe/nats-phase-0/nats-server.conf &

## Sub-probe 2: envelope-v1 on the wire. PASS.
Published envelope-v1 (REQUEST, agent://ops/michael) to
agent.aae-orc.ops.michael.inbox with Nats-Msg-Id = message_id. Delivered to a
durable per-agent consumer, validated intact (schema_version 1, performative
REQUEST, sender.principal null as the schema requires). Also landed on
agent.audit. DEDUPE VERIFIED: two publishes with the same Nats-Msg-Id left
exactly one message stored, which is R-13 (duplicate detection) satisfied at
the transport rather than in application code.

## Sub-probe 3: director-mcp shim + push-into-context. BUILT AND PASSING.

Shim at director-mcp/ (Go, ~600 lines, MCP stdio implemented directly, NATS
via the official client). Five tools: send_message, wait_for_message,
list_roster, set_presence, broadcast. Launched per agent with
DIRECTOR_AGENT_ID / DIRECTOR_TEAM / DIRECTOR_WORKSPACE / NATS_URL, logs to
stderr, JSON-RPC on stdout.

End-to-end through the real MCP stdio path, two independent shim processes:
- reviewer-a: initialize, tools/list (5 tools), send_message to
  agent://ops/michael -> accepted for delivery with a ULID message_id.
- michael: wait_for_message -> RECEIVED the envelope intact (REQUEST, correct
  sender and recipient, sender.principal null), list_roster -> both agents
  present from the KV.

PUSH-VS-POLL, SETTLED IN CODE: the working receive shape is wait_for_message,
a poll the agent calls. MCP is request/response, so an MCP shim cannot
originate a message into the model's context; the server answers, it never
speaks first. This is the brief's fallback shape, and it is the only shape an
MCP transport offers. The other shape (a hook bridging NATS to the harness
injection path) is not an MCP concern and remains unbuilt. The finding-159
sentence holds at the transport: the harness owns push and keeps it internal.

## Sub-probe 4: offline queueing. PASS (fell out of sub-probe 3).

reviewer-a sent and its process EXITED before michael ever connected. michael
then connected and its first wait_for_message returned the message. JetStream
limits retention plus a durable per-agent consumer with DeliverAll is
store-and-forward: a message sent to an absent session waits in the stream and
replays when that session first pulls. No loss, delivered in order.

## Sub-probe 5: human participation + latency. PASS (live two-party exchange through the operator's Claude Code, 2026-09-12).
The shim must be wired into an actual Claude Code session (claude mcp add) so
the operator's own session joins as agent://ops/michael and a live two-way
exchange can be timed. This is the harness-integration boundary and a config
change to the user's Claude Code, held for operator go-ahead. The bus-level
latency is sub-millisecond locally; the meaningful number is how long until
the model next CALLS wait_for_message, which is a poll-cadence property, not a
bus property.

TRANSPORT FLOOR MEASURED 2026-09-11. Two fresh shim processes (lat-rx
blocked in wait_for_message, lat-tx sending): send_message to a
blocked waiter surfaced the envelope end-to-end through the full MCP
stdio + NATS durable-consumer path in ~20 ms (single run, 1 ms poll
granularity in the harness; order is tens of ms, dominated by JetStream
pull-consumer delivery scheduling, not the sub-ms core publish). This is
the floor beneath the poll cadence, not the latency an operator feels.
The felt latency is how long until the model next CALLS
wait_for_message, which is seconds-plus and needs a live model to
measure. That half stays held for operator go-ahead (claude mcp add).

LIVE HARNESS BOUNDARY VERIFIED 2026-09-11. Registered the shim with the
operator's Claude Code at local scope (claude mcp add --scope local
director-mcp, env DIRECTOR_AGENT_ID=michael/TEAM=ops/WORKSPACE=aae-orc).
`claude mcp list` reports director-mcp Connected: Claude Code's own MCP
client launched the binary, connected to NATS, and completed
initialize + tools/list. The connect fired the presence heartbeat, and
presence.ops.michael landed in the KV (state idle, workspace aae-orc) as
read back live. So a genuine Claude Code session joins the bus as
agent://ops/michael, not a synthetic shim. That is the harness-integration
boundary of sub-probe 5, done against a live harness.

WHAT REMAINS, AND WHY: the tools (send_message, wait_for_message, ...) load
at Claude Code session START, so the session that ADDED the server does not
have them; a fresh session in this project does. The felt poll-cadence
number is therefore measured from a session that has the tools loaded, i.e.
after a restart. This is itself the finding-159/160 shape restated at the
harness: even wired in, an inbound message waits until the model chooses to
call wait_for_message. The harness offers no push into an already-running
session's context; the poll is the receive.

To remove the wiring: claude mcp remove director-mcp --scope local.

LIVE TWO-PARTY EXCHANGE 2026-09-12. After a session restart the tools
loaded, so this Claude Code session held director-mcp as agent://ops/michael
and drove the full loop from inside the model:
- set_presence(busy) + list_roster returned michael present (ops, aae-orc):
  presence is live transport state read back by the real harness client.
- An independent reviewer-a shim sent a REQUEST to agent://ops/michael and
  exited. The live session then called wait_for_message and the envelope
  surfaced intact (REQUEST, sender reviewer-a, principal null, refs kept,
  conversation_id set). This is the receive-is-a-poll shape confirmed in the
  real harness, not a script: the message sat in the durable inbox and
  surfaced only on the model's own wait_for_message call. There is no push.
- The session replied AGREE with in_reply_to set. reviewer-a was offline;
  it rejoined and its wait_for_message returned the AGREE intact. So
  store-and-forward (R-22) holds with the live session as the SENDER too,
  and the REQUEST/AGREE handshake completes end-to-end through the harness.

Felt latency, settled qualitatively: the number that dominates is not the
~20 ms transport floor but how long the message waits for the model's next
poll. That is a model-behavior property (when does the agent choose to call
wait_for_message), not a bus property, and the live run makes it concrete:
nothing arrived until the poll. finding-159/160 hold at the harness.

Note: killing processes by a broad `director-mcp` pattern also kills the
harness-spawned server for this session (it IS a director-mcp process).
Scope any cleanup to the reviewer-a shims by env or fifo, not the binary name.

## Sub-probe 6: per-session identity + shim-timer heartbeat. PASS (2026-09-12, aae-orc-lvzck).

Motivated by a live finding: during the ArcavenAE-status task the roster read
empty while a session's shim was running, because the shim set presence once on
connect and never renewed, so the 90s TTL expired under a live session. Built
R-49, R-50, R-56 into the shim (bus.go, main.go) and tested:

- R-50, unique durable per session: the durable name now includes a per-session
  instance ulid (mcp_<id>_<instance>). Two shims launched with the SAME id
  (duptest) each got their own copy of one INFORM addressed to agent://ops/duptest;
  neither silently lost it. A duplicate id degrades to duplication, not the
  silent-loss race of a shared durable. The real fix stays distinct ids (R-49);
  this is the safety net.
- R-49, collision is loud: presence is now keyed per session
  (presence.<team>.<id>.<instance>), so the roster showed two distinct duptest
  entries rather than collapsing to one, and the second shim logged a WARNING
  naming the other instance and pid. The probe chose warn-and-observe; whether
  the shim should REFUSE to start or auto-disambiguate on collision is left open.
- R-56, shim-timer heartbeat: a background goroutine renews presence every 30s
  (TTL/3), off the model's poll. Verified: presence ts advanced 15:02:26 to
  15:02:56 with no tool call. This closes the empty-roster gap that motivated
  the sub-probe.

Cost accepted for the probe: a per-instance durable under DeliverAll replays the
stream on a fresh instance, so a restart re-reads history. A stable per-logical-
session durable needs R-06's durable conversation identity, which does not exist
yet. Recorded, not fixed here.

## Reproducing a cross-harness exchange (2026-09-12)

The worked script is `cross-harness-demo.sh` in this directory. The recipe is
the durable part; the flags are easy to lose.

Join a NON-Claude-Code harness to the bus by pointing its MCP client at the
director-mcp binary, per invocation, without editing the user's config:

- codex: `codex exec -c 'mcp_servers.director.command="<abs path>"' -c
  'mcp_servers.director.env.DIRECTOR_AGENT_ID="codex-a"' -c '...TEAM' -c
  '...WORKSPACE' -c '...NATS_URL' -C <cwd> "<prompt>"`. Overrides layer on top
  of `~/.codex/config.toml`, so auth and model stay. Three gotchas: pass
  `--skip-git-repo-check` for a non-git cwd; pass
  `--dangerously-bypass-approvals-and-sandbox` or codex refuses the MCP tool
  call with "requires approval, but approval policy is never"; redirect stdin
  from `/dev/null` or `codex exec` blocks reading a piped stdin.
- headless Claude Code: `claude -p --mcp-config '{"mcpServers":{"director":
  {"command":"<abs>","env":{"DIRECTOR_AGENT_ID":"cc-planner", ...}}}}'
  --allowedTools "mcp__director__set_presence,mcp__director__wait_for_message,
  mcp__director__send_message,mcp__director__list_roster" --output-format text
  "<prompt>"`. The allowlist lets a non-interactive run call the tools without
  a permission prompt.

Read the live roster without a client: `nats --server nats://127.0.0.1:4222 kv
ls AGENT_STATE` and `kv get AGENT_STATE presence.<team>.<id> --raw`.

Two operational cautions learned here:
- Assign a DISTINCT id per session at the launcher. Two sessions launched with
  the same id is the michael collision (R-49): shared durable, raced or
  duplicated mail, one presence key.
- Do NOT `pkill -f 'director-mcp'` to clean up shims. The harness-spawned
  server for THIS session is also a director-mcp process, so the broad match
  kills your own bus tools. Scope cleanup by env
  (`pkill -f 'DIRECTOR_AGENT_ID=codex-a'`) or by fifo, never by the binary name.

## Sub-probe 8: broker authorization (director#4). PASS (2026-09-12).

Closes the open ">" defect (director#4, R-77, R-82). authorization.conf adds an
authorization block that binds a credential to the subjects it may use;
nats-server-auth.conf is the activation config (base plus authorization),
launched only at a coordinated relaunch, so nats-server.conf stays anonymous
and no routine restart drops the live fleet. It matches the shim contract in
bus.go connect() (DIRECTOR_NATS_USER, DIRECTOR_NATS_PASS). DIRECTOR_NATS_CREDS
(JWT or NKey) is the forward path and is deliberately not wired at the broker in
Phase 0, since full JWT needs operator plus account plus resolver mode.
Passwords are environment interpolated, so no secret is committed.

Verified on a throwaway broker on :4223 with its own store (verify-auth.sh),
the live :4222 untouched. 7 of 7: anonymous publish refused; the ops credential
publishes and subscribes inside agent.aae-orc.ops.>, drives the team broadcast,
and reads and writes the presence KV; the ops credential is refused both
publishing to and subscribing into agent.aae-orc.secops.>.

Two residuals, both forward (marvel-owned) and not this fix: one credential per
team rather than per session, which per-session minting closes (R-85); and
JetStream is account-scoped, so per-team stream isolation needs per-team
accounts or streams (R-86). The defect closed here is the unauthenticated open
">".

## Kill criteria status
Not triggered. The push risk is real but has at least the long-poll shape, so
the probe is not dead; it is checkpointed at a clean foundation.
