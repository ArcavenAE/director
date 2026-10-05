# Stalled big ideas on the board

Status: design for review, 2026-10-04. Owner: the architect role. Commission:
the operator, through the team supervisor: big-picture, board and status
answers should also surface stalled big ideas (research or design with no
forward pointer), alongside the Blocked / Stranded / Running shape. Design
only; a builder implements the skill change after review. The research seat
runs the companion audit of how many ideas are stalled today, separately;
this design gives that audit a rule to measure against (section 3).

## 1. Why

The sweep's answer shape (`skills/director/SKILL.md`, Mode: sweep, step 4)
has four blocks: Blocked on you, Stranded, Running, Uncaptured. Each is about
a session or an ask. Nothing in it is about an idea that nobody is moving: a
design that merged with no tickets filed, a probe brief with no finding, an
idea file that never became a question. Those do not block anyone, so they
never reach "Blocked on you", and they are not attached to a live session, so
they are not "Stranded". They go quiet.

What was checked, 2026-10-04:

| Premise | Command | Result |
|---|---|---|
| The workstream ledger would not show them | `board-workstream-ledger.md` section 3 | `idea`, `accepted` and `parked` "never flag" (line 123), so an idea row can sit unmoved forever by design |
| The ledger is not in use on kinu yet | `ls ~/.director/state/workstreams.jsonl` | absent; the sweep builds its blocks from the board and roster |
| The board would not show them either | `wc -l ~/.director/state/board.md` | 2,557 lines, appended by time; an idea mentioned once scrolls away |
| Ideas without any pointer exist | at aae-orc 94b02e2: `git grep -lE 'aae-orc-[a-z0-9]{4,5}([^a-z0-9]\|$)\|#[0-9]{2,}\|[Bb]ecame\|[Ss]uperseded\|finding-[0-9]+\|probes/brief-' 94b02e2 -- '_kos/ideas/*.md' \| wc -l` | 68 of 96 carry **pointer text**; 28 carry none. Pointer text is an upper bound for a live forward pointer, since a citation of a finding points back, not forward |
| How the counts move with the definition | the reviewer's pointer grep; research's audit (`.session/research/2026-10-04-operator-priorities/C-stalled-ideas.md` and `classes.tsv`, kinu-local, not in any repo) | reviewer: 70 with, 26 without (pointer text, a different pattern). Research: REALIZED 26, IN FLIGHT 5, STALLED 34, ORPHAN 31, so 65 with a resolved pointer and 31 without; research counts a background-only citation as no pointer. The spread is the definition, which is why section 3 fixes one |

## 2. The answer shape

One block is added after Running, in the same one-line style:

```
Blocked on you (N)
...
Stranded (N)  [dead process, ask never answered]
...
Running, not blocked: <names>
Stalled ideas (N)  [research or design, no live forward pointer, quiet 14d+]
9. <repo>/<path> - <title>  (quiet 23d; pointers all closed)
10. <repo>/<path> - <title>  (quiet 19d; no pointer; as of 2026-09-28)
11. <repo>/<path> - <title>  (quiet 16d; no pointer; as of 2026-09-21, remote unreachable)
12. ...  (+M more)
Unverified ideas: K  [bd: 4; remote unreachable: 2]
Uncaptured: <one line each, or "none">
```

- **At most five lines**, oldest quiet first, then `(+M more)`. Session 1's
  complaint ("a wall of distracting text") is a requirement, and a list of
  forty stalled ideas would be that wall. Detail on request, by number, as
  for the other blocks.
- **The same block** answers "what is the big picture", "show me the board"
  and "status": the sweep shape is the answer to all three.
- **The reason** is one of five words or phrases: `no pointer`, `pointers all
  closed`, `successor stalled`, `successor cycle`, `ruling asked, never
  answered`.
- **Unverified ideas** are counted apart, never in N: an idea whose verdict
  depends on a source the scan could not read (bd down, `gh` failing, a
  repo with no local default-branch ref). The bracket names **kinds** of
  source, never repos, at most three (`bd`, `gh`, `remote unreachable`),
  each with its count of ideas (`[bd: 4; remote unreachable: 2]`); which
  repos is detail on request. It is omitted when K is 0. A stale ref is not
  a kind here: it dates a verdict (step 2), it does not withhold one.
