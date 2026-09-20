# Binding harness status to NATS presence, and what else NATS can do for us

- **Status:** idea (pre-hypothesis, no commitment). A cluster of open
  questions raised by the operator (2026-09-19) after a session of standing
  up teams across two clusters and repeatedly hitting presence and delivery
  edges.
- **Date:** 2026-09-19
- **Tracking:** bd aae-orc-ebc32 (work item for these questions).
- **Subject:** director (the comms/bus layer owns presence). marvel (which
  runs the harnesses whose state we want to reflect) and NATS (the transport)
  are objects this reasons about, not co-owners. If director did not exist,
  this node would not be filed.
- **Related:** `nats-request-recovery-ledger.md` (durable memory of
  outstanding instructions; the "did the agent act" gap). marvel
  `tmux-harness-state-watchdog.md` (detecting a stuck/crashed/logged-out
  harness from its pane). findings 160 (receive is a poll; idle harness
  sessions never poll their inbox), 169 (bus addressing, unattended
  operation). Protocol facts observed this session: R-08 (accepted is not
  delivered or read), R-92 (a global send to an absent seat is refused, not
  queued). Capture patterns for the harness states themselves:
  `../../../docs/drafts/stagekeeper-first-run-states-2026-09-19.md`.

## The questions (as posed, not yet answered)

1. **How do we bind a harness's status to a NATS presence status?** A claude
   or codex seat has states like working, at an idle prompt, waiting on an
   interactive prompt it cannot answer, context-low, crashed, logged-out,
   self-update-pending. NATS/director presence has its own small vocabulary.
   What is the mapping, and who sets it (the shim on a timer, the harness via
   a hook, a watcher reading the pane)?
2. **What presence statuses exist?** Observed in `list_roster` this session:
   `idle`, `busy`, `away`. Are those the full set? What transitions them, and
   is `away` a timeout/stale-heartbeat state or an explicitly set one? The
   director seat showed `away` while it was the thing we most needed to read
   its inbox.
3. **What should happen when director or an agent goes offline?** Today the
   behavior is inconsistent: the local `AGENT_INBOX` queues (we drained hours-
   old backlog from it this session), while a global send to an absent seat is
   refused outright (R-92). Should offline seats get store-and-forward with a
   TTL, a dead-letter path, or a loud refuse? What is right for the director
   specifically, since a missed director message stalls a whole chain?
4. **How can use, or deliberate misuse, of presence benefit director and
   marvel?** Candidates to explore: presence transitions as a cheap liveness
   signal (distinct from marvel health, which is heartbeat-only); presence as
   a scheduling input (do not dispatch to a `busy` or context-low seat; prefer
   an `idle` one); presence-gated delivery (hold or reroute if the target is
   `away`); a supervisor watching its team's presence as the status cache the
   operator described.

## Grounding from this session (why the questions are live)

- Cross-cluster sends phantom: an `agent://` send from kinu to a skippy worker
  was accepted on `tier: local` and never routed, because the address resolved
  locally and the worker is not on kinu's bus. Presence is per-tier; a sender
  that ignores tier mis-delivers silently.
- The director seat was `away` and did not read its inbox; a skippy supervisor
  that needed to report fell back to appending durable copies to bd tickets,
  because the bus send "accepted" but the recipient was not reading. That is
  the offline-handling gap (Q3) and the presence-as-liveness question (Q4) in
  one incident.
- Idle marvel-managed seats do not poll their inbox (finding-160), so their
  presence can read `idle`/present while they are in fact not listening. A
  binding that treats "present" as "will act" is wrong today.

## Under-utilized NATS features (candidates to survey, not prescriptions)

We use JetStream streams (`AGENT_INBOX`, `GLOBAL_TO_*`, `GLOBAL_PRESENCE`),
leaf nodes (the global hub), and a presence bucket. Features we may be leaving
on the table, each worth a "would this close one of the gaps above" pass:

- **Request-reply** for real ACK / read receipts instead of fire-and-forget
  (attacks R-08 directly).
- **Durable consumers + per-message TTL** for offline store-and-forward with
  expiry, as an alternative to R-92's refuse.
- **Queue groups** for load-balancing a dispatch across a role's replicas
  (relevant once a role runs more than one seat).
- **KV bucket** for shared roster/state that any seat can read consistently,
  rather than each seat polling `list_roster`.
- **Ack / nak / term and max-deliver** for explicit delivery semantics and a
  dead-letter path.
- **Message headers + dedup** for idempotent dispatch and correlation without
  stuffing everything in the body.
- **Subject-scoped RBAC** (already noted for the multi-principal director
  question) for who may address whom.

## What would move this from idea to frontier

A single testable claim, e.g. "mapping harness pane-state to presence via the
shim on a 5s timer, plus request-reply ACK, eliminates the silent
accepted-but-unread failure for a dispatched task." When one such claim is
worth a probe, extract a frontier question node and a probe brief and point
this idea at it.
