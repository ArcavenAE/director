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

## Sub-probe 3: director-mcp shim + push-into-context. NOT BUILT. Early finding stands.

The shim is the multi-day core and is not built. But the probe's stated
most-valuable output, "which delivery shape works," has an answer already
grounded in this session's own evidence, ahead of the build:

Claude Code HAS push-into-context. Its native SendMessage delivers a
cross-session message that appears in a peer session's context as
<cross-session-message ...>, wrapped in the harness's own policy paragraph
(finding-159). That is server-initiated push into the model's context, and it
already works locally.

It is not available to an external MCP server. MCP is request/response: the
client (the harness) calls the server; the server cannot unilaterally inject
into the model's context. So an MCP-based director shim cannot achieve true
push on its own. Two shapes remain, to be measured when the shim is built:
1. wait_for_message as a long-poll tool the agent calls (the brief's fallback);
   cost is tokens and a blocked turn.
2. a hook that bridges NATS to the harness's own injection path (the mechanism
   SendMessage already uses), if that path is reachable from a hook.

This sharpens R-46 and finding-159: the harness owns the one capability the
bus needs (push into context) and does not expose it to the bus. Director's
independence from any one harness collides here with the fact that push is
currently a harness-internal privilege. This is the load-bearing risk the shim
build must resolve, and it is now stated before the build rather than after.

## Sub-probes 4 (offline queueing) and 5 (human participation, latency): NOT RUN.
Both depend on the shim. Offline queueing is partly pre-validated: JetStream
limits retention + durable consumer already stores messages for an offline
inbox (a consumer that connects later replays from the stream).

## Kill criteria status
Not triggered. The push risk is real but has at least the long-poll shape, so
the probe is not dead; it is checkpointed at a clean foundation.
