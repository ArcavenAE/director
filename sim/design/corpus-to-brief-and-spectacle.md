# director's corpus, to a product brief, a PRD and spectacle's documents

Status: a design party's recommendation, 2026-10-13. Six seats; every item was put to a forced vote, and no split was left open. It ratifies nothing. Owner: the architect role. Docs only.

- **Commission.** The operator asked how to analyze and organize the director materials (the requirements register, the kos nodes and the rest), how to bootstrap them into a bmad product brief and PRD, and how they map onto the document types spectacle is built for.
- **Pins:** director 1c6ec09, spectacle e8958c9.

## 1. Why

director has 204 numbered requirements, 29 candidate designs, 27 findings and a simulation record, but no product brief and no PRD. The tools to draw a brief from a project's own records already exist in spectacle and have run once, on marvel (marvel#526). Run as built on director, they would cite duplicate requirement rows and could not see the designs where the operator's newest rulings live. This doc says what to fix first, in what order, and how to keep a generated brief faithful to its sources.

## 2. Terms

- **R entry:** one numbered requirement in `sim/requirements.md`.
- **Source class**, in the register's own words (`sim/requirements.md:30-43`):
  - **OBSERVED:** a failure happened and was recorded, or a fact was measured.
  - **JUDGMENT:** the wizard concluded this.
  - **RULED:** the operator decided it.
  - **FORWARD:** the operator committed to build it.
- **The document shapes**, from ISO/IEC/IEEE 29148 unless noted:
  - **BRS:** business requirements specification, what a bmad product brief becomes in spectacle's shape.
  - **StRS:** stakeholder requirements specification, what a bmad PRD becomes.
  - **SRS:** software requirements specification.
  - **OpsCon:** operational concept.
  - **AD:** architecture description (ISO/IEC/IEEE 42010).
  - **ADR:** architecture decision record.
- **The chain:** spectacle's tools, in order.
  - `gather` builds the evidence index, the only sources a draft may cite.
  - `reverse-brief` drafts a BRS from the index.
  - `check` verifies that every claim has a class and a source that resolves. It does not verify support (`spectacle:scripts/check:31-32`).
  - `reverse-prd` drafts an StRS from a ratified BRS.
  - `req-drift` reports sources that moved under a ratified document.
- **Support:** the cited source says what the claim says. **Resolves:** the source exists and opens.
- **C- line:** a claim in a draft. **Q- line:** an open question in a draft.
- **EV id:** an id in a new public evidence ledger, for evidence the register cites from the gitignored `sim/notes/`.
- **Discovery:** bmad's elicitation session with the operator.

## 3. What was measured

- **The register:** 204 entries, R-01 to R-204, with no gaps and no duplicates.
  - Two entry formats: 95 older entries carry a `*Source:*` line, and 109 newer ones carry the class inline.
  - Counted by the first class on each entry: OBSERVED 129, JUDGMENT 49, RULED 22, FORWARD 4.
  - From the harvest diffs on, the file is an append log.
- **gather, run unchanged on 1c6ec09,** writes 262 requirement rows for those 204 ids, and 42 ids appear twice or more. Bullets in the harvest diffs also match its pattern (`spectacle:scripts/gather:101`).
  - It writes no rows for `sim/design/`, `sim/specs/` or `docs/`.
  - `check` cannot catch the duplicates: it needs a draft (`--brief`), and it keeps the last of two rows that share an id without a word (`spectacle:scripts/check:143-148`).
- **Evidence is not always public.** In one panel seat's reading, not yet re-checked by a second, only 7 of the 20 entries the corpus-fitness probe samples (`_kos/probes/brief-sim-corpus-fitness.yaml`) point at a tracked file, finding, ticket or PR. The other 13:
  - 6 point only at the gitignored notes;
  - 2 name a ruling date with no record;
  - 4 cite a session ("this session");
  - 1 has no Earned-by line.

  Git history anchors 3 of the 4 session-relative ones to a dated harvest commit. Read alone, a 20-entry sample only rejects the probe's "2 or fewer notes-only" line; it cannot estimate a rate.
- **The register cites 7 of the 29 designs, and none of the newest.** It has no entry dated 2026-10-10. The nine rulings of that day are in the ruling table of `director-functions-redesign.md` (lines 239 to 247), which merged in #276.
- **Some of director is built.** 15 test files under `cmd/director-mcp` name register ids. Whether their R-1, R-3 and R-6 mean the register's R-01, R-03 and R-06 is unchecked. The supervisor application is not built.

## 4. Part 1: analyze and organize the corpus