- **No action.** The sweep presents and stops, as it does for every block.
  The operator chooses per item: revive it (a seat files the ticket), park it,
  or drop it.

## 3. The rule

**A big idea** is a research or design artifact in one of the roots listed
in `$DIRECTOR_STATE/idea-roots.conf`, one path or glob per line. The default
file the builder ships:

```
~/work/aae-orc/_kos/ideas/*.md
~/work/aae-orc/_kos/probes/*.md
~/work/aae-orc/*/_kos/ideas/*.md
~/work/aae-orc/*/_kos/probes/*.md
~/work/aae-orc/*/sim/design/*.md
~/work/aae-orc/*/docs/design/*.md
~/work/aae-orc/docs/design/*.md
```

Never `run/`, `forks/` or `contrib/` (the three-tier taxonomy: fleet recipes
do not read them), and never a worktree directory (`*-wt-*`). An idea is
read **by ref, from the default branch**, never from the working tree: a
subrepo checkout often sits on a feature branch. **The scan never fetches**:
a fetch writes `refs/remotes/origin/*` and `FETCH_HEAD` in a checkout other
sessions share, can start `gc --auto`, races their ref locks, and can hang on
a credential prompt. For each repo it instead:

1. asks the remote, read-only: `git -C <repo> ls-remote --symref origin
   HEAD`, which names the default branch and its commit, whether or not the
   checkout has a local `origin/HEAD` (an unset one is common in older
   clones). It runs with **no way to prompt**: `GIT_TERMINAL_PROMPT=0` for
   HTTPS, and, because every orc remote is SSH, `GIT_SSH_COMMAND="ssh -o
   BatchMode=yes -o ConnectTimeout=5"` for a passphrase or host-key prompt.
   Each call runs in its own session and process group
   (`subprocess.Popen(..., start_new_session=True, stdin=DEVNULL)`), so it
   has no controlling terminal for ssh to open, and is bounded by the
   script's own timers (`dbi` is Python, like `dws`; stock macOS has no
   `timeout` command):
   - **per call**, `CALL_TIMEOUT = 10` seconds: `communicate(timeout=10)`,
     then `os.killpg(pgid, SIGKILL)`. The group kill is the point:
     `subprocess.run(timeout=...)` kills git but not the ssh child git
     started, and that child can keep the pipes open and hold the call;
   - **for the pool**, `REMOTE_DEADLINE = 60` seconds from the start of the
     step: the calls run 8 at a time (`ThreadPoolExecutor(max_workers=8)`),
     and the step waits with `concurrent.futures.wait(futures,
     timeout=<deadline remaining>)`. At the deadline every call still
     running has its group killed, every call not yet started is cancelled,
     and each of those repos takes the unreachable path (step 3). The
     per-call timeout alone does not bound the step: 8-way at 10 seconds a
     wave reaches 60 seconds only above 48 repos, so the deadline is a named
     constant with its own test (7f), not a consequence of the per-call one.

   Measured by the reviewer, read-only across the 30 orc-root checkouts:
   8-way took 5 seconds, against about 1.2 seconds a repo serially, with no
   prompt and refs and `FETCH_HEAD` unchanged;
2. compares that commit with the local `refs/remotes/origin/<default>`.
   Equal: the repo is current. Different: the repo is **judged as of its
   local ref**, and each of its listed ideas carries `(as of <date>)`, the
   local ref's commit date. A stale ref is the normal case, not the
   exception: 14 of the 30 checkouts (8 of the 16 with `_kos/ideas/`) were
   behind their remote on 2026-10-04. Sending half the ideas to Unverified
   on a normal day would empty the block, so a stale ref dates the verdict
   instead of withholding it. Bringing a checkout current is a fetch, which
   is the checkout owner's act, never the scan's;
3. if `ls-remote` fails or times out, the repo is still read from its local
   `origin/<default>` (resolved from a local `origin/HEAD`), judged as of
   that ref and marked `(as of <date>, remote unreachable)`. Only a repo with
   no local default-branch ref at all is unread, and its ideas count as
   Unverified (`remote unreachable`);
4. lists matching paths with `git ls-tree -r --name-only
   origin/<default>`, and reads each file with `git show
   origin/<default>:<path>`. The root globs select repos and path
