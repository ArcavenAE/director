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

## 1. Three layers, kept apart

Director must work for people who organize work entirely differently: with or
without marvel, with or without any role catalog, and with nothing from the
fleet that wrote this design. A user may have only director. So the design separates three layers:

| Layer | Owner | Holds |
|---|---|---|
| **Director core** | this repo | Director's own boundary (custody, not production; R-102), the bootstrap sequence, the hook and guard mechanisms, and the trap-door log. Says nothing about how teams are shaped. |
| **Operating profile** | the user, in their own repo or state directory | How this user organizes work: what a team is, which role is a team's entry point, the rules a team must satisfy, where roles are defined, where handoffs live. One profile per install. |
| **Fleet sources** | adapters | Where director reads who exists and how to reach them: marvel's seat register when marvel is present, director bus presence always, or nothing. |

Director core never names a role, a role catalog or a team shape. A rule like
"every team has a supervisor" is profile content. Director core enforces only
two things about teams: the profile names an entry point for each team, and
director routes into a team only through that entry point. That is director's
own boundary, defined here and in the skill: director is not the supervisor of
the sessions it serves, and its verbs are its own (R-102).

## 2. The operating profile

A TOML file, `$DIRECTOR_STATE/operating-profile.toml`, or a path named by
`DIRECTOR_PROFILE`. Fields (first build):

```toml
name = "example"
# where director reads seats; tried in order, every one that answers is used
fleet_sources = ["marvel-register", "bus-presence"]
# the role that is a team's entry point; director routes only to it
entry_role = "supervisor"
# rules a team must satisfy, checked against the fleet read (section 5)
[[team_rule]]
id = "has-entry"
require = "role:entry_role >= 1"
[[team_rule]]
id = "reviewer-with-pr-roles"
require = "role:reviewer >= 1 when any role has capability:opens-pr"
# prose the start-up hook prints verbatim: the user's own rulings, with sources
rules_text = "path/to/rules.md"
# where roles are defined, if anywhere; printed, never parsed by core
role_catalog = "none"
# where the predecessor's handoff is written and read
handoff = "$DIRECTOR_STATE/handoff.md"
```

With no profile, director runs with a built-in minimal one:

- `fleet_sources = ["bus-presence"]`;
- `entry_role` unset, so routing into a team is refused until the user names
  one;
- no team rules.

The start-up hook says that this is the minimal profile. The rule-expression
grammar is deliberately small and is settled in the profile ticket (9.1).

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

1. **Profile format and loader**: the TOML shape, the minimal built-in profile,
   the rule grammar, and `director-profile check`.
2. **Fleet-source adapters**: marvel-register and bus-presence, merged into
   one read.
3. **Start-up hook and launcher wiring**: section 4. Needs 1 and 2.
4. **Guard**: section 5. Needs 1 and 2.
5. **Trap doors and their log**, printed by the sweep and the harvest:
   section 6. Needs 4.
6. **Re-anchor counter in `standing-hook`**: section 7. Needs 4.
7. **Director alone**: a verify script that installs only director into a
   scratch home (no marvel, no tracker, no role catalog, no checkout of the
   fleet that wrote this design) and proves the start-up hook, the guard and
   the trap doors work on the minimal built-in profile. Needs 3, 4 and 5.

Outside this repo, in a user's own profile:

- the profile itself;
- the user's rules text;
- any structural check on the user's team manifests.

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
