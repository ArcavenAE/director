---
name: stansfield
description: >
  Full fleet roll call. Enumerate every agent/session across the director bus,
  every marvel cluster, and SendMessage; dedupe to ONE identity per session even
  when it is reachable by several methods; and record the highest-precedence
  reach method available for each. Optionally relay an action after enumerating
  (harvest, prepare-to-shutdown, get updates, wake check). Use when the user says
  "stansfield", "standsfield", "everyone", "poll everyone", "poll stansfield",
  "roll call", "get updates from stansfield", "pull a stansfield", "hey stansfield
  are you awake?", or "get a stansfield <verb>". "everyone" and "stansfield" are
  equivalent.
---

# stansfield — full fleet roll call

Named by the operator (2026-09-19) for Gary Oldman's Norman Stansfield in
*Léon*: "EVERYONE!" The base act is to enumerate everyone; with a verb attached
it enumerates, then relays that action to the live seats and collects replies.

## 1. Enumerate (three sources)

Run all three; they overlap and each sees sessions the others miss.

1. **Director bus** — `mcp__director__set_presence` (idle), then
   `mcp__director__list_roster`. Presence on BOTH tiers (local + global). Global
   rows carry `cluster` and `role`; local rows are implicitly this hub.
2. **Marvel, per cluster** — for each cluster in `~/.marvel/config.yaml`:
   `marvel get sessions --cluster <name>`. Shows ALL marvel-managed sessions,
   not just those heartbeating on the bus. A cluster that is down errors loudly
   (report it, do not silently drop it).
3. **SendMessage** — `ListAgents`. Interactive local Claude sessions +
   Remote Control sessions (often offline) on the account.

## 2. Dedupe to one identity per session

A session often appears in more than one source. Recognize them as the SAME
session and collapse to one row. Match keys, in order:

- **agent name/id** — marvel `AGENT NAME` == director-bus `agent_id`
  (e.g. `arcaven-supervisor-g1-0`).
- **instance** (ULID) — the canonical id; the director bus emits it, and a
  session on both tiers appears twice under one instance.
- **tmux target** — `marvel-aae:@1.%1` links marvel / direct-tmux / ListAgents.
- **title / short ref** — last resort for ListAgents-only sessions.

`cluster name == hostname` here: kinu = this laptop, mokuzai = skippy's host
(192.168.100.196), desk = desk.local.

## 3. Reach-method precedence (record per session)

For every session, the reach method is the HIGHEST layer that can actually
reach it; if that layer cannot, fall back to the next. Record the chosen layer.

1. **director** — `mcp__director__send_message` (structured envelopes,
   presence-aware, cross-host over the global tier). Preferred whenever the
   session holds a live bus presence. Local `agent://{team}/{id}` is
   bidirectional; cross-host must use `global://` (an `agent://` aimed at a
   remote seat is silently delivered to your local inbox). There is no
   per-seat global address; `global://{cluster}/supervisor` fans out to every
   supervisor on the cluster, so scope the recipient in the body. See the
   director skill's `reference/addressing.md`.
2. **marvel capture / inject** — the key is `<workspace>/<agent-name>`, NOT
   the bare AGENT NAME `get sessions` prints, plus `--cluster` for a remote
   cluster: `marvel capture aae/errand-supervisor-g1-0 --cluster skippy`,
   `marvel inject aae/migrated-supervisor-g1-0 "text" -e --cluster skippy`
   (`-e` submits; without it the text stages as an unsent draft). The bare
   name errors `resource not found` (marvel#337). Pane read + keystroke
   doorbell, executive privilege.
3. **marvel native tmux** — marvel's own tmux-backed session operations.
4. **direct tmux** — raw `tmux capture-pane` / `send-keys` for a local pane
   marvel is not managing.
5. **Claude SendMessage** — `ListAgents` + SendMessage. Lowest for now: reaches
   interactive / Remote Control Claude sessions the others cannot, but is the
   least structured and carries no presence guarantee.

Roadmap (do not use yet, note when relevant): **switchbox** (switchboard) will
be inserted into this ladder. Before that, add **codex / crush / opencode / pi**
harness support if those expose their own reach methods.

## 4. Present

One deduped table: `cluster (host) | workspace | team | role | agent |
state/health | reach (chosen layer)`, plus tier-visibility where it matters.
Flag housekeeping: crashed adhoc `run-…` sessions (reap candidates), down
clusters, and any session reachable only at a low-precedence layer.

## 5. Extended forms (roll call THEN relay a verb)

After enumerating, relay the action to the live seats via the highest reach
layer each supports (`mcp__director__send_message` to `agent://`, `role://`, or
`global://{cluster}/supervisor`; else marvel inject; else down the ladder), then
report each message_id and any reply.

- "stansfield tell em to harvest" / "get a stansfield harvest" → harvest directive.
- "stansfield prepare to shutdown" → prepare-to-shutdown directive.
- "get updates from stansfield" / "pull a stansfield and get updates" → solicit a
  status update from each live seat.
- "hey stansfield are you awake?" → quick liveness/presence confirmation.

Carry the standing constraints into any relayed directive that will produce text
under the operator's name (no em dashes, no marketing vocab, no AI attribution,
SSH-signed commits, redaction/tenant hygiene, first person singular).
