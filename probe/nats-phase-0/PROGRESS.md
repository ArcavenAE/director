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

## Sub-probe 5: human participation + latency. PARTIAL (transport floor measured; live model pending).
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

## Kill criteria status
Not triggered. The push risk is real but has at least the long-poll shape, so
the probe is not dead; it is checkpointed at a clean foundation.
