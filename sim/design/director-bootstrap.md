# Director bootstrap: what a director session knows and may do before its first act

Proposal, 2026-11-11. Nothing here is built. Section 9 splits it into one ticket
per mechanism.

## 0. Why

A director session started on 2026-11-11 with no teams up. Over the session it:

- patched installers;
- ran host installs;
- opened code PRs;
- proposed a team with no supervisor, a reviewer on a different host from
  the work, and a role name that exists nowhere.

Every rule it broke was already written down, in this repo's
`sim/requirements.md` (R-95, R-102, R-103) and in that fleet's own rulings. The fleet
read it needed, a seat register in marvel, was built and was never run.

Two things were missing:

- Nothing loads those rules at the start of a session.
- Nothing stops a director doing worker production when no team exists to
  route to.

finding-005 (Q6) predicted this: when the channel fails, doing the work
yourself is the only path that still terminates, and "guardrails do not beat
gradients". The director skill's re-anchor rule is text with no trigger. The
skill's own `standing-hook` was not wired in that session.

This design gives director start-up and boundary mechanisms that act whether
or not the session reads anything.

## 1. Four layers, kept apart

Director must work for people who organize work entirely differently: with or
without marvel, with or without any role catalog, and with nothing from the
fleet that wrote this design. A user may have only director. So the design separates four layers:

| Layer | Owner | Holds |
|---|---|---|
| **Director core** | this repo, installed with director | Director's own instructions and role definition (custody, not production; R-102), the bootstrap sequence, the hook and guard mechanisms, the trap-door log, and one adapter per model kind. Says nothing about how teams are shaped. |
| **Operating profile** | the user, in their own repo or state directory | A pointer: which model sources (packs, by sideshow name and version, or paths) apply at which scope, each team's entry role and rules, where handoffs live. |
| **Model sources** | their own authors, installed and versioned by sideshow | The roles, methods and engines a deployment uses: a role catalog, BMAD, vsdd-factory, team manifests, or a mix. |
| **Fleet sources** | adapters | Where director reads who exists and how to reach them: marvel's seat register when marvel is present, director bus presence always, or nothing. |

Director core never names a role, a role catalog or a team shape. A rule like
"every team has a supervisor" is profile content. Director core enforces only
two things about teams: the profile names an entry point for each team, and
director routes into a team only through that entry point. That is director's
own boundary, defined here and in the skill: director is not the supervisor of
the sessions it serves, and its verbs are its own (R-102).

## 2. Director's own instructions, and the operating model it points at

### 2.1 What ships with director

Director's own instructions are part of director and install with it
(`install.sh`):

- the skill;
- director's role definition, `reference/role.md` (what director is, its loop,
  its write domain, what it cannot do);
- this bootstrap's by-hand steps.

They are the canonical copy. A role catalog may carry a director role for its
own users, but it derives from this one and names the director version it was
derived from. It is never a second definition director must find.

Nothing in director's own instructions depends on any catalog, method pack or
tracker being installed.

### 2.2 The operating model is a list of sources, each a versioned pack

How a deployment organizes work comes from one or more **model sources**.
Examples:

- a role catalog;
- a method pack such as BMAD;
- a per-repo engine such as vsdd-factory;
- a directory of team manifests;
- nothing at all.

A deployment can mix them: one team cast from a role catalog, another running
BMAD agents inside those roles, one repo under vsdd-factory.

Each source is named by its sideshow pack and version, or by a local path when
it is not a pack. sideshow owns installing it, keeping more than one version,
and which version is active. So director never installs, upgrades or copies a
model source. It reads what sideshow reports (`sideshow list`) and records the
version each team was started against, so an upgrade under a running team is
visible rather than silent.

A source can apply at four scopes:

- the whole deployment;
- a workspace;
- a team;
- a repo.

The narrowest scope wins. This matters because some engines run inside one
repo and not from a coordinator (vsdd-factory activates per repo).

### 2.3 The profile file

The profile is the pointer, not the model: a TOML file at `$DIRECTOR_PROFILE`,
or else `$DIRECTOR_STATE/operating-profile.toml`. First-build fields:

```toml
name = "example"
fleet_sources = ["marvel-register", "bus-presence"]
handoff = "$DIRECTOR_STATE/handoff.md"
write_domain = []
rules_text = []          # files printed verbatim at start, in order

[[model]]                # one entry per source; order is precedence at one scope
kind = "role-catalog"    # adapter name
pack = "my-roles@0.2.0"  # sideshow pack and version, or path = "..."
scope = "deployment"     # deployment | workspace:<w> | team:<w>/<t> | repo:<path>

[[model]]
kind = "bmad"
pack = "bmad@6.12.4"
scope = "team:aae/research"

[[model]]
kind = "repo-engine"
pack = "vsdd-factory@1.0.0-rc.23"
scope = "repo:~/work/example"

[[team]]                 # what director must know per team, whatever the source
match = "*"              # glob over <workspace>/<team>
entry_role = "supervisor"
rules = ["has-entry"]
```

