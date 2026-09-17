# Design brief 6: the shim-timer heartbeat, three liveness axes, observed not declared

Status: candidate design, not a spec. Implements R-56 (liveness renewal is
driven by the always-running shim on its own timer, never by the model calling
a tool) and plans the build. Written 2026-09-14 from the Phase 0 probe
(sub-probe 6, aae-orc-lvzck), the supervisor's token-free heartbeat research
(director PR #16, folded into `skills/director/reference/adapters.md`), and
the seat-lease brief (SEAT-D, SEAT-E). Candidate requirements carry provisional
BEAT tags for harvest into `../requirements.md` section I.

## 0. Where the probe left it, and what is still wrong

Sub-probe 6 built the R-56 floor: `director-mcp` runs a goroutine that
rewrites this session's presence key every 30s against a 90s bucket TTL, off
the model's poll. Verified: the presence timestamp advanced with no tool call.
That closed the empty-roster gap that motivated it.

Five things the floor does not do, each observed in the probe code
(`probe/nats-phase-0/director-mcp/bus.go`, `main.go`, `tools.go`):

1. **The beat carries the model's word, not an observation.** The ticker
   rewrites whatever `set_presence` last declared (`idle` at connect, then
   whatever the model said). A session that finished a turn without calling
   the tool beats `busy` forever; one that never called it beats `idle`
   through a ten-minute turn. The research report names the model calling
   `set_presence` as the anti-pattern (a token per beat, and the window is
   missed anyway during a long turn). The probe removed the timing defect and
   kept the source defect.
2. **Renewal failure is silent.** `_ = b.writePresence(ctx, s)` discards the
   error. A broker outage produces a roster hole with no log line and no
   counter, which is finding-157's failure class (a component that reports
   nothing rather than what it checked).
3. **No seat renewal.** R-56 says presence AND seat. The seat lease
   (SEAT-A..G) is designed and unbuilt; nothing renews it, nothing fails closed
   (R-57).
4. **No shutdown edge.** A clean exit leaves the key to age out over 90s. The
   roster shows a session for a minute and a half after it closed, and a
   successor waits the full TTL to acquire a seat the holder released
   deliberately.
5. **No beat without a shim.** The shim is an MCP stdio child of the harness,
   so it exists only where a harness loaded it. A bare `codex exec`, a default
   `crush` TUI, or a Claude Code session launched without the director server
   has no presence at all, and the roster cannot tell "absent" from "never
   joined".

## 1. The mechanism

### 1.1 One timer, one record, three axes

The shim keeps one ticker. On every tick it assembles one presence record and
writes it under the per-session key (`presence.<team>.<id>.<instance>`, R-49).
The record carries three axes, each answering a different question (R-45,
SEAT-G):

| axis | question | source on this tick |
|---|---|---|
| process | is the session up | the write itself; the shim is alive because the beat landed |
| activity | is it working or idle | an observation of the harness (1.2), else the declared state, labeled |
| grant | does it hold the seat | the seat epoch from the CAS renewal on the same tick (1.4), else null |

The record fields: `agent_id`, `team`, `workspace`, `instance`, `pid`
(the shim), `harness_pid`, `harness` (name and version when known),
`state`, `state_source` (`observed` or `declared`), `observed_at`, `declared_at`,
`seat` (role and epoch, or null), `beat_seq`, `beat_failures`, `ts`.

The consumer rule follows from the split: a roster reader may treat
`declared` as a hint and only `observed` as a fact. Nothing in director ever
reads `declared` as if the shim had checked.

### 1.2 Observation is a per-harness adapter, and the parent pid is the key

The shim is a child process of the harness it serves, so `os.Getppid()` names
the harness process with no configuration. That is the whole adapter key for
Claude Code: read `~/.claude/sessions/<ppid>.json` (status busy/idle/null,
name, cwd, version, `statusUpdatedAt`), confirm with `kill -0 <ppid>`, and
note the socket at `/tmp/cc-socks/<ppid>.sock`. Token-free, no session
cooperation, works for headless `claude -p` too (the research report, section
1). Verified 2026-09-14 on this host: for four running shims, the parent pid was the `claude` process and `~/.claude/sessions/<ppid>.json` existed with `name` and `status` (supervisor idle, marvel-builder busy, and two more). The `updatedAt` stamps move on transitions, not on a timer, so a ten-minute
busy turn does not re-stamp; the observer reports `busy` with the age of the
stamp, and the pid check carries the liveness.

For the other three harnesses the observation is a health poll on the
harness's own server, which exists only under a specific launch model:
opencode `GET /global/health` (server-first, every session serves); codex the
app-server daemon pid and socket or MCP `ping` (only under daemon, mcp-server,
or `--listen ws`); crush `GET /v1/health` (only under `crush server`). The
adapter is gated on harness and version, and when the surface is absent it
returns `unavailable`, never a guess, and the record falls back to `declared`.
The launch-model preconditions are the "launch model needed" column already in
adapters.md; the shim does not paper over them.

