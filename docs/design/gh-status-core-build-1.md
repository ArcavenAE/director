# gh status core: build 1

- **Date:** 2026-10-26.
- **Status:** spec for review. Nothing is built by this document. Every default marked "proposed" is a recommendation, not a ruling.
- **Owner:** the architect role writes it; a builder builds it in the core's own repository once the operator names that repository.
- **Source:** `gh-ingress.md` sections 2 to 4. Where this spec and that design differ, the design wins and this spec is wrong.
- **Ticket:** to be added.

The operator agreed on 2026-10-26 to start the Rust status core. Build 1 gives the fleet one process that reads GitHub on a fixed query set and a local view that any seat or display reads without calling GitHub. It publishes nowhere, so nothing in the broker's authorization work gates it (`gh-ingress.md` section 7, item 1).

`<core>` stands for the binary until the repository is named.

## 1. What build 1 is

In scope:
- the collector, `<core> run`, which polls GitHub and writes the view;
- `<core> list --json` and `<core> watch --json`, which read the view;
- the allowlist file;
- the interim credential: gh's own login, in report mode.

Out of scope, each its own later build: `key`, `watch-key`, `open`, `select`, `quota` and `--image`; the desk profile, the no-token diagnostic and the bootstrap; the App and the minter; the grant check that refuses a dedicated source; a second provider; gh-ingress itself.

**Before the first live run.** Section 7 of the design runs the core on the interim credential "once the operator has ruled the credential card, which states the login's scopes". This spec does not record that card as ruled. Every test below runs against a fake GitHub and a fake `gh`, so none needs the card. The builder confirms the card with the supervisor before the first run against github.com.

## 2. The fixed query set

The set is compiled in and read-only. The only input to any query is a repo from the allowlist, or an owner taken from an allowlisted repo. Nothing on the command line, in the environment, on a socket or in a file other than the allowlist reaches a query.

| class | call | when | design source |
|---|---|---|---|
| membership | `GET /repos/{owner}/{repo}/pulls?state=all&sort=updated&direction=desc&per_page=100`, with `If-None-Match` from the last ETag | every cycle, per allowlisted repo | section 2, row 1 |
| state: open PRs | the open-PR view: `search(type: ISSUE, query: "is:pr is:open org:<owner>", first: 100)`, paginated, with `number`, `mergeable`, `headRefOid`, the last commit's `statusCheckRollup { state }`, and `rateLimit { cost }` | every cycle, per owner | section 2, "The query shapes" |
| state: default branch | the default-branch view: one aliased `repository(owner, name)` per allowlisted repo, with `defaultBranchRef.target` on `Commit` (`statusCheckRollup { state }`, `checkSuites(last: 10)` with `workflowRun`), `deployments(last: 1) { nodes { latestStatus { state } } }`, and `rateLimit` | every cycle | section 2, "The query shapes" |
| live read: PR | one PR by number on an allowlisted repo: `mergeable`, `headRefOid`, the last commit's rollup | a head SHA changed, or `mergeable` was UNKNOWN last cycle | section 2, rows 3 and rule 2 |
| live read: base | `GET /repos/{owner}/{repo}/branches/{branch}` with `If-None-Match`, for each distinct base of an open PR | every cycle; a moved base triggers live reads of the open PRs on it | section 2, row 4 |
| busy signal | `GET /rate_limit` | every cycle, and on backoff | section 2, "Busy signal" |

The membership row's path and parameters are this spec's reading of "REST lists by `updated_at`". The design names no path, so that row is **Q2**.

Collection rules carried over unchanged: opened, closed and merged come only from the membership list; a `mergeable` of UNKNOWN is re-read and never published, and after 3 cycles is a health warning naming the PR; only check suites with a workflow run count as running; pagination past 100 is built now, and more than 100 open PRs is a health warning; each class has an owner-set pacing ceiling per reset window (section 2, "Rules").

**Cadence (proposed).** A 60 s cycle. A repo with a real run in progress polls every 15 s for at most 30 minutes, then drops back. Section 2 sets the 60 s cycle by its cost arithmetic and gives no number for the fast poll, so the fast-poll pair is **Q3**. Collection test 11 (a manual 304 measurement on a branch read) runs before the cadence is fixed for release.

## 3. The allowlist file

**Location (proposed, Q4).** `$XDG_CONFIG_HOME/<core>/watch.toml`, falling back to `~/.config/<core>/watch.toml`, and overridden only by `--allowlist <path>` on `<core> run`. `list` and `watch` never read it.

**Format (proposed, Q4).** TOML:

