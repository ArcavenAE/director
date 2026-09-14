# Design brief 7: the marvel twin, its target manifest, and the cutover criteria

Status: candidate design, not a spec. Written 2026-09-14 for the operator's
program: stand marvel up beside the hand-run fleet, shift-change style, iterate
until it looks right, and only then cut over. Nothing is shut down first. Two
things are defined before cutover: who does what (the manifest) and what
"looks right" means (the criteria, each a check a person can run).

Premises were verified against marvel's code and the live fleet today, with
marvel-builder consulted on the marvel side (section 1). Requirements cited
are director `sim/requirements.md`; wardrobe items are the ratified v1 library
at `wardrobe/contents/` (all ten roles at `status: proposal`, castable under
rulings 47 and 61 with `--allow-proposal`).

## 0. The shape of the program

The hand-run fleet is nine Claude Code sessions on this host, launched by hand
through `director-session.sh`, each with its own director-mcp shim on the
Phase 0 NATS at 127.0.0.1:4222. The twin is the same nine functions declared
in one marvel manifest, run by a second marvel daemon on the same host, joined
to a bus, and observed from the live director seat. A role cuts over when its
twin session passes the criteria for that role and the hand-run session is
closed; the supervisor cuts over after every worker role, and the director
seat last, by lease acquisition rather than by inheritance (R-54). Nothing in
the program requires a big-bang switch, and the criteria are written so a
role can fail and be retried without touching the others.

Stages:

- **S0, twin up.** The second daemon, the declared broker, the manifest
  applied, all roles present on the roster with distinct identities. Nothing
  cut over yet; the live fleet keeps working.
- **S1, role-by-role cutover.** For each role in section 2 order: the twin
  session takes real traffic beside the hand-run one, the checks in section 3
  pass for that role, the hand-run session closes. Supervisor after the
  workers; director last.
- **S2, cutover complete.** No hand-run session remains; the twin is the
  fleet. The manifest is the record of who does what.
- **S3, the cross-host trial.** A second, independent operator (skippy) runs
  a local marvel plus local NATS on his own machine and dials the one global
  NATS, proving R-86 across a real trust boundary. S3 requires S0 through S2
  on this host first, so the trial never depends on a local piece that is
  not built.

## 1. Premises, verified 2026-09-14

Measured in marvel at origin/main aa2cfbf and in the live fleet, not read
from the register; marvel-builder verified items one to six from the code
(1.1):

- **Manifest role fields today** (`internal/api/types.go:3760`): `name`,
  `replicas`, `runtime` (`image`, `command`, `args`, `script`, `mode`,
  `prompt`, `context_window`, `context_feed`), `restart_policy`,
  `max_restarts`, `permissions`, `dangerous_permissions`, `persona`,
  `identity`, `policy`, `healthcheck`, `activity_timeout`. `persona` and
  `identity` are read only by the frozen forestage adapter; the claude adapter
  ignores them silently.
- **No identity-at-launch field exists.** The session name is computed
  (`<team>-<role>-g<generation>-<index>`), `baseEnv` stamps `MARVEL_SESSION`,
  `MARVEL_ROLE`, `MARVEL_TEAM`, `MARVEL_WORKSPACE`, `BEADS_ACTOR`, and the
  heartbeat token; the claude adapter passes no `-n`, no `--session-id`, no
  agent id (R-84 text, marvel idea `identity-at-launch-and-managed-nats.md`).
  Every identity field in section 2 is therefore FORWARD, written so the
  manifest parses today (unknown keys are dropped silently by the loader, which
  is itself a hazard: a FORWARD field that is silently ignored reads as
  "declared" while doing nothing; check 3.1 covers it).
- **No bus code in marvel.** Nothing references nats, jetstream, or the
  envelope. The working bus is director's Phase 0 shim. R-85 is FORWARD.
