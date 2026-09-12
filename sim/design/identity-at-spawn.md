# Design brief 1: session identity at spawn

Status: draft for the director requirements register, 2026-09-12. Written from
a live incident this session plus the A2A and NATS research. Provisional tags
(ID-A, ...) are not R-numbers; the register runs to R-48 and numbers are
assigned at harvest.

## The problem, stated precisely

`claude mcp add --scope local` wrote one MCP server config with
`DIRECTOR_AGENT_ID=michael` (the OS user) baked in. Every Claude Code session
launched in the project loads that same env, so every session's shim connected
to the bus as `agent://ops/michael`. Two real sessions ran at once and both
were michael.

The failure is worse than an ambiguous label. Both shims create the same
durable consumer, `mcp_michael`, on the same filter subject
`agent.aae-orc.ops.michael.inbox`. In JetStream two clients binding one durable
name share it: each message is delivered to exactly one of them, whichever
calls `wait_for_message` first. So a message addressed to michael is not
duplicated to both sessions, it is raced, and the session that loses the race
never sees mail that the sender was told was delivered. That is the original
silent-drop defect (the incident that started the project, R-08 and R-09)
re-entering through the identity layer. Presence collides too: both write
`presence.ops.michael`, last writer wins, so the roster shows one michael where
two sessions exist.

Root cause: identity is sourced from static configuration and defaults to the
OS user. The OS user answers "whose machine is this," never "which session am
I." Nothing per-session is handed to an MCP server by the harness at spawn, so
the shim has no per-session signal to derive identity from unless the launcher
supplies one.

## What the empirical run proved

Launched with distinct ids, `agent://ops/cc-planner` on Claude Code and
`agent://ops/codex-a` on codex, a full REQUEST then AGREE handshake routed
correctly across two harnesses (codex-a's REQUEST reached cc-planner;
cc-planner's AGREE, correlated by in_reply_to, reached codex-a). The bus is not
the problem. The collision is purely an identity-source problem, and distinct
identities route cleanly. This is OBSERVED, this session.

## Three concepts the collision had fused

- **Address**: where to reach a session (`agent://team/id`), the inbox subject.
- **Seat**: whether a session is currently the human's director (design-set 2).
- **Durable conversation identity**: the stable handle R-06 says to key on,
  which survives a restart even as pid, socket, and roster name change.

These are three different things. The michael collision fused address with the
OS user and left the seat undefined. Keeping them separate is the design.

## Options

### ID-A. Launcher-assigned name (recommended for the physical-access phase)

Whatever spawns a session assigns a distinct `DIRECTOR_AGENT_ID`. A `director
spawn` wrapper does it, or marvel does it (marvel already passes
`--name`/`--team`/`--workspace` per agent). The session never picks its own
name any more than a process picks its own pid.

- Cost: near zero. Closes the collision immediately.
- Honesty: it is a LABEL, not an attestation. It carries no forgery
  resistance. This is the right amount of rigor while every session is
  physical-access to the human, because the human is the trust boundary.
- Requires: per-session durable consumer naming so two sessions never bind one
  durable (the race fix below), and a distinct presence key per id.

### ID-B. Self-minted DID plus signed AgentCard (the A2A model)

At spawn the shim generates a W3C Decentralized Identifier and a keypair, and
publishes a JWS-signed AgentCard (JWS per RFC 7515 over a canonicalized card,
JCS per RFC 8785, so the signature is stable across serializers). Identity is
self-owned, needs no central authority, and is verifiable by any recipient.

- Cost: real. Key custody, card distribution, verification path.
- Value: forgery resistance, portability when sessions run off-host, and it is
  standards-based rather than homegrown.
- Unneeded while the human is the trust boundary; needed when that boundary
  leaves the local host.

### ID-C. Hybrid (recommended trajectory)

Human-meaningful launcher name now (ID-A), a DID bound to that name later
(ID-B) when sessions run off-host or under more than one operator. ID-A ships
today without becoming a dead end, because the DID attaches to the same name
rather than replacing the scheme.

## The durable-consumer race fix

Independent of which identity option is chosen: each session must bind its own
durable consumer. Today the durable name is derived from the agent id
(`mcp_<id>`), so identical ids share a durable and race. With distinct ids this
is already fixed; the requirement is to make it structural, the durable name
must be unique per session, never per configured id, so that even a
misconfigured duplicate id cannot silently steal another session's mail.

## Candidate requirements

- **ID-A (OBSERVED)**: Session identity is assigned at spawn by the launcher,
  never self-asserted by the session and never derived from the OS user. On one
  host, N sessions get N distinct addresses. Earned by: two live sessions
  collided on `agent://ops/michael` this session.
- **ID-B (OBSERVED)**: Two sessions sharing one address share one durable
  consumer and race for delivery, so the loser silently loses mail; a session's
  durable consumer must be unique per session. Earned by: the JetStream durable
  semantics plus the michael collision this session.
- **ID-C (OBSERVED)**: Distinct identities route correctly across harnesses; the
  bus supports multiple sessions once identity is distinct. Earned by: the
  cc-planner and codex-a REQUEST/AGREE handshake this session.
- **ID-D (JUDGMENT)**: Address, seat, and durable conversation identity are
  three separate concepts and must not be fused; an address says where to reach
  a session, not whether it is the director and not who owns it across a
  restart. Earned by: the collision fused address with the OS user; R-06 already
  separates durable identity from address.
- **ID-E (JUDGMENT)**: A launcher-assigned name is a label, not an attestation,
  and nothing may treat it as a security control; cryptographic identity (a DID
  plus a signed card, the A2A model) is a deferred axis that returns when the
  trust boundary leaves the local host. Earned by: A2A v1.0 puts identity
  out-of-band (DID, JWS-signed AgentCard, credentials in headers), which is the
  standards-based target; the physical-access phase does not need it.

## Open questions

- Where does the launcher get the name? Operator-supplied, or minted from a
  human-meaningful scheme (role plus ordinal, `reviewer-a`)? Names must be
  stable across a restart to satisfy R-06, so a random per-launch ULID is not
  enough on its own.
- Does the shim learn the harness session id at all? Today no harness hands it
  to an MCP server. If one did, it could seed durable-consumer uniqueness
  without a launcher.
- How does ID-A migrate to ID-C without renaming live sessions (R-06 says a
  rename is an identity change)?
