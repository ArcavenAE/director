# finding-director-7oiu: the register's evidence is public for about a quarter of its entries; GO for the BRS run

The corpus-fitness probe (`_kos/probes/brief-sim-corpus-fitness.yaml`), run as section 7 steps 1 to 4 of the design merged in director#288 (under `sim/design/`). The question: is the requirements register fit input for the reverse brief, given that some of its evidence lives only in the gitignored `sim/notes/`? The answer gates the director BRS run.

## Answer

**GO** under `sim/research/corpus-fitness/go-rule.md`, written before the judged column was filled: every StRS section the PRD needs has at least one tracked source that resolves at the pin. Section 2 (who holds a stake) is thin, and 107 of the 230 entries cite only notes. Both go on the clean-rewrite list, and neither empties a section. The hypothesis is **not disconfirmed**, reported separately below. The brief's success signal is met except for the class versus audit-theme diff, which stays owed as aae-orc-xxyag.

## Pin

- **This run:** director `62b55fc`. The register holds R-01 to R-230, one definition line each, no duplicates.
- **The design measured** 204 entries at `1c6ec09`. The 26 entries added since (R-205 to R-230) are in the rates below. All 20 sampled entries predate them, so the sample is the same set the design read.

## The 20-entry sample

Fixed in advance by the brief: every 9th id from R-05. Ids, counts and loci only; the parser's full row for each is in `sim/research/corpus-fitness/projection.tsv`.

| locus | count | entries |
|---|---|---|
| tracked | 7 | R-05, R-14, R-41, R-95, R-131, R-140, R-167 |
| notes-only | 6 | R-23, R-113, R-122, R-149, R-158, R-176 |
| session-relative | 4 | R-50, R-59, R-68, R-77 |
| ruling with no record | 2 | R-32, R-86 |
| none (no Earned-by line) | 1 | R-104 |

This is the split the design's single reader reported (7, 6, 4, 2, 1). A parser has now reproduced it from a grammar committed before the parser existed.

**R-77, both readings.** Its Earned-by line reads "nats-server.conf and bus.go read this sitting", followed by two items with no file token.

- **Tracked:** an early draft of the parser counted it as tracked, because it matched the bare name `bus.go` against `cmd/director-mcp/bus.go`, and the bare conf name against one of two tracked files of that name.
- **Session-relative, which counts:** neither token resolves as written. There is no `bus.go` at the root or beside the register, and the conf name does not say which of the two it means. The grammar now resolves a file token only as written (`README.md`, Loci), and a test holds that rule.

All four session-relative entries in the sample are anchored to the commit that introduced them: R-50, R-59 and R-68 to `d8399e2` (2026-09-12), and R-77 to `2a84ce4` (2026-09-12).

## All 230 entries

| locus | count | share |
|---|---|---|
| tracked | 64 | 28% |
| notes-only | 107 | 47% |
| ruling with no record | 9 | 4% |
| session-relative | 19 | 8% |
| unresolved | 21 | 9% |
| none (no Earned-by line) | 10 | 4% |

- **none:** R-96 to R-105. These entries carry their evidence in prose and have no Earned-by line.
- **unresolved, 21:** R-42, R-52, R-53, R-55, R-60, R-61, R-62, R-67, R-78, R-79, R-81, R-82, R-106, R-107, R-109, R-141, R-145, R-146, R-148, R-161, R-184. Each has an Earned-by line the grammar does not recognize: files in another repository, harness versions, incident ids from the predecessor system, design reasoning, or cross-references to other R entries. The grammar is unfinished for these entries, and this table is not complete while they stand. Extending the grammar shrank the list from 33 to 21 during this run. The remaining forms need a decision about what counts as evidence, not a pattern.

## Admission, for the 20

From the research team's reading (`sim/research/corpus-fitness/judged.md`, one reader), by the ruled test "would a working director fix this?":

- product 19 of 20 (product mechanism 17, role contract 2), practice 1 (R-68), environment fact 0;
- requirement 16, decision 4;
- L1 4, L2 11, L3 4, no level 1;
- for the product rows, transport 11 and application 8.

The admission split does not widen the scope beyond director for this sample (brief question 4): one entry goes to practice.

**Second-reader candidates:** R-68 (practice or product), R-122 (judged product, with an environment-fact half), R-176 (role contract or practice).

## Evidence ledger

`sim/evidence-ledger.md` holds the first 9 EV rows, one per notes citation, from the 6 notes-only entries in the sample. Every row is withheld. The disposition labels are 3 publishable, 4 rewrite (the notes name private or third-party work) and 2 withheld. The crosswalk to native note ids is outside this repository.

