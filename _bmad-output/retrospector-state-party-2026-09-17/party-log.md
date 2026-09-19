# Party log: retrospector state and write-surface model (2026-09-17)

Subject: re-think the wardrobe retrospector role's state, output, and handoff
model. wardrobe#11 writes outside git to `~/.marvel/retro/aae` with
write-restriction machinery; the operator wants the mechanism reconsidered
against component independence (SOUL section 2), harness permission reality, and
the original intent (a place to gather notes and keep state without touching the
code, possibly one place across projects). Redaction held throughout.

## Study first

I read the gen-1 retrospector (SLAES) in multiclaude-enhancements before casting,
as the commission required. The load-bearing discovery: gen-1 did NOT write
outside git as its main model. It kept its findings, its recommendation QUEUE,
and its checkpoint IN the repo, and kept only session scratch in a home-directory
tree. It was append-only and advisory; a different agent consumed the queue and
applied each recommendation through a governed PR. I also read the sibling
reviewer role, which is output-only and names its store abstractly ("the store
the team reads") with no physical path and no special schema field. Both point
the same way, and both predate wardrobe#11's outside-git departure.

## Cast

Four seats, each a real agent that web-checked unfamiliar claims:

- Sage: retrospection and prior-art harvest.
- Ravi: agent-team and hand-off design.
- Sable: sandbox and security, and the disposition of the write-restriction
  machinery.
- Isolde: component independence and the spec-to-architecture flow.

I held the role-library and P3-field disposition as orchestrator, since that is
the work I was mid-flight on.

## Where the room agreed at once

All four converged on the core without argument:

- Drop the outside-git `~/.marvel/retro/aae` surface. It scatters output, assumes
  marvel, and conflicts with component independence.
- Keep the retrospector advisory and append-only. It never applies its own
  recommendations; a consuming seat or a human lands the governed change. This is
  gen-1's separation of powers, and it is the part least tied to marvel.
- Name the store abstractly, the reviewer's pattern, never a fixed path.
- Work bare: no marvel, no wardrobe runtime, no `~/.marvel` dependency.

## The sharp findings

Sage grounded the homes: tiers 1 and 2 output tracked and in-repo (the gen-1
shape, diffable where the code lives); the outside-git home dropped; and, if a
cross-project home is ever forced, the standard shape is XDG_STATE_HOME, never an
invented path. Aider's convention (gitignored in-repo session files) is the
precedent for in-repo scratch if any is needed. Sage went further and questioned
whether private scratch needs to exist at all, since the tracked record answers
"have I flagged this" on its own.

Ravi answered write-versus-emit: the retrospector should emit, not maintain
private durable state. Tier 1 writes into the governed surface that already
exists (a PR or story note). Tier 2 uses an append-only recommendation record a
consuming seat applies. Tier 3 uses the same record, tagged for a human. Resume
does not force a state file; the tail of the record, each entry noting what it
was written against, is the resume point. One model across all tiers; only the
consumer and the governance weight change.

Isolde ruled on the cross-project question and this was the party's clearest
call: no cross-project place the retrospector owns. A single ambient location it
resolves by path is the same dependency shape as `~/.marvel`, whatever it is
named, so tier 3 emits and hands off, and cross-project aggregation is a
separate, optional consumer's job. Bare mode is not a degraded fallback; it is
the only mode the retrospector's code must support.

Sable disposed of the machinery with web-verified codex facts: keep the
CODEX_HOME trust-strip (a documented bypass where project trust overrides the
`-s` sandbox flag, threatening the code and the library, with a version pin as
the cost), keep the reserved tools field, drop the `~/.marvel/retro` surface, and
drop the `write_surface` schema field. The reviewer role already proves a prose
declaration plus a CANNOT list is enough for an output-only role, and the
harness's own working-directory scoping does the enforcement a declared field
cannot. The asymmetry that decides it: a misdirected write inside git is
revertible, a trust-bypassed write to the code or the library is not.

## The one open sub-decision

Tracked or gitignored for the retrospector's own record. Sage and Sable leaned
tracked (audit trail, cross-session handoff); Isolde and the operator's candidate
list leaned gitignored (proposals, pre-promotion, promotion is a human act). I
recommend tracking the tier-2 and tier-3 record and keeping tier 1 on the
existing governed surface, with a team free to gitignore the raw record as a
local choice. This is the one point I flagged for the operator rather than
settle.

## Convergence

Unanimous on the model: the retrospector holds no private out-of-band state, owns
no path outside a project's repo, writes only its own append-only record
addressed abstractly, and hands governed change to a consuming seat or a human.
The write-restriction machinery mostly falls away: keep the trust-strip and the
reserved tools field, drop the `~/.marvel/retro` surface and the `write_surface`
schema field. This supersedes the P3 write-surface-field work, which the operator
had already flagged as an expected outcome. The full model, the per-tier table,
the machinery disposition, and the implied revisions to wardrobe#11, #12, and the
P3 field are in `recommendation.md`.

## Sources the panel cited

The XDG Base Directory Specification (XDG_STATE_HOME as the standard cross-project
state home), Aider's coding-conventions and gitignored session-artifact
convention, the codex advanced-configuration docs (CODEX_HOME and project trust),
a codex configuration guide on the trust hierarchy, a note on codex retiring an
approval-policy value, and the Claude Code permissions docs (default
working-directory write scoping). Every load-bearing external claim is anchored
to one of these.
