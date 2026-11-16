# I-510: how kinu runs, supervises and observes its two NATS servers

Status: design and plan for review, 2026-11-16. Docs only. Nothing here
changes a live server. Every live change on kinu waits for the operator's
approval of a cutover window (section 7, phase 3). Candidate requirements are
provisional. The operator ruled on four points on 2026-11-16 (I-511, section 8).
Who owns the hub is an open question (section 8, Q1), and nothing here decides
it.

Author: the arcaven architect role, from a five-seat party (Anselm,
operability; Ezra, distributed systems; Batten, runtime substrate; Beatrix,
credential custody; Ferro, messaging protocol), four rounds. The party record is
`kinu-nats-5ptdd-2026-11-16` and is kept outside this repository.

- Ask: I-510, the operator: "route to arcaven supervisor to organize 5ptdd to
  improve kinu's deployment and management of nats, local AND global", and
  "make sure the tasks get bd tickets!".
- Issues: director#659 (both servers are supervised by launchd and observed by
  nothing), marvel#1463 (the upgrade post-check reads three states as "no
  hub"). Related: director#50, director#470, marvel#580.
- Tickets: design aae-orc-pi5y3, review aae-orc-urrxm, build slices in
  section 7.
- Checked against director origin/main c2b8bcc and marvel origin/main
  156613d0. A claim with no citation is marked inferred.

## 0. The point

Today a down hub or a dropped leaf on kinu is found when a seat's send fails.
Both servers run as launchd LaunchAgents that relaunch only after a nonzero
exit. A graceful stop exits 0, so it leaves the server down for good, and kinu's
marvel has no bus configured, so nothing on the hub host reports anything.

This design makes kinu's local broker marvel-managed, the way mokuzai's and
corporate's already are (ruled). It adds a hub observer that runs on whatever
host serves the hub, so a hub-down and a leaf drop become events with their own
names. It does not decide who manages the hub, which may move off kinu (open,
Q1). It also lists, plainly, the failures nothing in this plan can report.

## 1. What runs today (observed 2026-11-16)

| | kinu hub | kinu local broker | mokuzai | corporate |
|---|---|---|---|---|
| supervisor | LaunchAgent `com.arcaven.nats-global` | LaunchAgent `com.arcaven.nats-local` | marvel, `mode: managed` | marvel, managed |
| restart rule | `RunAtLoad`, `KeepAlive SuccessfulExit=false` | same | marvel supervisor | marvel supervisor |
| ports | client 4242, leaf 7442, monitor 127.0.0.1:8242 | client 4222, monitor 8222 | 4222 loopback | |
| version | 2.14.6, Homebrew Cellar | 2.14.6, Cellar | 2.14.6 | 2.15.0 |
| server_name | `global-hub` | `director-phase0` | its server id | its server id |
| leaf | (hub) | to `nats-leaf://127.0.0.1:7442`, loopback | to the hub on 7442, up, no TLS | up |

Sources: a kinu seat's read-only checks, relayed by the arcaven supervisor
(launchd labels, ports, versions, the hub conf's `server_name` and `http`
lines, the hub's `/leafz`, kinu's leaf remote URL); `marvel bus status` on mokuzai; marvel `internal/bus/render.go`
(no `server_name` is rendered). `marvel bus status --cluster kinu` reports no
bus configured.

Three facts shape the design:

1. **A graceful stop is permanent.** nats-server exits 0 on TERM and on INT
   (measured on 2.14.6, scratch server). `SuccessfulExit=false` relaunches only
   on a nonzero exit (`man launchd.plist`).
2. **The PATH binary is not the serving binary.** On kinu, `nats-server` on
   PATH is a mise shim, whose answer depends on the caller's working directory
   (v2.14.6 from `/`, v2.15.0 from a checkout that pins 2.15.0, measured on
   mokuzai). marvel resolves the binary once with `exec.LookPath`
   (`internal/bus/supervisor.go:273`) and hands it the leaf seed in its
   environment.