```toml
version = 1
repos = ["ArcavenAE/marvel", "ArcavenAE/director"]

[ceilings]            # per reset window, per call class
graphql_points = 300
rest_calls = 1500
live_reads = 200
```

The ceilings sit in the same file so one owner check covers both. Their numbers are placeholders until collection test 11 and the first week of samples.

**Checks at start and on SIGHUP.** The core opens the path with `O_NOFOLLOW` and runs every check on the open descriptor (`fstat`), so the file it checks is the file it reads.
- The file is refused when it is a symlink, when its owner uid is not the core's uid, when it is group- or world-writable, when it does not parse, when `version` is not 1, or when `repos` is empty.
- At start, a refused file stops the core with exit 78 and one line naming the reason. On SIGHUP, the old list stays in force, and the health record names the reason (design collection test 12).
- Each entry must be `owner/name`, with both parts in `[A-Za-z0-9_-]`. A failing entry, a dotted name such as `org/a.b` included, is dropped with a warning naming it, and the other entries load (design collection test 13, section 5 "Subject tokens"). A duplicate is dropped with a warning.
- A repo removed on reload stops being fetched from the next cycle, and its rows leave the view.

No socket, bus message, flag other than `--allowlist`, or environment variable adds to the list.

**Q1, the open-PR search scope.** Section 2's open-PR view searches `org:<owner>`, which returns PRs from repos not on the allowlist, while section 3 says the only parameter is an allowlisted repo. Recommended default: keep the measured `org:` shape for each owner that has an allowlisted repo, drop every result whose repo is not on the allowlist before it reaches the view, and count the drops in the health record. The alternative, `repo:` qualifiers per allowlisted repo, changes the query whose cost section 2 measured. It would need its own measurement, and GitHub's search query length limit for many repos is UNVERIFIED here.

## 4. The view, `list --json` and `watch --json`

The collector writes the view to `$XDG_STATE_HOME/<core>/view.json` (falling back to `~/.local/state/<core>/view.json`), mode 0600, by atomic rename. Its directory is 0700. There is no listener of any kind. The socket form the design allows is left for a later build (**Q5**).

Both read verbs read only that file, never the network. Neither starts the collector.

### `list --json`, schema `gh-status-view/1` (proposed, Q6)

One JSON object on stdout:

```json
{
  "schema": "gh-status-view/1",
  "generated_at": "2026-10-26T18:40:00Z",
  "cycle_s": 60,
  "stale": false,
  "credential": { "source": "gh-login", "mode": "interim" },
  "health": [ { "class": "unknown-mergeable", "repo": "ArcavenAE/marvel", "number": 1080, "since": "2026-10-26T18:37:00Z" } ],
  "repos": [
    {
      "repo": "ArcavenAE/marvel",
      "access": "read",
      "default_branch": {
        "head_sha": "<40 hex>",
        "ci": "passing",
        "runs_in_progress": 0,
        "deployment": "none"
      },
      "prs": [
        {
          "number": 1080,
          "state": "open",
          "mergeable": "MERGEABLE",
          "head_sha": "<40 hex>",
          "base": "main",
          "ci": "running",
          "author_association": "OWNER",
          "updated_at": "2026-10-26T18:31:12Z"
        }
      ]
    }
  ]
}
```

- `ci` is one of `passing`, `failing`, `running`, `unknown`, the words of the display contract (section 6, "the display contract").
- `mergeable` is `MERGEABLE`, `CONFLICTING` or `null`. UNKNOWN never appears (section 2, rule 2).
- `access` is `read`, `public`, `none` or `unknown:<reason>` (section 4).
- `generated_at` is the last successful cycle, never refreshed on a failure. `stale` is true once it is older than 3 cycles, and every `ci` then reads `unknown` (section 3, "Staleness"). `generated_at` is `null` before the first good cycle.
- The view holds no title, body, branch description, check name, status description, login, token or scope string (section 3, "Derived fields only"). `base` is a branch name, the one free-text field. It is kept because section 2 reads PRs by base, has its control characters stripped, and is cut at 255 bytes.

### `watch --json`, schema `gh-status-change/1` (proposed, Q6)

One JSON object per line on stdout, flushed per line:
- On start, one `{"type":"snapshot", ...}` line carrying the whole `gh-status-view/1` object.
- Then, for each change the collector writes, one line per changed fact:
  `{"type":"change","generated_at":...,"repo":"ArcavenAE/marvel","object":"pr","number":1080,"kind":"pr.ci","action":"failing","from":"running","to":"failing","head_sha":"<40 hex>"}`.
  The kinds are `pr.opened`, `pr.closed`, `pr.merged`, `pr.head`, `pr.ci`, `pr.mergeable`, `repo.ci` and `repo.deployment`, so gh-ingress can map `kind` and `action` onto its subject tokens (section 5).
