# gh-ingress: GitHub status onto the fleet bus

- **Date:** 2026-10-19, revised the same day; section 4 revised again for the desk and fleet credential shapes.
- **Status:** design for review. Nothing is built by this document.
- **Owner:** director, for `cmd/gh-ingress`.

Today every agent seat that watches GitHub polls it on its own clock, and a desk plugin spends hundreds of REST calls an hour asking the same questions. The clocks the seats use are session timers that die on restart; one seat's sweeps once went silent for 195 hours with no alarm.

The fix is one GitHub reader that keeps a local status view, plus optional adapters, so seats and the desk subscribe or read instead of polling. `gh-ingress` is the adapter that puts that view on the fleet's NATS bus.

## 1. The shape

There are three parts, and only the first talks to GitHub.

| part | what it is | where |
|---|---|---|
| status core | A Rust process that polls GitHub and keeps a local view. It reads with `<core> list --json` and streams changes with `<core> watch --json`. | a new small repository, still to be named |
| gh-ingress (this design) | A Go adapter that consumes `<core> watch --json` and publishes INFORM events. | `cmd/gh-ingress` in director |
| any other consumer | A Stream Deck key or dial, a shell prompt, tmux, or a seat on the same host. Each reads the CLI. | wherever it lives |

The core imports no NATS, marvel or desk-plugin code. It works with or without each of them, and each works with or without the others.

gh-ingress is Go so that the subject builder and the envelope type stay shared with `cmd/director-mcp`. director-mcp is all `package main` today, so the first change extracts a small importable package for both. The envelope type is generated from its JSON Schema, not hand-ported.

## 2. What the core collects

