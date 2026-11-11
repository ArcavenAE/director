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

### 2.4 What an adapter answers

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

## 4. Start-up: a director session starts only through `director-session`

`director-session` (the existing launcher) also sets `DIRECTOR_ROLE=director`
and loads director's own settings file. That file wires the hooks in sections
4 to 7, so a director session cannot run without them. A session started any
other way has no director role, and the guard treats it as an ordinary session.

The `SessionStart` hook prints into context, not as a pointer to read:

1. The profile's name and its `rules_text`, verbatim, then each model source
   with its scope, the version the profile names and the version sideshow has
   active, marking any mismatch or missing pack.
2. The fleet read (section 3) per cluster: each team, its entry-point seat, and
   that seat's readiness.
3. Each team that fails a profile rule, by rule id.
4. When no team is up: `no team is up: your first act is to stand one up,
   entry role first`.
5. Open asks from the ask ledger (`dws asks`), oldest first.
6. The predecessor's handoff, or `no handoff: write one before you end`.

The output is bounded: at most 60 lines, with the remainder counted rather than
cut silently.

## 5. The guard: a `PreToolUse` hook in director sessions

In a session with `DIRECTOR_ROLE=director`, the guard refuses:

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

## 7. Re-anchor counter

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

Each is one PR.

1. **Profile format and loader**: the TOML shape of 2.3, scope precedence,
   the minimal built-in profile, the rule grammar, and `director-profile check`.
2. **Fleet-source adapters**: marvel-register and bus-presence, merged into
   one read.
3. **Start-up hook and launcher wiring**: section 4. Needs 1 and 2.
4. **Guard**: section 5. Needs 1 and 2.
5. **Trap doors and their log**, printed by the sweep and the harvest:
   section 6. Needs 4.
6. **Re-anchor counter in `standing-hook`**: section 7. Needs 4.
7. **Director's own role definition**: `reference/role.md` shipped and
   installed with director (2.1), with the skill pointing at it.
8. **Model-source adapters**: the four questions of 2.4 for a role catalog,
   BMAD and vsdd-factory, reading versions from sideshow, with `unknown`,
   unread and mismatch states. Needs 1.
9. **Director alone**: a verify script that installs only director into a
   scratch home (no marvel, no tracker, no role catalog, no checkout of the
   fleet that wrote this design) and proves the start-up hook, the guard and
   the trap doors work on the minimal built-in profile. Needs 3, 4, 5 and 7.

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
