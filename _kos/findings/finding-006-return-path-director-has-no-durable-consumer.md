# finding-006: the cross-host return path fails because the director maintains no durable consumer on GLOBAL_TO_DIRECTOR

> RESOLVED-VERIFIED 2026-09-22. Fix applied and proven: rebuilt director-mcp
> clean from HEAD (b2cfa45, vcs.modified=false), reconnected the director MCP.
> The clean binary created mcp_global_director_01M33ZQ2VKZZHQX2A1WJZWZGQH
> (DeliverAll) on GLOBAL_TO_DIRECTOR, and a cross-host message from
> errand-supervisor-g1-1 (mokuzai) to global://director was delivered to the
> director inbox (tier=global). The dirty-build divergence was the cause.
> Residual follow-ups tracked separately (GlobalWarn silent fallback, stale
> consumer cleanup, director-mcp release/build path).

> RECURRED 2026-09-25 WITH A DIFFERENT CAUSE. The banner above verifies one
> start, not the path. On 2026-09-25 every mokuzai seat's reply to
> global://director was refused ("nothing is registered under presence.
> director."), because the director's registered MCP env carried only the
> agent id, team, workspace and broker URL. DIRECTOR_GLOBAL_DOMAIN,
> DIRECTOR_CLUSTER and DIRECTOR_GLOBAL_ROLE were unset, so there was no
> global presence and no GLOBAL_TO_DIRECTOR consumer. The broker was
> leafed and the binary supported the settings. So the symptom has at least
> two causes: this finding's dirty build (silent accept) and a missing
> configuration (loud refusal at the sender, silence at the director).
> Director register: R-119.


Date: 2026-09-22. Subject: director (cross-host inbound mail, O-23). Status:
root cause identified by direct hub read; fix is a clean rebuild + verify, not
a code hunt. Completes and corrects finding-005-cross-host-return-path (which
guessed consumer-side for the wrong reason and never read the hub).

## The method that settled it

Every prior pass diagnosed THROUGH the channel under test (send, then check
if the inbox drained). This pass read the hub's JetStream state directly via
the monitoring port (`http://127.0.0.1:8242/jsz`, no auth, no channel under
test) while the director was provably in a `wait_for_message` fetch. That is
the read m517d prescribed and nobody had done.

## What the hub shows (channel-independent)

- Stream `GLOBAL_TO_DIRECTOR`: msgs=51, first_seq=29, last_seq=79. The two
  live test AGREEs are on it (ops seq 78 msg 01M33Y5RFQ8R2FG82ZMB7NCD9T,
  errand seq 79 msg 01M33Y6BKN33Q168XE8B1ACKEE). **Send side works: replies
  land and persist.**