`set_presence` stays as the declared channel. It costs the model a tool call,
so the skill text stops asking for it on a cadence; it is for the one thing an
observer cannot see, an `away` the model means on purpose.

### 1.3 Cadence and jitter

Presence: TTL 90s, renew every 30s (TTL/3, tolerates one missed beat), plus
random jitter of up to 3s so seventeen shims reconnecting after a broker
restart do not write in lockstep. The observer runs on the same tick and is
bounded by a 2s deadline; an observer that overruns reports `unavailable` for
that tick rather than delaying the write. The write must never wait on the
observation.

### 1.4 Seat renewal rides the same tick

When this session holds the seat, the tick also performs the CAS Update from
SEAT-D (expected revision, `renewed_at` bumped, fencing token preserved). Seat
TTL and interval are separate knobs from presence; a proposed default is 45s
TTL, renewed every tick (15s effective, TTL/3) so a lost holder is replaced
within a minute. On a CAS conflict, a missing key, or an unreachable broker,
the shim drops seat authority before the tick returns (R-57): it clears the
seat block from the next presence record, refuses to stamp an `authority`
block on outbound envelopes (SEAT-F), and logs the loss with the revision it
expected and the revision it found. It does not retry acquisition on its own;
re-acquire is a deliberate act.

### 1.5 The shutdown edge

On any orderly exit (stdin EOF from the harness, SIGTERM, SIGINT) the shim
deletes its presence key and, if it holds the seat, releases it by a CAS
delete. The roster clears within the shutdown, not at TTL. A crash or SIGKILL
skips this and falls to TTL expiry, which is the designed fallback (SEAT-E).
The deletion is best-effort with a 2s deadline; a failed delete is logged and
the TTL still covers it.

### 1.6 Failure accounting

Every write and every CAS returns an error the shim counts. `beat_failures`
is the consecutive-failure count and rides the next successful record, so a
roster reader can see that a session was dark for four beats and came back.
The first failure and every tenth after it log at WARN with the broker error;
success after failure logs at INFO with the gap length. Presence keeps trying
forever (it is diagnostic). Seat fails closed on the first failure (1.4).

### 1.7 Sessions without a shim

A small watcher (`director-watch`, one process per host, on its own timer)
scans the harness surfaces the adapters already read (Claude Code
`~/.claude/sessions/*.json` plus `kill -0`; the opencode, codex, and crush
server planes where launched) and writes presence for any session that has no
shim of its own. Those records carry `reachable: false` and an address of the
form `observed://<harness>/<pid>`, because presence never implies an inbox
(finding-159: one of four harnesses is addressable). The watcher makes the
roster complete; it does not make anything reachable. It is the second half of
the research report's durable pattern and the lowest-priority piece here.

## 2. What this proves, and what it does not

- A landed beat proves the shim process is alive and the broker is reachable.
  It does not prove the harness is responsive; the observation axis carries
  that, and only where the harness offers a surface.
- `observed: busy` with a fresh stamp proves work happened recently. It does
  not distinguish a long legitimate turn from a wedged one. R-17 (finished vs
  died) stays open; no harness supplies it, and this brief does not claim to.
- Seat epoch in the record proves recency of the grant (R-55), not identity
  (R-53).
- None of this is a health gate. Marvel owns restart decisions through its own
  `heartbeat` and `process-alive` healthchecks and its opt-in activity
  advisory; director's beat is a roster, read by people and by the director
  seat. The two beats overlap on Claude Code (marvel's statusline context feed
  is also a cooperative heartbeat) and should converge later, not now (open
  question 6.3, aae-orc-gtpz).

## 3. Adversarial and failure pass

- **Long model turn.** The ticker runs in the shim, so a twenty-minute turn
  beats twenty-plus times with `observed: busy`. The case R-56 exists for.
- **Harness wedged, shim alive.** Beat continues with `process: up`; the
  Claude observer reports the stale `busy` stamp with its age growing. A reader
  sees "up, busy for 40 minutes, stamp not moving", which is the honest
  reading. Test: SIGSTOP the harness and watch the record.
- **Harness died, shim alive.** Stdin EOF ends the shim (main.go serves on
  stdin) and the shutdown edge fires. If the harness dies without closing the
  pipe, the observer's `kill -0` fails and the record reads `harness: gone`
  until the shim's own exit; a shim must exit within one tick of a dead parent.
- **Broker unreachable.** Presence write fails, counted and logged; the key
  expires at TTL and the session vanishes from the roster until the broker
  returns. Seat drops at once. A brief vacancy beats two seats.
- **Two shims, one id.** Already handled by the per-instance key (R-49, R-50);
  the beat does not change it.
