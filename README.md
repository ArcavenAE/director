# director

Supervisor communications and multi-agent coordination for the Dark Atelier
platform. The human's assistive agent for running work across many agent
sessions on many machines, under many harnesses.

**Status: simulation, with a proven transport and a cross-host tier.** The
director software does not exist yet. What exists:

- a skill that has a session play the role by hand, plus the instruments it
  uses and the requirements that fall out (`skills/director/`,
  `sim/requirements.md`);
- a Phase 0 probe that proves the message bus the software will ride: a
  local NATS broker and a small MCP shim carry the director envelope between
  sessions end to end, across Claude Code and codex, with store-and-forward,
  deduplication, presence, and broker-enforced authorization
  (`probe/nats-phase-0/`);
- a global tier that connects one host's broker to another's through a hub,
  so a director reaches a remote supervisor without a rename and without
  holding a hub credential (`probe/nats-global-tier/`, design brief 8);
- the marvel twin: the hand-run fleet declared as one marvel manifest, cast
  from wardrobe roles, with the check runbook that decides cutover
  (`sim/twin/`, design brief 7).

This repo is the authoritative source for all of it, and the requirements
are the point.

## Why it exists

An operator running several agent sessions is the message bus, and that is
the defect. Sessions stop to ask a question and the question waits on one
human's attention; when the session ends first, the work it did and the
answer it needed are both lost. Observed with nine sessions on one laptop:
five had died holding unanswered questions.

Director's product is therefore **custody**, not coordination. Nothing
produced is lost and nothing asked is dropped. Message routing is downstream.

## Not made redundant by any one harness's remote control

Harness vendors are building session-to-session messaging, and it is good.
It is also single-vendor, single-account, single-machine. Director must reach
sessions running Claude Code against Bedrock or the Claude platform on AWS,
codex, crush, opencode, raw SDK streams, and whatever is next, across hosts
and accounts. The measured capability table in
`skills/director/reference/adapters.md` is the argument: of four harnesses
installed on one machine, exactly one exposes presence or an address.

There is a subtler reason too. Claude Code's own cross-session messaging does
not deliver a message, it delivers the message wrapped in a policy paragraph
the receiving harness writes, which tells the receiver the sender is "very
likely" acting for the user. That hedge is a probability estimate standing
where a signature belongs, it is not ours to version or extend, and no other
harness ships anything like it. Building director on that feature means
inheriting safety we cannot inspect and cannot carry:
`skills/director/reference/relay.md` and
`sim/specs/vendor-injected-receiver-policy.md`.

## Documentation

| Read this | For |
|---|---|
| [docs/getting-started.md](docs/getting-started.md) | running the broker, provisioning it, building the shim, joining Claude Code and codex sessions, sending and receiving, authorization, the global tier, marvel |
| [docs/shim-reference.md](docs/shim-reference.md) | every environment variable, tool, address scheme, subject, stream, and envelope field the shim uses |
| [docs/architecture.md](docs/architecture.md) | the target shape in diagrams: bus, addressing, receive-is-a-poll, acknowledgement, the marvel seam, ask lifecycle, the performative handshake, the global tier |
| [docs/use-cases.md](docs/use-cases.md) | worked use cases from the simulation, each with a diagram and the requirements it exercises |
| `sim/requirements.md` | the requirements register, R-01 onward, each traced to the observation that earned it |
| `sim/design/` | design briefs: identity at spawn, the seat lease, continuous custody, the shim heartbeat, the twin, the global tier, credential enrollment, broker supervision |

## Layout

```
skills/director/     the skill: role, modes, output contract, capture triggers
  reference/         measured capability table; the settled and open relay questions
  scripts/dsi        session inventory across all four adapters
  scripts/dsx        external state verification (the board has no expiry)
commands/director.md thin command that invokes the skill
install.sh           symlink or copy the skill and command into ~/.claude
docs/                operator guide, shim reference, architecture and use-case diagrams
probe/nats-phase-0/  the transport probe: NATS broker + director-mcp shim
  PROGRESS.md          sub-probe results (poll is the receive, finding-160)
  director-mcp-seat    the launcher a seat registers; see Install
probe/nats-global-tier/
                     the global tier probe: a TLS hub and leaf-connected
                     clusters, so separate clusters share one director tier
  brief.md             what the tier is for and what it has to prove
  nats-mechanics.md    the NATS behaviour the tier leans on
  recipe-mokuzai.md    the worked setup for one leaf cluster
  hub/                 hub config, the CA ceremony, provisioning scripts
  leaf-remote.conf.example  the leaf side of the connection
  verify-*.sh          checks that a tier is actually carrying traffic
  director-mcp/        the shim (Go): five tools, local and global tiers, preflight
  PROGRESS.md          sub-probe results 1 through 9 (poll is the receive, finding-160)
  authorization.conf   the credential-to-subject binding (director#4)
  cross-harness-demo.sh  codex and headless Claude Code joining the bus
probe/nats-global-tier/  the R-86 hub: config, provisioning, leaf recipe, verify scripts
sim/                 the simulation record
  prompt-session-1.md  the original direction, verbatim
  requirements.md      the requirements register (the point)
  specs/               requirements firm enough to constrain the software
  design/              design briefs 1 through 10 (candidates, not specs)
  twin/                the marvel twin: manifest, cast launcher, check runbook
  artifacts/           work products the simulation produced
```

