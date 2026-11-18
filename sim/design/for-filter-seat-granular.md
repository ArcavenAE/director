# director-mcp: FOR filter is team-granular, and a probe error replays the stream

Rulings recorded (director, 2026-11-18): Q1 yes, kinu first, after D1 to D3 merge; Q2 yes, with the gap logged and shown on `inbox_summary`; Q3 yes, one read-only hub consumer list by a mokuzai seat before D4.

## Why

Supervisor mail on the global tier reaches seats it was not meant for. Every seat that consumes a cluster's supervisor inbox gets every message, and the FOR filter that should drop the misaddressed ones cannot tell two seats of the same team apart. A new seat whose start probe errors also replays the whole stream: one mokuzai seat began 1675 messages back. The fix is four small changes inside director-mcp plus one ruling on the start position.

## Premises, checked on director main 937d1d6 (one command each)

| # | input | check | result |
|---|---|---|---|
| 1 | forfilter.go:240 returns on `team == f.team` without a cluster compare | `git show origin/main:cmd/director-mcp/forfilter.go \| grep -n 'team == f.team'` | line 240, inside `otherTeamSeat`: a fresh row on this seat's team returns false (deliver) whatever its cluster. Line 218 `namesSelf` also matches the bare team name. Confirmed. |
| 2 | probe error keeps DeliverAll | `sed -n 580,625p global.go`; `consumerConfig` at 749 | the head start runs only when `floor == 0 && why == ""` and `earlierDurableProbe` returns no error and not found; on `perr != nil` it sets `floorWhy` and the config keeps `DeliverAllPolicy`. Confirmed. The probe is a `var` seam (global_fresh.go:17) and lists consumers (`ConsumerNames`, `ListConsumers`, lines 45 and 59). The mokuzai-credential hypothesis is not checked: it needs a consumer list on the hub with mokuzai's credential, which is a live-bus act I may not run. |
| 3 | research-supervisors share the fan-out | `inboxSubject()` global.go:167 | every global supervisor consumes `global.<cluster>.supervisor.inbox`; only `supervisor` and `director` are global roles (line 157), so a research-supervisor joins as `supervisor`. Each seat has its own durable on that one subject, so each gets every message. Confirmed. |
| 3b | (found) the filter's own role is the global role | bus.go:1565 `role: gcfg.Role` | a research-supervisor seat answers to `<team>-supervisor` and `role://<team>/supervisor`, the team supervisor's own aliases. Presence rows carry the local role (`research-supervisor`, as in forfilter_test.go line 36), so the row side is right and the self side is wrong. |
| 4 | two FOR lines: only the first is read | `forText`, forfilter.go:94 | returns on the first non-blank line. A second FOR line is never read, so with the filter on, a seat named only on line 2 can be dropped. Confirmed. |
| 5 | corporate false pass (seq 5626) | rules at 218 and 240 | a FOR line naming a sibling seat of the same team resolves to a fresh row with `team == f.team`, so the rule returns deliver. That is the team-granular rule, not a cluster miss: input 5 shares input 1's line, not its cause. To confirm on the red: RT1 below. |
| 6 | fix (b) "drop before the wake" | cue_filter.go:66 `leaveOutDropped` | already built: the cue count leaves out what the filter will drop. It acts only when `DIRECTOR_FOR_FILTER=1` (bus.go:1564). The supervisor's own seat reports the filter "off", so on kinu (b) is not running at all. |

## The smallest fix