3. **Two hub leafs have no name.** marvel's `RenderConf` writes no
   `server_name`, so each managed broker's name is its server id. The id is new
   on every start. A name change needs a restart, not a reload ("config reload
   not supported for ServerName", `hub-user-lifecycle.md`).

## 2. Limits no option crosses

- The hub stays on kinu for now (I-232, I-332); where it runs later is Q1.
- No NATS or bd credential rotation (I-259). Rotation is later work only.
- No live stop, restart or config change on either kinu server before the
  operator approves a cutover.
- Kinu acts by a kinu seat only. No scp. Keys never move between hosts.
- Detection reports; it does not judge (SOUL section 8, ADR-007). Nothing here
  gates a merge or a spawn on a health number.
- Custody per ADR-009 as amended 2026-10-19.
- The cluster-gone alert's source is ruled: "director's hub-side reader,
  driven by ledger state, never by `/leafz`" (K7, `cluster-gone-alert.md`
  section 1). Nothing here raises a cluster-gone alert from `/leafz`.

## 3. Supervision (provisional KN-1 to KN-4)

**KN-1. marvel manages kinu's local broker, as it does on mokuzai and
corporate.** Ruled by the operator: "marvel on kinu should take over kinu's
local nats server, as it is with corporate and mokuzai". This is the flip
already planned as aae-orc-bxg5f, run from a launchd-aware runbook. Kinu's
local broker then uses the same marvel config shape as the other two hosts,
with no kinu-only keys.

**The hub's supervisor is not decided here** (Q1). The operator: "I'm not sure
who manages the global nats, I think it is likely to move off host in the
future". The party's reasons against putting the hub under the hub host's
marvel stay on record for whoever answers Q1:
- it gives the hub a second conf writer beside `director hub user`;
- a daemon restart becomes a fleet outage;
- a credential put restarts nats, which on the hub bounces every leaf.

Until Q1 is answered, the hub keeps its launchd service on kinu, and this plan
makes no change that assumes it stays there.

**KN-2. The hub's current service file gets `KeepAlive true`** (party 5 to 0;
ruled, R2). It is set in the approved cutover window and not before, since it is
a live config change to the hub. A deliberate hub stop is then a `launchctl
bootout`, never a kill, because launchd now relaunches the server after any
exit. A bad conf relaunches every 10 seconds (the default `ThrottleInterval`),
which is loud by design. This fixes the clean-exit hole on today's host. It
binds no future host.

**KN-3. While the hub is on kinu, logout and reboot-before-login are a hub
outage.** On a host where files under the user's home are not readable
before the first login, a boot-time LaunchDaemon cannot read the hub's conf
and keys there (inferred, not measured on kinu). It would buy only survival of logout, at the cost of a root install. Moving the
conf and keys out of the home, or turning on auto-login, would be a custody
change, not a boot fix. The outage is named in section 6. The operator
accepted it for now, not as a design decision (R3). A move off host (Q1) is
the larger answer.

**KN-4. One writer per port at the cutover.** Two supervisors for 4222 is a
split brain with the port as the lease. The window:
1. moves the old local plist to a kept rollback directory, since `bootout`
   alone is undone at the next login by `RunAtLoad`;
2. boots it out;
3. proves 4222 is free;
4. starts the managed broker.

Rollback reverses those steps with the same check. marvel already refuses to
adopt a stranger on its port (`TestSupervisorRefusesStrangerOnThePort`,
`internal/bus/supervisor_test.go:228`).

## 4. Binary and name (provisional KN-5, KN-6)

**KN-5. Pin an absolute binary path** (`bus.binary`), never a shim and never a
PATH lookup, on every marvel-managed cluster alike. The pin is a custody fact
as well as a version fact: that binary receives the leaf seed. At the cutover,
kinu takes the same path form and version that mokuzai serves today (2.14.6),
so the window changes supervision and not version. A version move is a later
ticket for all three hosts together. `marvel bus status` reports the configured
path, its realpath, whether it still exists, and the serving `/varz` version.
Skew is diagnostic.

Changed to fit the ruling "like they are with the other two hosts". The party
voted 5 to 0 for a kinu-specific `brew pin` of the serving keg. That is now a
fleet choice: if a pin is wanted, it is the same on all three hosts, and it
lands as part of n5a4y's rollout rather than as a kinu step.