| what | how | cost |
|---|---|---|
| membership: opened, closed, merged, new comments | REST lists by `updated_at`, per watched repo, with `If-None-Match` | A 304 did not count against the REST limit when measured: one conditional call left the used count unchanged. GitHub's documentation page "Best practices for using the REST API" (section "Use conditional requests if appropriate") says the same; a reviewer checked the page on 2026-10-19. |
| state: open-PR `mergeable` and CI, default-branch CI, last deployment status | Two GraphQL queries per cycle, the open-PR view and the default-branch view, each carrying `rateLimit` | MEASURED 2026-10-19: 1 point for the open-PR view on 78 open PRs, and 1 point for the default-branch and deployment view on 43 repos. That these are the costs GitHub's published formula gives is UNVERIFIED; the query shapes are below. |
| a PR whose head SHA changed this cycle | A live read of that PR | A few points, only when heads move. |
| a base branch that moved (default, release, or a stacked PR's parent) | A live read of the open PRs on that base, because `mergeable` can change with no head SHA change (INFERRED from how merge checks work, not measured) | ETag'd branch reads. Free on a 304 is INFERRED: the one measured conditional call was a list, not a branch read. |

Rules:
- Opened, closed and merged come only from the REST lists. Absence from a search result is never an event.
- A `mergeable` of UNKNOWN is re-read on the next cycle and never published. That UNKNOWN means GitHub is still computing is INFERRED, not measured.
- "A run in progress" counts only check suites that carry a workflow run. Suites registered for apps that never run sit at QUEUED. MEASURED 2026-10-19: all 58 QUEUED or IN_PROGRESS suites on default-branch heads lacked a workflow run (measured only: not checked against GitHub's documentation).
- **Busy signal:** the `rate_limit` read, with exponential backoff to a cap. MEASURED 2026-10-19: three reads in a row left `used` at 526, so it does not count against the primary limit. Per GitHub's "Rate limits for the REST API" page, it can still count toward the secondary limits, which `used` does not show.
- **Secondary limits.** GitHub also enforces secondary limits on REST: 900 points a minute and 100 concurrent requests, per GitHub's "Rate limits for the REST API" page, checked by a reviewer on 2026-10-19. A secondary-limit response is a 403 or a 429, handled by collection test 7.
- **Fast poll:** only for a repo with a real run in progress, with a ceiling so a stuck run drops back.
- **Pacing ceilings.** Each call class (membership, state, live reads, quota) has an owner-set ceiling per reset window. A class that reaches its ceiling makes no more calls until the window rolls over, and says so in the health record.
- **Pagination** past 100 results is built from the first version. That GraphQL caps `first` at 100 is INFERRED from GitHub's API limits, not measured here. More than 100 open PRs is a health warning, never a silent truncation.
- **UNKNOWN is bounded.** A PR whose `mergeable` stays UNKNOWN for 3 cycles is reported as a health warning naming the PR, and is still never published as a state.

**The query shapes.** Both are recorded so a later cost can be compared like for like.
- The open-PR view: one `search(type: ISSUE, query: "is:pr is:open org:<owner>", first: 100)` page, each `PullRequest` with `number`, `mergeable`, `headRefOid` and `commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`, plus `rateLimit { cost }`. MEASURED 2026-10-19 at about 12:12Z: 1 point for 85 open PRs. This is the shape as re-run for this record; the 10:26Z measurement's exact field list was not kept.
- The default-branch view: one aliased `repository(owner, name)` field per watched repo, each with `defaultBranchRef.target` on `Commit` (`statusCheckRollup { state }` and `checkSuites(last: 10) { nodes { status conclusion workflowRun { runNumber workflow { name } } } }`) and `deployments(last: 1) { nodes { latestStatus { state } } }`, plus `rateLimit`.
- MEASURED 2026-10-19 at about 12:10Z: the default-branch view cost 1 point at 43 aliases, and still 1 point at 50 and at 60 aliases (the 50 and 60 runs repeat repo names to pad the query). Computed from the formula as quoted (UNVERIFIED, like the formula match in the table above): GitHub's page "Rate limits and query limits for the GraphQL API", section "Predicting the point value of a query" (title and heading read through a fetch summarizer), as quoted by a reviewer, sums the requests for each unique connection, assumes the `first`/`last` limits are reached, divides by 100, rounds to the nearest whole number, with a minimum of 1. This shape has two connections per repo, `checkSuites` and `deployments`; `statusCheckRollup`, `latestStatus` and `workflowRun` are single objects. That computes to about 2N requests: 86, 100 and 120 at 43, 50 and 60 repos, each 1 point, which matches the measurement. The earlier estimate of 2 points near 50 assumed three connections per repo. By the same computation, not measured, the view reaches 2 points from about 75 repos (GitHub does not say how it rounds exactly .5) and 3 points near 125. The open-PR view is about 101 requests, 1 point a page. Whether GitHub counts repeated aliases once is not known, and does not change this. Either way the cost is small, and collection test 11 re-measures before the cadence is fixed.

**Cost per cycle, measured 2026-10-19 at 10:26Z.** There were 81 open PRs across 23 repos, on 23 distinct base branches, none of them a non-default base. The open-PR search cost 1 point. A 60 s cycle then spends:
- 1 GraphQL point for the open-PR view and 1 for the default-branch view;
- one ETag'd read for each of the 23 bases, and one ETag'd membership list for each watched repo (43). These are free on a 304: MEASURED once for a list, INFERRED for branch reads;
- a few points for live reads, only when a head or a base moved.

That is about 120 GraphQL points an hour, against 5,000. The REST lists cost nothing while unchanged: the 304 was MEASURED for a list. Free 304s on branch reads are INFERRED. If that is wrong, the 23 base reads cost about 1,380 REST calls an hour at a 60 s cycle, against 5,000. Collection test 11 measures it before the cadence is fixed.

### Tests for the collection rules

Each rule above has a red test against a fake GitHub server:
1. A PR missing from a search result, with no REST change, publishes nothing.
2. Opened, closed and merged come from a REST list fixture, and each is published once.
3. UNKNOWN is re-read on the next cycle, never published, and after 3 cycles raises one health warning.
4. A suite at QUEUED with no workflow run is not "in progress"; a suite with a run is.
5. A base head that moves with no PR head change triggers a live read of the open PRs on that base, a stacked PR's parent included.
6. 101 open PRs are all read across two pages, and a health warning is raised.
7. A 403, a secondary rate limit, or a 429 backs off exponentially from a floor of 1 minute to the cap, and the generation time stays where it was. With no `Retry-After`, the first retry reaches the fake server no sooner than 1 minute after the failure. When `Retry-After` is present, no request reaches the fake server before it elapses.
8. A failed cycle leaves the generation time unchanged, and a reader shows `unknown` after 3 cycles.
9. A class that reaches its pacing ceiling makes no further requests in the fake server's log until the window rolls over, and the health record names the class.
10. A fast poll on a run that stays IN_PROGRESS past the ceiling drops back to the normal cycle.
11. **A manual measurement, run once before release.** A conditional branch read that returns 304 leaves `x-ratelimit-used` unchanged. If it does not, the cycle for base reads lengthens until they fit the REST ceiling.
12. **Reload.** On SIGHUP, a list file that has turned group-writable, or that another uid now owns, is refused: the old list stays in force and the health record says why. A valid new file takes effect, and its removed repos stop being fetched.
13. **Dotted repo at load.** A list entry `org/a.b` is refused with a warning naming it. The fake server logs no request for it, and other entries still load.

## 3. The core's guarantees

- **Fixed queries.** The query set is compiled in and read-only. The only parameter is a repo from the allowlist. There is no proxy and no pass-through.
- **Allowlist.** The watch list is a file, read at start and on SIGHUP. No socket or bus message can add to it. The core refuses to start, or to reload, unless the file is owned by the core's own uid and is writable by no one else. A 0644 file owned by another uid is refused.
  - A repo name that would not survive as one subject token (section 5) is refused at load, with a warning naming it.
- **CLI.** All verbs read the local view. None of them causes a forge call:
  - `<core> list --json`, `<core> watch --json`;
  - `<core> key <repo>`, `<core> watch-key <repo>`, `<core> open <repo>`;
  - `<core> select`, the Select Function provider;
  - `<core> quota <view> <window> [--image]`.

  `<core>` is a placeholder until the operator names the repository.
- **Derived fields only.** The view holds typed fields: state words, run numbers, timestamps, head SHA, `author_association`. It holds no token, no bodies and no titles. Free text shown on a display has control characters stripped and is truncated.
- **Access.** The view is a 0600 file, written by atomic rename, or a socket in a 0700 directory with a peer-uid check. There is no network listener. Every process under the same uid can read it; it holds only derived status.
- **Staleness.** Its generation timestamp is never refreshed on a failed cycle. A reader shows `unknown` after 3 cycles, never the last good value.

## 4. Credential

The reader needs a GitHub credential that can read and cannot write, and one shape cannot serve everyone. The operator observed that a per-org GitHub App works only where an org owner installs it: one App covers ArcavenAE, a second App could cover a second org, but neither helps a user who has no App, and neither reaches repos outside those orgs. So the credential splits in two:

- **the desk:** any user, on their own machine, reading repos they may not own;
- **the fleet:** the one reader whose events reach the bus.

Both shapes are recommendations on the operator's credential cards, voted by the design party, and are not yet ruled.

### The interim

The core shells out to `gh api graphql`, so it never holds a token. The login gh uses carries whatever scopes its owner granted. For the login in use, those include write scopes (MEASURED; recorded privately). Read-only then rests on the fixed query set alone. The operator is told the measured scopes plainly, in a private record, not here.

- **The grant check in report mode.** With the gh login as the source, the core prints the login's scopes and the word `interim` at start, and does not refuse. It refuses only for a dedicated source.
- **No publishing.** The publishing refusal below means the interim never feeds the bus.

### How the grants are read

At start the core reads its credential's grants and refuses any write grant on a dedicated source.

- A classic OAuth token reports `X-OAuth-Scopes`. MEASURED on the gh login (measured only: not checked against GitHub's documentation).
- An App installation token carries a `permissions` object in the mint response. INFERRED, not yet measured.
- How to read a fine-grained token's grants is UNVERIFIED. Such a token's scopes are shown as `unknown`, and a dedicated source whose grants cannot be read fails closed.

### The desk: layered, never upward

The design party voted this shape 6-0. The core picks its credential in this order:

1. **A dedicated gh profile, one per resource owner.** It is a separate `GH_CONFIG_DIR`, so the main login's config files are not written. The main login's keychain is another matter: any gh login into the profile, the bootstrap's included, rewrites a shared keychain entry (see "The keychain side effect" under the bootstrap). A write scope on a dedicated profile is refused at start.
2. **Else the user's own gh login, only with an explicit acknowledgment flag.** Without the flag the core does not start on the login.
3. **Never a fall-through upward.** A failed, revoked or missing profile token reports `unknown` with its reason and refuses. It never retries on the user's main login.

Each repo then reports `access: none | public | read | unknown:<reason>`, so a repo the credential cannot see reads as that, not as quiet.

- **The no-token diagnostic.** With no credential at all, the core runs REST only, for a short public list. It refuses to start with more than 5 repos, because unauthenticated REST allows 60 calls an hour and no GraphQL (MEASURED).
- **Storage.** The profile's token is stored with `--insecure-storage`, in plain text in the profile's `hosts.yml`, created with mode 0600 (see the bootstrap's "What is on disk afterwards"). With that flag gh does not write the token to the keyring (gh v2.102.0 `internal/config/config.go:431-434`). That is not the same as leaving the keyring alone: every login also rewrites a shared keychain entry, as the bootstrap's "The keychain side effect" sets out. Any process of the same uid can read that file; the design states this and does not solve it.
- **A gh version check at start.** A gh upgrade that changes how `GH_CONFIG_DIR` or `--insecure-storage` behaves would otherwise break the profile with no test noticing (INFERRED).

### The desk bootstrap: the tool fetches the no-scope token

The operator asked whether the tool can get the profile's token for the user, as needed (2026-10-19). It can, gated on one test.

- **How it works.** The tool runs GitHub's OAuth device flow against an OAuth App that ArcavenAE registers once, with the device flow enabled. The flow needs only the App's public client id, with no client secret, and requests no scope. A token with no scope grants read-only access to public information. The user approves one page in their browser. They need no membership in the ArcavenAE org. (GitHub docs: "Authorizing OAuth apps" and "Scopes for OAuth apps", read through a fetch summarizer.)
- **Why an App of the tool's own.** gh's own device flow always requests `repo`, `read:org` and `gist` (gh v2.102.0, `internal/authflow/flow.go:34`, MEASURED in the source).
- **The hand-off.** The tool passes the token to gh through stdin, with `gh auth login --with-token --insecure-storage` into the dedicated profile. For `--with-token`, gh runs two checks before it stores anything, and either one failing ends the login with nothing written (gh v2.102.0 `pkg/cmd/auth/login/login.go:206-216`, where the store is line 216; MEASURED in the source):
  1. the minimum-scope check, which passes on an empty scopes header (`pkg/cmd/auth/shared/oauth_scopes.go:72-77`);
  2. a GraphQL query, `query UserCurrent{viewer{login}}`, to learn whose token it is (`pkg/cmd/auth/shared/login_flow.go:263-275`). An error here fails the login with `error retrieving current user`.

  So the hand-off itself needs GraphQL: a token that GraphQL refuses never reaches gh. The tool holds the token in memory only. It never writes it to disk itself, never puts it in an argument or the environment, and keeps no copy once gh has it. A failed or revoked token offers the device flow again, under the never-upward rule above.
- **What is on disk afterwards.** gh writes the token, in plain text, under the `oauth_token` key in `hosts.yml` in the profile's config directory (gh v2.102.0 `internal/config/config.go:419-434`; go-gh v2.16.1 `pkg/config/config.go:232` for the file name, and `:334`, which creates it with mode 0600). That file is the one the storage bullet above describes. "The tool keeps nothing" means the tool's own process and files; gh's profile does hold the token.
- **The keychain side effect.** Every gh login, `--insecure-storage` included, ends in `activateUser` (gh v2.102.0 `internal/config/config.go:452`). That function runs three steps (`:523-553`, MEASURED in the source):
  1. It deletes the keychain entry with service `gh:<host>` and an empty account (`:525`).
  2. If the keyring holds a token stored for the logging-in user, it copies that token into the empty-account entry (`:533-534`).
  3. It records the active user in the profile's `hosts.yml`.

  The service name is `"gh:" + host` (`:577-579`). The keyring is go-keyring v0.2.8 (gh `go.mod:59`), which on macOS runs `/usr/bin/security` against the user's own keychains (gh `docs/macos-keyring.md:15`). It writes an entry by sending `add-generic-password -U -s <service> -a <account> -w <secret>` to `security -i`, with an empty account quoted as `''` (go-keyring v0.2.8 `keyring_darwin.go:76-101`, `internal/shellescape/shellescape.go:30-31`). It deletes one with `delete-generic-password -s <service> -a <account>`, passing `""` for the empty account (`keyring_darwin.go:104-109`). Nothing in that name or path depends on `GH_CONFIG_DIR`, so a login into a dedicated profile touches the same entries as the user's main login. What that does to the main login is INFERRED, not measured, and it turns on one unknown:
  - **How `security` treats an empty account** is UNVERIFIED, for both the delete and a lookup. If `-a ""` matches only an entry whose account is empty, step 1 removes only that entry. If it matches any account under the service, step 1 can delete the main user's own entry instead, and the main login loses its token.
  - In the first case, the main login likely keeps working: gh resolves its token from the entry stored for the active user first (`:265-266`), and uses the empty-account entry only when that fails (`:273`).
  - Either way, the empty-account entry itself is deleted at `:525`, if the delete reaches it. In the only-empty case it is then rewritten with the main token when the bootstrap's GitHub account is the main login's account, and left absent when it is a different account. In the any-account case, which entry the delete removed, and what `:533-534` then writes, is what the measurement below answers.
  - Shipped gh has no switch to point it at a mock or separate keyring: its mock is used only from `internal/config/test.go:130`.

  So the bootstrap writes to the user's keychain, and the design states that as a cost. Before the bootstrap is built, the effect is measured on a disposable machine ("The keychain measurement", after the section 6 table). If the measurement shows the main login breaking, the recommendation is to drop the bootstrap.
- **Lifetime.** The token has no expiry and no refresh token. GitHub revokes it after a year unused, and the user can revoke it in their settings at any time (GitHub docs, "Token expiration and revocation", read through a fetch summarizer).
- **Custody.** After the hand-off, gh holds the token in that file and the tool holds nothing, so the steady state is delegation. For the moment of the hand-off, the tool receives a long-lived token from GitHub and passes it on: a broker step. The custody rule allows brokering a short-lived credential; this one is long-lived, so whether the step fits is the operator's call, and the credential card says so.
- **The gate.** GitHub's GraphQL docs say a classic token needs `public_repo` to read public repos, and `public_repo` includes write. They say nothing about a token with no scope, and nothing about `viewer`. Two GraphQL reads matter, and they can differ:
  - `viewer{login}`, which the hand-off needs (above);
  - the repository views of section 2, which the core needs.

  One operator test answers both, with one approval and the token revoked at the end. It reports REST, a private repo the token must not see, the scopes header and each GraphQL read. It runs no gh login, because of the keychain side effect above. Instead it reports whether gh would take the token, predicted from the two inputs gh's `--with-token` path checks, which it measures directly. Nothing in this subsection is built before it reports, and before the keychain measurement in section 6 has run.
- **The outcomes, and the way on from each:**
  - **Both GraphQL reads work.** The bootstrap is built as described.
  - **`viewer` works, the repository views do not.** gh takes the token, and the core reads through `gh api` REST only: 5,000 calls an hour, public data. Section 2's state view then needs REST equivalents, which is a design change of its own before any build.
  - **`viewer` is refused.** gh refuses the token, so there is no path through gh. Any REST-only path would have the tool hold the token itself: in a `GH_TOKEN` it sets for every call, or in a file it writes. Either is custody by the tool, so the recommendation is no REST-only fallback for this outcome. The way on is to re-run the same test with a GitHub App that has no permissions and user-token expiry turned off, in place of the OAuth App. Its user token passes through the same two gh checks, so it needs `viewer` too (INFERRED, not tested). Expiry must stay off: with it on, the tool would need a refresh token, which is custody. If that is refused as well, the recommendation is to drop the bootstrap and keep the layered order without it.

### The fleet: Apps in the orgs the operator owns

The design party voted this shape 6-0.

- **The App.** A read-only GitHub App in each org the operator owns, personal-account installs included, installed on the watched repos only. That an installation token lasts 1 h, and that an installation can be limited to selected repos, are INFERRED from GitHub's App model, not yet measured, and UNVERIFIED against GitHub's documentation.
- **The minter.** A minter holds the App key and writes an installation token. It runs as its own local user on the core's host (round 7, 6-0). The key is 0600 and owned by that user. The core's user can read the token file and never the key, so the same-uid reader the earlier draft named cannot reach the key.
- **Permissions:** metadata, contents, pull requests, checks, commit statuses, deployments and actions, all read. INFERRED from the queries in section 2, not yet checked against GitHub's permission tables. The start-up grant check is the test.
- **Nothing outside the owned orgs reaches the bus.** A seat that needs a repo outside them reads it through the desk path, on its own machine, and its reads never become fleet events.
- **The fine-grained PAT: the recommendation is to drop it** as a fleet option (5-1, not yet ruled). The dissent, recorded: keep a 90-day read-only PAT as a stated fallback where an org owner will not install an App, since it gives a dated, revocable path, with the card naming the one account it ties the reader to.
- **Date.** No date is set. The operator sets it on the credential card. The card recommends about two weeks after it is ruled.
- **This is an exception to the custody rule.** The rule says a component must not hold an artifact that is bearer authority at a third party, and an App key is. Only the operator's ruling can grant the exception. The design records that it asks for one.

**gh-ingress refuses to publish while the core reports a gh login or a desk profile as its credential source,** so a desk credential never becomes fleet events.

## 5. What gh-ingress adds

- **Subjects.** `events.<fleet>.gh.<org>.<repo>.<number|repo>.<kind>.<action>`, built by the shared package. Repo-level kinds cover default-branch CI and deployment status.
- **Subject tokens.** Every GitHub-derived token placed in a subject (org, repo, number, kind, action) is checked against `[A-Za-z0-9_-]`. A failure is dropped, logged and counted in the health record. It is never hashed or rewritten (vote V4, 6-0). A dotted repo name such as `a.b` would split the subject, so it is never published; the core warns about it at load (section 3).
- **`Nats-Msg-Id`** is derived from the fact: object id, kind, action, and `updated_at` or head SHA, plus the window number for derived ages such as `review.stale`. A webhook delivery GUID, if one exists later, goes in a header only.
- **The watch list.** The core's allowlist is the ceiling. A fleet registry may narrow what gh-ingress publishes, never widen what the core fetches.
- **Heartbeat.** Published every cycle on a subject only gh-ingress may publish. It carries the core's last successful generation time, not the adapter's own liveness, so a live adapter over a stale core reads as stale.
- **Durables** match the inbox durables on main: `DeliverAllPolicy` within the stream's 24 h retention, and a 73 h inactivity threshold (round 7, 6-0). A seat that is down for a while gets its cues on return.
- **Silence controls,** each with its threshold, its reader and its recipient. All are required before any seat relies on the bus.

| failure | control | threshold | reader | recipient |
|---|---|---|---|---|
| core stale, or adapter dead | heartbeat carrying the core's generation time | age over 3 core cycles (180 s at 60 s) | director-mcp on the seat's turn; the watchdog every 60 s | the seat; the role's supervisor via director |
| healthy producer, silent consumer (the 195 h case) | the watchdog reads `CONSUMER.INFO` for each expected durable | pending plus ack-pending above 0, and the oldest unacked event or the consumer's last activity older than the role's window | the watchdog | the role's supervisor via director; director too after twice the window; and one local notice that does not ride the bus |
| expected durable absent | the same read | a role the registry expects has no durable | the watchdog | as above |
| seat away longer than retention | gap signal | the durable's ack floor is below the stream's first sequence, or the durable was reaped | director-mcp at attach | the seat, which sweeps before trusting the stream |
| restart | start-up sweep duty in role text | every start | the seat | itself |

  - **The window** comes from the role's text, its one source. A seat that is intentionally unregistered is skipped.
  - **The watchdog's grant** is `$JS.API.CONSUMER.INFO` on the named durables only, with no ack, no delete and no create.
  - **Before JetStream schedules exist,** a launchd job runs the check every 60 s. It is OS-owned, so it outlives a session. It is a clock for the check only, never for a role's work.
  - **Limits, stated, not solved:**
    - The check is only as loud as its host. launchd does not fire while the host sleeps (INFERRED), and no always-on second host is measured. The host goes to the operator.
    - A time-based duty on a quiet repo has no pending event until role timers exist, so the check cannot see that duty stop. Session crons remain the only clock for it until then.
    - A seat that acks every event and then ignores it passes every control.
    - On an unauthenticated broker, any local process can forge an all-clear, so an alert is a cue and never proof.

  The tests for director-mcp and the watchdog cover each row's failure path: the 195 h case in a fixture (a fresh heartbeat, a pending event older than the window), a seat silent between 24 h and 73 h, an expected durable that is absent, and a quiet repo that stays silent.

## 6. The constraints, each with its test

| constraint | test that fails if the design is wrong |
|---|---|
| core without marvel | No `MARVEL_SOCKET` and no marvel daemon: `<core> list --json` returns the view. |
| core without NATS | No broker reachable and the adapter disabled: the view is returned, the log shows no connect attempt, and the core's dependency tree has no NATS client. |
| core without a desk plugin | Plugin absent: `<core> watch` prints a change line within one cycle. |
| each present | The view bytes are identical with marvel, NATS and the plugin present. |
| NATS present, marvel absent | The broker carries a hand-written authorization block, and there is no marvel binary or socket. gh-ingress publishes its subjects, and a second user is denied publish on the heartbeat subject. The grant rows marvel renders (aae-orc-lgthv) are one way to write that block; none of them is required. |
| the display contract | Every `<core> key` line exits 0 and starts with a word from `passing`, `failing`, `running` or `unknown`. This holds in every state, stale and error included. A desk-controller plugin's own design defines the same rules and reads the same fixture file. |
| no caller-chosen fetch: an unlisted repo | `<core> key other/repo` and an adapter request for it each leave the fake server's log at zero requests. |
| no caller-chosen fetch: a raw query | No CLI flag, socket message or bus message takes query text. A test sends GraphQL text through each input, and the log shows only the compiled queries. |
| no write call | The HTTP layer issues only GET, plus POST to `/graphql` with a compiled document. The fake server fails the test on any other method or path, so a REST write fails it as well as a GraphQL `mutation`. In the interim, a recording `gh` shim on `PATH` logs every argv. The test fails on any argv other than `api graphql` with a compiled query, or `api -X GET` on an allowlisted path. A source check for `mutation` stays as a second line. |
| no caller-chosen fetch: a URL | A URL passed through each input is refused, and the log shows no request to it. |
| no stranger widens the list | A bus or socket "add" leaves the list and the request log unchanged. A group- or world-writable file stops start-up. So does a file whose owner uid is not the core's. The test injects a `stat` result with owner set to another uid and mode 0644, and asserts that start-up and reload both refuse, so an owner check that only runs when the file is writable fails it. A test cannot `chown` without root, which is why the result is injected. |
| nothing sensitive leaves | Token-shaped strings, and canary strings in fixture titles, bodies, branch names, commit status descriptions, check names and deployment descriptions, never appear in the view, logs, CLI output or events. A positive control proves the scan works. |
| the token stays out of env and argv | A canary token is placed in the credential source. A recording spawner shows that no child's argv or environment carries it. `ps -E -o command= -p <pid>` on the core shows no canary in its argv or environment. In the interim the core sets no `GH_TOKEN` or `GITHUB_TOKEN` for `gh`, and `gh` reads its own login. |
| the minter's files | The App key is 0600 and owned by the minter user, so the core's uid gets EACCES reading it. The token file is 0640, owned by the minter user, in a group whose only member is the core's user. A third uid gets EACCES on both files. Creating the users needs root, so this test runs only in the core repo's `minter-files` CI job, on a disposable GitHub-hosted macOS runner that can `sudo` (INFERRED that hosted runners allow it), and never on a fleet host. The job is created with the repo. Run anywhere else, it reports skipped, never passed. |
| no unknown readers | A second uid is refused at the socket. `lsof -nP -a -p <pid> -iTCP -sTCP:LISTEN` and `lsof -nP -a -p <pid> -iUDP` both print nothing for the core's pid, so it has no listener of either kind. Its only inet sockets are outbound TCP to the forge, seen with `lsof -nP -a -p <pid> -i`. |
| no publishing from a desk credential | With the credential source set to a gh login, or to a desk profile, and the adapter enabled, gh-ingress refuses and says why. |
| desk: never upward | A dedicated profile whose token the fake server answers with 401 reports `unknown:<reason>` and refuses. A recording `gh` shim shows no call that runs on the user's main login. Without the acknowledgment flag, the main login as the only source refuses at start. |
| desk: the no-token diagnostic | With no credential and 6 repos in the list, start-up refuses. With 5, it runs, and the fake server's log shows REST calls only, no `/graphql`. |
| desk bootstrap: the token stays out of the tool | A fake device-flow server returns a canary token. A recording spawner shows the canary only on the stdin of `gh auth login --with-token --insecure-storage`, never in any child's argv or environment, and a scan of every file the tool wrote finds no piece of the canary. |
| desk bootstrap: the keychain entry | The keychain measurement below the table. It fails closed when no reader can single out gh's entry, and fails when a canary appears, when the main user's own entry changes, or when the main login stops resolving. |
| desk bootstrap: gh refuses a token GraphQL refuses | gh reaches the fake server with `--hostname github.localhost`, which it maps to plain HTTP at `api.github.localhost` for REST and `/graphql` (gh v2.102.0 `internal/ghinstance/host.go:66-67` and `:53-54`). The keychain service is then `gh:github.localhost`, never `gh:github.com`, but the positive control still runs `activateUser` against it. So the row runs only on a disposable CI runner, never on a fleet host. Against a fake server that passes the scopes check (empty `X-OAuth-Scopes`) and answers `/graphql` with an error, the hand-off into a scratch profile exits nonzero with `error retrieving current user`, and the profile's `hosts.yml` holds no `oauth_token`. A positive control with a fake `/graphql` that answers `viewer{login}` exits 0, and `hosts.yml` then holds the canary token. This pins gh's behaviour, so a gh upgrade that changes it fails here. |
| desk bootstrap: the operator test | The test of section 4 reports `viewer{login}` and the repository views as separate lines. It runs no gh login, so there is no hand-off exit code: it reports whether gh would take the token, predicted from the scopes header and the `viewer{login}` result, which are the two inputs gh's `--with-token` path checks (`login.go:206-216`). It fails if any gh auth command runs. The bootstrap is built only on the first outcome, or on the second once its REST design change exists. |
| least-privilege token | With a dedicated credential that carries a write grant, start-up fails and names the grant. With the gh login as source, start-up prints the scopes and `interim` and runs (report mode). The interim fails least privilege by design until the operator's date (section 4). |
| grants fail closed | With a dedicated credential, the grant read is faked to error (a network failure or a 5xx), to return an unknown shape (no scopes header, no `permissions` object, or an unrecognised field), and to return an empty grant set. Start-up refuses in each case, names the reason, and makes no collection request. |
| viewing never calls the forge | Every CLI verb in section 3, every quota view and window, and `--image` is run 100 times against the fake forge, and its request log does not change. A manual check against GitHub reads `x-ratelimit-used` before and after: unchanged. |
| an image carries no active content | `<core> quota ... --image` output passes the image rules in section 8, and is checked by image tests 1 to 4 there. |
| subject tokens | A repo `a.b` in a fixture publishes nothing, and logs and counts one drop. A repo `a-b_1` publishes. |
| dedupe | The same fact read twice gives one `Nats-Msg-Id`. A changed `updated_at` or head SHA gives a new one. A `review.stale` in the same window gives the same id, and in the next window a new one. A webhook GUID in a header does not change the id. |
| webhook receiver (later) | A bad or missing `X-Hub-Signature-256` gets 401 and changes no state. The header's name and its HMAC-SHA256 scheme are INFERRED from GitHub's webhook model, and get checked in that build's own review. |

### The keychain measurement

Run on a disposable macOS user or runner, never on a fleet host or the operator's account, because the test changes the keychain. Run it against the fake server through `--hostname github.localhost` (as in the next row), so it needs no real token and its entries carry the service `gh:github.localhost`; the code path is the same as for `github.com` (`config.go:523-553`, `:577-579`).

Secrets are never read. Each plant's secret is a canary, and every output the measurement captures is checked for it: a canary seen anywhere stops the measurement as a failure. Two readers are allowed, and step 1 decides which one steps 2 and 3 use:
- **The lookup:** `security find-generic-password -s gh:github.localhost -a <account>`, with neither `-g` nor `-w`, which prints the item's attributes, `acct` and `mdat` among them, and not its data.
- **The listing:** `security dump-keychain <the disposable user's keychain>`, with none of `-a`, `-d`, `-i` or `-r`. Every item under the service is listed with its `acct` and `mdat`. That this prints no item data rests on security(1), which offers `-d` for decrypted data and `-r` for raw data (INFERRED from the man page wording; the canary check is the test).

1. **Prove the reader, both ways, before anything else.**
   - Plant a positive entry exactly the way gh writes one: send `add-generic-password -U -s gh:github.localhost -a '' -w <canary>` to `security -i`, the same form as go-keyring `keyring_darwin.go:86`. A lookup with `-a ""` must find it, with an empty `acct`. If it does not, the measurement fails closed and nothing after it runs.
   - Plant a negative entry the same way, with account `main-user` and no empty-account entry beside it. Run the same `-a ""` lookup, then plant both entries and run it once more.
   - **If the `-a ""` lookup never returns the `main-user` entry,** it matches only the empty account. The lookup is the reader for steps 2 and 3: `-a ""` for gh's entry, `-a main-user` for the main user's.
   - **If it returns the `main-user` entry in either run,** it matches any account, and a lookup cannot single out gh's entry. The listing becomes the reader. With both plants present, the listing must show two items under the service, one with an empty `acct` and one with `acct` `main-user`, each with an `mdat`. If it does not, the measurement fails closed and reports that no reader can single out gh's entry.
2. **Answer the delete question,** with the reader from step 1. With only the `main-user` entry planted, run `security delete-generic-password -s gh:github.localhost -a ""`, the argv of go-keyring `keyring_darwin.go:104-109`, and record whether the `main-user` entry survives. Then plant both entries and repeat, and record which one is gone. This settles whether step 1 of `activateUser` (`config.go:525`) can delete the main user's own entry.
3. **Measure the login,** with the same reader. Remove the plants. Seed a main login in the default config, stored in the keyring, whose fake `viewer` answers `main-user`. Read both entries: presence and `mdat`. Run the bootstrap into a dedicated `GH_CONFIG_DIR` twice, once with the fake `viewer` answering `main-user` and once answering `other-user`, and read both entries after each run. Record `gh auth status` and `gh api user` on the main config after each run.

**Verdict.** The source says `activateUser` deletes the empty-account entry on every login (`:525`), so a change to it is the expected result. It is reported, with its before and after presence and `mdat`, and is never left unobserved. The measurement fails in any of these cases:
- it cannot single out gh's entry (step 1);
- a canary appears in any captured output;
- the main user's own entry changes or disappears;
- the main login stops resolving.

No outcome passes without having read both entries before and after each run.

## 7. Sequencing

1. **Now:** the core, its tests and its CLI. It publishes nowhere, so the broker's authorization work does not gate it. It runs on the interim credential once the operator has ruled the credential card, which states the login's scopes.
   - The desk path (section 4) is built with the core once the desk credential card is ruled. Its bootstrap is built only after the operator test reports on GraphQL.
2. **After the broker's authorization block is live on every broker that `events.<fleet>` reaches**, meaning each host's local broker, the hub and any leaf (aae-orc-umw8p, director#4): streams, grants, director-mcp's event tools, and gh-ingress publishing. This also needs the target credential, because of the refusal in section 4.
3. **Later:** a webhook receiver on an always-on reachable host, with its own review. The poller stays as reconciliation.

## 8. Other forges and quota

**Providers.** The core reaches forges through a provider seam:
- fixed read-only operations per forge;
- an auth source named by reference;
- a typed status record;
- a quota record made of named buckets, each with its own window.

Only GitHub is built first. The seam is tested with a fake second provider. The test fails if the core needs any GitHub-specific field to handle the fake provider's records, or if a provider with no quota buckets shows anything but `unreported`. GitLab's admin pages describe per-minute rate-limit headers, and Forgejo's config reference names no API limit. Both were read through a summarizer and not collected, so both are unverified. Forgejo's quota is therefore treated as unknown: it is reported as unreported, never as zero or unlimited, and the core paces at an owner-set ceiling.

gh-ingress publishes only what the core's providers deliver. Subjects already carry the forge token (`gh` today), so another forge is a new family, not a change to this one.

**Quota.** The core samples its identity's quota once a minute; on GitHub that read is free (MEASURED, section 2).
- **The series:** an append-only file, mode 0600, owned by the core's user. It holds 7 days at 1-minute resolution, plus a 3 s ring for the last hour.
- **Unexplained spend:** a drop in remaining larger than the core's own counted calls (retries included), beyond a tolerance, inside one reset window and never across a reset. It is shown as "unexplained spend", with no verdict on who caused it. The tolerance is set from real sampling before the flag is trusted.
- **Process attribution:**
  - optional and off by default;
  - local only, own uid only;
  - records the process name and uid, never command lines or environment;
  - every result is labelled `candidate`.
- **The bus:** gh-ingress may publish a quota health summary (remaining, reset, the flag) on its heartbeat. It never publishes the series detail or any attribution.
- **Viewing** never causes a forge call (section 6).
- **A desk controller as a quota console:** views and windows are `<core> select` entries, a press opens settings or a local report, and charts arrive as bounded images (section 8, image rules). The core checks its own output against the image rules below, so a consumer receives no active content.

Tests:
1. The series file is created 0600, and a sample older than 7 days is trimmed.
2. A fixture where remaining drops by 50 while the core counted 10 raises the flag. A drop across a reset does not. A retried 5xx is counted as the core's own spend.
3. With attribution off, no `lsof` or `nettop` is spawned (recording spawner).
4. With attribution on, output is labelled `candidate` and carries name and uid only. A canary placed in a fixture process's arguments never appears.
5. The heartbeat summary carries remaining, reset and the flag, and no series or attribution field. With attribution on and a canary process name in its fixture, the canary appears in no bus event, health row or log file. It appears only in the local attribution output.
6. A quota view for an identity that is not configured is refused, and the fake forge's log shows zero calls.
7. With the sampler stopped, every quota view reads `unknown` after 3 cycles, and an `--image` output carries no chart.

**Image rules.** `--image` prints either a PNG data URI, or an SVG data URI in base64 or percent-encoded UTF-8. The SVG holds no `script`, no `foreignObject`, no `on*` attribute, no `href` or `xlink:href` except to a fragment, no CSS `url()` except to a fragment, and no `@import`. Each image is at most 64 KiB. Label text drawn into a chart is escaped as character data.

Image tests:
1. A fixture whose labels carry each forbidden construct produces an SVG that holds none of them, checked by parsing the output, in both encodings.
2. A positive control: the same checker rejects a hand-written SVG holding each construct.
3. Output stays under 64 KiB at the largest window and view.
4. A stale series yields no image (quota test 7).

## 9. Work items

| item | ticket | depends on |
|---|---|---|
| shared subject and envelope package | aae-orc-5wmeg | |
| status core (new repo) | to be filed when the operator names the repo | |
| desk credential path and bootstrap | to be filed when the desk card is ruled and the operator test reports | the status core |
| fleet App and minter | to be filed when the fleet card is ruled | the status core |
| gh-ingress adapter | aae-orc-bstdv | the core, aae-orc-5wmeg, aae-orc-aubd6 |
| streams | aae-orc-aubd6 | aae-orc-umw8p |
| grant rows, including the heartbeat and registry grants | aae-orc-lgthv | aae-orc-umw8p |
| director-mcp tools, the heartbeat-age check and the gap signal | aae-orc-7vw44 | aae-orc-lgthv, aae-orc-aubd6, aae-orc-5wmeg |
| off-host watchdog | aae-orc-kht67 | aae-orc-bstdv |
| webhook receiver | aae-orc-i4im0 | aae-orc-bstdv |
| role text: wake on events, start-up sweep | aae-orc-jbx9y | aae-orc-7vw44, aae-orc-bstdv |