patterns; they are never expanded against the working tree. A draft on a
branch is not counted twice or counted before it lands.

**A forward pointer** is live when it names one of:

1. an **open** bd ticket (by id), or an open PR or issue (by owner/repo and
   number), found either in the artifact's text or in the ticket's or PR's
   own text citing the artifact's path or slug;
2. a **successor artifact** named by `supersedes`, "became", or "superseded
   by", which is not itself stalled. The successor walk keeps a visited set,
   and reaching an artifact already on the walk stops it. The members of a
   cycle are judged together, as one: the cycle is live when **any** member
   has a live pointer outside the cycle (an open ticket or PR, a ruling
   request, or a successor outside the cycle that is itself live), and then
   no member is listed. Only a cycle with no such pointer lists every member,
   with the reason `successor cycle`;
3. a **ruling request** on the board that names the artifact and has not
   been answered.

A merged PR or a closed ticket is a delivered pointer, not a live one. It
ends the idea's stall only when the artifact also says it is done (below);
otherwise the reason is `pointers all closed`, which is the commonest stall
shape: a design merged and its build never filed.

**An idea is resolved**, and never listed, when its own status line or front
matter says `complete`, `done`, `graveyard`, `rejected`, `superseded` or
`parked`, or when its path is in `$DIRECTOR_STATE/ideas-parked.txt` (below).

**Quiet** is days since the artifact's last commit on the default branch
(`git log -1 --format=%ct origin/<default> -- <path>`), or since a live pointer last moved,
whichever is later. The threshold is 14 days.

**Stalled** is: not resolved, no live forward pointer, and quiet past the
threshold.

## 4. Parking

Director does not write other corpora (it delegates every edit to a seat).
So parking an idea from the board is a line in director's own state:
`$DIRECTOR_STATE/ideas-parked.txt`, one `<path>  <date>  <who ruled>` per
line, written when the operator says "park it". The scan skips those paths.
A seat may later write `parked` into the artifact itself; until then the
file is the record.

## 5. Parts

| # | Part | Depends on |
|---|---|---|
| S1 | `scripts/dbi` (director big ideas): reads `idea-roots.conf` and `ideas-parked.txt`, applies section 3, prints JSON rows `{path, title, quiet_days, reason, pointers, as_of, remote}`. `remote` is `current`, `stale` or `unreachable` (section 3, steps 2 and 3); `as_of` is the local ref's commit date (`YYYY-MM-DD`) when `remote` is not `current`, else `null`. `quiet_days` counts from the last commit touching the path on the local ref, so on a stale ref it can overstate quiet (a commit since then is not seen), which is why the tag travels with every row. S2 renders `as of <as_of>` for `stale` and `as of <as_of>, remote unreachable` for `unreachable`. Read-only, no fetch: `git ls-remote --symref` (with no prompt and a timeout), then `git ls-tree`, `git show` and `git log` against the local `origin/<default>` (section 3), `bd sql` for open tickets citing a path or slug, `gh` for each PR or issue the artifact names. A source it cannot reach is reported in the output (`unread: bd`), and an idea that depends on it goes to the Unverified count, never read as "no pointer" | none |
| S2 | `SKILL.md`: the block in step 4's shape; step 4 runs `scripts/dbi --json` and renders at most five rows; the "park it" line in section 4; the installer (`scripts/director-install`) places `dbi` and the default `idea-roots.conf` (never overwriting an existing one) | S1 |
| S3 | With a ledger: a row at stage `idea`, `defined` or `designed` with no `pr:` or `bd:` link that is open, quiet past the threshold, is listed in the same block as `<owner> - <slug>`. The ledger's own stage flags are unchanged | S1, ledger W1 |

## 6. Tests (red first)

1. A fixture root with an idea that cites no ticket, PR or successor, last
   committed 20 days ago (fake clock): listed, reason `no pointer`.
2. The same idea citing an open bd ticket: not listed. With that ticket
   closed and no `complete` status: listed, `pointers all closed`.
3. An idea whose successor is named by `superseded by` and the successor is
   itself stalled: listed, `successor stalled`. With the successor live: not
   listed.
4. An idea with `status: graveyard`, and another whose path is in
   `ideas-parked.txt`: neither listed.