**KN-6. Render `server_name: <cluster>`** from the already validated `Domain`.
It applies at each managed broker's next restart:
- on mokuzai and corporate, one bus restart each, approved by that cluster's
  supervisor;
- on kinu, at the cutover, where the local broker's name changes from
  `director-phase0` to `kinu`.

Nothing in code reads the old name. Only the conf itself and docs mention it.
The event subject's cluster segment, the `/leafz` name and the name in a report
are one string, pinned by a golden test.

## 5. Detection and reporting (provisional KN-7 to KN-10)

The wires, so each claim names one:
- the **monitor** is HTTP `/varz` and `/leafz`, and needs no credential;
- the **local bus** is each cluster's own broker;
- the **leaf link** runs to the hub on 7442;
- the **global tier** is the hub's 4242 and domain `global`.

A detector reads the monitor, and a report rides a local bus. Neither may need
the leaf link or the global tier to be up.

**KN-7. Each cluster's marvel detects its own leaf drop** from its loopback
`/leafz`, as it already does (`supervisor.go`, 30 s poll, `bus.leaf.down`
repeated every 30 minutes). `bus.leaf.up` goes out at warning after a reported
down, with `since` (aae-orc-x2wz4). Today it is info (`supervisor.go`, the
`KindBusLeafUp` emit), and the tap passes only warning and above, so a recovery
is never heard.

**KN-8. A hub observer on the hub host.** Any marvel cluster may be told
where a hub's monitor is (a `hub_monitor` address in its bus config). The
cluster on the same host as the hub reads that monitor. Today that is kinu,
and nothing in the code or config names kinu: if the hub moves (Q1), the key
moves with it to whichever cluster shares the new host. The monitor is
loopback-only, so the observer has to be on the hub's host. A hub host with no
marvel cluster has no observer. That is a known gap, not a reason to open the
monitor (section 6). It needs no credential and no new principal
(`observer.majordomo` is not needed here).

- **Status lines** (`marvel bus status` on the observing cluster): present
  versus expected leafs by name, and a partition line when every remote leaf is
  missing while the observing cluster's own is present. Each line carries its
  reading time and is labelled "not an alert". A stale, timed-out or failed
  read says "not observed". These lines never raise an event, because
  cluster-gone is director's (K7).
- **Events**: `bus.hub.down` only when the hub monitor refuses the connection
  and the observing cluster's own loopback leaf reads 0. A timeout or a bad
  reply is "not observed", never down. `bus.hub.up` goes out at warning after a
  reported down. These are separate kinds, so `bus.leaf.down` never carries the
  hub-down meaning.

Changed to fit the ruling that the hub stays portable. The party voted for "a
hub observer on kinu". It is now an observer on whichever cluster shares the
hub's host, configured by address.

**KN-9. `bus.hub.down` has its own dedupe key**, `hub-down:<hub server_name>`,
outside O4's shared key (4 to 0, 1 abstain). With the hub down, director's
hub-side reader has no hub to read and raises nothing (`cluster-gone-alert.md`
sections 4 and 7), so this event is the only page and must not fold into
anything. A proposal for cashp C4, not a ruling: an open `bus.hub.down`
suppresses kinu's own `bus.leaf.down`, one way only.

**KN-10. Reports ride the local bus through the tap.** They reach a supervisor
only after the tap (aae-orc-zhx6x) and the cluster-local subscriber
(aae-orc-mn10f). The tap is gated never to run on an anonymous broker, and it
waits on aae-orc-lgthv, aae-orc-31or7 and aae-orc-umw8p. Kinu's local broker is
anonymous until the cutover turns authorization on. Nothing is lifted to the
hub, since a leaf-down cannot cross the link that is down.

What the hub host's readings mean together (Ezra's table, readings and never
verdicts; "local leaf" is the observing cluster's own loopback leaf):

| local leaf | hub monitor and `/leafz` | reading |
|---|---|---|
| 0 | refuses | hub process down (`bus.hub.down`) |
| 1 | answers; every remote leaf missing, the local one present | the hub host off the LAN, or 7442 unreachable from it (status line) |
| 1 | answers; one leaf missing by name | that leaf dropped; its own marvel reports it too (status line) |
| no reading | no reading | the hub host down, logged out, or rebooting |