- After every cycle, one `{"type":"generation","generated_at":...,"stale":false}` line, changed or not, so a reader can tell a quiet repo from a stopped collector. gh-ingress's heartbeat carries this time (section 5, "Heartbeat").

A change line appears within one cycle of the collector writing it (section 6, "core without a desk plugin").

### Exit codes (proposed, Q7)

| verb | 0 | 1 | 2 | 78 |
|---|---|---|---|---|
| `list --json` | a view was printed, stale or never generated included | the view file exists and cannot be read or parsed | usage | |
| `watch --json` | stopped by SIGINT or SIGTERM | the view file became unreadable | usage | |
| `run` | stopped by SIGINT or SIGTERM | an unrecoverable internal error | usage | the allowlist or the credential check refused start-up |

A stale view is data, not a failure. This follows the display contract, where every `key` line exits 0 in every state (section 6).

**Q8, how the collector is kept running.** The design lists no daemon verb. Recommended default: `<core> run` runs in the foreground, under the operator's launchd on the host that runs it. That is an OS-owned clock for the poll only, as section 5 has for the watchdog.

## 5. The interim credential, in report mode

The core uses gh's own login and never holds a token (section 4, "The interim").
- Every forge call is `gh api graphql` with a compiled document, or `gh api -X GET` on a path built from an allowlisted repo, plus `GET /rate_limit`. Nothing else is spawned. This is the argv the recording-shim test allows (section 6, "no write call").
- The core sets no `GH_TOKEN`, `GITHUB_TOKEN` or `GH_ENTERPRISE_TOKEN` for `gh`, and clears them from the child environment if its own environment carries them. It never runs `gh auth token`, `gh auth status --show-token` or any `gh auth` verb. It never reads gh's `hosts.yml` or the keychain.
- **Report mode.** At start, the core prints one line to stderr naming the credential source `gh-login` and the word `interim`, with the login's scopes. It does not refuse on a write scope. The scopes are proposed to come from the `X-OAuth-Scopes` header of the `GET /rate_limit` response, which is already in the fixed set (**Q9**). The scopes appear in that start line only: never in the view, a change line or any other log line, since the design keeps them in a private record (section 4).
- The view's `credential` field says `gh-login` and `interim`, so gh-ingress's publishing refusal (section 4, last paragraph) has something to read.

**The custody test (SOUL section 3).** The artifact that matters is the gh login's OAuth token, which is bearer authority at GitHub, so the core must not hold it. Under this build it does not. gh holds the token, the core passes nothing but query text and an allowlisted path to gh, and nothing the core writes or prints carries it. That is delegation to the tool that owns the auth. Read-only rests on the fixed query set alone, because the login carries write scopes (section 4, MEASURED and recorded privately). The least-privilege row of section 6 says the interim fails least privilege by design until the operator's date for the fleet credential.

**Step 0 for the builder, before any test.** Run each against the installed `gh`, read-only, and record the gh version:
1. `gh api -i` prints the response headers, `ETag` and `X-OAuth-Scopes` among them.
2. What `gh api` does with a 304 to an `If-None-Match` request: its exit status, and whether the headers print. This was not measured.
3. What `gh api` does with a 403, a 429 and a `Retry-After`: its exit status and output.

Any answer that differs from what section 2 needs becomes a note on this spec and a question to the architect, not a workaround.

## 6. Tests, red first

Every test runs against a fake GitHub server and a recording `gh` shim on `PATH`, never against github.com, with a scratch `HOME`, `XDG_CONFIG_HOME` and `XDG_STATE_HOME`.

Collection, from `gh-ingress.md` section 2 (same numbers):
1. A PR missing from a search result, with no REST change, publishes nothing.
2. Opened, closed and merged come from the REST list fixture, once each.
3. UNKNOWN is re-read next cycle, never in the view, and after 3 cycles raises one health warning.
4. A QUEUED suite with no workflow run is not running; one with a run is.
5. A base that moves with no PR head change triggers a live read of the open PRs on it, a stacked PR's parent included.
6. 101 open PRs are read across two pages, and a health warning is raised.
7. A 403, a secondary limit or a 429 backs off from a 1 minute floor to the cap, honours `Retry-After`, and leaves `generated_at` where it was.
8. A failed cycle leaves `generated_at` unchanged, and `list --json` shows `stale: true` and every `ci` as `unknown` after 3 cycles.
9. A class at its ceiling makes no request in the fake server's log until the window rolls over, and the health record names the class.
10. A fast poll on a run that stays IN_PROGRESS past the ceiling drops back to the 60 s cycle.
12. On SIGHUP, a file now group-writable, or now owned by another uid, is refused; the old list stays and the health record says why. A valid new file takes effect, and a removed repo stops being fetched.
13. `org/a.b` is refused at load with a warning naming it, the fake server logs no request for it, and other entries load.