1. **Pin one director commit for a run.** Record it in the run record and the index header. The projection below records `instances_at: <sha>`, and req-drift carries what lands later.
2. **The register projection** (`register-projection.md`):
   - written by one parser, never by hand, with one row per R id (204 rows, class totals 129, 49, 22 and 4);
   - both entry formats parse into one shape;
   - the register itself is not edited.

   Its mechanical columns: class, title, line, topic letter, Earned-by tokens, inline Instances and Cross-refs tokens, `Design:` targets, amendment dates, edges (amends, extends, supersedes, tracked by), the harvest-diff instance count, and the evidence locus from item 4.
3. **The judged columns,** in a separate table joined on R id. The parser writes none of them. 6-0.
   - **Admission bucket**, by the ruled test "would a working director fix this?" (`skills/director/SKILL.md:219-232`). Four values:
     - **product mechanism;**
     - **role contract:** binds whoever holds the director seat, which the shipped skill implements today. Counted as product;
     - **practice:** still true with director working, so it goes to the platform graph;
     - **environment fact:** a dated harness or network property.
   - **Shape:** requirement, or decision. A decision becomes an ADR.
   - **Layer:** transport (the message bus, where the shim and its tests exist) or application (not built).
   - **Level:** L1 goal, L2 invariant or L3 contract candidate. A practice row takes no level. 6-0.
4. **The evidence locus, for all 204 entries,** computed by the parser from an id grammar committed first. The grammar covers the observation and friction id forms the register uses, and lists the session-relative phrases. Each entry falls in one locus:
   - tracked;
   - notes-only;
   - ruling with no record named;
   - session-relative, anchored by its introducing commit (`git log -S`, not blame) or a dated section header;
   - none.
5. **The evidence ledger** (`evidence-ledger.md`, in this repository) and a crosswalk to the native note ids (gitignored, in `sim/notes/`).
   - One parser allocates EV ids: dense, in first-citation order, append-only, never reused.
   - A row is publishable, a rewrite, or withheld. A withheld row carries only its id, date, R ids and the word `withheld`.
   - Every row passes a disclosure scan before commit.
   - The probe's 20 entries get rows first, then every notes-only entry in a product bucket, all before `reverse-brief` runs. 6-0.
6. **Index the designs, specs, docs and `_bmad-output/`.**
   - Each design's Status line maps, through a small judged table, to the highest class a claim citing it may carry.
   - A design section is cited as RULED only when its ruling has a record outside the design (a merged PR, or a RULED register entry), confirmed by one command. Otherwise it is cited as JUDGMENT.
   - A design that surfaces options and decides nothing is never cited as a requirement.
   - The keyring migration plan in `_bmad-output/` is about other repositories and is left out.
   - The 2026-10-10 rulings enter through the redesign's ruling table, one row per ruling, cited as RULED, with #276 as the record. 6-0.

## 5. Part 2: bootstrap into bmad artifacts

1. **Order:** the brief (as a BRS), then the PRD (as an StRS), then the architecture (as an AD), with an SRS per built slice. Epics are bd tickets, filed from ratified StRS rows by whoever owns the work, not bmad markdown epics.
2. **"Not built" does not block a run; it changes what the run produces.**
   - An OBSERVED claim says it observes a hand-played simulation.
   - Every row starts `proposed`.
   - Verification properties are FORWARD for the application layer.
   - For entries the shim's tests exercise, the factory's brownfield pass drafts contracts and properties from those tests. 6-0.
3. **bmad drives the process, and spectacle owns the shape.** bmad adds five things. 6-0.
   - **Discovery:** one operator session on the framing questions, about product rows only: who the users are, the value, who pays the cost today, and the constraints. Answers enter as RULED claims, with the operator's words verbatim and dated.
   - **A concern scan:** for concerns no template section covers.
   - **Input reconciliation:** sources that have no claim.
   - **A verdict-first review** in `brs-review.md`: plain text on every row, with one need clustering several R entries, so the operator never accepts 204 rows in bulk.
   - **The addendum:** the register's mechanism detail waits there as input to the AD.

   bmad's readiness check runs before the first built slice, not in the first run.
4. **What earns a claim.**
   - A notes-only entry enters as a Q- line by default.
   - An EV row earns a C- line only when all three hold:
     - it is publishable;
     - a seat other than the rewriter has compared it with the native note, marked it `supports`, and recorded its label and the date;
     - its kind is failure or measurement. 5-1; one seat would add friction.
   - A ruling enters only in the operator's own words. In Discovery the operator may re-confirm an unrecorded RULED entry one entry at a time, never as a list, recorded verbatim and dated in `brs-review.md`. 6-0.
