# finding-023: a cluster's global supervisor address delivers every supervisor's traffic to every supervisor, and two seats measured what that costs to read

- **Date:** 2026-10-04
- **Session:** a builder seat placing the ops team's 2026-10-04 harvest report (item 5), beside a second measurement from my own team's supervisor seat. Neither measurement is mine; I recorded both as reported and checked the mechanism against finding-006.
- **Subject:** director global-tier addressing and delivery, so this belongs in director's graph.
- **Confidence:** measured, twice, on one cluster during one pause. Open question, not a decision.

## 0. The sentence

**A message to `global://<cluster>/supervisor` is delivered to every supervisor seat on that cluster, and the `FOR` line that says who it is for is a convention the receiver reads after delivery. So each supervisor reads, and pays for, every other supervisor's global traffic.**

## 1. The mechanism, already recorded

finding-006 established the shape: the global tier has no per-agent address, so every inbound message to a cluster is a fan-out to every seat that holds the cluster's supervisor inbox. This finding adds what that costs a receiver, measured by two seats on the same cluster across the same pause.

## 2. The two measurements

**The ops team's report.** Draining global sequences 395 to 680 meant reading about 285 messages, at about 90k characters per batch of 50. Six of them were for the ops team. Most of the rest were another team's review-gate traffic.

**My own team's supervisor seat.** During the same pause its global inbox held 300 messages, from 2026-10-02T04:37Z to 2026-10-03T19:11Z.

- 273 of the 300 (91%) were addressed to one other team's supervisor or its reviewer.
- About 20 were relevant to my team.
- One `wait_for_message` call at `max` 50 returned about 90k characters, too large for a single tool result.
- The six batches came to 552 KB.

Both seats report the same shape: well over nine messages in ten were for someone else, and a full drain reads hundreds of kilobytes to find a few dozen relevant lines. The cost falls hardest on a seat returning from a pause, which is exactly when it most needs a quick read of what it missed.

## 3. The open question

Two shapes have been named. Neither is decided here.

- **Team-addressed global subjects.** `global://<cluster>/<team>/supervisor`, so a message reaches one team's supervisor and no other. This is an addressing change at the broker, the global twin of the per-agent address finding-006 section 4 asks for.
- **Filter by `FOR` before delivery.** Keep the address, and have the shim drop or fold an envelope whose `FOR` names a different team before it reaches the model. That is cheaper, and it is a filter, not a boundary: the seat still holds the inbox, and the message is still on its stream. finding-006 section 4 already names the weaker form of this, marking an envelope addressed to someone else.

What any answer must keep: a broadcast meant for every supervisor still has to reach every supervisor.

## 4. Limits

- Both measurements are from one cluster during one pause. I have no measurement from a cluster with one supervisor seat, where the fan-out costs nothing.
- The relevance counts ("6 for ops", "about 20 relevant") are each seat's own judgement, not a field in the envelopes.
- The 90k-character batch size comes from both reports and was not re-measured by me.