(Test 11 is the manual 304 measurement before release, not a unit test.)

Guarantees, from section 6 rows that build 1 reaches:
- **Without marvel:** with no `MARVEL_SOCKET` and no daemon, `list --json` returns the view.
- **Without NATS:** with no broker, the view is returned, the log shows no connect attempt, and `cargo tree` shows no NATS crate.
- **Within one cycle:** `watch --json` prints a change line within one cycle of the fake server changing a PR.
- **An unlisted repo:** no verb causes a request for it; the fake log stays at zero.
- **A raw query:** GraphQL text sent through every input (flag, env, allowlist entry, stdin) never reaches the fake server; its log shows only the compiled documents.
- **No write call:** the fake server fails the test on any method other than GET, or POST to `/graphql` with a compiled document; the shim fails it on any argv other than the set in section 5; a source check for `mutation` stays as a second line.
- **A URL:** a URL passed through each input is refused, with no request to it.
- **No stranger widens the list:** an injected `stat` result with another owner uid and mode 0644 is refused at start and on reload.
- **Nothing sensitive leaves:** canaries in fixture titles, bodies, branch descriptions, check names, status and deployment descriptions never appear in the view, change lines, logs or stdout. A positive control proves the scan finds a planted canary.
- **The token stays out:** with a canary in the parent's `GH_TOKEN`, no child's argv or environment carries it, and `ps -E -o command= -p <pid>` on the core shows no canary.
- **Report mode:** with the gh login as source and a fake `X-OAuth-Scopes: repo, workflow`, start-up prints `gh-login`, `interim` and the scopes to stderr and runs; the scopes string appears nowhere else.
- **Viewing never calls the forge:** `list` and `watch` run 100 times leave the fake log unchanged.
- **No listener:** `lsof -nP -a -p <pid> -iTCP -sTCP:LISTEN` and `-iUDP` print nothing for the collector.

New in this spec:
- **Schema golden:** `list --json` against a fixed fixture matches `gh-status-view/1` byte for byte, and each `watch --json` line type matches its golden.
- **Generation lines:** with no change for 3 cycles, `watch --json` prints 3 `generation` lines and no `change` line.
- **Exit codes:** each cell of the table in section 4 has a test, a stale view exiting 0 among them.
- **Symlink:** an allowlist path that is a symlink to a valid file owned by the core's uid is refused.
- **Search scope (Q1 default):** an `org:` search result for an unlisted repo never reaches the view, and the drop count rises by one.
- **No gh auth verb:** the shim fails the test if any `gh auth` argv is spawned.
- **Base field:** a branch name carrying control characters and 300 bytes is stored stripped and cut at 255 bytes.

## 7. Open questions, each with a recommended default

| | question | recommended default |
|---|---|---|
| Q1 | The open-PR search is org-wide; the allowlist is per repo. | Keep `org:`, drop unlisted results before the view, count the drops (section 3). |
| Q2 | Which REST list carries membership. | `GET /repos/{o}/{r}/pulls?state=all&sort=updated&direction=desc` with `If-None-Match`; PRs only in build 1, issues later. |
| Q3 | The fast-poll interval and its ceiling. | 15 s, dropping back after 30 minutes. |
| Q4 | The allowlist's format, path, and where the ceilings live. | TOML at `$XDG_CONFIG_HOME/<core>/watch.toml`, ceilings in the same file. |
| Q5 | The view as a file or a socket. | A 0600 file by atomic rename in build 1; a socket waits for a reader that needs push. |
| Q6 | The `list` and `watch` schemas. | `gh-status-view/1` and `gh-status-change/1` as in section 4. |
| Q7 | The exit codes. | The table in section 4: a stale view is 0, refused start-up is 78. |
| Q8 | How the collector stays up. | `<core> run` in the foreground under launchd on its host. |
| Q9 | Where the interim reads the login's scopes. | The `X-OAuth-Scopes` header of the `GET /rate_limit` read already in the fixed set. |
| Q10 | Whether the provider seam ships in build 1. | Define the provider trait in build 1; defer the fake second provider test to the build that adds a second forge. |

These recommendations are valid until 2026-11-09. The architect role re-checks them then, or when the operator names the repository, whichever comes first.
