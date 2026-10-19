# Card ingest: one parse from a seat's card file to a Desk write

Status: design for review, 2026-10-19. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Issue: #320.
- Commission: the operator's "yes please execute on each" on director's
  improvement list. This is item 4a: one tool turns a seat's standard card
  file into a Decision Desk card write, and refuses a card missing any part.
- Requirements: R-202, R-171, R-177, R-198, R-203 (`sim/requirements.md`),
  and the sell-by rule for expiries (the operator's ruling of 2026-10-05,
  carried in the fleet's `expiry-is-sell-by` rule).
- Prior art: `desk-snapshot` and `verify-desk-snapshot.sh` (K13, the only
  validated path into Desk state today); `finding-director-glst` (K5: a
  script cannot reach the Desk); `reference/functions.md` section 1;
  `director-functions-redesign.md` rows K5, K12 and K13.
- Checked against director `origin/main` 59f8512.

## 1. Why

Every operator decision reaches the Desk by director reading a seat's card
file and retyping it as a `decisions/<slug>` document. In one session
director hand-parsed nine cards that way (reported by director, not
re-measured here). Retyping is how R-202's specimen happened: a pending
question held as bare option numbers, with no option text and no source.
Nothing checks a card's shape today
(`reference/functions.md:37`, "Honor-only: nothing checks a card's shape").

One parser ends the retyping and defines the card rules on main for the
first time. A card missing a part goes back to the seat that wrote it, not to
the operator. The check is structural (`diagnostic-not-gate.md`): "is this
card well-formed?", never "is this decision good?", and it gates nothing but
its own output.

## 2. What the real cards look like (measured)

Three local files were read: six cards from a seat-decision-authority study,
one card from a rule-loading study, and one card from a build-target design.
That is eight cards; the ninth of the session was not available. Counts below
are from reading the files and a short Python pass over their bold labels.

| part | how it is marked (cards) |
|---|---|
| card start | `## Card N. <question>` (6); `# Card: <question>` (2) |
| question | the heading, ending in `?` (7); a statement, then the question sentence (1) |
| IN SHORT | `**IN SHORT.**` (8) |
| problem | `**The problem.**` (7); `**Problem.**` (1) |
| terms | `**Terms.**` with `- *term*: definition` lines (6); absent (2; one has `**The test.**` instead) |
| options | `1.` `2.` at column 0: plain (1), with an italic name `1. *Name.*` (6), or `1. **Name.**` with sub-bullets and a trailing `RECOMMENDED.` (1) |
| recommendation | `**Recommendation.** Option N.` (7); `**Recommendation:** option N.` (1) |
| valid until | `**Valid until** YYYY-MM-DD.` then `The <role> re-checks it then.` (6); the date, then `, or until <event>, whichever comes first` (1); the same with a parenthetical after the date (1) |
| source | no card has a source part (8). Preamble prose names a bus request id (6) or a re-check record (1); a closing `Pointer:` line names a design file (1) |

Other bold-led sections appear and are not in the six parts: `**Why it came
up.**` and `**Current state.**` (6 cards), `**Can a seat predict your
ruling?**` (7 cards). The six-card file also has 66 lines of preamble before
card 1, with a table and two sub-headings that are commentary for director.

Each recommendation's first sentence names exactly one option. 25 of 27
options say what they cost ("The cost is" or "Cost:"). No card uses an
action-on-silence phrase ("absent a ruling", "takes effect", "if no answer",
"default holds"; `grep -ciE` gives 0 in all three files).

So the labels vary in small, predictable ways (a leading "The", a trailing
"." or ":"), and the real gaps are two: no card marks a source, and two cards
have no Terms.

## 3. Strict or tolerant

| | option | on the eight cards | costs |
|---|---|---|---|
| a | strict: exact canonical labels only | 0 pass; the build-target card also fails twice on spelling (`Problem.`, `Recommendation:`) | seats rewrite labels that carried the meaning fine |
| b | tolerant labels, strict parts | 0 pass: 8 for no source, 2 of them also for no Terms | each file needs a `Source:` line; two cards need `**Terms.** None.` |
| c | tolerant parts: source guessed from preamble prose, Terms optional, `Pointer:` accepted | 8 pass | the tool guesses which prose id is the source, and a card with undefined jargon passes |

**Recommended: (b).** The label variants are spelling, and reading through
them loses nothing. A missing source or Terms part is content: R-202 names
the source pointer, and a card that defines nothing leaves the operator
ruling on undefined labels. (c) passes all eight by guessing, and a guess on
the Desk is the hand parse again with a script's name on it. That (b) refuses
every real card is expected, since no standard existed when they were
written; the fix per file is one or two lines (section 9).

Who settles it: this is a design choice, not an operator ruling. It is
settled in this design's review, and director decides at merge. The operator
is asked only if the reviewer and the architect role disagree. This
recommendation is valid until 2026-10-26, or until a builder opens the draft
PR, whichever comes first. The architect role re-checks it then.

## 4. The card file contract

**Card start.** A heading line `#`, `##` or `###`, then either `Card N.`
(N a positive integer) or `Card:`, then the heading text. Text before the
first card heading is the file preamble.

**Preamble.** Only lines of the form `Key: value` at column 0, with Key one
of `Source`, `Kind`, are read; they apply to every card that does not set its
own. Everything else in the preamble is skipped, and the tool prints how many
lines it skipped, so nothing is dropped silently.

**Parts.** A part starts at a line beginning with a bold label, matched
after removing `**`, a leading `The `, a trailing `.` or `:` (inside or just
after the bold), and case. Required: `IN SHORT`, `Problem`, `Terms` (`None.`
is a valid value), `Options`, `Recommendation`, `Valid until`, and `Source`
(on the card or in the preamble). Optional: `Kind`, `choice` (default) or
`action`.

Any other bold-led section (`Why it came up`, `Current state`, `The test`,
and the rest) is a context section, kept in file order. A part runs until the
next bold label, card heading, or a `---` line.

**Options.** Inside the options part, a line `N. ` at column 0 starts option
N. Indented lines and sub-bullets continue it, with line breaks kept. The
marker is the exact token `[RECOMMENDED]` (a final period is allowed) ending a
line of the option: its first line, a middle line or its last, outside a code
fence. It is removed from the text and checked against the recommendation, and
a marker on two lines of one option is refused. Everything else is text, never
a marker and never a refusal, and the Desk text keeps what the seat wrote:

- the bare word, as in "and this is not RECOMMENDED";
- a near form of the token: lowercase `[recommended]`, spaces inside the
  brackets `[ RECOMMENDED ]`, a trailing colon `[RECOMMENDED]:`, the token at
  the start of a line or in the middle of one, the token in backticks, and the
  bold spellings `**RECOMMENDED**` and `**Recommended.**`.
  These draw a NOTE that names the exact form ("is not the marker; it is read
  as text. The marker is [RECOMMENDED] ending a line of the option, outside a
  code fence"), so a seat that meant the marker sees why it did not count. The
  bare word at a line end, in any case ("Recommended."), draws a NOTE too, including on ordinary prose that happens to end a line with it; that is known, and it is only a NOTE. They are not refused, because a
  card that says "recommended" in its own words is a well-formed card;
- a token inside a code fence, which is an example and draws nothing. A fence
  line is three or more backticks or tildes, then an optional info string (after
  a backtick fence the info string holds no backtick, so a line that starts with
  inline code such as ```` ```x``` ```` is not a fence). Any leading whitespace
  is allowed before a fence line (0, 3, 4 or 8 spaces all count), and a closer
  may carry trailing spaces. This is wider than CommonMark, where four spaces
  make an indented code block and not a fence. The width is deliberate: option
  text is indented continuation (sub-bullets, a fence nested under a list
  item), so a three-space limit would read a nested fence as plain text and
  take the token inside it as the marker, which is the silent wrong read this
  rule exists to prevent. The cost of being wide is a line of indented code that
  looks like a fence and opens one; an option that then ends inside it is
  refused loudly, never read silently. A closer is a line of
  only the same character, at least as long as the opener. Fence state resets at
  every option boundary, so a fence opened in option 1 cannot swallow the marker
  of option 2. An option that ends inside an open fence is refused (table in
  section 5), because a silent read would drop the marker of the next option
  without a trace. `~~~` is a fence, as in CommonMark.

(The first build read the bare word at the end of the first line; a review
showed prose can end in it, so the marker needs a form prose cannot take.
Measured: of the eight real cards, one used a marker, a trailing
`RECOMMENDED.`, so this form costs one line fix. Review round 4 asked what
happens to forms near the token; the answer above is NOTE and text, because a
refusal would block a card over a spelling and a silent read would leave the
seat believing the option was marked.)

**Recommendation.** Its first sentence must name one option as `Option N`
(any case), and N must exist.

**Expiry.** The grammar, with nothing else allowed before the first period
or the event clause:

```
YYYY-MM-DD [ (<aside>) ] .
YYYY-MM-DD [ (<aside>) ] , or [until] <event>, whichever comes first
```

and one sentence with `re-checks` naming who does it. That is the sell-by
rule as written: a date, or an event or a date, whichever comes first, a
named re-checker, and nothing executes on it.

**A minimal valid card**, the standard seats follow:

```markdown
Source: bus 01EXAMPLE, the request for this card

## Card 1. Should the nightly report run at 06:00 or 07:00?

**IN SHORT.** The report runs at 06:00 and often misses late data. Moving
it to 07:00 catches that data and lands after the morning stand-up starts.

**The problem.** Late data arrives between 06:00 and 06:40 on most days.

**Terms.** None.

**Options.**
1. *Keep 06:00.* Nothing changes. The cost is a report short of late data.
2. *Move to 07:00.* One schedule line changes. The cost is a later report.

**Recommendation.** Option 2. Complete data matters more than an hour.

**Valid until** 2026-11-02. The reporting seat re-checks it then.
```

### Mapping to Desk fields

| Desk field | from |
|---|---|
| slug (`decisions/<slug>`) | section 6 |
| `title` | the heading text after `Card N.` or `Card:` |
| `question` | the heading's last sentence ending in `?` |
| `context` | IN SHORT, problem, terms, then each context section, each led by its label as written, joined with blank lines |
| `options` | `[{key: "N", text}]`, in order |
| `rec` | the recommendation part, verbatim |
| `expiry` | the valid-until part, verbatim, re-checker included |
| `source` | the card's `Source` part, or the preamble's `Source:` |
| `kind` | `Kind`, default `choice` |
| `area`, `owner` | `--area` and `--owner`, required flags, one value per file |
| `status` | `open` |
| `ruling` | empty string |
| `opened` | the tool's clock at parse time (`DWS_NOW` under `DWS_TEST_CLOCK=1`, as in `desk-snapshot:39-41`) |

IN SHORT leads the context because it is the part written for a cold reader.
Whether the page renders markdown inside `context` and `options` is not
measured here; the tool keeps text as written and leaves rendering to the page.

## 5. Refusals

Refusal is all or nothing per file: on any refusal the tool writes no batch
file and no log line. It collects every problem in the file before refusing,
so a seat fixes them in one pass. Each line reads
`card-ingest: refused: <file>: card <N>: <reason>`, on stderr, exit 1.

| case | reason text |
|---|---|
| no card heading | `no card heading; a card starts with '## Card 1. <question>' or '# Card: <question>'` |
| heading without `?` | `the heading holds no question ending in '?'` |
| two `?` in the question sentence or heading | `the heading asks 2 questions; one decision per card` |
| a required part missing | `no <part> part` (for terms: `write '**Terms.** None.' when there are none`; for source: `add '**Source.** <pointer>' to the card or 'Source: <pointer>' above the first card`) |
| a part empty | `the <part> part is empty` |
| a part twice | `two <part> parts` |
| a label-like line that is not a part (`Word:` at the start of a paragraph inside a card) | `'<Word>:' is not a card part; a source is written '**Source.** <pointer>'` |
| options misnumbered | `options are numbered 1, 2, 4; number them 1 to n with no gap` |
| too few options | `a choice card needs at least two options; found 1` (an action card needs one) |
| `Kind` not known | `kind '<v>' is neither choice nor action` |
| recommendation names no option | `the recommendation's first sentence names no option ('Option N')` |
| names a missing option | `the recommendation names option 5; the card has 3` |
| names two | `the recommendation names options 1 and 2; recommend one` |
| marker disagrees | `RECOMMENDED marks option 1 and the recommendation names option 2` |
| two markers | `RECOMMENDED marks 2 options` |
| marker twice on one option | `option 1 carries RECOMMENDED on 2 lines` |
| option ends inside an open fence | `option 1 ends inside an open code fence; close it with a line of just the fence token before the next option` |
| expiry not a date first | `Valid until does not start with a date (YYYY-MM-DD); an event alone may never arrive, so it is not an expiry` |
| not a calendar date | `2026-02-30 is not a calendar date` |
| date already passed | `2026-10-01 is before today (2026-10-19); re-check the recommendation and renew it before posting` |
| bad clause after the date | `after the date, write '.' or ', or until <event>, whichever comes first'` |
| no re-checker | `Valid until names nobody who re-checks it; write 'The <role> re-checks it then.'` |
| action on silence, in the recommendation or expiry (`absent a ruling`, `takes effect`, `if no answer`, `default holds`) | `'<phrase>' reads as an action on silence; an expiry is the recommendation's sell-by date, and nothing executes on it` |
| slug errors | section 6 |

Not refused, printed as `NOTE`: an option with no cost wording ("cost",
"costs"). Two real options would trip it, and whether an option says what it
costs is a reading judgment, which belongs to the reviewer, not the parser.

## 6. Slugs, multi-card files, re-ingest

**Slug.** `--prefix P` is required: 3 to 48 characters of `a-z`, `0-9` and
`-`. A `Card N.` card gets `P-N`; a single `Card:` card gets `P`. A file with
both forms, or two `Card:` headings, or two cards numbered alike, is refused
(`two cards number 3`). Deriving slugs from question words was rejected: an
edited question would change the slug and turn a revision into a new card.

**Collisions.** A new slug must not exist on the Desk. A script cannot read
the Desk, and the snapshot holds only open ids (`desk-snapshot:100-101`), so
a slug held by a relayed or closed card is invisible to it today. Build item
B2 has `desk-snapshot` also record every id with its status. card-ingest then
refuses:

- a slug the snapshot shows, of any status: `slug P-1 is on the Desk
  (status relayed, snapshot <read_at>); pass --update to revise an open card`;
- a slug in its own log (section 7) not yet in a snapshot: `slug P-1 was
  emitted at <time>; read the Desk and run desk-snapshot write first`;
- any run with no snapshot, or one older than the dws threshold
  (`THRESHOLD_HOURS["desk snapshot"]`, 24 hours, `dws:62`): `no Desk snapshot
  read in the last 24 hours; UNREAD`.

UNREAD costs one Desk read; a write over a relayed card destroys a ruling.

**Re-ingest of an edited card.** `--update` revises an open card in place,
and only that. A ruled card (relayed, closed, superseded) is never rewritten;
a revision of it is a new card under a new prefix whose source names the old
slug.

- refused unless the snapshot shows the slug with status exactly `open`;
- refused if the snapshot shows an answer for it (B2 adds an optional
  answers read): `P-1 has an answer on the Desk; a card the operator has
  started answering is not rewritten under them. Ingest it under a new
  prefix`;
- the emitted write is a merge of the content fields only (`title`,
  `question`, `context`, `options`, `rec`, `expiry`, `source`, `kind`), so
  `opened`, `status` and `ruling` stay as the Desk has them.

**Source pointer.** `source` is the seat's pointer, verbatim. The card
file's path, sha256 and card number go to the local log only, since card
files often live in local or private paths.

## 7. Output and the trust boundary

A script cannot write the Desk (`finding-director-glst`), so the split is:

1. **`card-ingest parse`** validates the file and, only if every card passes,
   writes `$DIRECTOR_STATE/card-ingest/<prefix>-<sha8>.json`:
   `{"made_at", "file_sha256", "writes": [{"op": "set"|"update",
   "collection": "decisions", "doc_id": slug, "data": {...}}]}`, one write
   per card. Under one `flock` on `$DIRECTOR_STATE/card-ingest.lock` it
   appends one line per card to `$DIRECTOR_STATE/card-ingest.jsonl`:
   `{at, slug, op, data_sha256, file_sha256, card}`. It prints the batch path
   and each slug.
2. **The session** passes the writes to the Desk tool's batch write, one
   approval for the file. Mapping `op`, `collection`, `doc_id` and `data` onto
   that tool's batch input is a build-time check against its schema (inferred
   to fit; not measured).
3. **`card-ingest check --batch B --read FILE`** takes what the session read
   back from the `decisions` collection after the write, and compares each
   emitted slug field by field. It prints `PASS <slug>` or `MISMATCH <slug>
   <field>` and exits nonzero on any mismatch or missing slug, and appends a
   `checked` line to the log. The session then refreshes the snapshot with
   `desk-snapshot write`, as K13 already asks.

In the labels of `reference/functions.md`:

- **Enforced:** every refusal in sections 5 and 6; all or nothing per file;
  slug collision against the snapshot and the log; the clock on `opened`
  (the parser takes no time argument, as `relay-log` does); the read-back
  comparison, given a read file.
- **Honor-only:** that the session sends the batch unchanged, and that the
  read file is a real Desk read (the same claim `desk-snapshot` makes).
  A Desk write made without the tool stays unchecked, and functions.md says
  so. Content stays the author's and the reviewer's: one decision per card
  beyond the `?` count, plain language in IN SHORT, that the card is not a
  clerical relay (R-203), that a named PR is ready and not a draft (R-171,
  R-198), and that a seat's claim carries its source (R-177). The tool checks
  that a source exists, not that it supports the claim.
- **Not defined:** who may author a card file. Any seat's file is parsed
  alike; the author is whoever `--owner` says, which is a claim, as
  `dws --actor` is.

## 8. Build items (one red/green draft PR)

| item | what |
|---|---|
| B1 | `skills/director/scripts/card-ingest`, Python 3, stdlib only, in the shape of `desk-snapshot`: a module docstring stating the contract, a `Refused` exception, `refused:` on stderr, exit 1, `DWS_NOW` only under `DWS_TEST_CLOCK=1`, temp file plus `os.replace`, one `fcntl.flock` around the log append. Subcommands `parse FILE --area A --owner O --prefix P [--update]` and `check --batch B --read FILE` |
| B2 | `desk-snapshot write`: add `"ids": {id: status}` for every card, and optional `--answers FILE --answers-reported N` recording `"answered": [ids]`, with the same count and limit refusals as the cards read. `dws` reads only `open`, so it is unaffected |
| B3 | `skills/director/scripts/verify-card-ingest.sh`, picked up by CI's `verify-*.sh` loop (`.github/workflows/ci.yml:96`) |
| B4 | `install.sh`: `install_one "$SRC/skills/director/scripts/card-ingest" "$DIRECTOR_HOME/bin/card-ingest"`; `scripts/verify-install.sh`: `director/bin/card-ingest` in `targets()` and `card-ingest` in its three `for n in ...` lists |
| B5 | SKILL.md: the card standard (section 4's minimal card), and the call as `${DIRECTOR_HOME:-$HOME/.director}/bin/card-ingest`, which verify-install requires of every installed script |
| B6 | `reference/functions.md` section 1: card writes through card-ingest enforced for shape; direct writes honor-only |

**Fixtures** are written fresh as heredocs inside the verify script, as
`verify-desk-snapshot.sh` does, on a neutral subject (a report schedule, a
log retention period). None is copied from a real card.

**Cases in `verify-card-ingest.sh`** (each refusal also checks that no batch
file and no log line appeared):

- positive control: a valid two-card file with a fresh snapshot gives two
  `set` writes with every Desk field, slugs `P-1` and `P-2`, `opened` equal to
  `DWS_NOW`, and two log lines;
- accepted: `**Problem.**`, `**Recommendation:**`, an agreeing `[RECOMMENDED]` on the first, a middle and the last line of an option, a bare
  `RECOMMENDED` in prose read as text,
  the event clause with and without an aside, `**Terms.** None.`, a preamble
  `Source:` for every card, preamble prose skipped and counted;
- refused, one case each: no card heading; no `?`; two questions; each of the
  seven required parts missing; an empty part; a duplicate part; `Pointer:`
  in a card; an option-number gap; a one-option choice card; an unknown kind;
  a recommendation naming no option, a missing one, two; a disagreeing
  marker; two markers; an event-only expiry; `2026-02-30`; a past date; a bad
  clause after the date; no re-checker; `takes effect` in the expiry; a time
  argument;
- all or nothing: card 1 valid and card 2 without Terms writes nothing and
  names card 2; a card missing source and Terms prints both;
- slugs: a bad prefix; two cards numbered alike; a snapshot slug that is
  relayed; a log slug not in the snapshot; no snapshot and a 25-hour-old one
  (both UNREAD);
- update: an open, unanswered card emits one content-only `update`; relayed
  and answered cards are refused;
- check: a matching read prints PASS per slug, exit 0; one changed option
  text prints MISMATCH, exit 1; a missing slug, exit 1.

`verify-desk-snapshot.sh` gains: `ids` holds every card with its status; an
answers file whose count differs from `--answers-reported` is refused.

## 9. What the eight real cards need

Under (b) each fix is one or two lines by the seat that wrote the file. The
six-card file: a preamble `Source:` line (the request id it names in prose)
and a Terms part on card 1. The rule-loading card: a `Source:` line. The
build-target card: `Pointer:` renamed `**Source.**`, a Terms part, and its trailing `RECOMMENDED.` written `[RECOMMENDED]`.

## 10. Candidate requirements (provisional tags)

- **CI-A (RULED, the commission; extends R-202).** A card reaches the Desk
  through one parse that maps a seat's card file to the decision record and
  refuses a card missing the question, IN SHORT, problem, terms, options,
  recommendation, expiry or source.
- **CI-B (RULED, the sell-by ruling of 2026-10-05).** A card's expiry is a
  date, or an event or a date, whichever comes first, names who re-checks,
  is not already past, and carries no wording that makes it act on silence.
- **CI-C (JUDGMENT).** Refusal is all or nothing per file and reports every
  problem in one pass.
- **CI-D (JUDGMENT).** A ruled card is never rewritten; an ingest that would
  reuse its slug is refused, and a revision of it is a new card naming the
  old one.
- **CI-E (OBSERVED, the nine hand-parsed cards).** After a Desk write the
  session can check the written cards field by field against what the parser
  emitted.
- **CI-F (JUDGMENT).** The card standard is written once in the skill, with
  a minimal valid card, and the parser accepts the label spellings measured
  in section 2 while still requiring every part.
