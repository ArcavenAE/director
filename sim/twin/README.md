# The marvel twin, S0: manifest, casts, and the check runbook

The concrete half of design brief 7 (`../design/marvel-twin-manifest-and-
cutover.md`). The brief says who does what and what "looks right" means;
this directory is the manifest that declares it, the launcher that casts it,
and the runbook that checks it. The operator said GO on S0 on 2026-09-14;
marvel-builder is build-lead; I own the manifest and cast details and the
validation against criteria 3.1 to 3.6.

## Files

- `ops2-fleet.toml`: workspace `ops2`, team `fleet`, nine standing roles,
  replicas 1, the claude adapter through the launcher, `plan` permissions and
  `always` restart from wardrobe's marvel-map. Roles declared-not-run
  (merge-queue, retrospector, nats-local) are comments, because marvel refuses
  `replicas = 0` at parse time.
- `cast-launch.sh`: the launcher. Slices the wardrobe role for `MARVEL_ROLE`
  (the spawn line is the side effect), sets `DIRECTOR_AGENT_ID` from
  `MARVEL_SESSION` unless marvel already set it, wires the director shim in
  with `--strict-mcp-config`, and execs claude with the slice as the system
  prompt. It adds no refusal of its own; every refusal is slice.sh's.

## What is real today, and what is FORWARD

Real, and the S0 stand-up relies on nothing else:

- marvel resolves the adapter by `image`, so `image = "claude"` with a wrapper
  `command` gives the wrapper the adapter's flags in `$@` (`internal/runtime/
  adapter.go` `resolveCommand`, `manager.go:569`).
- `baseEnv` stamps `MARVEL_SESSION` (`<team>-<role>-g<gen>-<index>`, unique
  per replica) and `MARVEL_ROLE` into every session; the wrapper derives the
  bus id from it. N sessions, N distinct addresses, with zero marvel changes.
- The claude adapter skips its own `--append-system-prompt` when the args
  already carry one, so the wardrobe slice is the whole system prompt.
- Two daemons under one HOME isolate on `--socket`, `--state-bolt`, and
  `MARVEL_TMUX_SOCKET` (brief 7, 1).

FORWARD, and the manifest does not pretend otherwise:

- The declared identity block (R-84): the name is marvel's computed one at
  S0, so the address is `agent://fleet/fleet-director-g1-0`, not
  `agent://fleet/director`. The wrapper defers to `DIRECTOR_AGENT_ID` when
  marvel sets it (marvel-builder's baseEnv one-liner), and to the declared
  block when it exists. Check 3.1 accepts computed names at S0 and demands
  stability across a shift only once the declared block lands.
- A marvel-supervised broker (R-85): S0 reuses the Phase 0 broker at
  nats://127.0.0.1:4222 under the `fleet` team token, so twin subjects and
  presence keys never collide with the live `ops` team.

## Cast preconditions (wardrobe `recommended-shape.md` 4.12), state at S0

| # | precondition | state | who |
|---|---|---|---|
| 1 | `validate` and `index --check` clean at the tagged commit the launcher reads | wardrobe main 8b3d247 validates; NO TAG EXISTS yet | operator tags (`v1.0.0-s0` or the sha) |
| 2 | helper refuses a dirty tree, a writable root, a path outside, a marker | slice.sh does; the install root must be a separate read-only checkout (`test -w contents/` false) | build-lead makes the checkout: `git clone --branch <tag> ... ~/.local/share/wardrobe && chmod -R a-w ~/.local/share/wardrobe/contents` |
| 3 | spawn log writable, line appended before exec | slice.sh appends to `${WARDROBE_SPAWN_LOG:-~/.local/state/wardrobe/spawn.log}` | build-lead confirms the path is writable by the daemon's uid |
| 4 | ruleset on main with `validate` required | human repo setting; PRs #2 to #9 went through review | operator confirms |
| 5 | stale-reader grep over `repos.yaml` checkouts empty | not run today | build-lead runs it before the first cast |
| 6 | the broker is provisioned, not bare: `AGENT_INBOX`, `AGENT_AUDIT`, and the `AGENT_STATE` bucket exist before `marvel work` | pre-existed on the origin host from phase 0; absent on a fresh broker (skippy, aae-orc#327, finding-166) | build-lead runs the block below and the verify line before the first cast |

Precondition 6, the exact spec that connected the shim on the second host
(the same objects `probe/nats-phase-0/verify-auth.sh:85-92` creates):

```sh
nats stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox,agent.*.*.role.*.inbox' \
  --storage file --retention limits --max-age 24h --max-msg-size 65536 --dupe-window 2m
nats stream add AGENT_AUDIT --subjects 'agent.audit' --storage file --retention limits --max-age 720h
nats kv add AGENT_STATE --ttl 90s --storage file
```

Verify: `nats stream ls` shows both streams, `nats kv ls` shows `AGENT_STATE`,
and a hand run of the shim prints `connected to ... as agent://...` rather
than `presence KV AGENT_STATE: nats: bucket not found`. A bare broker fails
silently otherwise: `--strict-mcp-config` makes a dead shim a non-error, the
harness starts without its tools, and marvel reports the session running
(finding-166; fix shapes and candidate R-93 there).

Until precondition 1 holds, a cast is a development run: `ALLOW_DIRTY=1`
lets slice.sh proceed with `tree=dirty` in the spawn line, and every such
line is visible in `just running` as the record that the cast was not from a
tag. Do not cut over any role on a dirty-tree cast.

All ten roles are `status: proposal`; the wrapper passes `--allow-proposal`,
which rulings 47 and 61 make safe at the read floor (no twin role is at the
write floor; merge-queue, which is, is not run).

## Stand-up sequence (S0)

1. Build the shim if needed: `cd director/probe/nats-phase-0/director-mcp
   && go build -o director-mcp .` (the wrapper's default `DIRECTOR_SHIM_BIN`).
2. Make the read-only wardrobe install root (precondition 2 above).
3. Start the twin daemon: `MARVEL_TMUX_SOCKET=marvel-twin marvel daemon
   --socket ~/.marvel/run/twin.sock --state-bolt ~/.marvel/state/twin.bolt`.
4. Apply: `MARVEL_SOCKET=~/.marvel/run/twin.sock marvel work
   director/sim/twin/ops2-fleet.toml`, run from a cwd where the relative
   `command` resolves (the director repo root), or set an absolute path.
5. Trust: sessions start in `TWIN_CWD` (default the orc root), which the
   operator has already trusted, so no trust dialog blocks the shim. Do not
   answer the dialog with `dangerous_permissions`; it also drops the `plan`
   permission mode the wardrobe read floor projects to.
6. Watch: `marvel get sessions -w` on the twin socket; the roster from the
   live director (`list_roster`) should show nine new `fleet` entries beside
   the nine `ops` ones.

## Check runbook (brief 7, section 3), per role

Record each as PASS, FAIL, or NOT RUN with the command and the output line.
NOT RUN is never PASS (finding-157).

- **3.1 identity.** `marvel get sessions` (twin socket) joined on pid with
  `~/.claude/sessions/*.json` and the roster: nine `agent://fleet/<name>`
  entries, distinct `instance` each, session record `name` equal to the bus
  id, no `agent://fleet/<os user>`. Then `marvel shift ops2/fleet --role
  envoy`: new pid, socket, pane, index; at S0 the bus id changes with the
  computed name (expected, recorded as the FORWARD gap), and once the
  declared block lands it must not.
- **3.2 delivery.** Director-facing; the seat runs it: a nonce REQUEST to
  each twin role answered with a receipt and `in_reply_to`; a send to
  `agent://fleet/nobody` and to a stopped role fails loudly within one TTL.
- **3.3 presence.** Pick one role, six idle minutes, then a long turn;
  `nats kv get AGENT_STATE presence.fleet.<name>.<instance>` every 30s: `ts`
  advances throughout, no gap over 90s while the pid lives.
- **3.4 bus survives restart.** Queue three nonce messages to a role,
  `marvel stop` that session, let `always` respawn it, read the inbox: three
  receipts, in order. Then stop and restart the twin daemon (detach, adopt):
  sessions survive, nothing queued is lost.
- **3.5 custody.** Two open asks and one parked on the twin supervisor;
  `marvel shift --role supervisor`; the successor reports all three from the
  custody store before reading any note. Then SIGKILL mid-turn; same result.
  Director: the twin director acquires the seat after the TTL and one
  receiver rejects the old fencing token.
- **3.6 gate.** A role cuts over when 3.1 to 3.4 pass for it; supervisor adds
  3.5 A and B; director adds 3.5 C. S2 when no hand-run fleet session remains
  in `~/.claude/sessions/*.json`.

Results go in `sim/twin/checks-<date>.md`, one file per run, appended never
rewritten.