5. **The support audit,** beside `check`, fixed before the run.
   - **Sample:** up to 20 claims per class, at fixed positions.
   - **Scoring:** a seat that did not draft scores each claim against its cited span as supports, partial, stretched, does-not-support or unreadable. A second scorer takes a quarter, and disagreements are listed, not averaged. A model may score only after it matches the human labels on that quarter.
   - **Positive control:** three planted, stretched claims per scorer. A scorer who catches two or fewer has their labels voided, and the catch rate is reported.
   - **Two hops:** the audit reads claim to R entry, and R entry to its evidence.
   - **Stop rule:** the draft goes back to drafting, not to the operator, if more than 4 of 20 sampled claims are stretched or does-not-support, or if RULED recall is under 100%. RULED recall counts every RULED entry, plus every ruling row in a ruled design whose record is a merged PR; each is either in the draft or excluded with a reason.
   - **Reporting:** the evidence-locus rate is reported beside the result.
   - **Scope:** all of this is diagnostic for that run only. It gates no merge, and no number in it becomes a standing threshold.

## 6. Part 3: map onto spectacle's documents

| spectacle document | made from | when |
|---|---|---|
| BRS (`docs/spec/29148/brs.md`, `evidence-index.md`, `brs-review.md`) | gather and `reverse-brief`, then Discovery and the operator's review | run 1 |
| StRS (`strs.md`) | `reverse-prd`, after the BRS is ratified. The eight-column row is unchanged (spectacle's `strs-check` enforces it): one stakeholder need per row, with the trace back to its R entries | run 2 |
| Environment-fact rows | BRS 7.2 Assumptions and StRS 3.3 Constraints, each with the harness version and date it held for | with each |
| OpsCon | written by hand from `docs/use-cases.md` UC1 to UC6, which gather indexes as DOC rows. Optional, after the BRS is ratified; gates nothing (6-0) | optional |
| Domain invariants | the projection filtered on level L2, joined to the StRS rows that cite it | with the StRS |
| ADRs | projection rows whose shape is "decision" | when filed |
| AD, then SRS | `spectacle.ad` with StRS rows as concerns; `spectacle.srs` for the shim and the first built slice | after the StRS |
| SyRS, SDD | once director spans several processes; per component once code exists | later |

The vsdd factory takes the BRS as its first-level input only after the operator ratifies it. Behavioral contracts for test-backed entries come from the brownfield pass over the shim's tests. The rest come from the factory's greenfield pass, from StRS rows whose traced entries are level L3.

## 7. Order of work, and the gate

1. **Pin the commit. Then, in parallel:**
   - the id grammar;
   - the projection with the evidence locus;
   - the gather fix in spectacle. The fix: requirement rows from definition lines only; design, spec, doc and EV row kinds; and the class ceiling.
2. **The judged admission columns,** on the projection.
3. **Then, in parallel:**
   - the framing questions (product rows only);
   - the ledger rows for the probe's 20, then the other product rows.
4. **The probe's finding,** with the locus table and a go or no-go.
5. **The gate before `reverse-brief`:**
   - the probe says go;
   - the gather fix is merged;
   - one recorded command on gather's output at the pinned commit shows exactly 204 requirement rows, none repeated, and one row per tracked file under `sim/design/`, `sim/specs/` and `docs/`. 6-0.

   All three are structural or a written finding. None is a health metric.
6. **Discovery** with the operator, either side of the gate, done before `brs-review.md` is filled.

## 8. Tickets (a recommendation; filing is the supervisor's)

- **A spectacle ticket for the gather fix.**
- **The probe run.**
- **One flat ticket for the director BRS run,** blocked by the probe finding and the gather fix. The StRS ticket is filed once the BRS is ratified, blocked then by marvel's StRS run (aae-orc-j0ori). 5-1. The other seat would file both now, since the StRS run has its own PR and a blocker known today.

## 9. Dissent on record

- **Acceptance is not support** (evals seat). The audit, the positive control and the verbatim rule are the only parts of this design that measure whether a brief says what its sources say. Dropping any of them as overhead would let RULED claims carry the operator's authority without the operator's words.
- **Role contract as product was carried inside the whole, never voted alone** (distributed-systems seat). If a later harvest routes those rows to practice, the entries that record how the director seat was held (R-03, R-35, R-102, R-131, R-175) leave director's specification.
- **A gate must prove it ran on the artifact in hand** (corpus and factory seats). This is why the gate counts gather's output at the pin rather than trusting the fix's own tests.
- **Recall that cannot see designs would reward a draft for ignoring the newest rulings** (gen-1 seat).

## 10. Corrections to inputs

- **ticket aae-orc-k0l75 states the admission test inverted** against the ruled reading in `skills/director/SKILL.md:219-232`. A note on the ticket fixes it.
- **The register has no entries for the 2026-10-10 rulings.** They live in the redesign (section 3).

Each recommendation here is valid until 2026-10-27; the architect role re-checks it then. Nothing executes on silence.