**KN-11. The upgrade post-check names what it saw** (marvel#1463, 5 to 0):
- "no bus configured" passes;
- "managed bus, no hub configured" passes;
- "adopted bus" reads the loopback `/leafz` with no credential and never prints
  "no hub". It exits nonzero only when the cluster declares a hub and the leaf
  is down or unread, the contract the post-check already applies to a managed
  bus (`cmd/marvel/reexec_postcheck.go:108-141`).

marvel is never given a credential to observe an adopted bus.

## 6. Known gaps (written down, not fixed here)

- **The hub host down, logged out or rebooting is a hub outage that the hub
  host cannot report.** Its only witnesses are the leaf clusters' marvels, each
  reporting its own `bus.leaf.down` on its own local bus. No path from them to
  the operator avoids the hub host today. Kinu is handled like the other two
  hosts here (ruled): no special path is added for it.
- **A leaf cluster whose own supervisor is not running hears nothing.**
- **A hub host with no marvel cluster has no hub observer**, because the hub's
  monitor is loopback-only. Relevant if the hub moves off host (Q1).
- **Until the tap is live on a cluster,** and on kinu until the cutover turns
  authorization on, every detector here writes only to the ring. Nobody hears it
  unless they run `marvel bus status` or `marvel events`.
- **The ring does not survive a reexec.** The reexec handoff carries
  credentials only, and the daemon builds a fresh ring at start
  (`internal/daemon/daemon.go:424-427`). A ring-only `bus.leaf.down` is lost at
  the next reexec.
- **Under KN-1, a kinu daemon restart drops kinu's leaf** until a human
  re-pushes the seed (credentials are transient by design). Expect a
  `bus.leaf.down` that is about this plan, not about the fabric.
- **A link that is up while the global tier fails** (director finding-024) is
  not detected by any monitor read here. It needs a JetStream round trip, which
  is later work behind its own ruling.

## 7. Plan

Flat tickets, no umbrella. "R" names the red tests a ticket turns green.
"After" names its dependency edges, as filed in bd.

### Phase 1: lands now, needs no tap and no live change

| ticket | repo | change | R | after |
|---|---|---|---|---|
| aae-orc-a7h5h | marvel | render `server_name` (KN-6) | TestRenderConfEmitsServerNameFromDomain | none |
| aae-orc-c6o29 | marvel | post-check states (KN-11, marvel#1463) | TestReexecPostcheckNamesEachNoHubState; TestReexecPostcheckAdoptedWithHubDeclaredErrors; TestPostcheckAdoptedReadsLeafzWithoutCredential | none |
| aae-orc-v10vo | marvel | golden kinds and subjects | TestBusEventKindsArePinned; TestBusEventSubjectClusterMatchesServerName | a7h5h |
| aae-orc-n5a4y | marvel | `bus.binary` (KN-5) | TestSupervisorExecsConfiguredBinaryNotPath | none |
| aae-orc-ktts0 | marvel | status reports path, realpath, existence, version | TestBusStatusReportsConfiguredRealpathAndVersion | n5a4y |
| aae-orc-x2wz4 | marvel | leaf events carry `since`; up at warning (KN-7) | TestLeafUpAfterReportedDownIsWarning; TestLeafDownRepeatCarriesOriginalSince | none |
| aae-orc-stqn9 | marvel | hub status lines (KN-8) | TestHubStatusNamesMissingExpectedLeafAsDiagnostic; TestHubStatusPartitionLineWhenAllRemoteMissing; TestHubStatusStaleReadingIsNotObserved | a7h5h |
| aae-orc-xsyfv | marvel | `bus.hub.*` events on the ring (KN-8, KN-9) | TestHubObserverHubDownNeedsMonitorRefusedAndLocalLeafZero; TestHubObserverTimeoutIsNotObserved; TestHubObserverBadReplyIsNotObserved; TestHubUpAfterReportedDownIsWarning; TestHubDownCarriesOwnDedupeKey | stqn9, v10vo |
| aae-orc-5jpjc | director | the reviewed service file for the hub's current host, plus the kept rollback copy of kinu's old local plist (KN-2) | TestPlistProgramIsAbsoluteAndNotShim; TestPlistKeepAliveIsUnconditional; TestPlistCarriesNoSeed | none |

### Phase 2: runbook, rehearsal, and names on the remote leafs

| ticket | repo | change | R | after |
|---|---|---|---|---|
| aae-orc-kfkjp | marvel | rewrite `kinu-bus-cutover-runbook.md` for launchd (KN-4), plus a scratch-port rehearsal script | CheckPortFreeAfterBootout; CheckOldPlistMovedToRollbackDir; CheckHubLeafzNamesKinu; CheckHubLeafzLacksOldName; CheckRollbackRestoresLaunchdBroker; CheckKinuConfigMatchesFleetShape; CheckKinuLeafConfKeptForRollback; CheckServingVersionMatchesConfiguredBinary | a7h5h, n5a4y, ktts0, 5jpjc |
| aae-orc-02w0t | ops, mokuzai and corporate | apply `server_name` by one bus restart per host, each approved by that cluster's supervisor | the hub `/leafz` names mokuzai and corporate, with no server id | a7h5h, released |
| aae-orc-ku0g8 | ops, a kinu seat | rehearsal on scratch ports and labels; touches no live server | every runbook check passes, transcript on the ticket | kfkjp, c6o29, ktts0, stqn9, xsyfv, x2wz4, released |

### Phase 3: the cutover, only with the operator's approval

| ticket | repo | change | R | after |
|---|---|---|---|---|
| aae-orc-bxg5f | ops, a kinu seat | KN-1 flip from the rewritten runbook: the same bus config shape and binary form as mokuzai and corporate, re-push of the leaf seed before the post-checks, and every post-check reading taken after the window opens. The hub's service file gets `KeepAlive true` in the same approved window (KN-2, ruled R2); from then on a deliberate hub stop is `launchctl bootout`, never a kill | the upgrade post-check reads "hub leaf up" on kinu; the hub `/leafz` names kinu, mokuzai and corporate and not `director-phase0`; the hub status lines show no missing leaf | ku0g8, 02w0t, **the operator's approval** |

The window's earlier preconditions on bxg5f stand: the director seat
credential, every kinu session respawning, and the authorization block going
live (marvel `docs/design/kinu-bus-cutover-runbook.md` section 2, and
`bus-as-service.md` section 6).

### Phase 4: delivery to a supervisor (rides the existing tap chain)

| ticket | repo | change | R | after |
|---|---|---|---|---|
| aae-orc-zhx6x | marvel | the ring tap to the local bus (existing) | its own tests | lgthv, 31or7, umw8p (existing) |
| aae-orc-mn10f | director | the cluster-local subscriber, widened to `bus.hub.*` | TestLeafDownEventBecomesSupervisorInboxItem; TestHubDownEventBecomesSupervisorInboxItem | zhx6x, v10vo, x2wz4 |
| aae-orc-gtzgt | director | write the stage-file ruling into `bus-credential-enrollment.md`: kept on disk, allowed for now; the move to a credential store is aae-orc-3fyyy | the doc's "transient" wording is replaced by the ruled wording | none (ruled 2026-11-16) |

mn10f is not a precondition of the cutover. On kinu it cannot go live before
the cutover turns authorization on.

### Plan check (run by the architect before this PR)

- Every ticket names its red tests, or for the ops and doc tickets the checks
  that stand in for them.
- Each test is reachable from its ticket and that ticket's dependencies:
  - the post-check tests need only marvel's reexec path;
  - the hub status and event tests need the rendered name (a7h5h) and the
    pinned kinds (v10vo);
  - the rehearsal checks need the runbook, the binary key, status and the
    plists, all upstream of ku0g8;
  - the cutover post-checks need the names on the remote leafs (02w0t) and the
    rehearsal;
  - the subscriber's tests need the tap, the pinned kinds and the leaf-up
    change.
- `bd dep cycles` reports none.
- One edge was left out on purpose. The `bus.hub.*` events (xsyfv) are built
  and tested on the ring, so they do not wait on the tap. Their delivery is
  mn10f's, which does.

### Later work (no ticket now)

- A boot-time LaunchDaemon for the hub, if logouts happen in practice.
- A version move off 2.14.6.
- A cross-cluster correlator, which has no wire today.
- TCP probes of 4242 and 7442 (4 for later, Ezra dissenting for "drop").
- A JetStream round-trip health check with `observer.majordomo`.
- An envelope version field, triggered by the first non-additive change or a
  second consumer. The envelope never carries a credential, a seed path or a
  password-file path.
- `credential put` recording its issuer (observed blank on mokuzai), to be
  filed as a marvel issue.
- Moving the leaf seed files to a vault, keychain or other credential store
  (aae-orc-3fyyy, from the operator's ruling).
- Who owns and supervises the hub, and where it runs (Q1).
- Credential rotation (I-259).

## 8. Rulings

### Ruled by the operator, 2026-11-16 (I-511; quotes from director's relay of 19:33Z)

| | ruling, verbatim where quoted | effect here |
|---|---|---|
| O1 | "marvel on kinu should take over kinu's local nats server, as it is with corporate and mokuzai" | KN-1; the cutover bxg5f |
| O2 | on the leaf seed files at rest: "keeping the credential file on disk is allowed for now but file a bd ticket that it should probably move to a vault, keychain, or other credential store in the future" | the files stay; gtzgt writes the ruling into the enrollment doc; the move is later work, aae-orc-3fyyy. The party had split on this (Beatrix for recording the files as the durable form, Batten for treating them as non-conforming) |
| O3 | on open items: "they should be like they are with the other two hosts" | where the design had a choice, kinu matches mokuzai and corporate: the bus config shape and binary form (KN-5); the first team passwords minted at the flip, as they were on the other two hosts' flips; no special off-host alert path for kinu (section 6) |
| O4 | on the hub: "I'm not sure who manages the global nats, I think it is likely to move off host in the future" | Q1 below stays open; the hub observer is configured by address, not by host (KN-8); nothing here assumes the hub stays on kinu |

### Ruled by the operator after review, 2026-11-16 (relayed by director, 20:15Z)

- **R2.** The operator, verbatim: "R2 let lanchd restart the hub even after a
  clean exit". Approved: the hub's current service file gets `KeepAlive true`,
  set in the approved cutover window and not before (KN-2). A deliberate hub
  stop is then a `launchctl bootout`, never a kill.

### Accepted for now by the operator, 2026-11-16 (relayed by director)

- **R3.** On accepting logout and reboot-before-login as a named hub outage
  while the hub is on kinu, the operator: "I think we can accept this for now,
  not as a design decision". This is an interim acceptance, which the design
  does not settle. KN-3 and section 6 name the outage, and Q1 may make it moot.

### Open question

- **Q1. Who owns and supervises the global hub, and where does it run?** Not
  decided here. The design keeps the hub portable: the observer follows the
  hub's host by config, the service file in 5jpjc describes the hub's current
  host only, and no ticket depends on the hub staying on kinu. When Q1 is
  answered, the party's reasons on the hub (KN-1) are the starting evidence.

### Votes changed to fit a ruling

- KN-5: the party voted 5 to 0 for a kinu-only `brew pin` of the serving keg.
  Under O3 the pin is a fleet choice made the same on all three hosts. The
  rehearsal check `CheckServingKegPinned` became `CheckKinuConfigMatchesFleetShape`.
- KN-8: the party voted for a hub observer "on kinu". Under O4 it is an
  observer on whichever cluster shares the hub's host, configured by address.
- KN-1, the hub half: the party voted 4 to 0 to keep the hub under launchd as
  the settled design. Under O4 that is the hub's current state, not a decision.
  Who supervises the hub is Q1.
- The plists in director (party 4 to 0): kept, narrowed to the hub's current
  host's service file and the kept rollback copy of kinu's old local plist.
  After the cutover, kinu's local broker has no plist, like the other two hosts.
- R6 (the first passwords minted at the flip) and R7 (an off-host path for
  kinu's outages) were recommendations. They are now answered by O3 and are no
  longer asked.

The cutover window itself (bxg5f) is a separate approval from these rulings.