- **Two daemons on one host.** marvel's isolation unit is its own home
  (`$HOME/.marvel`, `paths.go:63`), from which the control socket, the bolt
  state store, the keys, and the tmux server name all derive; there is no
  home flag or env override (confirmed in `cmd/marvel/main.go` and
  `internal/paths`). Overriding the OS `HOME` de-authenticates the harness
  (marvel finding-025), so the twin daemon runs under the same `HOME` with the
  three overrides that do exist: `marvel daemon --socket <path> --state-bolt
  <path>` and `MARVEL_TMUX_SOCKET=<name>`. The keys directory is then shared,
  which is acceptable for a local twin, and neither daemon binds the mrvl
  TCP port unless asked. A daemon adopts only panes it has a record of and
  leaves the rest (`AdoptOrLeave`), so the twin never touches the hand-run
  fleet's panes.
- **Shift is built**: rolling, new generation beside the old, workers first
  and supervisor last (`shiftOrder`, `controller.go:1729`), with a 10m timeout
  and rollback. A restarted or shifted interactive replica is a NEW session
  under a new index (max plus one, no gap fill) in a fresh pane; the old pane
  is destroyed. So marvel's computed session name changes on every restart and
  shift by design, which is why the bus id and the session key in 2.1 are
  projected from the declared name and never from the computed one. The
  handoff artifact is UNBUILT (zero references in marvel code); the owner
  split is ruled (`elem-handoff-schema-ownership`) and has no code. A
  completed headless run holds its replica slot (ADR-010). Graceful daemon
  stop detaches and leaves panes running; a restarted daemon adopts them.
- **The live roster drifts between name and bus id already.** Nine sessions
  are present on the bus. The session named `architect` is `agent://ops/
  agentic-engineering-architect`; the session named `director` is
  `agent://ops/planner`. That is R-73's two-records-to-reconcile in the wild,
  and the one-source projection of R-84 is what removes it.
