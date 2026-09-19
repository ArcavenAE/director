# Retrospector state, output, and handoff model: recommendation

Status: PROPOSAL, held for the operator. Nothing committed. wardrobe#11 stays
held and the retrospector role stays not-live until this resolves.
Date: 2026-09-17. Produced by a design party (record in `party-log.md`).
Redaction held: no origin organization, environment token, or infrastructure
repository is named.

## The question

wardrobe#11 (the retrospector role) writes its output OUTSIDE git entirely, to a
physical path `~/.marvel/retro/aae`, guarded by write-restriction machinery. The
operator's read: this scatters retrospectives across the user's systems, will
likely hit harness permission problems, assumes marvel is in use (which
component independence forbids, SOUL section 2), invents a mysterious folder, and
is a lot of machinery just to restrict write. The original intent stands: give
the retrospector a place to gather its notes, keep what state it needs, and do
its work without touching the code. The mechanism needs new thinking.

## The recommendation, in one paragraph

The retrospector holds no private out-of-band state and owns no path outside a
project's own repository. It is advisory and append-only, exactly like the
sibling reviewer role: it writes only its own findings and recommendations,
addressed abstractly as "the record the team reads," an in-repo, per-project
location, never a fixed path, never outside git, never `~/.marvel`. It never
applies its own recommendations; a consuming seat (named per project, not
hardcoded) or a human turns a recommendation into a governed change. The
elaborate write-restriction machinery mostly falls away: keep the codex
trust-strip that protects the code and the library, keep the reserved tools
field, and drop both the `~/.marvel/retro` surface and the `write_surface`
schema field. This supersedes the P3 write-surface-field work.

The party reached this across four independent researchers (retrospection
prior-art, agent-team handoff design, sandbox and security, and component
independence), each web-checking unfamiliar claims. It rests on two pieces of
prior art the platform already owns: the gen-1 retrospector (SLAES), and the
sibling reviewer role.

## What the prior art already settled

The gen-1 retrospector (SLAES), proven across 420-plus PRs, did NOT write outside
git as its primary model. It kept its output IN the repo (a findings log, a
recommendation QUEUE, a checkpoint) and kept only session scratch in a
home-directory tree. It was append-only and advisory: it appended
recommendations to the queue, and a DIFFERENT agent consumed the queue and
applied each entry through a GOVERNED PR, marking it applied. The retrospector
never wrote code, the library, the board, or opened a PR. Its safeguards were a
no-self-modification rule, a visible recommendation audit trail, confidence
scoring, a kill switch, and, after a shared-checkout contamination incident, a
rule that it wrote only to its own designated output files and never touched
shared git state.

The sibling reviewer role (wardrobe) is output-only and names its store
abstractly: "its review record, in the store the team reads." It bakes in no
physical path and no marvel dependency, and it achieves output-only with no
special schema field, only its prose write domain and its CANNOT list.

wardrobe#11 departed from both: it hardcoded a physical outside-git path and
built machinery to defend it. The redesign returns to what the prior art already
proved.

## The model, per tier

The write shape is identical across all three tiers (the reviewer's abstract
store). Only the consumer and the amount of governance between a recommendation
and the change it describes differ.

| Tier | What it reflects on | Where the reflection goes | Consumer | Governance |
|---|---|---|---|---|
| 1 immediate | code and PR vs SPEC | the governed surface that already exists (a note on the PR or the story record), inventing no new store where one is already there | the reviewer or dev seat already on that PR | the PR's own review and merge |
| 2 second | SPEC vs PRD and architecture | an append-only recommendation record in the project's own in-repo store | the spec or architecture owning seat, or a human | a governed change (a PR against the spec, an architecture note) |
| 3 step back | root cause at the CLAUDE/AGENT.md and SOUL level | the same record, tagged cross-cutting, routed to a human | a human only; doctrine change is a judgment call, never auto-applied | a human edit, optionally aggregated across projects by a separate, optional consumer |

The consuming seat is named per project, never hardcoded to one agent. When the
team runs bare (no marvel, no consumer present), the retrospector still emits its
record; a consumer is not assumed to exist. Bare mode is the only mode the
retrospector's own code has to satisfy, which is what keeps it inside SOUL
section 2.

## No private state file

The retrospector does not keep a checkpoint or a private working directory. To
answer "where did I leave off" and "have I already flagged this," it reads the
tail of its own record; each entry names what it was written against (a commit, a
spec version, a prior recommendation id), so the record is its own resume point.
A second, untracked source of truth is exactly the surface that produced the
shared-checkout contamination the gen-1 system hit, so removing it is a safety
gain, not only a simplification.

If a concrete scale case ever shows the record is too large or slow to re-read
each run, the fallback is a gitignored in-repo scratch file (the same convention
Aider uses for its in-repo session artifacts), and only if a genuine
cross-project home is ever forced, the standard shape is XDG_STATE_HOME
(`~/.local/state`), never a marvel-specific path. Neither is part of the default
design; both are named so a future need does not reinvent `~/.marvel`.

## Cross-project tier 3: ruled against a store the retrospector owns

