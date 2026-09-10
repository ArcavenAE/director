# Director spec: delivery and acknowledgement

Status: draft, evidence-backed, root cause NOT fully isolated.
Written 2026-09-04 from a live incident during the director simulation.
No em dashes by house rule; the finding is `finding-151` and this
document is the requirement it produces.

## The incident, stated plainly

A coordinator sent five cross-session messages to one peer over three
hours, from two different sender sessions in two different directory
trees. Every send returned `{"success": true}` with a message id. **None
of the five was delivered.** The coordinator reported the peer as
unresponsive to a human three separate times, on the strength of those
success returns.

## What is established

1. **`SendMessage` success does not mean delivered.** It reports on the
   local write, not on acceptance by the peer. This is the whole
   finding; everything else is detail.
2. The unreachable peer's socket exists at
   `/tmp/cc-socks/<pid>.sock` and is held open by its own process, so
   there is a live listener and the write had a destination.
3. Delivery, when it works, is observable: the recipient's transcript
   gains a `queue-operation` entry and then a `user` entry containing
   `<cross-session-message from=...>`, within about one second, and its
   roster `status` flips to `busy`. The unreachable peer shows none of
   these, for any of the five messages.
4. **Idle does not prevent delivery.** The peer that DID receive a probe
   had been idle for 26 days and drained within one second, waking to
   `busy` at the exact send timestamp.

## What is ruled out, and the errors made ruling them out

- **Version skew: RULED OUT.** First hypothesis, published to the
  operator, wrong. The unreachable peer runs 2.1.234 with
  `peerFeatures: None`, and every peer that answered runs 2.1.255+ with
  `reply_across_default_dirs`. That correlation is real and it is not
  causal: a peer on 2.1.226, an OLDER build with no peer features at
  all, received a probe without trouble.
- **Sender directory scope: RULED OUT.** Second hypothesis, formed after
  the first died. A peer in the SAME tree as the unreachable one, with
  the feature present, sent it a probe. Also not delivered.
- **Receiver being idle: RULED OUT**, per established fact 4.

Both wrong hypotheses came from correlations over the live roster, and
both survived a control that felt sufficient and was not. The first
control tested cwd inequality when the variable was tree containment.
The second tested tree containment when that was not the variable
either. The general lesson: **on a small roster, a clean correlation is
cheap and almost always available. Only a direct probe discriminates.**

## RESOLVED 2026-09-04, later the same session

The discriminating experiment ran. The operator gave the unreachable
session a turn of its own. It took two full turns and produced work
(a Jira ticket). **The queued messages did not drain. The count stayed
at zero.**

So they were DROPPED, not pending. Delivered-later is ruled out, and the
success returns were fiction rather than optimism. The session confirmed
it independently from its own side: "Cross-session delivery: nothing
arrived."

Second fact, visible only after the turn: **the process had restarted.**
The old pid is gone. The same conversation now runs under a new pid on
2.1.260 with the full peer feature set, and the roster renamed it. The
old process had been running seventeen days on 2.1.234, and shortly
before restarting it also failed with `API Error: 403 The security token
included in the request is expired`.

The surviving explanation is **process state in one long-running
session**: it held a live socket, its own process held the descriptor,
it reported `idle` in the roster, and it accepted writes while its
message handling was dead. Not addressing, not tree scope, not
idleness, and not version as such, since an older build received a probe
without trouble.

**The load-bearing observation is that every health signal available
said the peer was fine.** Process alive, socket present and held by that
process, roster status `idle`, sends returning success with ids. A
coordinator checking all four would have concluded the peer was healthy
and merely quiet. The only signal that told the truth was the
recipient's own transcript, which is not something a peer can normally
read about another peer.

R7 follows from this and is added below.

## What remained open before that experiment

The unreachable peer has taken no conversational turn since 2026-08-19,
sixteen days, and shows only a `/login` today. Three candidates survive:

- a messaging listener that has died or wedged inside a long-running
  process while the socket file and the holding descriptor remain;
- a defect specific to that build;
- a queue that drains only under a condition this session has not met
  since Aug 19, in which case the messages are pending rather than lost.

**The discriminating experiment** is to give that session one turn of
its own and watch whether the five queued messages appear before it
answers. Pending delivery and permanent loss are indistinguishable from
outside until that happens.

## Requirements for director

R1. **Acknowledgement comes from the peer, never from the send call.**
    A send returns "accepted for delivery" at most. Delivered, read, and
    acted-on are distinct states, each reported by the receiver.

R2. **Undeliverable must be loud.** A peer that cannot or does not accept
    a message produces a delivery failure the sender can see. Silent
    drop with a success return is the worst available behavior: it gives
    the coordinator false state, which the coordinator then reports to a
    human as fact. That happened three times here.

R3. **Capability negotiation before send.** Director must know each
    peer's protocol version and feature set, refuse or degrade a send
    the peer cannot accept, and never infer capability from a roster
    entry that was written when the peer started.

R4. **Reachability is a roster field, distinct from status.** Today an
    unreachable peer and a working idle peer both render as `idle`.
    Director's roster must carry last-successful-delivery and
    last-acknowledgement per peer, so unreachable-but-alive is visible
    without an experiment.

R5. **Queue state must be inspectable by the sender.** Pending, drained,
    expired, and dropped are four different things. A coordinator that
    cannot tell them apart cannot tell a human what is true.

R7. **Liveness must be end-to-end, not inferred from the plumbing.** A
    process that is running, holding its socket, and reporting `idle` can
    still be accepting and discarding messages. Every observable short of
    the recipient's own record agreed the peer was healthy while it was
    silently dropping five messages over three hours. Director's health
    signal for a peer must be something the peer produces after
    processing a message, not something the sender can observe about the
    peer's plumbing.

R8. **A restart changes a peer's identity, capabilities, and address at
    once.** The recovered session kept its conversation id but changed
    pid, socket path, roster name, version, and feature set. Any
    coordinator state keyed on name, pid, or socket is stale from that
    moment, silently. Director must key on the durable conversation
    identity and re-resolve the rest on every send.

R6. **Long-lived sessions are the normal case, not the edge case.** The
    two peers involved had been running 16 and 26 days while the harness
    updated underneath them. Any assumption that a fleet is homogeneous,
    or that a peer's capabilities are fixed at roster-write time, is
    already false on a single laptop with one user and one account.

## Why this outranks the work it interrupted

The simulation's premise was that same-user, same-machine, same-account
is the easy case, and that director's real problems start at cross-host,
cross-account, cross-harness. This incident happened inside the easy
case, with no network, no second account, and no second harness. The
messaging layer reported success while doing nothing, for three hours,
and the failure was invisible to both the sender and the human until
someone measured the recipient's transcript rather than trusting the
sender's return value.

Cross-reference: `question-silent-success-instruments` is exactly this
class, and this is a live instance of it inside the substrate the
simulation borrowed as its local reference implementation.
