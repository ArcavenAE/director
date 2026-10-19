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
| status core | A Rust process that polls GitHub and keeps a local view. It reads with `ghstate list --json` and streams changes with `ghstate watch --json`. | a new small repository, still to be named |
| gh-ingress (this design) | A Go adapter that consumes `ghstate watch --json` and publishes INFORM events. | `cmd/gh-ingress` in director |
| any other consumer | A Stream Deck key or dial, a shell prompt, tmux, or a seat on the same host. Each reads the CLI. | wherever it lives |

The core imports no NATS, marvel or desk-plugin code. It works with or without each of them, and each works with or without the others.

gh-ingress is Go so that the subject builder and the envelope type stay shared with `cmd/director-mcp`. director-mcp is all `package main` today, so the first change extracts a small importable package for both. The envelope type is generated from its JSON Schema, not hand-ported.

## 2. What the core collects

| what | how | cost |
|---|---|---|
| membership: opened, closed, merged, new comments | REST lists by `updated_at`, per watched repo, with `If-None-Match` | A 304 did not count against the REST limit when measured: one conditional call left the used count unchanged. GitHub's documentation on this is not verified here. |
| state: open-PR `mergeable` and CI, default-branch CI, last deployment status | One batched GraphQL query per cycle, carrying `rateLimit` | 1 point for the open-PR view, measured on 78 open PRs. 1 point for the default-branch and deployment view, measured on 43 repos. |
| a PR whose head SHA changed this cycle | A live read of that PR | A few points, only when heads move. |
| a base branch that moved (default, release, or a stacked PR's parent) | A live read of the open PRs on that base, because `mergeable` can change with no head SHA change | ETag'd branch reads, free on 304. |

Rules:
- Opened, closed and merged come only from the REST lists. Absence from a search result is never an event.
- A `mergeable` of UNKNOWN is re-read on the next cycle and never published.
- "A run in progress" counts only check suites that carry a workflow run. Suites registered for apps that never run sit at QUEUED forever.
- **Busy signal:** the `rate_limit` read, which is free (measured), with exponential backoff to a cap.
- **Fast poll:** only for a repo with a real run in progress, with a ceiling so a stuck run drops back.
- **Pagination** past 100 results is built from the first version. More than 100 open PRs is a health warning, never a silent truncation.
- **UNKNOWN is bounded.** A PR whose `mergeable` stays UNKNOWN for 3 cycles is reported as a health warning naming the PR, and is still never published as a state.

**Cost per cycle, measured 2026-10-19 at 10:26Z.** There were 81 open PRs across 23 repos, on 23 distinct base branches, none of them a non-default base. The open-PR search cost 1 point. A 60 s cycle then spends:
- 1 GraphQL point for the open-PR view and 1 for the default-branch view;
- one ETag'd read for each of the 23 bases, and one ETag'd membership list for each watched repo (43). These are free on a 304 (measured once, above);
- a few points for live reads, only when a head or a base moved.

That is about 120 GraphQL points an hour, against 5,000. The REST spend is only the lists and branches that changed.

### Tests for the collection rules

Each rule above has a red test against a fake GitHub server:
1. A PR missing from a search result, with no REST change, publishes nothing.
2. Opened, closed and merged come from a REST list fixture, and each is published once.
3. UNKNOWN is re-read on the next cycle, never published, and after 3 cycles raises one health warning.
4. A suite at QUEUED with no workflow run is not "in progress"; a suite with a run is.
5. A base head that moves with no PR head change triggers a live read of the open PRs on that base, a stacked PR's parent included.
6. 101 open PRs are all read across two pages, and a health warning is raised.
7. A 403 or a secondary rate limit backs off exponentially to the cap, and the generation time stays where it was.
8. A failed cycle leaves the generation time unchanged, and a reader shows `unknown` after 3 cycles.

## 3. The core's guarantees

- **Fixed queries.** The query set is compiled in and read-only. The only parameter is a repo from the allowlist. There is no proxy and no pass-through.
- **Allowlist.** The watch list is a file owned by the token's owner, read at start and on SIGHUP. No socket or bus message can add to it. The core refuses to start if the file is group- or world-writable.
- **Derived fields only.** The view holds typed fields: state words, run numbers, timestamps, head SHA, `author_association`. It holds no token, no bodies and no titles. Free text shown on a display has control characters stripped and is truncated.
- **Access.** The view is a 0600 file, written by atomic rename, or a socket in a 0700 directory with a peer-uid check. There is no network listener. Every process under the same uid can read it; it holds only derived status.
- **Staleness.** Its generation timestamp is never refreshed on a failed cycle. A reader shows `unknown` after 3 cycles, never the last good value.

## 4. Credential

- **Interim.** The core shells out to `gh api graphql`, so it never holds a token. The login gh uses carries whatever scopes its owner granted, which for an interactive login usually includes write scopes. Read-only then rests on the fixed query set alone. The operator is told the measured scopes plainly, in a private record, not here.
- **Target.** A dedicated read-only credential. The core reads the credential's grants at start and refuses any write grant. The operator chooses the date.
  - **App.** A GitHub App owned by the ArcavenAE org, installed on the watched repos only, with read-only permissions. A minter holds the App key and writes a 1 h installation token. The minter runs as its own local user on the core's host (round 7, 6-0). The key is 0600 and owned by that user. The core's user can read the token file and never the key, so the same-uid reader the earlier draft named cannot reach the key.
  - **PAT.** A fine-grained token with the ArcavenAE org as its resource owner, "only select repositories" set to the watch list, and read-only permissions, kept in its own keychain item.
  - **Permissions,** for either: metadata, contents, pull requests, checks, commit statuses, deployments and actions, all read. INFERRED from the queries in section 2, not yet checked against GitHub's permission tables. The start-up grant check is the test.
- **This is an exception to the custody rule.** The rule says a component must not hold an artifact that is bearer authority at a third party, and both shapes are. Only the operator's ruling can grant the exception. The design records that it asks for one.
- **gh-ingress refuses to publish while the core reports a gh login as its credential source,** so a desk shortcut never becomes fleet events.

## 5. What gh-ingress adds

- **Subjects.** `events.<fleet>.gh.<org>.<repo>.<number|repo>.<kind>.<action>`, built by the shared package. Repo-level kinds cover default-branch CI and deployment status.
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
| core without marvel | No `MARVEL_SOCKET` and no marvel daemon: `ghstate list --json` returns the view. |
| core without NATS | No broker reachable and the adapter disabled: the view is returned, the log shows no connect attempt, and the core's dependency tree has no NATS client. |
| core without a desk plugin | Plugin absent: `ghstate watch` prints a change line within one cycle. |
| each present | The view bytes are identical with marvel, NATS and the plugin present. |
| no caller-chosen fetch: an unlisted repo | `ghstate key other/repo` and an adapter request for it each leave the fake server's log at zero requests. |
| no caller-chosen fetch: a raw query | No CLI flag, socket message or bus message takes query text. A test sends GraphQL text through each input, and the log shows only the compiled queries. A source check fails on any `mutation`. |
| no caller-chosen fetch: a URL | A URL passed through each input is refused, and the log shows no request to it. |
| no stranger widens the list | A bus or socket "add" leaves the list and the request log unchanged. A group-writable file stops start-up. |
| nothing sensitive leaves | Canary strings in fixture titles and bodies, plus token-shaped strings, never appear in the view, logs, CLI output or events. A positive control proves the scan works. |
| no unknown readers | A second uid is refused. `lsof -nP -iTCP -sTCP:LISTEN` shows nothing for the core's pid. |
| no publishing from a gh login | With the credential source set to a gh login and the adapter enabled, gh-ingress refuses and says why. |
| least-privilege token | A scope check at start refuses any write grant. The interim fails this by design until the operator's date. |
| webhook receiver (later) | A bad or missing `X-Hub-Signature-256` gets 401 and changes no state. |

## 7. Sequencing

1. **Now:** the core, its tests and its CLI. It publishes nowhere, so the broker's authorization work does not gate it. It runs on the interim credential once the operator has seen the scope note.
2. **After the broker's authorization block is live on every broker that `events.<fleet>` reaches**, meaning each host's local broker, the hub and any leaf (aae-orc-umw8p, director#4): streams, grants, director-mcp's event tools, and gh-ingress publishing. This also needs the target credential, because of the refusal in section 4.
3. **Later:** a webhook receiver on an always-on reachable host, with its own review. The poller stays as reconciliation.

## 8. Other forges and quota

**Providers.** The core reaches forges through a provider seam:
- fixed read-only operations per forge;
- an auth source named by reference;
- a typed status record;
- a quota record made of named buckets, each with its own window.

Only GitHub is built first, and the seam is tested with a fake second provider. GitLab's admin pages describe per-minute rate-limit headers, and Forgejo's config reference names no API limit. Both were read through a summarizer and not collected, so both are unverified. Forgejo's quota is therefore treated as unknown: it is reported as unreported, never as zero or unlimited, and the core paces at an owner-set ceiling.

gh-ingress publishes only what the core's providers deliver. Subjects already carry the forge token (`gh` today), so another forge is a new family, not a change to this one.

**Quota.** The core samples its identity's quota once a minute; on GitHub that read is free. It keeps a local, owner-only series and flags unexplained spend: a drop larger than its own counted calls, inside one reset window.
- Process attribution is optional, off by default, local only, and records no command lines.
- gh-ingress may publish a quota health summary (remaining, reset, the flag) on its heartbeat. It never publishes the series detail or any attribution.
- Viewing quota never causes a forge call.

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