### 2.4 What an adapter answers (later, not in the first build)

The first build reads no model source itself. It prints each source the
profile names with its version from sideshow, and takes entry roles from the
profile. Adapters come when a deployment needs one; BMAD, for one, has no team
or entry-role concept, so its adapter would answer `unknown` to most of this.

Each model kind has a small adapter in director that answers four questions
from the installed pack, and answers `unknown` when the pack does not say:

1. Which roles exist (names, versions).
2. Which role is a team's entry point, if the model defines one. Otherwise the
   profile's `[[team]] entry_role` decides.
3. What each role may do that the guard and team rules read (for example,
   "opens PRs").
4. The pack's own version, as sideshow reports it.

A kind with no adapter is listed at start as present and unread. A pack that
is named but not installed, or installed at another version, prints as a
mismatch. Neither is ever read as empty.

### 2.5 With no profile

Director runs on a minimal built-in profile:

- `fleet_sources = ["bus-presence"]`;
- no model sources;
- no entry role, so routing into any team is refused until the user names one.

The start-up hook says that this is the minimal profile.

## 3. Fleet sources

Each adapter returns rows of `seat, team, role, cluster, alive, reach,
observed_at`, with `-` for a cell it did not observe, never a guess (R-27).

- **marvel-register**: `marvel get register --cluster <c>` for each cluster
  `marvel config list` names (the cluster set `freebusy` already reads). Role,
  team and alive come from the register's checked cells. Duty cells
  (`duty.first-turn`, `duty.ack`) give readiness.
- **bus-presence**: director bus presence, so a fleet without marvel still has a
  roster.

The merged read is R-103's fleet-state model, built from instruments that
exist rather than by hand.

## 4. Start-up: the role is bound where the session actually starts

### 4.1 What happens today

The operator starts director sessions with a shell function named
`director-session` in their shell profile. It sets `DIRECTOR_PRESENCE_WATCH=1`
and runs `claude --dangerously-load-development-channels server:director-mcp`.
That function shadows the installed launcher of the same name in
`~/.director/bin`. So the path actually used carries no director role and loads
no director settings, and no hook in this design would fire. A design that
binds to a launcher nobody runs binds to nothing.

### 4.2 Two bindings, both installed by `director init`

1. **The start path.** `director init` installs one start path and makes it the
   only one. The installed `director-session` sets `DIRECTOR_ROLE=director` and
   passes `--settings` with director's own settings file, which wires the hooks
   of sections 4 to 7. It is either the launcher itself or a shell function
   that calls it, and init removes the shadowing: it reports any shell
   function or alias of that name, and with the user's yes replaces it with
   one that calls the launcher with the same flags.
2. **The session mark.** Adopting the role in any session (`/director
   standing`) writes `$DIRECTOR_STATE/sessions/<session_id>`, keyed by the
   session id every hook already receives. Hooks act on a session that has the
   env var or the mark. So a director started some other way (a marvel seat,
   plain `claude` plus the skill) is still covered from the moment it adopts the
   role. A session with neither is ordinary: no guard and no start-up output,
   so a builder on the same host is never blocked.

### 4.3 What the start-up output prints

At session start (with the env var), or at the role adoption that writes the
mark, director prints this into context, not as a pointer to read:

1. The profile's name and its `rules_text`, verbatim.
2. The fleet read (section 3) per cluster: each team, its entry-point seat, and
   that seat's readiness.
3. Each team that fails a profile rule, by rule id.
4. When no team is up: `no team is up: your first act is to stand one up,
   entry role first`.
5. Open asks from the ask ledger (`dws asks`), oldest first.
6. The predecessor's handoff, or `no handoff: write one before you end`.

The output is bounded: at most 60 lines, with the remainder counted rather than
cut silently.

## 4a. Install and `director init`

### 4a.1 One package

Director ships as one versioned package: the skill, `reference/role.md`, the
hook scripts, the tools under `~/.director/bin`, and a settings file wiring the
hooks. `director-mcp` installs as a release binary. A checkout plus
`install.sh` stays the development path.

The vehicle is open, and it is checked first (ticket 0): a Claude Code plugin
is the native way to ship a skill with its hooks, and sideshow already installs
versioned packs, some carrying their own hooks. Whether sideshow installs a
plugin, or carries the hooks itself, decides between them.

