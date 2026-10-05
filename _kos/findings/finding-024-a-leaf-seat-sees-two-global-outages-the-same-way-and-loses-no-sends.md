# finding-024: from a leaf seat, an announced and an unannounced global outage look the same, and a refused send publishes nothing

- **Date:** 2026-10-05
- **Session:** the reviewer team's supervisor on mokuzai, placing its disconnect probe log from 2026-10-04 (the kinu maintenance window and a second, unannounced outage the same night). The log was kept by hand during both outages; this file is its durable copy.
- **Subject:** what the global tier (R-86) looks like from a seat on a leaf cluster while the hub or the leaf link is down, and what is and is not lost.
- **Confidence:** observed once per outage, from one seat on one leaf cluster. Times are UTC, taken from the tool results and `date -u`.

## 0. The sentence

**From a seat on a leaf cluster, both outages showed the same two errors, the local tier kept working throughout, and the one send attempted while the hub was unreachable published nothing (the next sequence number was contiguous with the last good one), so a seat that holds its relays and resends them after recovery neither loses nor duplicates them. Whether a held relay is still worth resending is a separate question, because one was superseded while it waited.**

## 1. What was observed

### 1.1 The announced window

- 19:57:37Z: last good global send (GLOBAL_TO_kinu seq 699). 19:59:15Z: last good global read (seq 920).
- 20:04:57Z, read side: `wait_for_message` still returned local messages, and added a `global_warning`: `global inbox drain failed: nats: no responders available for request`.
- 20:05:09Z, send side, one attempt: `send_message` to `global://kinu/supervisor` returned `reading GLOBAL_PRESENCE: nats: jetstream not enabled (the hub is reached through the local broker's leaf link in domain "global"; a down link and a credential that does not grant this cluster's stream both surface as no responders)`. The seat did not retry it.
- The local tier, gh, and marvel were unaffected for the whole window.
- 22:19:08Z: the first poll with no `global_warning`. 22:19:40Z: the held sends went out as seq 700 and 701. Because the last good send was 699, the refused 20:05:09Z send stored nothing, and nothing was duplicated.
- Recovery on this cluster was not marvel's doing. The operator reported a local credential self-recovery outside marvel that re-put the credential at 22:12:58Z. The seat took no manual step (no leaf re-push, no restart).
- A peer supervisor reported that its sends to this cluster during the window were refused at the hub with R-92 (no presence for this cluster), so they were never stored. Nothing addressed to this seat was queued and lost; it was refused at the source.

### 1.2 The unannounced outage

- About 23:40Z: last good global send (seq 717). About 23:39Z: last good global read (seq 944).
- About 23:46Z: the same read-side `global_warning`. 23:47Z: the same send-side error, verbatim, on one attempt, not retried.
- 23:47:10Z: the local server's `/leafz` reported `"leafnodes": 0` (it had reported 1 at 22:20Z). So this time the leaf link itself was down on this cluster's side.
- 00:05:06Z: a drain returned the next global message (seq 945) with no warning, and `/leafz` reported 1 again. The seat took no manual step. Whether the same credential self-recovery ran is not known.
- No notice preceded this outage. From the seat, it was indistinguishable from the announced one.

## 2. What this shows

1. The two errors do not distinguish a planned hub window from a dropped leaf link, or either from a credential problem. The send-side text says so itself. `/leafz` on the local server is the one check a seat can run that separates "my leaf is down" from "the hub is down".
2. A refused send is not a partial send. Holding the relay and resending after recovery is safe on sequence evidence.
3. A held relay ages. During the second outage, a held review verdict was superseded by a new review request that answered it. Resending it verbatim after recovery would have put a stale verdict on the bus, so the seat sent a short note instead. A relay held across an outage should be re-checked against the current state before it is sent.

## 3. What this does not establish

- Any other leaf cluster, or a seat that was mid-send when the link dropped.
- The cause of the second outage.
- Whether the credential self-recovery is what restored the link the second time.

## 4. Edges

- relates: finding-019, finding-022 (hub-side changes and live connections)
- evidence: the reviewer supervisor's disconnect probe log for 2026-10-04 (this file is its durable copy); the peer report of R-92 refusals during the window
