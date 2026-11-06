# finding-director-0adz: the supervisor-first trial never ran its core loop; supervisors lacked reach, and director stayed the relay

- **Date:** 2026-11-06, written after the fact
- **Subject:** director, the operating model in `_kos/probes/brief-supervisor-first-trial.yaml`
- **Probe:** brief-supervisor-first-trial (timebox through 2026-09-28)
- **Confidence:** reconstructed from records, not measured live. The sources are director's observation log and relay log (`sim/notes/observations.md`, `sim/notes/relay-log.md`), which are operator-local and not tracked here, a team supervisor's harvest note of 2026-09-27, and director#111 on main. Each entry below is restated as mechanism, as `_kos/ideas/director-as-work-state-cache.md` does for the same logs.

## Answer

**The trial started on 2026-09-26 and its core loop never ran.** Step 1 had supervisors on one cluster route their GATEs to the reviewer supervisor on another, with director out of the path. On the first day, director's log records that those supervisors held no global-tier presence, so the reviewer side could not address them (R-92 refused it: the hub's presence listing for that cluster held only director). Director stayed the relay "by necessity", and every cross-cluster review went through a director turn for the whole timebox and past it.

So the hypothesis was not tested. What the trial did show is the gap it was meant to find, in its own classes: **reach** first, then **protocol**. The brief's predicted_confidence was 0.6; the result is `redirects`, because the gap it measured sits under the question the brief asked.

## The four measures

The brief asked for each measure to be read from a record. None was recorded as specified.

1. **Interventions per hour, before and during.** Not computable. Step 4 asked director to tag every intervention `TRIAL-INTERVENTION` in one log. The tag appears on ten lines across the two logs, from 2026-09-26 to 2026-10-01, and many forwards in the relay log in the same span carry no tag. A rate read from that would count the tagging, not the interventions.
2. **GATE-to-verdict latency.** Not measured as a series. Single delays are recorded, all on the director hop:
   - overnight 2026-09-27, 14 GATEs queued for the reviewers and sat 6 to 16 hours while the reviewer seats were idle, because no director turn ran;
   - 2026-09-28 to 2026-09-30, five forward requests sat 27 hours (O-30a);
   - 2026-09-30, two forwards sat about 2 hours (O-30e);
   - 2026-10-01, three of three trial-intervention GATEs were forwarded 35 to 40 minutes late (O-31j, O-31m, O-31o).
3. **Items lost or duplicated.** Two losses are recorded.
   - A reviewer verdict addressed to a seat with no global address was dropped, not queued, on 2026-09-26. The seat found it 19 hours later through its own GitHub sweep (O-2026-09-27b).
   - On 2026-09-30, one trial intervention fell between two bus reads and was never forwarded, because director's read marker was a high-water mark rather than a per-message record (O-30k).
   No entry I read records a duplicate.
4. **Supervisor escalations, and whether each was warranted.** Not tallied. The supervisors' hourly beats did report the stall ("awaiting" the reviewers, "no director reply") for as long as 26 hours (O-30a, O-31d). Nothing acted on those beats.

Step 2, supervisor merges on ArcavenAE repositories, did not take hold in the record. Through 2026-09-26 and 2026-09-27 the relay log records director merging ArcavenAE PRs in batches on almost every sweep, and I found no supervisor merge under the trial in that log. Other records may hold one; I did not search them all.

## The gaps, by class

- **Reach.** Supervisors on the operator's cluster had no global-tier presence. After the peer ruling, director's log records that no supervisor there held a global address until it restarted. One supervisor's handoff of 2026-10-01 gives the cause for that seat (it started before its launcher set the global levers) and names it as the reason every GATE it sent went through director. Worker seats on that cluster had no global domain at all, so the envoy's review list also needed a hand forward.
- **Protocol.** The peer path was unratified when the trial began. The operator then ruled that director "should NOT be a bottleneck, supervisors can coordinate, talk with each other, cross-cluster and otherwise", and director#111 (merged 2026-09-27) put supervisors publishing to each other on the global tier into the design. After that ruling, director still hand-forwarded items and rewrote each local reply-to address into a global one, because a local agent address on one cluster cannot be reached from another (O-2026-09-27a).
- **The relay itself.** Every forward depended on a director turn. Director had no wake when a trial intervention arrived, no durable queue of owed forwards, and no age signal on that queue (O-30a, O-30e, O-31d, O-2026-09-27e). Each delay above is this gap, not a supervisor's judgement.
- **Rights.** Not reached. The loop that would have exercised merge rights never ran.

## What this changes

- The brief's question stays open for this graph. It can be retried once supervisors hold global reach from the launcher at start. Record the four measures as the trial runs, not afterwards.
- The reach and relay gaps already have homes: question-cross-cluster-supervisor-reach, question-global-supervisor-delivery-scope and question-idle-seat-receive-latency. This finding is evidence for them and does not open a new question.
- The brief is marked complete with this finding as its harvest.
