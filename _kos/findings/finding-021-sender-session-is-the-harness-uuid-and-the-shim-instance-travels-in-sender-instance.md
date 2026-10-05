# finding-021: `sender.session` is the harness session UUID; the shim's per-process ULID travels in `sender.instance`, self-asserted, and unread's reads-outside-durable evidence keys on it and says so

- **Date:** 2026-10-03
- **Session:** the arcaven builder seat, placing the 2026-10-03 team harvest. The work is the sibling builder seat's (#190, #191); I checked the claims against the merged tree at 063f470.
- **Subject:** the director envelope and the unread report (unread slice M)
- **Confidence:** bedrock for the shape, since it is merged and fixture-tested; the self-asserted caveat is a design statement, not a measurement

## 0. The sentence

**`sender.session` keeps its meaning, the harness session UUID. The shim's per-process instance id, a ULID minted at shim start, travels in a separate optional field, `sender.instance`, which is the sender's own claim and never a routing or authorization input. unread's reads-outside-durable evidence matches on `sender.instance` and carries a `basis` field that says so.**

## 1. What is in the tree

- **The design** (`sim/design/envelope-sender-instance.md`, #196, cdffb10): one harness session can span several shim instances (a restart) and one agent can run several at once, so the instance needs its own field. Optional, ULID, informational; the schema description says "self-asserted by the sender, never a routing or authorization input (as role, R-71, R-82)".
- **The sender** (#190): the shim sets `sender.instance` on every send.
- **The reader** (#191): `unread` keys its reads-outside-durable evidence by `sender.instance` (a message with no `sender.instance` is never kept as evidence) and sets `basis` on every such row to `sender.instance is self-asserted by the sender, not verified` (`evidenceBasis` in `probe/nats-phase-0/director-mcp/unread.go`). Any process holding the agent's bus credentials can write any `sender.instance`, so the match is the sender's claim, not proof.

## 2. Why it is worth recording

The earlier unread draft matched on `sender.session`, which names the harness session, not the shim process whose durable is idle. The correction is the split above, and the basis field keeps a reader from taking the match as verified.

## 3. Edges

- derives: finding-003 (session identity collision, live instance), finding-026-instance-ulid-collides-on-simultaneous-start