The one candidate the party rejected outright is a single cross-project place the
retrospector writes to by path. Any ambient fleet-wide location the retrospector
must resolve at runtime is the same shape of dependency as `~/.marvel`, whatever
it is named; the retrospector would still need one physical answer to "where is
the fleet-wide store," and providing that answer is component conscription. So
tier 3 emits a tagged cross-cutting recommendation and hands off. Whatever
aggregates tier-3 findings across projects (a human copying notes, a chosen
repository, a knowledge graph) sits outside the retrospector and is optional by
construction. The retrospector's contract ends at "here is a pattern that recurs
above this project's scope." Scattering becomes a real problem only when the
retrospector itself picks the destination; if it emits and hands off, the choice
belongs to a human or a separate aggregator, where it should.

## Disposition of the write-restriction machinery

| Piece | Disposition | Reason |
|---|---|---|
| codex CODEX_HOME trust-strip (a config home that trusts nothing) | KEEP | A documented, real bypass: codex project trust is a config-file fact that can override the `-s` sandbox flag on a trusted project, widening what the harness executes. That threatens the code and the library regardless of where the retrospector's output lands. Cost: pin the codex version and re-verify on each upgrade, since the trust vocabulary is still moving (codex retired an approval-policy value recently). |
| reserved `tools` field (ruling 81) | KEEP | A one-line schema constraint, orthogonal to where output lands, cheap either way. |
| `~/.marvel/retro` physical surface | DROP | Once output is append-only records in an in-repo, per-project store, there is no reason for a path outside git, and it conflicts with SOUL section 2. |
| `write_surface` schema field (the P3 work) | DROP | The reviewer role proves a prose write domain plus a CANNOT list is enough for an output-only advisory role. Once the write target is an ordinary in-repo directory, the harness's own default working-directory scoping gives the enforcement a declared field never could. A schema field that states an intent the harness does not read off it is documentation dressed as a control. |

The asymmetry that decides this: a misdirected write INSIDE git is a revertible
mistake; a trust-bypassed write to the code or the library is not. That is why
the trust-strip earns its keep as a harness-level control protecting the code,
while the `write_surface` field does not survive as a schema-level control
protecting the retrospector's own output location.

The declaration-versus-enforcement gap still holds in the abstract (no schema
field enforces anything, ever), but the stakes drop a tier once the target is a
plain in-repo directory, which is why the field is no longer worth its cost.

## What this implies for wardrobe#11 and the P3 work (held)

These are proposed changes, not applied. wardrobe#11 stays held.

1. wardrobe#11, section 3 (write domain): rewrite to the reviewer pattern. Name
   the store abstractly ("its own append-only findings and recommendation
   record, in the store the team reads, an in-repo per-project location"). Remove
   the physical `~/.marvel/retro/aae` path and the codex workspace-write
   pinned-cwd prose that existed to defend an outside-git surface. Keep the
   codex-trust caveat, but reframed as protection for the code and the library
   (the real exposure), not as enforcement of the output location.
2. wardrobe#11 frontmatter: revert the `write_surface` encoding and the major
   version bump that was tied to it. With the policy field gone, the border no
   longer changes for that reason; re-version as an ordinary proposal iteration.
   The role stays status proposal, not-live.
3. wardrobe#12 (the P3 field PR): drop the `write_surface` schema field, its
   contract prose, and its validator change. Keep the independently-useful
   tools:null projection documentation note, which stands on its own. #12 shrinks
   to that note.
4. P3=(i) is superseded. The dedicated write-surface schema field is retired
   before landing, which is the outcome the operator flagged as expected. The kos
   node question-role-tools-field-projection and the probe aae-orc-ofs4l stay
   valid; they are about what the tools field projects to, independent of the
   retired write_surface field.
5. permission_floor: the retrospector reads everything and writes only its own
   in-repo record, the same posture as the reviewer. Set its floor to match the
   reviewer's, not a write floor tied to an external surface.

## The one sub-decision to confirm with the operator

Tracked or gitignored for the retrospector's own record. The panel split, and the
tradeoff is real:

- TRACKED (the gen-1 choice) makes the record a visible, diffable audit trail and
  a handoff a consuming seat reads across sessions and checkouts. This is the
  transparency safeguard the gen-1 system relied on.
- GITIGNORED (the operator's candidate ii, and the Aider precedent) keeps the
  raw record as the retrospector's proposals, pre-promotion, out of committed
  history, with a human promoting an entry into a tracked decision when it earns
  it (SOUL section 8: compute and surface, promotion is a human act).

My recommendation is to TRACK the tier-2 and tier-3 recommendation record,
because its purpose is to be read, consumed, and audited, and a gitignored record
does not survive a fresh checkout for a consuming seat; and to keep tier 1 on the
existing governed surface (the PR or story), which is already tracked. A team
that wants the raw record private may gitignore it as a local choice, relying on
promotion-only tracking. Either way, no separate private scratch file exists.
This is the one point where I would want the operator's call rather than assume
it.

## What stays true

- The retrospector stays advisory, never blocking (SOUL section 8). It proposes;
  a consuming seat or a human decides and applies.
- The separation of powers from the gen-1 system (the retrospector appends, a
  different seat applies through governed change) is the part most worth keeping
  whole, and the part least tied to marvel or any harness.
- The role works with no marvel and no wardrobe runtime, and depends on no
  `~/.marvel` directory.