- **Observer lies.** A stale `sessions/<pid>.json` outliving a crash reads as
  busy. The pid check is mandatory in the same observation; a record is never
  `observed` on the json alone.
- **Clock skew.** TTLs run on the broker clock; the record's `ts` is
  informational. Readers compute staleness from the KV revision age where
  available, not from the client stamp.

- **Stale presence after a quit, observed 2026-09-17.** The operator quit an
  architect tab (its harness pid 86260 and shim pid 43993 both dead at the OS
  level). `list_roster` kept reporting that session `present` across three
  polls, its `ts` frozen at `2026-09-17T21:42:21Z` while every live seat
  advanced about 40s. This is section 0 item 4 in the wild: no shutdown edge, so
  the key ages out over the 90s TTL, and separately the reader applies no
  cutoff, so a frozen record reads `present` until then. The only
  roster-visible liveness signal was a recent-and-advancing `ts`, not list
  membership. Recorded as finding-166 instance (c) in the orc graph; the
  writer-side deregister is tracked by aae-orc-kz4t5 and aae-orc-lebdu.

## 4. Candidate requirements (provisional tags, source-classed)

- **BEAT-A.** A presence record separates declared state from observed state
  and names the source of each; a consumer treats only observed state as
  checked. Source: JUDGMENT, from the probe's floor plus finding-157.
- **BEAT-B.** One always-running shim timer serves every liveness axis
  (presence and seat) with per-axis TTLs; the write never waits on the
  observation. Source: JUDGMENT, sharpening R-56.
- **BEAT-C.** On orderly exit a shim deletes its presence and releases a held
  seat; a crash falls to TTL. Source: JUDGMENT.
- **BEAT-D.** Renewal failures are counted, logged, and carried in the next
  record; presence retries without limit, a seat fails closed on the first
  failure. Source: JUDGMENT, grounding R-57 in the beat.
- **BEAT-E.** Harness observation is an adapter gated on harness and version;
  an absent surface reports unavailable and the record degrades to declared,
  never to a fabricated observation. Source: JUDGMENT, the finding-157
  discipline applied to liveness.
- **BEAT-F.** A session with no shim is roster-visible only through an external
  watcher and is marked unreachable; presence never implies an inbox. Source:
  JUDGMENT over finding-159 (OBSERVED).

- **BEAT-G.** The roster reader applies a liveness cutoff at read time:
  `list_roster` marks or drops any entry whose presence record is older than a
  staleness bound (from the KV revision age where available, else the record
  `ts`), so a stale key never reads as plainly `present`. This is distinct from
  the writer-side deregister (BEAT-C): deregister removes the key on an orderly
  exit, the read-time cutoff covers the ungraceful case within the observation
  window instead of waiting the full TTL. Source: JUDGMENT, earned by the
  2026-09-17 stale-presence instance (finding-166 c).

## 5. Build plan

Flat bd tickets, labels `aae-orc`, `director`, `source:session`, dependency
edges only (no umbrella):

1. (aae-orc-c911j) Observed state with source label, Claude observer via ppid (1.1, 1.2), and
   the `set_presence` skill-text change. First step is a one-command probe that
   `~/.claude/sessions/<ppid>.json` exists for a running shim.
2. (aae-orc-kz4t5) Shutdown edge and failure accounting for presence (1.5, 1.6).
3. (aae-orc-c0n9d) Seat lease in the shim: acquire, CAS renew on the tick, fail closed,
   release on exit (1.4; SEAT-A..E). Independent of 1 and 2 in code, shares
   the ticker.
4. (aae-orc-21c26) opencode, codex, and crush observers behind the adapter gate (1.2), with
   the launch-model preconditions in adapters.md. After 1.
5. (aae-orc-buprm) `director-watch` for shimless sessions (1.7). After 1.
6. (aae-orc-rcu0g) Live-broker test set: SIGSTOP harness, SIGKILL harness, broker restart,
   CAS conflict. After 1, 2, 3.
7. (aae-orc-f7y5q) Harvest BEAT-A..F into requirements.md section I with source classes and
   update adapters.md. After 1 and 3 have run against a live bus.

## 6. Open questions

1. Seat TTL and interval values (1.4 proposes 45s/15s); the seat-lease brief
   left this open and a live run should set it.
2. Should a shim refuse to start when the observer cannot identify its parent
   harness, or run with `declared` only? Proposed: run, log once.
3. Convergence with marvel: marvel's statusline context feed and activity
   advisory read the same Claude Code surfaces. One beat consumed by both, or
   two beats sharing an observer library? Ties to aae-orc-gtpz (shim-in-pane).
4. Whether `observed://` addresses belong in the roster at all, or in a
   separate "seen" list, so no reader ever sends to one.
5. The `away` state: is a deliberate declared `away` still worth a tool call,
   or does an observed idle with an age cover it?
