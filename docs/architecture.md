# Director architecture

How the pieces fit, in diagrams. The prose that governs each shape lives in
`../sim/requirements.md` (the R-numbers cited below) and the envelope design
in `aae-orc/docs/design/director-envelope-and-adapter-events.md`. This file is
the picture; those files are the contract.

Status: the transport is proven (NATS Phase 0 probe, `../probe/nats-phase-0/`),
the director software proper is unbuilt, and the role runs as a skill a session
plays by hand. Diagrams show the target shape, with the current reality called
out where it differs.

## System overview

One human with one attention budget. Director spends it well and loses nothing
while it does. Sessions join a message bus through a small per-session shim; the
custody store is director's own memory and needs no transport at all.

```mermaid
flowchart TB
  H["Human<br/>(one attention budget, R-33)"]
  D["director<br/>assistive agent, not supervisor, not peer"]
  BD["custody store<br/>board.md + ask state<br/>no transport, survives restart (R-34, R-42)"]

  subgraph bus["message bus, NATS JetStream"]
    IN["per-agent inbox<br/>durable, store-and-forward (R-22)"]
    RO["role inbox<br/>resolved at delivery"]
    BC["broadcast<br/>no durable queue"]
    AUD["audit stream<br/>append-only, every envelope"]
    PR["presence KV<br/>heartbeat, TTL (R-15, R-19)"]
  end

  subgraph sessions["agent sessions"]
    CC["Claude Code<br/>+ director-mcp shim"]
    CX["codex + shim"]
    SUP["supervisor agent<br/>(remote fleets, R-32)"]
  end

  H <-->|"terse, ranked, one line per item (R-35)"| D
  D <--> BD
  D <-->|"envelope v1, FIPA-ACL performatives"| bus
  bus <--> CC
  bus <--> CX
  bus <--> SUP
  SUP <-->|"other hosts, other accounts"| sessions
```

The custody store sits beside director, not on the bus, on purpose: R-34 says
director's first useful product is a board that needs no transport, and R-42
says the store survives a restart with the queue intact. Routing is downstream
of custody, never the other way around.

## The reachability reality today

Measured on one machine (`../skills/director/reference/adapters.md`): of four
installed harnesses, exactly one exposes presence or an address. The other
three can be read off disk and cannot be messaged. This is why director cannot
be a thin wrapper over any single harness.

```mermaid
flowchart LR
  D["director"]
  subgraph reach["reachable: presence + address"]
    CC["Claude Code"]
  end
  subgraph read["readable only, no address, R-28"]
    CX["codex"]
    OC["opencode"]
    CR["crush"]
  end
  D <-->|"send and receive"| CC
  D -.->|"read store, cannot message"| CX
  D -.->|"read store, cannot message"| OC
  D -.->|"read store, cannot message"| CR
```

R-27 is the rule this reality forces: adapters declare what they supply, and
director degrades per capability rather than assuming a session can be reached.

## Addressing

An envelope names a recipient by address. The bus resolves the address to a
subject. Three address forms, each with its own delivery guarantee.

```mermaid
flowchart TD
  A1["agent://team/id"] --> S1["agent.ws.team.id.inbox<br/>durable, at-least-once, dedupe on message_id (R-13)"]
  A2["role://team/role"] --> S2["agent.ws.team.role.role.inbox<br/>resolved to current holder at delivery"]
  A3["broadcast://ws[/team]"] --> S3["agent.ws.broadcast<br/>fan-out, no replay for late joiners"]
  S2 -->|"no holder"| NU["NOT-UNDERSTOOD back to sender"]
```

## Sequence: receive is a poll, not a push

The Phase 0 probe settled this in code (finding-160, finding-159). MCP is
request and response, so a shim cannot originate a message into a running
model's context. The message waits in the durable inbox until the model calls
`wait_for_message`. Store-and-forward (R-22) means a message sent to an absent
session is not lost; it replays when that session first pulls.

