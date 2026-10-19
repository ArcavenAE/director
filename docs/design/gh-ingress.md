# gh-ingress: GitHub status onto the fleet bus

- **Date:** 2026-10-19, revised the same day.
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
| membership: opened, closed, merged, new comments | REST lists by `updated_at`, per watched repo, with `If-None-Match` | A 304 did not count against the REST limit when measured: one conditional call left the used count unchanged. GitHub's documentation on this is not verified here. |
| state: open-PR `mergeable` and CI, default-branch CI, last deployment status | One batched GraphQL query per cycle, carrying `rateLimit` | MEASURED 2026-10-19: 1 point for the open-PR view on 78 open PRs, and 1 point for the default-branch and deployment view on 43 repos. |
| a PR whose head SHA changed this cycle | A live read of that PR | A few points, only when heads move. |
| a base branch that moved (default, release, or a stacked PR's parent) | A live read of the open PRs on that base, because `mergeable` can change with no head SHA change | ETag'd branch reads. Free on a 304 is INFERRED: the one measured conditional call was a list, not a branch read. |

Rules:
- Opened, closed and merged come only from the REST lists. Absence from a search result is never an event.
- A `mergeable` of UNKNOWN is re-read on the next cycle and never published.
- "A run in progress" counts only check suites that carry a workflow run. Suites registered for apps that never run sit at QUEUED. MEASURED 2026-10-19: all 58 QUEUED or IN_PROGRESS suites on default-branch heads lacked a workflow run.
- **Busy signal:** the `rate_limit` read, with exponential backoff to a cap. MEASURED 2026-10-19: three reads in a row left `used` at 526.
- **Fast poll:** only for a repo with a real run in progress, with a ceiling so a stuck run drops back.
- **Pacing ceilings.** Each call class (membership, state, live reads, quota) has an owner-set ceiling per reset window. A class that reaches its ceiling makes no more calls until the window rolls over, and says so in the health record.
- **Pagination** past 100 results is built from the first version. That GraphQL caps `first` at 100 is INFERRED from GitHub's API limits, not measured here. More than 100 open PRs is a health warning, never a silent truncation.
- **UNKNOWN is bounded.** A PR whose `mergeable` stays UNKNOWN for 3 cycles is reported as a health warning naming the PR, and is still never published as a state.

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
7. A 403, a secondary rate limit, or a 429 backs off exponentially to the cap, and the generation time stays where it was. When `Retry-After` is present, no request reaches the fake server before it elapses.
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

- **Interim.** The core shells out to `gh api graphql`, so it never holds a token. The login gh uses carries whatever scopes its owner granted. For the login in use, those include write scopes (MEASURED; recorded privately). Read-only then rests on the fixed query set alone. The operator is told the measured scopes plainly, in a private record, not here.
- **Target.** A dedicated read-only credential. The core reads the credential's grants at start and refuses any write grant.
  - **How the grants are read.**
    - A classic OAuth token reports `X-OAuth-Scopes`. MEASURED on the gh login.
    - An App installation token carries a `permissions` object in the mint response. INFERRED, not yet measured.
    - How to read a fine-grained PAT's grants is not verified. If none is found, the check fails closed: the core does not run on a credential whose grants it cannot read.
  - **Date.** No date is set. The operator sets it on the credential card. The card recommends about two weeks after it is ruled.
  - **The interim:** with the gh login as the source, the grant check runs in report mode. It prints the login's scopes and the word `interim` at start, and does not refuse. It refuses only for a dedicated source. The publishing refusal below means the interim never feeds the bus.
  - **App.** A GitHub App owned by the ArcavenAE org, installed on the watched repos only, with read-only permissions. A minter holds the App key and writes an installation token. That such a token lasts 1 h, and that an installation can be limited to selected repos, are INFERRED from GitHub's App model and not yet measured. The minter runs as its own local user on the core's host (round 7, 6-0). The key is 0600 and owned by that user. The core's user can read the token file and never the key, so the same-uid reader the earlier draft named cannot reach the key.
  - **PAT.** A fine-grained token with the ArcavenAE org as its resource owner, "only select repositories" set to the watch list, and read-only permissions, kept in its own keychain item.
  - **Permissions,** for either: metadata, contents, pull requests, checks, commit statuses, deployments and actions, all read. INFERRED from the queries in section 2, not yet checked against GitHub's permission tables. The start-up grant check is the test.
- **This is an exception to the custody rule.** The rule says a component must not hold an artifact that is bearer authority at a third party, and both shapes are. Only the operator's ruling can grant the exception. The design records that it asks for one.
- **gh-ingress refuses to publish while the core reports a gh login as its credential source,** so a desk shortcut never becomes fleet events.

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
| no stranger widens the list | A bus or socket "add" leaves the list and the request log unchanged. A group- or world-writable file stops start-up. So does a file whose owner uid is not the core's, tested over an injected `stat` result because a test cannot `chown` without root. |
| nothing sensitive leaves | Token-shaped strings, and canary strings in fixture titles, bodies, branch names, commit status descriptions, check names and deployment descriptions, never appear in the view, logs, CLI output or events. A positive control proves the scan works. |
| the minter's files | The App key is 0600 and owned by the minter user, so the core's uid gets EACCES reading it. The token file is 0640, owned by the minter user, in a group whose only member is the core's user. A third uid gets EACCES on both files. |
| no unknown readers | A second uid is refused at the socket. `lsof -nP -a -p <pid> -iTCP -sTCP:LISTEN` and `lsof -nP -a -p <pid> -iUDP` both print nothing for the core's pid, so it has no listener of either kind. Its only inet sockets are outbound TCP to the forge, seen with `lsof -nP -a -p <pid> -i`. |
| no publishing from a gh login | With the credential source set to a gh login and the adapter enabled, gh-ingress refuses and says why. |
| least-privilege token | With a dedicated credential that carries a write grant, start-up fails and names the grant. With the gh login as source, start-up prints the scopes and `interim` and runs (report mode). The interim fails least privilege by design until the operator's date (section 4). |
| viewing never calls the forge | Every CLI verb in section 3, every quota view and window, and `--image` is run 100 times against the fake forge, and its request log does not change. A manual check against GitHub reads `x-ratelimit-used` before and after: unchanged. |
| an image carries no active content | `<core> quota ... --image` output passes the image rules in section 8, and is checked by image tests 1 to 4 there. |
| subject tokens | A repo `a.b` in a fixture publishes nothing, and logs and counts one drop. A repo `a-b_1` publishes. |
| dedupe | The same fact read twice gives one `Nats-Msg-Id`. A changed `updated_at` or head SHA gives a new one. A `review.stale` in the same window gives the same id, and in the next window a new one. A webhook GUID in a header does not change the id. |
| webhook receiver (later) | A bad or missing `X-Hub-Signature-256` gets 401 and changes no state. The header's name and its HMAC-SHA256 scheme are INFERRED from GitHub's webhook model, and get checked in that build's own review. |

## 7. Sequencing

1. **Now:** the core, its tests and its CLI. It publishes nowhere, so the broker's authorization work does not gate it. It runs on the interim credential once the operator has ruled the credential card, which states the login's scopes.
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
5. The heartbeat summary carries remaining, reset and the flag, and no series or attribution field.
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
| gh-ingress adapter | aae-orc-bstdv | the core, aae-orc-5wmeg, aae-orc-aubd6 |
| streams | aae-orc-aubd6 | aae-orc-umw8p |
| grant rows, including the heartbeat and registry grants | aae-orc-lgthv | aae-orc-umw8p |
| director-mcp tools, the heartbeat-age check and the gap signal | aae-orc-7vw44 | aae-orc-lgthv, aae-orc-aubd6, aae-orc-5wmeg |
| off-host watchdog | aae-orc-kht67 | aae-orc-bstdv |
| webhook receiver | aae-orc-i4im0 | aae-orc-bstdv |
| role text: wake on events, start-up sweep | aae-orc-jbx9y | aae-orc-7vw44, aae-orc-bstdv |