## StRS section map

Sections are given by number in the StRS outline the reverse-PRD step fills. Their published ISO/IEC/IEEE 29148 clause names could not be checked from this seat, so no section is named here. The middle column says what each section needs, in this finding's own words. Tracked sources at director `62b55fc` only.

| section | what it needs | tracked sources at the pin |
|---|---|---|
| 1.1 | why the system exists | `README.md` ("Why it exists"); `docs/architecture.md` (system overview) |
| 1.2 | where it stops | `README.md` ("Not made redundant by any one harness's remote control"); `docs/architecture.md` |
| 1.3 | the documents it cites | the tracked documents themselves |
| 1.4 | its terms | `sim/requirements.md` (Source class); the #288 design, section 2 |
| 2 | who holds a stake | the operator only, in `docs/use-cases.md` UC1 to UC6 and `docs/architecture.md`. No other stakeholder class, value statement or "who pays the cost today" (brief question 6). Thin |
| 3.1 | the needs themselves, grouped | the 64 tracked register entries |
| 3.2 | how it is run day to day | `docs/use-cases.md` UC1 to UC6 |
| 3.3 | limits the stakeholders set | tracked RULED entries; the sample alone has R-05, R-140 and R-167 |
| 4 | why each need exists | the designs under `sim/design/`, and the tracked Earned-by lines |
| 5.1 | the trace back to the BRS | none: no BRS exists at the pin |
| 5.2 | its terms, collected | as 1.4 |

## Go or no-go

- **The needed set** is every section except 5.1. 5.1 traces to the BRS, which is what the gated run produces, so requiring it would make every first run NO-GO by construction.
- **Every needed section has a tracked source that resolves at the pin**, so the verdict is GO.
- **Section 2 is thin, not empty.** It has a tracked source (the operator in UC1 to UC6), and the go rule counts a section only as empty or not.
- A withheld evidence-ledger row counted for no section.

## Hypothesis verdict (reported separately)

The brief's hypothesis is disconfirmed only if no more than 2 of the 20 sampled entries depend only on notes. Six do, so the hypothesis is **not disconfirmed**. Across all 230 the notes-only share is 47%, above the brief's 25% base rate. Two causes contribute: the register grew from 169 Earned-by lines at `165900f` to 220 at this pin, and the grammar now recognizes slug and dated observation forms. The brief's rate came from a different count, so the two shares are not a trend.

## Clean-rewrite list

- **The 107 notes-only entries.** Each enters a draft as a Q- line, not a claim, until its evidence has a publishable ledger row (the #288 design, section 5 item 4): R-01, R-02, R-03, R-04, R-46, R-12, R-13, R-15, R-16, R-45, R-22, R-23, R-25, R-26, R-47, R-29, R-30, R-48, R-33, R-34, R-36, R-37, R-70, R-110, R-113, R-114, R-115, R-116, R-117, R-119, R-120, R-121, R-122, R-143, R-144, R-149, R-150, R-152, R-154, R-155, R-157, R-158, R-159, R-160, R-162, R-163, R-164, R-166, R-169, R-170, R-171, R-172, R-173, R-174, R-175, R-176, R-177, R-178, R-179, R-180, R-181, R-182, R-183, R-185, R-186, R-187, R-188, R-189, R-190, R-191, R-192, R-193, R-194, R-195, R-196, R-197, R-199, R-200, R-201, R-202, R-203, R-204, R-205, R-206, R-207, R-208, R-209, R-210, R-211, R-212, R-213, R-214, R-215, R-216, R-218, R-219, R-220, R-221, R-222, R-223, R-224, R-225, R-226, R-227, R-228, R-229, R-230.
- **Section 2, who holds a stake.** Only the operator has a tracked source.
- **A Discovery item from section 2:** before `brs-review.md` is filled, the operator is asked which stakeholder classes exist beyond the operator, and the value statement for each.

## Not done in this run

The brief's step 3, the diff of the register's source classes against the audit themes, was not run. It decides whether the StRS can be stable, not whether the BRS can be drafted, and it stays owed as aae-orc-xxyag, which depends on this probe.

## Measured with

`sim/research/corpus-fitness/` at the commits of this pull request: `python3 test_locus.py` (10 tests, red before the parser and green after, in the host HOME and in a clean one), and `locus.py . 62b55fc id-grammar.json [--summary | --ids ... | --ledger ...]`, which regenerates `projection.tsv` byte for byte.