1. **Seat-granular drop (inputs 1, 5).** A token is foreign when it resolves, through fresh rows on the cluster it names (this seat's cluster when it names none), to a seat that is not this seat. The team compare goes. Kept: a bare team name, this seat's own names, and any name whose `-gN-M`-stripped form equals this seat's stripped id (so mail for a predecessor reaches its successor during a shift) still deliver; every doubt still delivers.
2. **Local role for the filter's own names (3b).** Build the filter with the session's own role (`DIRECTOR_ROLE`), not the global role, so a research seat answers `<team>-research-supervisor` and not `<team>-supervisor`.
3. **Every leading FOR line (input 4).** Read the names from each consecutive FOR line at the top of the body, so a seat on line 2 delivers.
4. **Fresh seat, probe error (input 2), only on a ruling.** When no floor was found, no lookup failed (`why == ""`), the seat has no predecessor, and only the probe errored, start at the head, log the gap and the skipped count as the clean path does, and report it on `inbox_summary`. This trades a replay for a possible missed message, against the file's own "every doubt goes to delivery", so it is ruling Q2.

Not in this fix: per-seat global addresses (director's (c)), which change the address space; the filter stays opt-in until ruling Q1.

## Plan (flat tickets, red first; not filed)

| id | ticket | depends on | red test it turns green (cmd/director-mcp) |
|---|---|---|---|
| D1 | seat-granular drop | none | RT1 `TestForFilterSameTeamSiblingDrops`: self `alpha-supervisor-g1-0`, fresh row `alpha-research-supervisor-g1-0` (team alpha), `FOR alpha-research-supervisor-g1-0` drops. Fails today. RT2 `TestForFilterOtherClusterSameTeamDrops`: self on c1, row team alpha on c2, `FOR alpha-research-supervisor-g1-0@c2` drops. Fails today. Guards, green now and after: `TestForFilterOwnPredecessorDelivers` (self g2-0, fresh row g1-0, `FOR` g1-0 delivers); `TestForFilterBareTeamDelivers`. |
| D2 | local role in the filter's names | none | RT3 `TestForFilterResearchSeatIgnoresTeamSupervisorAlias`: a filter built by the production constructor for a seat with `DIRECTOR_ROLE=research-supervisor` and global role supervisor drops `FOR alpha-supervisor` when that alias resolves to the team supervisor's fresh row. Fails today (namesSelf matches). Needs the constructor at bus.go:1565 lifted into a function the test can call. |
| D3 | every leading FOR line | none | RT4 `TestForFilterReadsEveryLeadingForLine`: self beta, body `FOR alpha-supervisor\nFOR beta-supervisor\n...` delivers. Fails today (drops). Guard: a FOR line after a non-FOR line is still not read. |
| D4 | fresh seat starts at head on probe error | ruling Q2 | RT5 `TestGlobalDurableProbeErrorFreshSeatStartsAtHead`: `earlierDurableProbe` stubbed to an error, no floor, no predecessor; the consumer config is `DeliverByStartSequence` at head+1 and the log line names the probe error and the skipped count. Fails today (`DeliverAll`). Guard: with a predecessor id, a probe error still delivers all. |
| D5 | turn the filter on for supervisor seats | ruling Q1, D1, D2, D3 | not a director-mcp change: manifest env `DIRECTOR_FOR_FILTER=1`. Check: `inbox_summary` reports for_filter on, and a seeded FOR-sibling message is dropped with a log line. |

Plan check: each ticket names its red test; RT1 to RT5 need only the filter, its constructor and the probe seam, all in D1 to D4 themselves; D5's check needs D1 to D3. No cycle.

## Rulings

- **Q1.** Turn the FOR filter on for every global supervisor seat once D1 to D3 land? Default: yes, kinu first. Without it, fixes 1 to 3 change nothing on kinu.
- **Q2.** May a fresh seat with no predecessor start at the head when only the earlier-durable probe errors? Default: yes, with the gap logged and shown on `inbox_summary`; the alternative keeps today's replay.
- **Q3.** Is the root of the probe error (input 2's hypothesis) worth a read-only consumer list on the hub with mokuzai's credential? It is a live-bus read I may not run; default: yes, by a mokuzai seat, before D4 builds.

Each recommendation is valid until 2026-12-02; the arcaven architect re-checks it then.

## Where it lands

ArcavenAE/director, as `sim/design/for-filter-seat-granular.md`, because the code, its tests and its sibling designs live there. director is PUBLIC, so the landed text names no team of ours that is a client or org token (the corporate seat names in input 5 become `alpha` and `beta`, as the existing tests do); cluster names are host names and allowed.