`sim/notes/` and the state root (`~/.director/state` by default) are
gitignored. They carry live operational detail about real sessions and are
never published; anything durable is rewritten clean into `sim/specs/` or
into the platform's knowledge graph.

## Install the skill

```sh
./install.sh          # symlink into ~/.claude, edits go live
/director             # sweep: what is blocked on you
/director standing    # adopt the role for the session
```

That installs the skill and the command and creates the state root. It is the
whole of what `install.sh` does.

### The bus seat, by hand

The skill alone does not let a seat send a message. Sending runs over the
director MCP server, and nothing in `install.sh` puts it in place, creates
`~/.director/bin`, or mentions a broker. On this fleet that gap is closed by
hand. It is honest to say what this is: Phase 0 probe material being run in
anger, not a packaged install. Writing the installer is a separate job.

Two files, both under `probe/nats-phase-0/`:

- `director-mcp`, the MCP server. Build it and install it at
  `~/.director/bin/director-mcp`, or point `DIRECTOR_MCP_BIN` at it. The
  launcher refuses to start if it is missing.
- `director-mcp-seat`, the launcher that works out who the seat is. The fleet
  runs it from `~/.director/bin/director-mcp-seat`, a symlink back to this
  repo.

Register it once, with no identity of its own. The same registration is then
correct both for a marvel-managed session and for a seat you start yourself:

```sh
codex mcp add director -- ~/.director/bin/director-mcp-seat
```

What the launcher needs, and where it gets it:

| Needs | In a marvel session | Started by hand |
|---|---|---|
| identity | `MARVEL_SESSION`, `MARVEL_TEAM`, `MARVEL_WORKSPACE`, stamped by marvel | `DIRECTOR_AGENT_ID` falls back to `director-seat`; `DIRECTOR_TEAM` and `DIRECTOR_WORKSPACE` are required and it exits without them |
| bus credentials | `DIRECTOR_NATS_USER` and `DIRECTOR_NATS_PASS`, stamped by marvel | user `director`, password read from `~/.marvel/state/nats/director.pass` |
| broker | `NATS_URL`, default `nats://127.0.0.1:4222` | same |

That password file exists only if the cluster declares a seat, so a hand-run
seat also needs this in the cluster config, then a daemon restart:

```yaml
bus: { managed: true, seat: { workspace: <ws>, team: <team> } }
```

One extra line is required for codex, which does not pass its environment to
an MCP server. Without it a marvel-managed codex session cannot see marvel's
stamps and falls back to the seat identity. It belongs in the role's manifest
beside the command registration:

```
-c 'mcp_servers.director.env_vars=["MARVEL_SESSION","MARVEL_TEAM",
     "MARVEL_WORKSPACE","DIRECTOR_NATS_USER","DIRECTOR_NATS_PASS",
     "NATS_URL"]'
```

Diagnostic: a marvel-managed agent that appears on the roster under the seat
id rather than its own session name is missing that line.
## Run the bus

```sh
probe/nats-phase-0/start.sh &                              # local broker, loopback, JetStream
nats stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox,agent.*.*.role.*.inbox' \
  --storage file --retention limits --max-age 24h --max-msg-size 65536 --dupe-window 2m
nats stream add AGENT_AUDIT --subjects 'agent.audit' --storage file --retention limits --max-age 720h
nats kv add AGENT_STATE --ttl 90s --storage file
(cd probe/nats-phase-0/director-mcp && go build -o director-mcp .)
```

Then join a session by handing the harness the shim with an identity in its
environment. The [getting-started guide](docs/getting-started.md) walks
each step, both harnesses, and what to do when a message does not arrive.
Under marvel, a cluster with a managed bus does the broker half itself.

## Reference clones

`multiclaude/` and `tmate/` are third-party checkouts kept locally for
reference and are not vendored. multiclaude is the store-and-forward design
predecessor; tmate is prior art for reaching a terminal session you do not
share a machine with.

## Provenance

Session 1 of the simulation ran 2026-09-04 over nine sessions and three
hours. It produced nine envelope fields, each earned by a specific observed
failure, and one unpredicted result: the coordination was not the valuable
part, the custody was. It also silently dropped five of its own messages
while every send reported success, which is why liveness is a requirement
here and not a nicety.
