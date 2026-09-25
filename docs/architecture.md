# Director architecture

How the pieces fit, in diagrams. The prose that governs each shape lives in
`../sim/requirements.md` (the R-numbers cited below) and the envelope design
in `aae-orc/docs/design/director-envelope-and-adapter-events.md`. This file is
the picture; those files are the contract.

Status: the transport is proven (NATS Phase 0 probe, `../probe/nats-phase-0/`),
the global tier runs as a hub with leaf clusters (`../probe/nats-global-tier/`),
marvel provisions and credentials the local tier for the fleets it manages
(`../sim/twin/`), and the director software proper is unbuilt: the role runs as
a skill a session plays by hand. Diagrams show the target shape, with the
current reality called out where it differs. Operating detail is in
`getting-started.md` and `shim-reference.md`.

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

## The two tiers

One broker per cluster carries team traffic; one global broker carries the
director channel between clusters and hosts (R-86). A session keeps a single
connection, to its local broker. Global traffic rides a leaf link the local
broker holds to the hub, authenticated with a cluster-scoped NKey the hub
operator minted. The director's own session connects to the hub directly.

```mermaid
flowchart TB
  subgraph hub["global hub (kinu)<br/>client 4242, leaf 7442, monitor 8242, domain global"]
    GD["GLOBAL_TO_DIRECTOR<br/>global.director.inbox"]
    GC["GLOBAL_TO_&lt;cluster&gt;<br/>global.&lt;cluster&gt;.supervisor.inbox"]
    GP["GLOBAL_PRESENCE KV<br/>director and supervisor records"]
  end
  DIR["director session<br/>DIRECTOR_GLOBAL_ROLE=director"]
  subgraph c1["cluster kinu (hand-run broker)"]
    B1["nats-server, domain kinu"]
    S1["sessions + shims"]
  end
  subgraph c2["cluster mokuzai (marvel-managed broker)"]
    B2["nats-server, domain mokuzai"]
    SUP2["supervisor session<br/>DIRECTOR_GLOBAL_ROLE=supervisor"]
    S2["team sessions + shims"]
  end
  DIR <--> hub
  B1 -. "leaf, NKey for kinu" .-> hub
  B2 -. "leaf, NKey for mokuzai" .-> hub
  S1 <--> B1
  SUP2 <--> B2
  S2 <--> B2
```

The hub binds each leaf credential to its cluster's subject prefix, so a
cluster can publish only into its own global subjects and the director's inbox.
A down hub is an observable, never a health state: local traffic continues,
global sends queue on the leaf side until the link returns. The hub's
configuration, provisioning, and the per-host recipe are in
`../probe/nats-global-tier/`.

## Addressing

An envelope names a recipient by address. The bus resolves the address to a
subject. Five address forms, each with its own delivery guarantee. The first
three stay inside the cluster; the last two cross it.

```mermaid
flowchart TD
  A1["agent://team/id"] --> S1["agent.ws.team.id.inbox<br/>durable, at-least-once, dedupe on message_id (R-13)"]
  A2["role://team/role"] --> S2["agent.ws.team.role.role.inbox<br/>resolved to current holder at delivery"]
  A3["broadcast://ws[/team]"] --> S3["agent.ws.broadcast<br/>fan-out, no replay for late joiners"]
  A4["global://director"] --> S4["global.director.inbox<br/>durable on the hub, from any cluster"]
  A5["global://{cluster}/supervisor"] --> S5["global.{cluster}.supervisor.inbox<br/>durable on the hub, one per cluster"]
  S2 -->|"no holder"| NU["NOT-UNDERSTOOD back to sender"]
```

A global address carries no workspace token. The cluster name stands in for
it, which is why marvel validates a cluster name as a subject token and why a
shim needs `DIRECTOR_CLUSTER` before it can send or receive globally (R-92).
The workspace in `DIRECTOR_WORKSPACE` still scopes every local address.

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

## The marvel seam

Director owns the protocol: the envelope, the addresses, the shim, the hub.
Marvel owns the substrate under a managed fleet: the local broker, its
provisioning, per-team credentials, and the identity every session is stamped
with at spawn. Neither requires the other (a hand-run broker and a hand-set
environment reproduce everything below), and the seam is four environment
variables.

```mermaid
flowchart LR
  subgraph marvel["marvel daemon (cluster config: bus.managed = true)"]
    N["nats-server child<br/>renders conf, provisions AGENT_INBOX / AGENT_AUDIT / AGENT_STATE"]
    AUTH["authorization.conf<br/>marvel_admin + one user per team"]
    ENV["session environment<br/>NATS_URL, DIRECTOR_NATS_USER, DIRECTOR_NATS_PASS,<br/>DIRECTOR_AGENT_ID = session name"]
    LEAF["bus/leaf credential (memory only)<br/>becomes the leaf remote to the hub"]
  end
  subgraph director["director (per session)"]
    SHIM["director-mcp shim<br/>reads exactly those variables"]
    PROTO["envelope v1, presence, six tools"]
  end
  ENV --> SHIM
  N <--> SHIM
  LEAF -.-> HUB["global hub"]
```