- The ONLY consumer on `GLOBAL_TO_DIRECTOR` is `dir_T1`, a hand/test leftover
  stuck at delivered stream_seq=10 (below the stream's first_seq=29) with
  num_pending=51. It consumes nothing current.
- There is **no `mcp_global_director_<instance>` durable** — the per-session
  JetStream durable the committed shim code creates (`global.go` `ensureConsumer`
  -> `globalDurable` = `mcp_global_<agentID>_<instance>`) — even DURING an
  active director fetch. Compare `GLOBAL_TO_mokuzai`, which correctly carries
  `mcp_global_ops-supervisor-g3-0_<instance>`.
- `connz` shows the director connected to the hub (`conn director-phase0`)
  with a **core NATS subscription on `global.director.inbox`**, not a
  JetStream pull.

## Root cause

The running director does not durably consume `GLOBAL_TO_DIRECTOR`. A core
subscription only receives messages published WHILE it is actively subscribed;
it never replays the stream backlog. `wait_for_message` subscribes only for the
duration of each poll. So a supervisor reply is caught only if it is published
during the exact window the director is polling; any reply sent between polls
lands durably on the stream and is stranded there, unread, because no durable
consumer advances. The 51 stranded messages are that backlog.

This explains the whole symptom shape:
- "Worked so well ~Sep 20" (finding O-30): the FIPA handshake was in use with a
  reply_by deadline and the director was actively waiting, so AGREEs were
  published INTO an open poll window and delivered in real time.
- "Return path down" Sep 21+: the handshake lapsed and the director polls
  intermittently, so replies land outside poll windows and strand on the
  stream. It reads as a transport outage; it is a missing durable consumer.

## Why the running binary diverges from the source

The director binary is `probe/nats-phase-0/director-mcp/director-mcp`,
`vcs.revision=0eade31`, **`vcs.modified=true`** (built Sep 19 from a DIRTY
working tree). The committed code at 0eade31 and at HEAD (29a87be) both use the
durable path; the working tree is clean now, so the uncommitted changes that
were in the Sep-19 build are unrecoverable. The running director is therefore
an artifact that cannot be reproduced from any commit and whose hub behavior
(core sub, no durable) does not match the source of record. This is the
unreleased-probe install hazard (finding O-31): unpinned, dirty, multi-path,
multi-machine binaries with no release.

## The four m517d candidates, resolved

- A (send refused when the director presence row is absent): REAL but separate.
  errand-supervisor-g1-0 reported 6 consecutive R-92 refusals across 140 min
  while no director row was present; with the row live its send was accepted.
  Presence and acceptance move together. Mitigated by the director holding
  presence; not the cause of the stranded backlog.
- B (aae-orc-7xrdo, presence resolver silently skips the director row ->
  false refusal): NOT the current cause. The mokuzai sends were ACCEPTED, not
  refused. 7xrdo remains a real latent bug; it is not O-23 here.
- C (build skew): the kinu wire format is version-stable (the only diff
  eb40aa3..0eade31 on the wire code is the added `deletePresence`); m517d's
  "mokuzai on ad76746" premise is contradicted (ad76746 is Sep 12, predates
  global mode; the mokuzai seats hold global presence now and errand reports
  eb40aa3). Skew is not the wire cause. The DIRECTOR's dirty unreproducible
  build IS the culprit, as an unreproducible binary rather than wire skew.
- D (delivered-not-consumed): ROOT, refined. Not "director idle / not polling"
  but "director maintains no durable consumer, so even while polling it cannot
  read the backlog, and a core sub loses anything published between polls."

## Fix direction (tracked on aae-orc-m517d)

1. Rebuild `director-mcp` from a known clean commit (no dirty tree); confirm
   the receive path creates `mcp_global_director_<instance>` with
   DeliverAllPolicy on `GLOBAL_TO_DIRECTOR` and pulls it. Do NOT ship a bus
   code change on a guess; the committed code already does the right thing, so
   the first move is to run the code of record, not patch it.
2. Delete the stuck `dir_T1` leftover consumer.
3. `wait_for_message` must surface the global-tier GlobalWarn instead of
   returning a bare "no message" — the silent local-only fallback is what hid
   this for a day.
4. Keep the handshake + reply_by discipline standing so a dropped return leg is
   visible at once (O-30), independent of the transport fix.
5. Give `director-mcp` a `--version` that does not connect (errand hit an R-49
   collision trying to version-probe the seat binary), and treat presence rows
   as hints not liveness (a row outlived its process during this probe).

## Cross-refs

- finding-005-cross-host-return-path (superseded; the consumer-side guess was
  right in substance, wrong in mechanism, and unproven)
- O-23 (symptom), O-28 (idle seats do not consume), O-29 (request delivered not
  consumed), O-30 (handshake worked Sep 20 and lapsed), O-31 (version
  provenance; dirty unreproducible build)
- bd: aae-orc-m517d (this diagnosis), aae-orc-7xrdo (candidate B, latent),
  aae-orc-nzh7c (director-silence split), aae-orc-l15b5 (the agent:// R-92 fix,
  a different path)
- Hub read: `http://127.0.0.1:8242/jsz` (monitoring, no auth)
