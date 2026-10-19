# gh-ingress: one GitHub reader for the fleet

- **Date:** 2026-10-19
- **Status:** design for review. Nothing is built by this document.
- **Owner:** director. This is a build design for `cmd/gh-ingress`.

Today every agent seat that cares about GitHub polls it on its own, on its own clock. One host alone ran about 11,000 `gh` commands in a week. The clocks those seats use are session timers that die on restart, and one seat's sweeps once went silent for 195 hours with no alarm.

`gh-ingress` is one process per fleet that reads GitHub and publishes what changed as events on the fleet's NATS bus, so seats subscribe instead of polling. This document is the build design. The plane-wide rules (subjects, streams, grants) are the fleet's event-plane design, and this document restates only what the build needs.

## 1. What it is

- A Go binary at `cmd/gh-ingress`, run by launchd. It is a service, not a role, and its liveness is its health row and its heartbeat, not a presence row.
- It reads a registry of watched orgs and repos, polls GitHub, and publishes INFORM envelopes that carry pointers, not payloads:
  - URL, number, `kind.action`, author login, `author_association`, head SHA;
  - never bodies or titles.
- Subjects: `events.<fleet>.gh.<org>.<repo>.<number|repo>.<kind>.<action>`. The subject is a pure function of the `events://` address.
- **Shared code.** The subject builder and the envelope type are shared with `cmd/director-mcp`. director-mcp is all `package main` today, so the first change extracts a small importable package for both binaries. The envelope type is generated from its JSON Schema, not hand-ported.

## 2. Collection

The poll runs every 60 seconds.

| what | how | cost |
|---|---|---|
| membership (opened, closed, merged, new comments) | REST lists by `updated_at`, per watched repo, with `If-None-Match` | a 304 does not count against the REST limit (measured: the used count did not move across a conditional call) |
| open-PR state (`mergeable`, CI rollup) | one GraphQL search for open PRs in the watched orgs | 1 point for all 78 open PRs on 2026-10-19 (measured), from GraphQL's own 5,000 an hour |
| a PR whose head SHA changed this cycle | a live GraphQL read of that PR | a few points, only when heads move |
| a repo whose default branch moved | a live read of that repo's open PRs, because `mergeable` can change with no head SHA change | ETag'd branch read (free on 304), then a few points |

Rules:

- Opened, closed and merged come only from the REST lists. Absence from a search result is never an event: the search index can lag.
- Pagination past 100 results is built from the first version. More than 100 open PRs is a health warning, never a silent truncation.
- Open measurement: the search-index lag on one PR, taken before the cadence is fixed.
- A seat that acts on its own push reads its own PR directly. Point reads stay allowed; what the plane removes is polling on a clock.

## 3. Events and dedupe

- **`Nats-Msg-Id`** is derived from the fact: object id, kind, action, and `updated_at` or head SHA. Derived events add their window number.
  - A webhook delivery GUID, when a later receiver exists, goes in a header only. Otherwise the poller and the receiver would publish every fact twice, and a sender could choose its own dedupe identity.
  - The dedupe window equals the stream's retention (24 h).
- **Derived ages.** The ingress holds the state needed to age a fact, so it emits ages as events.
  - Example: a review requested with no review within the owning role's window becomes `review.stale`.
  - The window value is read from the role's configuration, never a second constant.
  - An event is a cue: the seat's own sweep verifies it.
- **Subject tokens.** Every GitHub-derived token placed in a subject (org, repo, label) is checked against `[A-Za-z0-9_-]`. A failure is dropped, logged and counted in the health row. It is never hashed into the class: a hashed subject is one nobody subscribes to, so the event would look delivered and be lost.

## 4. Credential

The ingress holds its own read-only GitHub credential. It never borrows a person's `gh` login or another tool's identity: that would share a rate budget, widen reach to write access, and tie revocation to a human's login.

The fleet's custody rule: a component may hold what it can revoke or re-mint itself, and a token that is bearer authority at a third party is custody. A GitHub credential is custody either way, so its shape is an operator decision.

|  | preferred: read-only GitHub App installation | fallback: fine-grained read-only PAT |
|---|---|---|
| on disk for the poller | a 1 h installation token | the PAT |
| durable artifact | the App key | the PAT, 90-day expiry |
| holder of the durable artifact | a separate minter step, not the poller | the poller's file |
| revoker | the operator at GitHub | the operator at GitHub |

- The token file is mode 0600 and separate from the NATS credential.
- The health row reports the durable artifact's age from its own file date and warns 14 days before 90. GitHub's token-expiry header is not relied on, because a token without an expiry returns none.
- **Known residual.** Every process under the same uid on the host can read a 0600 file. Custody rests on file mode and the operator's revoke, not on isolation.

## 5. Silence is an error

All four of these controls are required before any seat relies on the plane:

1. **Heartbeat.** `gh-ingress` publishes a heartbeat every cycle on a subject only its own NATS user may publish. director-mcp checks the heartbeat's age on the subscribing seat's behalf and surfaces a gap into the seat. A seat does not tick on its own, so a check left to each seat would fail the way session timers did.
2. **Gap signal.** When director-mcp creates a durable consumer that did not already exist, it tells the seat to sweep before trusting the stream. Consumers start at `DeliverNew` with a 1 h inactivity window, so a seat that was away longer has missed events with no other record.
3. **Off-host watchdog.** A check on another host reads the heartbeat's age and alerts a person. A dead ingress, or a sleeping host, cannot report itself. Open: no second host is measured as always-on yet.
4. **Start-up sweep.** Each consuming role's process text names a catch-up sweep at start-up.

The health row also records consecutive failed cycles, token drops (section 3), the credential's age, and the last good watermark.

Tests for director-mcp cover each control's failure path: a missing heartbeat, a recreated durable and a stale health row each have a test that shows the seat is told. A bug in these paths would bring the silence back with the controls looking present.

## 6. Sequencing

1. **Now:** code, tests against a throwaway broker on a random port, and a stdout dry-run. No unattended process holds a GitHub credential until the operator rules on section 4.
2. **After that ruling:** a launchd poller that publishes nowhere and writes pointers (never bodies) and rate-limit headers to a log file. This proves sleep, wake, network change and restart against real GitHub, with nothing on the bus to spoof.
3. **After the broker's authorization block is live** (aae-orc-umw8p, director#4): the streams, the grants, director-mcp's `subscribe_events`, `unsubscribe_events` and `set_timer`, and the live publisher. On an unauthenticated broker every process can publish to every subject, and JetStream would store a forged event for its full retention.

A webhook receiver is a later build in the same binary. It needs an always-on host GitHub can reach, and it gets its own review as a new inbound surface. The poller stays as reconciliation after it lands.

## 7. Work items

| item | ticket | depends on |
|---|---|---|
| poller (this design), including the minter, health row, heartbeat and dry-run | aae-orc-bstdv | the streams (aae-orc-aubd6) to go live; nothing to start |
| streams | aae-orc-aubd6 | aae-orc-umw8p |
| grant rows, including the heartbeat publish grant and the registry write grant | aae-orc-lgthv | aae-orc-umw8p |
| director-mcp tools, the heartbeat-age check and the gap signal | aae-orc-7vw44 | aae-orc-lgthv, aae-orc-aubd6 |
| webhook receiver | aae-orc-i4im0 | aae-orc-bstdv |
| role text: wake on events, start-up sweep, verify by sweep | aae-orc-jbx9y | aae-orc-7vw44, aae-orc-bstdv |
