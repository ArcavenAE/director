# The envelope's sender.instance field

Status: design for review, 2026-10-02. Owner: the architect role. Unblocks
director#190 (review 5393494558) and amends the merged unread slice M design
(`sim/design/unread-slice-m.md`, director#185). Design only; builds route
after review.

## 1. Why

Slice M's evidence rule needs to know which session of an agent sent a
message, because two live sessions of one agent share `sender.agent_id`.
director#190 put the shim's per-process instance id into `sender.session`.
That field is already taken: the canonical envelope schema in marvel
(`contracts/schema/director-envelope.schema.json`) defines `sender.session`
as the harness session UUID, the key of R-71, nullable until marvel sets it
at spawn (R-73, R-84), with a UUID pattern. The shim's instance is a ULID,
which fails that pattern, and it names a different thing: one shim process,
not one harness session. So the instance needs its own field.

## 2. Today (checked at marvel dd63426, beadle and director main)

- **The sender object is closed.** The schema sets `additionalProperties:
  false` on `sender`. beadle generates its types from a pinned copy
  (`crates/director-envelope/contracts/`, `PINNED.md`, `PINNED.sha256`) with
  `#[serde(deny_unknown_fields)]`, so beadle refuses an envelope whose sender
  carries a field it does not know.
- **marvel's Go package** (`contracts/go/envelope`) is generated from the
  same schema and validates against an embedded copy. Nothing in marvel's own
  daemon imports it today.
- **The director shim does not validate inbound envelopes.** It decodes with
  `encoding/json`, which ignores unknown fields, so it reads a new field
  without change.
- **The instance id** is `ulid.MustNew(...).String()` (`bus.go`,
  `newInstanceID`): 26 characters of Crockford base32, upper case. It is the
  suffix of the session's durable, `mcp_<agent>_<instance>`.

## 3. The field

Add to `sender` in the canonical schema:

```json
"instance": {
  "type": ["string", "null"],
  "pattern": "^[0-9A-HJKMNP-TV-Z]{26}$",
  "description": "The sending shim process's instance id, a ULID minted at shim start. It is the suffix of that process's durable, mcp_<agent>_<instance>. One harness session can span several instances (a shim restart) and one agent can run several at once. Informational: self-asserted by the sender, never a routing or authorization input (as role, R-71, R-82)."
}
```

- Optional, not in `required`. A sender that does not set it omits it or
  sends null; every existing envelope stays valid.
- `sender.session` is unchanged and keeps its meaning (the harness session
  UUID, marvel's to set).
- The schema `$id` stays `.../envelope/v1` and `schema_version` stays 1. The
  change is additive for a lenient reader. It is not additive for a strict
  one (beadle), which is why the order in section 4 matters.
- Fixtures: `valid-sender-instance.json` (a ULID), `valid-sender-instance-null.json`,
  `invalid-bad-instance.json` (lower case, 25 characters, a UUID).

## 4. Order of the three PRs

A strict reader must know the field before any sender sets it. Otherwise
beadle refuses every message from an upgraded shim.

| step | repo | change | done when |
|---|---|---|---|
| 1 | marvel | schema gains `sender.instance`; `just contracts-gen` regenerates the Go types and `schema.gen.json`; the three fixtures | merged |
| 2 | beadle | re-pin: copy the schema at step 1's merge commit, update `PINNED.md` and `PINNED.sha256`, `just contracts-gen`, add the fixtures; `just contracts-check` passes | merged, released, and the running beadle on every host that reads the bus is that release or later |
| 3 | director | #190 reworked: a `Sender.Instance` field (`json:"instance,omitempty"`) set to the instance on every send; `Sender.Session` left unset | merged after step 2's "done when" |

Step 3 waits on a deployed beadle, not only a merged one. The supervisor
confirms the running beadle's version on each host before step 3 merges. If
any other strict reader of the envelope appears before then, it joins step 2.

## 5. director#190, reworked

- Keep the test shape, and change the field: a sent envelope's
  `sender.instance` equals the bus's instance, and `sender.session` is absent.
- Add one test that validates a sent envelope against the canonical schema at
  step 1's commit, copied into the test's testdata with its commit named, so a
  later drift fails the test.
- The commit stays on #190's branch as a new commit; the title becomes "set
  sender.instance on every send (unread slice M, part E1)".

## 6. The unread slice M edit (director#185, merged)

`sim/design/unread-slice-m.md` changes in this PR:

- M2's evidence rule matches on `sender.instance`, not `sender.session`, and
  explains why `sender.session` is the wrong field (section 1).
- Part E1 is "the shim sets `sender.instance`", depending on this design's
  steps 1 and 2.
- It states the review's non-blocking note: the field is self-asserted. Any
  process holding the agent's bus credentials can write any instance, so
  `reads-outside-durable` is evidence from the sender's own claim, not proof.
  The default (`durable-idle`, with its age) is what a reader gets whenever
  the claim is absent.
- Test 2's negatives name `sender.instance`.

## 7. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Name the field `sender.instance` (optional, ULID, informational) | yes |
| 2 | Step 3 waits on beadle deployed on every host, not only merged | yes |