### 4a.2 `director init`

`director init` is the one command a new user runs after the install:

1. It detects what is there: `marvel config list`, `sideshow list`, team
   manifests under a path the user names, an existing profile.
2. It asks only for what it cannot detect, chiefly each team's entry role.
3. It writes the profile, installs the start path (4.2), and runs the start-up
   output once, so the user sees what a session will see.

With nothing detected, it writes the minimal profile and says what that means:
director runs, and routing into a team is refused until an entry role is named.
`director init --check` re-reads everything and reports drift without writing.

## 5. The guard: a `PreToolUse` hook in director sessions

In a director session (the env var or the session mark of 4.2), the guard
refuses:

- Edit and Write outside director's write domain. The domain is the director
  state directory, plus paths the profile lists under `write_domain`.
- `git commit`, `git push` and `gh pr create` in a repository outside the write
  domain.
- An inject or send into a seat whose role is not the team's entry role, read
  from the fleet read's role cell, never from a name.

A refusal says what was refused, names the entry-point seat to route to when
one exists, and gives the trap-door command.

A refusal, not a timeout, is the contract (R-09, R-109).

## 6. Trap doors

`director-trapdoor open --reason "<text>" --grant "<the operator's words>"
[--actions N | --minutes M]` opens a window, by default one action. Each use is
appended to `$DIRECTOR_STATE/trapdoors.jsonl` with:

- the time (from the clock);
- the reason and the grant, verbatim;
- the action allowed;
- `routing_defect`: what was missing that forced it, for example `no team up`
  or `entry seat not ready`.

finding-005 names three cases where a director may act directly:

- read-only work, when no entry-point seat holds the needed grant;
- reviving a seat only director can reach;
- when the operator says so in that session.

The first two need no operator grant, but they are still logged. The third
carries the operator's words.

The sweep and the harvest print the trap-door log, so each use is a routing
defect someone sees.

## 7. Re-anchor counter (later, not in the first build)

`standing-hook` adds one line before each answer: `re-anchor: N of the last 20
tool calls were production`. Production means calls the guard would refuse
without a trap door. It informs and never blocks: a count is a
diagnostic, not a gate. The guard does the blocking.

## 8. Readiness before the first order

Director gives a team's entry-point seat its first order only when the fleet
read shows that seat ready. With marvel, that is `duty.first-turn` and
`duty.ack` proven, the same rule `marry` applies before it pairs a seat. A
seat that is not ready gets no order. The sweep lists it as blocked, with the
cell that is missing.

## 9. Tickets

First build, one PR each, in order:

0. **The vehicle**: check whether sideshow can install a Claude Code plugin or
   carry the hooks itself, and pick the package shape of 4a.1. A check and a
   note, no build.
1. **Director's own role definition**: `reference/role.md` shipped and
   installed with director (2.1), with the skill pointing at it.
2. **Profile format and `director init`**: the TOML of 2.3, the minimal
   profile, detection, the start path with the shadowing check (4.2), and
   `--check`.
3. **The session mark and the start-up output**: 4.2 item 2 and 4.3, with the
   fleet read of section 3 (marvel register when marvel is present, bus
   presence always).
4. **The guard with its trap doors and their log**: sections 5 and 6.
5. **Director alone**: a verify script that installs only director into a
   scratch home (no marvel, no tracker, no role catalog, no checkout of the
   fleet that wrote this design), runs `director init`, and proves the
   start-up output, the guard and the trap doors on the minimal profile.

Later, filed when a deployment needs them: model-source adapters (2.4), the
re-anchor counter (7), and other harnesses' guards (10).

Outside this repo, in a user's own profile:

- the profile itself;
- the user's rules text;
- any structural check on the user's team manifests;
- publishing a role catalog or method as a sideshow pack, which is that
  catalog's own release work.

For the fleet that prompted this design, those are tracked where its profile
lives.

## 10. Open questions for the operator

- Whether the guard covers codex and other harnesses in its first build, or
  only Claude Code's hooks. The other harnesses have no `PreToolUse`
  equivalent today.
- Whether one profile per install is enough, or a director serving two
  organizations needs one per workspace.
- The start-up output bound (60 lines is a first guess).

## 11. Prior art

- director `sim/requirements.md`: R-09, R-27, R-95, R-102, R-103, R-109, R-110.
- `_kos/findings/finding-005-workforce-organization-model.md`: Q6 and its
  trap doors.
- `sim/research/supervisor-development/`: the synthesis and its
  recommendations.
- marvel `docs/design/seat-register.md`.
- marvel `docs/design/marry-seat-key-and-classes.md`.
- The skill's Standing checklist (`standing-hook`).