```mermaid
sequenceDiagram
  participant Snd as reviewer-a (sender)
  participant Bus as NATS inbox (durable)
  participant Sh as director-mcp shim
  participant M as michael (model session)

  Snd->>Bus: send_message to agent://ops/michael
  Note over Bus: envelope stored, michael offline
  Snd-->>Snd: "accepted for delivery" + message_id (R-08: not delivered, not read)
  M->>Sh: wait_for_message (the poll)
  Sh->>Bus: fetch(1), long wait
  Bus-->>Sh: envelope replays from the stream
  Sh-->>M: envelope surfaces in context
  Note over M: felt latency = time until the next poll,<br/>transport floor measured ~20 ms
```

## Sequence: acknowledgement comes from the receiver

The defect that started the project: in session 1, five messages were dropped
while every send returned success. R-08 rules that the send call acknowledges
nothing but acceptance; only the receiver, after processing, produces liveness
(R-14). R-09 rules that an undeliverable message must be loud.

```mermaid
sequenceDiagram
  participant D as director
  participant Bus as bus
  participant R as receiver

  rect rgb(245, 225, 225)
    Note over D,Bus: the old substrate (the defect)
    D->>Bus: send
    Bus--xR: silently dropped
    Bus-->>D: success (a lie, R-09)
  end
  rect rgb(225, 240, 225)
    Note over D,R: director's contract
    D->>Bus: send
    Bus-->>D: accepted for delivery + message_id
    Bus->>R: deliver
    R-->>D: processed / done (R-14, receiver-produced)
  end
```

## Sequence: the lift (marvel seam)

Director carries speech; marvel's adapters carry observations. A supervisor
(or an adapter, by policy) may lift an observation into an envelope. Lifting is
explicit, never automatic mirroring, so the bus carries decisions and signals
rather than raw telemetry.

```mermaid
sequenceDiagram
  participant Ha as harness
  participant Ad as marvel adapter
  participant Sup as supervisor
  participant Bus as bus

  Ha->>Ad: permission.requested (blocking approval)
  Ad->>Sup: normalized event
  Sup->>Sup: policy decides this is worth the human's attention
  Sup->>Bus: REQUEST to role://team/supervisor (lifted)
  Note over Sup,Bus: correlation_id = session:harness_session_id,<br/>pointer back to the event stream, never the raw payload
```

## State: the lifecycle of an ask

An ask is not binary. R-21 gives it explicit states, R-22 makes it survive the
session that raised it, and R-24 keeps blocked distinct from failed and from
done. The state that costs the most when it is missed is `stranded`: a dead
process leaves no roster entry, so without custody the ask vanishes with it.

```mermaid
stateDiagram-v2
  [*] --> open: session raises an ask
  open --> parked: reason + wake condition (R-21)
  parked --> open: wake condition met
  open --> discharged: answered, delivered and acked (R-08)
  parked --> discharged: answered
  open --> stranded: session dies holding it (R-22)
  stranded --> discharged: director re-routes or human answers
  discharged --> [*]
```

## Transaction: the performative handshake

An envelope's `performative` (a 12-verb FIPA-ACL subset) is the state of a
commitment, not just a message type. A REQUEST is answered by AGREE or REFUSE;
an agreed request that cannot be met is a FAILURE, which R-25 keeps distinct
from partial compliance and R-24 keeps distinct from a plain refusal.

```mermaid
stateDiagram-v2
  [*] --> Requested: REQUEST
  Requested --> Agreed: AGREE
  Requested --> Refused: REFUSE
  Requested --> NotUnderstood: NOT-UNDERSTOOD (protocol reject)
  Requested --> Cancelled: CANCEL (withdrawn)
  Agreed --> Done: INFORM (result)
  Agreed --> Partial: INFORM (partial compliance, R-25)
  Agreed --> Failed: FAILURE (agreed then could not)
  Refused --> [*]
  NotUnderstood --> [*]
  Cancelled --> [*]
  Done --> [*]
  Partial --> [*]
  Failed --> [*]
```

## Where to go next

- Worked use cases with diagrams: `use-cases.md`
- The requirements each diagram serves: `../sim/requirements.md`
- The transport probe that proved the bus: `../probe/nats-phase-0/PROGRESS.md`
- The envelope and adapter-event co-design:
  `aae-orc/docs/design/director-envelope-and-adapter-events.md`