- **wardrobe v1 at 838aebe**: roles `director`, `supervisor`, `envoy`,
  `research-supervisor`, `worker`, `merge-queue`, `retrospector`, `author`,
  `reader`, `filer`; processes `team-supervision` (assembles supervisor,
  worker), `ops-maintenance` (reader, filer, author, director),
  `issue-board-tracking` (envoy, director, reader), `attention-routing`
  (director), `research-dispatch` (research-supervisor). Every role has
  `permission_floor: read` except `merge-queue` (`write`); `spawn` is
  `persistent` for the seven standing roles and `ephemeral` for worker,
  author, reader, filer; no role sets `beats: true`, so every healthcheck
  projects to `process-alive` (the bus presence beat is director's, brief 6).
  `scripts/marvel-map.yaml` maps `read` to `permissions = "plan"`, `write` to
  `"default"`, persistent to `restart_policy = "always"`, ephemeral to
  `"never"`.

### 1.1 marvel-builder's answers (2026-09-14, against aa2cfbf)

Six questions, answered from the code and folded into the bullets above. The
three that change the design: `baseEnv` (`adapter.go:187`) is the single
injection point every adapter applies and is documented as the seam a
future manifest env surface overrides, so the smallest identity step is one
line there (`DIRECTOR_AGENT_ID = ctx.Session.Name`, deterministic and unique
per replica) with a small `Role.AgentID` or `Role.Env` passthrough for a
human-chosen name; there is no `Role.Env` and no role-level args today
(`runtime.args` is copied verbatim, so it can carry a static `--mcp-config`
but nothing per replica); and the `generic` adapter is the capture-pane
fallback with no production call site, so a declared broker workload is R-85
work, not something the manifest can lean on at S0.

## 2. Who does what: the target manifest

Workspace `ops2`, team `fleet`. The twin uses a distinct team token so its
subjects, presence keys, and durables never collide with the live `ops` team
on the same broker during S0 and S1; at S2 the name is the fleet's name and
the old one is simply absent. One manifest role per fleet function, replicas
1 unless stated. Wardrobe casts are `wardrobe/<kind>/<id>@<version>` at the
tagged commit the launcher reads (precondition 1 of `recommended-shape.md`
4.12).

| manifest role | wardrobe cast | replicas | adapter | permissions | restart | healthcheck | notes |
|---|---|---|---|---|---|---|---|
| director | roles/director@0.1.1 (attention-routing) | 1 | claude | plan | always | process-alive | holds the seat as a lease, never by name (R-54); last to cut over |
| supervisor | roles/supervisor@0.1.1 (team-supervision) | 1 | claude | plan | always | process-alive | routes, does not decide (operator definition 2026-09-12); cuts over after every worker role |
| envoy | roles/envoy@0.1.1 (issue-board-tracking) | 1 | claude | plan | always | process-alive | tracking, not delivery |
| research-supervisor | roles/research-supervisor@1.0.0 (research-dispatch) | 1 | claude | plan | always | process-alive | router, not dispatcher |
| maintainer | roles/reader@0.1.1 (ops-maintenance) | 1 | claude | plan | always | process-alive | the READER seat; filer and author are cast ephemeral by ops-maintenance when pulled, not standing |
| builder | GAP, see 2.2 | 1 | claude | plan | always | process-alive | general scope |
| marvel-builder | GAP, see 2.2 | 1 | claude | plan | always | process-alive | scope: marvel |
| sideshow-builder | GAP, see 2.2 | 1 | claude | plan | always | process-alive | scope: sideshow, sideshow-packs |
| architect | GAP, see 2.2 | 1 | claude | plan | always | process-alive | parties, briefs, reviews |
| merge-queue | roles/merge-queue@0.1.0 | 0 | claude | default | always | process-alive | declared, not run: D5 keeps PRs human-merged until an AI-reviewer seat exists |
| retrospector | roles/retrospector@0.1.0 | 0 | claude | plan | always | process-alive | declared, not run until a retro pass is pulled |
| nats-local | none (a workload, not an agent) | 1 | generic | n/a | always | process-alive | the local tier of R-86, supervised by marvel (R-85); FORWARD: `generic` has no production call site today, so S0 reuses the Phase 0 broker and this row lands with R-85 |

Permissions are the marvel-map projection of the wardrobe floor, provisional
until a projector casts (the map's own author line says so). `activity_timeout`
is left unset on every role: a fixed default over-fires on a long turn, and
the bus presence beat is the activity signal here.

### 2.1 The identity block (FORWARD, R-84)

Every agent role carries one block from which three things are projected. The
field names follow marvel's own idea file (`identity: { name, bus }`) so the
two halves meet:

```toml
[team.role.identity]
name = "{role}"            # replicas > 1: "{role}-{index}"
bus  = "nats-local"        # the declared broker role this session joins
```

From `name`, marvel projects, in one place (`baseEnv` or the adapter's
`Prepare`): the harness display name (`claude -n <name>`), the bus id
(`DIRECTOR_AGENT_ID=<name>`, address `agent://<team>/<name>`), and the session
key (`<workspace>/<team>/<name>/g<generation>`), with `--session-id` derived
from the key so a restart changes pid and socket and keeps the durable
conversation identity (R-06, R-79). From `bus`, marvel injects `NATS_URL` and
a per-session credential minted the way `MARVEL_HEARTBEAT_TOKEN` is today,
validated at mint against the closed character class (R-76) and bound at the
broker to the session's own subjects (R-77). Until the field exists, S0 has two
steps that are real today: `runtime.args` carries the static `--strict-
mcp-config --mcp-config` for the shim, and one line in `baseEnv` sets
`DIRECTOR_AGENT_ID` from the session name (marvel-builder's smallest change).
That yields distinct, deterministic bus ids at S0 with zero new manifest
fields; the declared `identity` block is the S1 item that makes the name
human-chosen and projects `-n` and the session key from the same source.

The character class is the one director#3 fixes: a role name with a `.`, `*`,
or `>` is refused at mint, never rewritten.

### 2.2 The gaps wardrobe does not cover yet

Four live functions have no v1 role: `builder` (three scoped instances),
`architect`. Casting them as `worker@0.1.0` is wrong on the spawn axis
(worker is ephemeral, one task in its own worktree) and `author@0.1.1` is
wrong on the CAN axis (class fixes on the repos it authors, not a standing
build seat). The honest manifest names the gap rather than casting a role that
does not fit. Two proposal PRs in wardrobe, human-ratified, close it:

- `roles/builder@0.1.0`, persistent, `permission_floor: read`, faces "one repo
  scope", CAN the worker's build set plus PR authoring, CANNOT self-merge (D5),
  reports to director, escalates to supervisor; assembled by team-supervision.
  The scope (marvel, sideshow, general) is a cast-time parameter in the cast
  record (ruling 18: compositions are records, not items), so one role serves
  three manifest roles.
- `roles/architect@0.1.0`, persistent, read floor, faces "the design corpus and
  the party room", CAN run parties, write briefs, review PRs without merging,
  reports to director.

Until they land, the four rows above carry the marvel role and no cast, and
check 3.1 records "cast: none" for them. That is a visible gap on the roster,
which is the intended state.

### 2.3 Seat and succession in the manifest

The director role is one replica by design: the seat is a lease on the seat
KV key (SEAT-A), so a second director replica would be a second candidate,
never a second holder. `marvel shift ops2/fleet` rotates workers first and
supervisor last, which is the order S1 needs; the director's rotation is the
graceful shiftchange of the custody brief (1.3), and the twin director
acquires the seat only after the hand-run director releases it.

## 3. Success criteria: the register, made into checks

Each criterion names the requirement, the check as a person runs it, and the
pass condition. A check that cannot be run is recorded as such, never as
passed (finding-157).

### 3.1 N sessions, N distinct stable identities at launch (R-49, R-84, R-73)

Check: `marvel get sessions` for the twin team, the bus roster
(`list_roster`, or `nats kv get` over `presence.fleet.>`), and
`~/.claude/sessions/*.json` for the twin pids, joined on pid. Pass: for every
agent role with replicas N, N roster entries under `agent://fleet/<name>`,
each with a distinct `instance`, each session record's `name` equal to the
bus id, and each `MARVEL_SESSION` key projecting to that same name; zero
entries under any OS-user-derived address (`agent://fleet/michael` is a fail).
Repeat after `marvel shift ops2/fleet --role envoy`: the new generation has a
new pid, socket, pane, and marvel index, and the same bus id and session key
(R-06); if the bus id followed marvel's computed name it would change here,
which is the fail this check exists to catch. Second pass:
apply the manifest with a deliberately unknown identity key and confirm the
loader refuses or warns; a silent drop is a fail of the FORWARD field's own
precondition.

### 3.2 Receiver-acknowledged delivery, loud undeliverable (R-08, R-09, R-88)

Check A: from the live director, `send_message` a REQUEST to each twin role
with a nonce in the body. Pass: each receiver writes its receipt (the
recipient's own durable write echoing a digest of the content, R-88) and
answers AGREE or REFUSE carrying `in_reply_to`; the send's "accepted for
delivery" is never counted. Check B: send to `agent://fleet/nobody` and to a
twin role after `marvel stop` of that one session. Pass: the sender sees a
loud failure (NOT-UNDERSTOOD, a delivery FAILURE, or a tool error) within the
presence TTL, and no "accepted" is the last word. Five messages over three
hours with no receipt is the finding-151 shape and fails the stage.

### 3.3 Shim-timer presence, not model self-report (R-56)

Check: pick one twin role, leave it idle for four presence TTLs (six minutes
at TTL 90s) with no tool call, then start a long turn (a task that runs past
one TTL) and watch `presence.fleet.<name>.<instance>`. Pass: the record's
`ts` advances every ~30s in both cases; the roster never drops the session;
after brief 6 lands the record also carries `state_source: observed` during
the long turn. Fail: any gap longer than one TTL while the pid is alive.

### 3.4 The local bus survives a session restart (R-42)

Check: queue three messages to a twin role, `marvel stop` that session (or
kill its pid), let the restart policy respawn it, then read its inbox. Pass:
all three arrive, in order, exactly once, with their receipts; the JetStream
stream and the presence KV are intact across the restart. Second pass:
restart the twin daemon itself (`daemon reexec`, or a graceful stop that
detaches and a start that adopts) with sessions live. Pass: sessions survive
in their panes, the broker was never restarted by the daemon, and no queued
message is lost. Note the R-50 cost recorded in the probe: a fresh
per-instance durable replays the stream, so "exactly once" is measured by
receipt digest, not by delivery count, until durable conversation identity
(R-06) removes the replay.

### 3.5 Shift-change custody handoff without loss (R-60, R-61)

The custody store is director's (CUST-A, CUST-H); marvel's handoff
artifact is unbuilt, so the note in these checks is director's own until
marvel's schema exists, and the checks do not wait on it.

Check A, graceful: with two open asks and one parked ask owned by the twin
supervisor, run `marvel shift ops2/fleet --role supervisor`. Pass: the
successor reads the custody store and reports the three asks by id and
owner_generation before it reads any handoff note; the note exists and is
consistent, and deleting it before the successor starts changes nothing about
what the successor reports (R-61). Check B, ungraceful: SIGKILL the supervisor
mid-turn with the same custody. Pass: after the restart the successor reports
the same three asks; nothing depended on a flush at exit (R-60). Check C: the
same two checks on the director seat, where "successor" means the twin
director acquiring the seat lease after the TTL, and the fencing token of the
old holder is rejected by one receiver (R-55, R-57).

### 3.6 Cutover gate

A role cuts over when 3.1 through 3.4 pass for it; the supervisor additionally
needs 3.5 A and B; the director additionally needs 3.5 C. S2 is reached when
every row in section 2 with replicas 1 has cut over and no hand-run session
appears in `~/.claude/sessions/*.json` with a fleet role name.

## 4. S3: the cross-host trial (skippy)

What is stood up on skippy's machine: his own marvel daemon, his own local
NATS as a declared workload, and the same manifest with `workspace` set to a
token he chooses, dialing the one global NATS this fleet exposes (a leaf-node
or gateway connection from his local broker, credentials issued by the global
side). His supervisor is cast from wardrobe at the same tag.

Success criteria, each a check:

- **R-86, two tiers, no rename.** His supervisor is present on the global
  roster under the address minted locally (`agent://<his-team>/supervisor`),
  and the local director here sends it a REQUEST and receives AGREE with a
  receipt. Pass: no rename, no re-registration, the local id is the global id
  (R-06, R-79).
- **R-77, credential binding holds.** From his host, an attempt to publish on
  a subject outside his session's grant (this fleet's inbox, or the seat key)
  is refused by the broker, not by convention. Pass: the refusal is logged on
  the global broker and the message never appears in the target's stream.
- **R-49, identity minted locally.** His session's identity came from his
  launcher's manifest field, not from his OS user; `agent://<his-team>/<os
  user>` is absent.
- **R-56 across the link.** His presence beats on the global tier through
  the local tier on the shim's timer; a five-minute idle on his side does not
  drop him from the global roster.
- **R-09 across the link.** A send from here to a role he has not started
  fails loudly here.

Prerequisites: S0 through S2 on this host; the global broker running with an
authorization block (director#4) and leaf-node listener; a credential issued
for his local broker. What he needs from us: the manifest, the wardrobe tag,
the global NATS URL and credential, and the five checks above as a checklist.
The director seat files the aae-orc ticket for the operator to relay; this
brief supplies the shape, not the ticket.

## 5. Open questions

1. Does the twin's local tier run as the declared `nats-local` role from S0,
   or does S0 reuse the Phase 0 broker at 4222 and the declared role arrive
   with R-85? Proposal: S0 reuses 4222 under the `fleet` team token; the
   declared broker is an S1 item so the twin daemon is not blocked on it.
2. The builder and architect role files (2.2): who drafts, and whether
   `builder` takes its scope as a cast parameter or as three role files.
3. Whether `merge-queue` at replicas 0 belongs in the manifest at all before
   D5 lifts, or in a comment.
4. The identity block's `--session-id` derivation: from the key alone, or
   minted once and stored, given a harness restart must keep the conversation
   identity (R-06) while a shift must not.
5. How the global tier issues a leaf-node credential to a second operator
   without a human on this side pasting a secret (R-77's trigger, the first
   principal that writes the bus without a human launching it).