What marvel supplies, and what it does not:

- **Broker and streams.** Started before any session, provisioned with the
  parameters the shim expects, reloaded when teams change, restarted under
  backoff if it exits. No session spawns while the bus is down or bare.
- **Identity.** `DIRECTOR_AGENT_ID` is the marvel session name
  (`<team>-<role>-g<gen>-<index>`), so a bus id is unique per generation and
  a shift produces a new presence record rather than a collision.
- **Credentials.** A per-team broker user confined to that team's subtree;
  the leaf seed for the hub held in daemon memory and pushed by the hub
  operator over a scoped `mrvl://` key. Neither is written to disk by
  marvel outside the broker's 0600 authorization file.
- **Not the protocol.** Marvel never reads or writes an envelope. It does
  not know what a REQUEST is, and its events are observations, not speech.

### Sequence: the lift

Director carries speech; marvel's adapters carry observations. A supervisor
(or an adapter, by policy) may lift an observation into an envelope. Lifting is
explicit, never automatic mirroring, so the bus carries decisions and signals
rather than raw telemetry. This is target shape; today the supervisor is a
session reading `marvel events`.

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

### Sequence: the cast under marvel (the twin)

`../sim/twin/` is the worked instance: a marvel manifest for a fleet whose
sessions join the bus at spawn. The launcher in the manifest's `command` is a
shell wrapper that runs the shim's preflight, then execs the harness with the
shim registered.

```mermaid
sequenceDiagram
  participant M as marvel reconciler
  participant T as tmux pane
  participant L as cast-launch.sh
  participant Sh as director-mcp
  participant B as local broker
  participant H as Claude Code

  M->>B: broker ready, provisioned (bus.started, bus.provisioned)
  M->>T: spawn: env = MARVEL_*, NATS_URL, DIRECTOR_NATS_USER/PASS, DIRECTOR_AGENT_ID
  T->>L: exec
  L->>Sh: director-mcp --preflight
  Sh->>B: connect, check streams and KV
  alt preflight fails
    Sh-->>L: exit 2 (config) or 1 (connect)
    L-->>T: exit non-zero
    Note over M: session.crashed, charged to the role; no silent running-with-no-presence
  else ok
    L->>H: exec claude, adapter flags passed through, shim as an MCP server
    H->>Sh: MCP handshake
    Sh->>B: presence record (TTL 90 s, renewed every 30 s), durable consumer mcp_&lt;id&gt;_&lt;instance&gt;
    H-->>M: statusline heartbeat (marvel health) while the shim keeps presence (director liveness)
  end
```

Two liveness signals, two owners, on purpose. Marvel's heartbeat says the
process is alive and how full its context is; director's presence says the
agent can be reached. A session can have one without the other, and finding-166
is the case where a bus that was down or unprovisioned let a session report
running with no presence; the preflight in the launcher is what closes it.

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

## The reachability reality today

Measured on one machine (`../skills/director/reference/adapters.md`): of four
installed harnesses, exactly one exposes presence or an address without the
shim. The other three can be read off disk and cannot be messaged. With the
shim, any harness that speaks MCP joins the bus; codex does today.

```mermaid
flowchart LR
  D["director"]
  subgraph reach["reachable: presence + address"]
    CC["Claude Code + shim"]
    CX2["codex + shim"]
  end
  subgraph read["readable only, no address, R-28"]
    OC["opencode"]
    CR["crush"]
  end
  D <-->|"send and receive"| CC
  D <-->|"send and receive"| CX2
  D -.->|"read store, cannot message"| OC
  D -.->|"read store, cannot message"| CR
```

R-27 is the rule this reality forces: adapters declare what they supply, and
director degrades per capability rather than assuming a session can be reached.

## Where to go next

- Get two sessions talking: `getting-started.md`
- Every variable, tool, subject, and field: `shim-reference.md`
- Worked use cases with diagrams: `use-cases.md`
- The requirements each diagram serves: `../sim/requirements.md`
- The transport probe that proved the bus: `../probe/nats-phase-0/PROGRESS.md`
- The global tier, hub side: `../probe/nats-global-tier/`
- The fleet as a manifest: `../sim/twin/`
- The envelope and adapter-event co-design:
  `aae-orc/docs/design/director-envelope-and-adapter-events.md`