5. An idea 10 days quiet with no pointer: not listed (under threshold).
6. A file under `run/`, `forks/`, `contrib/` or a `*-wt-*` directory matching
   a root glob: never read (the scan logs the skip).
7. With `bd` unreachable: the output carries `unread: bd`, an idea that
   would otherwise be stalled is counted in `Unverified ideas: K` and not in
   N, and an idea with a live non-bd pointer is unaffected. The gap is
   visible, not silent, and never read as "no pointer".
7a. A cycle: A is superseded by B and B by A, both quiet with no other
   pointer. The scan terminates; both are listed with `successor cycle`. A
   three-artifact cycle (A, B, C) gives the same.
7c. A cycle with a way out: A and B supersede each other, and B also
   "became" C, which has an open ticket. Neither A nor B is listed. The
   same when A itself has an open PR and C does not exist. With C stalled
   and no other pointer, all three are listed (A and B with `successor
   cycle`, C with its own reason).
7d. No fetch: the scan runs against a repo whose remote has moved past the
   local `origin/<default>`. Its ideas are judged against the local ref and
   each listed one carries `(as of <date>)`, and the checkout's
   `refs/remotes/*`, `FETCH_HEAD` and reflog are byte-identical before and
   after. With the remote unreachable but a local ref present, its ideas are
   judged and marked `(as of <date>, remote unreachable)`. With no local
   default-branch ref, the repo is unread and its ideas count as Unverified
   (`remote unreachable`).
7e. No prompt. Host-key arm: an SSH remote whose host key is not in
   `known_hosts` (a fixture `UserKnownHostsFile` that is empty): the call
   returns within 10 seconds without reading the terminal, and the repo
   takes the unreachable path. Passphrase arm: ssh asks for a passphrase only
   after the server accepts the public key, so this arm proves nothing
   unless the encrypted key is authorized at the remote. It runs only where
   such a key is supplied (`DBI_TEST_ENCRYPTED_KEY`, `SSH_AUTH_SOCK` unset)
   and is reported as skipped, never as passed, where it is not. Both arms
   also assert the child's command line carries `BatchMode=yes` and that the
   child has no controlling terminal (its session id differs from the
   test's), which hold with or without a willing server.
7f. The pool deadline binds. With the two constants overridden for the test
   (`REMOTE_DEADLINE = 3`, `CALL_TIMEOUT = 2`, 8-way), and 40 fixture repos
   whose remote is a fake `ssh` first on `PATH` that records its pid, starts
   a child that records its own, and sleeps: the remote step returns within
   the deadline plus one second, every unanswered repo takes the unreachable
   path, calls never started are cancelled, and no recorded pid (the fake
   ssh or its child) is alive afterwards. Run at the shipped constants with
   49 hanging repos, the same holds at 60 seconds.
7g. The tag reaches the output: for 7d's stale and unreachable repos,
   `dbi --json` rows carry `remote` and `as_of` as section 5 states, a
   current repo's row carries `remote: current` and `as_of: null`, and the
   rendered block shows each tag as in the section 2 example.
7b. A subrepo checkout on a feature branch whose working tree has an idea
   file that is not on `origin/<default>`: not read. A file on the default
   branch that the working tree has deleted: read.
8. Eight stalled ideas: the rendered block shows five, oldest first, then
   `(+3 more)`.
9. (S3) A ledger row at `designed` with only a merged `pr:` link, 15 days
   unmoved: listed in the block; its ledger stage flag is unchanged.

## 7. Rulings needed

1. **Threshold.** Default 14 days. Alternative: per root (ideas 30 days,
   designs 7). This recommendation is valid until 2026-10-19 or S1's build start,
   whichever comes first; the architect re-checks it then, and no default
   applies without a ruling.
2. **Cap.** Default five lines plus a count. Alternative: no cap, with the
   block folded behind its count (`Stalled ideas (23), detail on request`).
   This recommendation is valid until 2026-10-19 or S2's build start,
   whichever comes first; the architect re-checks it then, and no default
   applies without a ruling.
3. **Roots.** Default the list in section 3, aae-orc and its subrepos only.
   Whether client orchestrators get their own roots file, kept on the host
   that holds them, is the operator's call; until then nothing outside
   aae-orc is scanned.

None of this is a gate (ADR-007): the block informs the operator, and the
count is never a pass/fail condition anywhere.
