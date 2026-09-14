# Adapters: what each harness actually supplies

Measured 2026-09-10 on one machine, by reading each harness's own store.
The table is the durable part. The paths are scaffolding and will rot.

An adapter declares what IT supplies, not what the harness theoretically
has. "metadata only" means the store holds the transcript and this adapter
does not read it yet. A capability table that overstates is the finding-157
failure class (a tool that answers confidently without having checked).

| | identity | presence | liveness | inbox | transcript | terminal signal |
|---|---|---|---|---|---|---|
| claude | yes | yes (`idle`/`busy`/`shell`) | pid only | yes, unix socket | yes | no |
| codex | yes | no | no | no | yes | no |
| opencode | yes | no | no | no | metadata only | no |
| crush | yes | no | no | no | metadata only | no |

**Only Claude Code exposes presence or an address.** The other three can be
read and cannot be reached. This is finding-144's liveness requirement
reappearing as a measurement instead of an argument, and it is the reason
director cannot be a thin wrapper over any one harness's local features.

No harness supplies a terminal signal distinguishing "finished" from "died".
All four leave the reader to infer it from file mtimes and process state,
which is exactly the state-from-proxy defect finding-144 named at two layers.

## Where each adapter reads

- **claude** - `~/.claude/sessions/<pid>.json` for the roster (sessionId,
  name, cwd, status, pid, version, peerFeatures, messagingSocketPath) and
  `~/.claude/projects/<slug>/<sessionId>.jsonl` for transcripts.
- **codex** - `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<id>.jsonl`. First
  line is `session_meta` (session_id, id, cwd, cli_version, originator,
  thread_source). Body lines are `response_item` with `payload.role` in
  developer/user/assistant. `thread_source: subagent` marks a spawned
  thread, not a session a human is driving.
- **opencode** - `~/.local/share/opencode/opencode.db`, table `session`
  (id, slug, title, directory, version, agent, model, cost, time_created,
  time_updated). `message`/`part` hold the transcript, unread by this
  adapter. The `permission` table is a persisted allow-list, not pending
  approvals, so it is not an ask source.
- **crush** - `~/.local/share/crush/projects.json` registers each project's
  `data_dir`; the sessions live in `<data_dir>/crush.db`, table `sessions`
  (id, title, message_count, cost, created_at, updated_at, todos).

## Gotchas found while writing these

- crush's schema comments declare milliseconds while its own update trigger
  writes seconds. `_ts()` accepts either rather than trusting the store.
- Every sqlite read opens `immutable=1`, so a live harness holding the file
  is never blocked and nothing is ever written back.
- Row caps that bind silently are the finding-154 failure. The queries take
  a generous limit and let the time window do the filtering; a cap of 200 on
  opencode was quietly dropping 16 sessions before it was raised.
- Claude transcripts open with harness-injected wrappers. `NOISE` must skip
  `local-command-caveat` or the "goal" of every session that started with a
  slash command is a paragraph about local commands.

## Token-free liveness: the heartbeat surface per harness

Sourced 2026-09-14 from each harness's current docs plus one live Claude Code
install (v2.1.269). This is a lighter grade of evidence than the 2026-09-10
store reading above: read it as a documented-mechanism survey, not a direct
measurement of every path, and hold it to the same finding-157 discipline.

The R-56 principle names the split. Liveness a separate always-running process
observes on its own timer (an HTTP health poll, a socket connect, a PID check,
a status subcommand, a state-file mtime) is token-free. Liveness that needs the
model to take a turn and call a tool is not. A second axis matters as much:
up-or-down (a health poll or PID check) is not busy-or-idle (an event stream or
an activity mtime). A durable beat reads both.

| | token-free up/down | token-free busy/idle | launch model needed |
|---|---|---|---|
| claude | `sessions/*.json` + `kill -0` + socket | `sessions/*.json` status, statusline cost/context, OTEL active_time | none for interactive; headless loses statusline |
| codex | daemon pid/socket, `doctor --json`, `daemon version`, mcp `ping` | `exec --json` stream, file mtimes | app-server daemon, mcp-server, or `--listen ws` (experimental) |
| opencode | `GET /global/health` | `GET /event` SSE, `setInterval` plugin, DB `time_updated` | none; server-first, every session serves |
| crush | `GET /v1/health` | `/v1/.../events` SSE, DB WAL mtime | `crush server` (experimental); default TUI has none |

Per-harness detail (paths and flags rot; the mechanism is the durable part):

- **claude** - no server to poll, so the beat is passive plus timed. An external
  daemon scans `~/.claude/sessions/<pid>.json` for the per-session
  busy/idle/name record and confirms with `kill -0 <pid>` and the
  `/tmp/cc-socks/<pid>.sock` socket. This is the only harness whose passive
  store carries a real presence value. A `statusLine` command with
  `refreshInterval` (minimum 1 second) is the author-controlled timer: the
  harness runs the shell command with no model turn, even while idle, and feeds
  it session JSON with `cost` and `context_window`, so it can write a presence
  file and separate working from idle in one render. SessionStart, SessionEnd,
  and Stop command hooks (not prompt hooks) give clean edges. OTEL
  (`CLAUDE_CODE_ENABLE_TELEMETRY=1`) exports on a fixed interval independent of
  turns. Caveat: the sessions store, sockets, and status timestamps are
  undocumented and version-specific, and a busy session does not re-stamp, so a
  stale record can outlive a crash. Gate behind the version.
- **codex** - one always-on token-free surface, the app-server daemon, and it is
  opt-in. `codex app-server daemon start` gives a control socket, a pid file,
  `codex app-server daemon version` (JSON, errors if down), `codex doctor
  --json` (app_server status), and `codex agents` (a live roster). Driven as
  `codex mcp-server` over stdio, MCP `ping` is the simplest per-session liveness
  and a pipe close means dead. A bare `codex exec` child registers with none of
  this: its only liveness is process presence plus activity-gated file mtimes.
  OTEL is no help here (no metrics from `exec` or `mcp-server`). The daemon
  plane is marked experimental.
- **opencode** - the cleanest fit, because it is server-first: every invocation,
  the TUI included, starts the same HTTP server, so liveness is a property of
  that process. `GET /global/health` is a pure token-free up/down probe; `GET
  /event` is an SSE lifecycle feed; an in-process plugin can hold a
  `setInterval` and beat through idle, stopping exactly when the process dies.
  Launch each agent as `opencode serve` on a fixed per-agent port (the default
  is a random port with no discoverable lock file) and poll health on the
  orchestrator's timer.
- **crush** - thinnest and mode-dependent. `crush server` exposes `GET
  /v1/health` over a unix socket (experimental, and unauthenticated in the
  tested build, so bind it host-local); a workspace tears down when the last SSE
  client disconnects, so a monitor holding the stream keeps it resident. The
  default TUI has no server, no socket, and no status subcommand: process
  presence, the data-dir lock, and `crush.db-wal` mtime are the whole floor.
  Only a `PreToolUse` hook has shipped.

Bearing on the table at the top: the server or daemon launch model upgrades
codex and crush from the "cannot be reached" column to a pollable health
surface, at the cost of running an experimental plane. It does not give any of
the four a clean finished-versus-died terminal signal (R-17); the closest
reconstruction stays a health or PID probe for up/down paired with an activity
mtime for the last sign of work. R-56 stands: drive the beat from the
always-running shim on its own timer, never from the model calling a tool.
