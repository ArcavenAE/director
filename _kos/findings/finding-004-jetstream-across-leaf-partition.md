# finding-004: JetStream across a leaf partition reconciles by stored sequence; the one loss is source-side retention eviction during a long isolation

- **Date:** 2026-09-17
- **Probe:** the leaf-attach direction (`sim/design/global-nats-leaf-attach.md`, bd `aae-orc-ct0l4`); the instrument is `probe/nats-global-tier/verify-jetstream-partition.sh`
- **Subject:** the global bus tier's JetStream behavior across the leaf edge link (R-86, `sim/design/global-bus-tier.md`)
- **Confidence:** bedrock for the mechanics measured on nats-server 2.14.6 (what reconciles, what stalls, what is lost); the numbers are duration-independent by mechanism, so the "long isolation" claim rests on the eviction model, not on a clock
- **Live servers:** untouched. Every broker ran on random free loopback ports in a temp dir with its own store, and was killed on exit. The live hub (pid 78897, 3 days up, conf mtime Sep 14) and the phase-0 broker (pid 68720) were never contacted.

## 1. The question

`global-bus-tier.md` section 1 proved that a leaf keeps serving local traffic through a hub outage, relinks, and that hub streams keep their messages after a hub restart. It left the JetStream-across-partition nuance open, and `global-nats-leaf-attach.md` listed it as an open question: do durable streams mirror or source from the hub and reconcile cleanly on reconnect after a long isolation, and what is the back-pressure while the hub is unreachable. This probe answers it on the edge topology: a leaf that mirrors a hub stream down across the domain boundary.

## 2. What was proven (8 of 8)

A throwaway hub (JetStream domain `global`, leaf listener) holds `GLOBAL_INBOX`. A throwaway leaf (domain `local`, leaf remote to the hub) holds `MIRROR_GLOBAL`, a mirror of `GLOBAL_INBOX` addressed across the boundary by the external API prefix `$JS.global.API`, plus a purely local stream.

```
PASS leaf link up to the hub
PASS mirror tracked 5 hub messages while connected

## P1: hub unreachable, leaf stays up
PASS local publish still stored while the hub is down (local traffic keeps flowing)
PASS cross-domain hub operation fails loud during the outage (no silent queueing)
PASS leaf and its mirror stream stay healthy while the hub is gone (mirror stalls, no fault)
     mirror count held at 5 during the outage

## P2: hub accrues while the leaf is isolated, then reconcile
     hub now holds 20 messages, published while the leaf was isolated
PASS mirror reconciled to the hub count (20) after isolation in ~0s
PASS reconciled set is sequence-exact (first=1 last=20 count=20, no gaps or dupes)

## P3: isolation past the hub stream's retention window
     hub tight stream kept 10 messages (first_seq=16); the earlier ones were evicted during the isolation
PASS mirror reconciled to the surviving window (first_seq=16, 10 messages)
     the 15 messages evicted before reconnect are permanently absent from the mirror
```

## 3. What it means

- **Reconciliation is by stored sequence, so it is clean and duration-independent, up to a limit.** A mirror copies messages preserving the source stream's sequence numbers. On reconnect the leaf's mirror resumes from its last stored sequence and pulls forward. P2 landed first_seq 1, last_seq 20, count 20: the exact set the hub held, in order, no gaps and no duplicates. The clock does not enter into it; the mirror catches up from where it stopped whether that was seconds or hours ago.

- **The one loss is source-side retention eviction, and that is what "long isolation" actually risks.** The mirror cannot recover a message the hub already discarded. In P3 the hub stream kept only its last ten; during the isolation the hub evicted the earlier fifteen, and the mirror reconciled to the surviving window (first_seq 16, ten messages) with the evicted fifteen permanently absent. The exposure is therefore not the outage's duration in the abstract but whether it outlasts the hub stream's retention (max messages, max bytes, or max age) for the volume published in the meantime. This is the real design constraint behind the open question: size the hub stream's retention for the longest isolation a cluster is expected to survive without loss, or accept that a leaf returning after a very long absence starts from the oldest surviving message.

- **Back-pressure during the outage is fail-fast, not queue-and-block.** Local publishes kept working and stayed local (P1). A cross-domain operation to the hub failed loud rather than hanging or silently queueing. The mirror stalled at its last sequence without faulting the leaf or its local streams; nothing accumulated on the leaf waiting to drain, because the mirror pulls from the hub rather than the leaf pushing up. Detached operation degrades to local-only cleanly, which is the property R-86 exists for.

## 4. Scope and what is not characterized

- Measured on the edge topology (leaf mirrors the hub down), which is the current work. A leaf that sources many upstream messages faster than the link drains is a different flow and is not measured here; the edge case does not exhibit it.
- The retention-eviction loss is specific to durable JetStream streams mirrored across the leaf. Ephemeral request and reply over core NATS (the A2A envelope's synchronous path) is not mirrored and not subject to it; during a partition such a call fails loud with no responders, the P1 behavior, and loses nothing silently. The loss here is about persisted stream state, not about all bus traffic.
- Retention sizing against real isolation windows is a per-deployment decision, not a mechanism; this finding gives the model, not a number.
- No auth in the probe: the credential-to-subject binding is already proven in `finding-001` and `global-bus-tier.md` section 4 and is orthogonal to the JetStream reconciliation measured here.

## 5. Cross-references

- `sim/design/global-nats-leaf-attach.md` (the direction this closes an open question for)
- `sim/design/global-bus-tier.md` section 1 (the local-survives-outage property this extends)
- `probe/nats-global-tier/verify-jetstream-partition.sh` (the instrument)
- bd `aae-orc-ct0l4` (the marvel leaf connect and disconnect work)
