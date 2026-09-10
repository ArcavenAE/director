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
