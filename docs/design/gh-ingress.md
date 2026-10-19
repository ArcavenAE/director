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
| membership: opened, closed, merged, new comments | REST lists by `updated_at`, per watched repo, with `If-None-Match` | A 304 does not count against the REST limit. GitHub documents this, and a conditional call left the used count unchanged when measured. |
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

## 3. The core's guarantees

- **Fixed queries.** The query set is compiled in and read-only. The only parameter is a repo from the allowlist. There is no proxy and no pass-through.
- **Allowlist.** The watch list is a file owned by the token's owner, read at start and on SIGHUP. No socket or bus message can add to it. The core refuses to start if the file is group- or world-writable.
- **Derived fields only.** The view holds typed fields: state words, run numbers, timestamps, head SHA, `author_association`. It holds no token, no bodies and no titles. Free text shown on a display has control characters stripped and is truncated.
- **Access.** The view is a 0600 file, written by atomic rename, or a socket in a 0700 directory with a peer-uid check. There is no network listener. Every process under the same uid can read it; it holds only derived status.
- **Staleness.** Its generation timestamp is never refreshed on a failed cycle. A reader shows `unknown` after 3 cycles, never the last good value.

## 4. Credential

- **Interim.** The core shells out to `gh api graphql`, so it never holds a token. The login gh uses carries whatever scopes its owner granted, which for an interactive login usually includes write scopes. Read-only then rests on the fixed query set alone. The operator is told the measured scopes plainly, in a private record, not here.
- **Target.** A dedicated read-only credential, as a GitHub App installation token or a fine-grained read-only token. The core reads the credential's scopes at start and refuses any write grant. The operator chooses the date. Both shapes are custody under the fleet's rule, because a GitHub token is bearer authority at a third party.
- **gh-ingress refuses to publish while the core reports a gh login as its credential source,** so a desk shortcut never becomes fleet events.

## 5. What gh-ingress adds

- **Subjects.** `events.<fleet>.gh.<org>.<repo>.<number|repo>.<kind>.<action>`, built by the shared package. Repo-level kinds cover default-branch CI and deployment status.
- **`Nats-Msg-Id`** is derived from the fact: object id, kind, action, and `updated_at` or head SHA, plus the window number for derived ages such as `review.stale`. A webhook delivery GUID, if one exists later, goes in a header only.
- **The watch list.** The core's allowlist is the ceiling. A fleet registry may narrow what gh-ingress publishes, never widen what the core fetches.
- **Heartbeat.** Published every cycle on a subject only gh-ingress may publish. It carries the core's last successful generation time, not the adapter's own liveness, so a live adapter over a stale core reads as stale.
- **Silence controls.** Required before any seat relies on the bus:
  - director-mcp checks the heartbeat's age on the seat's behalf;
  - a gap signal when a durable is recreated;
  - an off-host watchdog;
  - a start-up sweep in each role.

  The tests for director-mcp cover each control's failure path.

## 6. The constraints, each with its test

| constraint | test that fails if the design is wrong |
|---|---|
| core without marvel | No `MARVEL_SOCKET` and no marvel daemon: `ghstate list --json` returns the view. |
| core without NATS | No broker reachable and the adapter disabled: the view is returned, the log shows no connect attempt, and the core's dependency tree has no NATS client. |
| core without a desk plugin | Plugin absent: `ghstate watch` prints a change line within one cycle. |
| each present | The view bytes are identical with marvel, NATS and the plugin present. |
| no caller-chosen fetch | A fake GitHub server logs zero requests for an unlisted repo, a raw query or a URL. A source check fails on any GraphQL `mutation`. |
| no stranger widens the list | A bus or socket "add" leaves the list and the request log unchanged. A group-writable file stops start-up. |
| nothing sensitive leaves | Canary strings in fixture titles and bodies, plus token-shaped strings, never appear in the view, logs, CLI output or events. A positive control proves the scan works. |
| no unknown readers | A second uid is refused. `lsof -nP -iTCP -sTCP:LISTEN` shows nothing for the core's pid. |
| no publishing from a gh login | With the credential source set to a gh login and the adapter enabled, gh-ingress refuses and says why. |
| least-privilege token | A scope check at start refuses any write grant. The interim fails this by design until the operator's date. |
| webhook receiver (later) | A bad or missing `X-Hub-Signature-256` gets 401 and changes no state. |

## 7. Sequencing

1. **Now:** the core, its tests and its CLI. It publishes nowhere, so the broker's authorization work does not gate it. It runs on the interim credential once the operator has seen the scope note.
2. **After the broker's authorization block is live** (aae-orc-umw8p, director#4): streams, grants, director-mcp's event tools, and gh-ingress publishing. This also needs the target credential, because of the refusal in section 4.
3. **Later:** a webhook receiver on an always-on reachable host, with its own review. The poller stays as reconciliation.

## 8. Other forges and quota

**Providers.** The core reaches forges through a provider seam:
- fixed read-only operations per forge;
- an auth source named by reference;
- a typed status record;
- a quota record made of named buckets, each with its own window.

Only GitHub is built first, and the seam is tested with a fake second provider. GitLab documents per-minute rate-limit headers. Forgejo documents none, so its quota is reported as unreported, never as zero or unlimited, and the core paces at an owner-set ceiling.

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
