# finding-003: two different-purpose sessions collided on one director address; identity derives from the OS user, and the only roster discriminators are the instance ULID and the pid

- **Date:** 2026-09-17
- **Session:** director seat, from OS-level process ancestry read directly on this host
- **Subject:** director session identity (R-49, R-50; `sim/design/identity-at-spawn.md`), so this belongs in director's own graph
- **Confidence:** observed. Process ancestry read on this host; the mechanism verified in `probe/nats-phase-0/director-mcp/main.go` and the out-of-repo project-scope MCP config
- **Sibling:** the presence-liveness half of the same run is recorded as instance (c) on the orc graph's `finding-166`. The two are one class seen from two sides; see the placement note at the end.

## 1. What was observed

Two director-mcp roster entries both registered as `agent://ops/michael` (team `ops`, workspace `aae-orc`), colliding on one address. Process ancestry separated them:

- pid 74798 (ttys025), parent `claude --resume director`: the genuine director seat, instance `01M2M33CE6C8S8PP7T82XJBWZW`.
- pid 43993 (ttys047), parent `claude --resume architect` (pid 86260): a different-purpose session, not a second director. Its shim loaded the same project-scope MCP config, took `DIRECTOR_AGENT_ID=michael`, and heartbeat to the same address by accident.

The sharper fact this run adds over the 2026-09-12 incident in `identity-at-spawn.md` (two sessions that were both director-flavored) is that the colliding sessions had different purposes. An architect session silently occupied the human's director address. The failure is not "two directors ran," it is "every session in the project dir is michael, and one of them happens to be the director."

## 2. Mechanism

Identity is sourced from static configuration and defaults to the OS user. `probe/nats-phase-0/director-mcp/main.go:54-61` requires `DIRECTOR_AGENT_ID` and exits if it is empty, so the code does not itself derive from the OS user. The OS-user default enters from outside the repo: an earlier `claude mcp add --scope local` wrote one project-scope MCP server config with `DIRECTOR_AGENT_ID=michael` baked in, and every Claude Code session launched in the project dir loads that same env (`sim/design/identity-at-spawn.md:9-14`). So N sessions in one project dir claim one address.

The roster makes the collision hard to see. `list_roster` (`probe/nats-phase-0/director-mcp/tools.go`, then `bus.go` `roster`) returns every presence key, and the only fields that differ between two colliding sessions are the instance ULID and the shim pid. The addressable `agent_id` is identical, so nothing a sender addresses distinguishes them, and two shims that share one durable name race one consumer (the original silent-drop defect re-entering through the identity layer, documented in `identity-at-spawn.md`).

## 3. What it bears on

- R-49 (identity assigned at spawn by the launcher, never the OS user) and R-50 (durable consumer unique per session) are the ratified fixes; this is a second, sharper live instance earning them.
- R-06 (a restart changes identity and address; key on durable conversation identity): the collision fuses address with the OS user and leaves durable identity undefined, which is R-06's separation failing.
- R-01 (a message carries a verifiable principal): a launcher-assigned name closes the collision but is a label, not an attestation (ID-E); the nonrepudiation plane (R-05) is the deferred axis.
- The marvel shift-change succession set (`aae-orc-c3be5` and siblings, marvel PR #295): a successor or returning session must not collide on a live address, which is the same identity-on-restart concern on the marvel side.

## 4. Fix (already designed, not self-ratified here)

`identity-at-spawn.md` ID-A (a launcher assigns a distinct `DIRECTOR_AGENT_ID` at spawn and passes `--strict-mcp-config` so the baked project-scope config is bypassed; `probe/nats-phase-0/id-a-demo/director-session.sh` already does this) closes the collision now. ID-C (a DID bound to that name) is the deferred trajectory. R-50's per-session durable name makes even a misconfigured duplicate id unable to steal another session's mail.

## 5. Placement note (for the operator)

I filed this in director's graph per the subject test, since the subject is director session identity. Its sibling, the presence-liveness half, is instance (c) on the orc graph's `finding-166`, and the wider director-identity finding cluster (finding-159, finding-160, finding-166) currently lives in the orc graph. So this finding sits apart from that cluster. If the operator wants the cluster consolidated into one graph, that is a migration decision; I did not move the existing orc findings.

---

## 6. Correction appended 2026-09-21 (finding-005): section 4's R-50 claim is conditional, and section 2's discriminator is not reliable

Appended rather than rewritten, so the original reasoning stands as it was
written and the change is visible.

**What does not hold.** Section 4 says "R-50's per-session durable name makes
even a misconfigured duplicate id unable to steal another session's mail." That
was always conditional on the instance being unique, and
[finding-005](finding-005-instance-ulid-collides-on-simultaneous-start.md)
measures that it is not: two processes started in the same instant mint the same
instance ULID in 47 of 200 simultaneous pairs, because `ulid.Make()` seeds
`math/rand` from the wall clock at package init. The durable is
`mcp_<agentID>_<instance>` (`bus.go:141`). If the agent id collides, as it does
in this finding, AND the instance collides, the durable name collides too and
two shims race one consumer. R-50's protection does not degrade in that case, it
fails.

**What is weakened.** Section 2 says "the only fields that differ between two
colliding sessions are the instance ULID and the shim pid." One of those two is
unreliable under precisely the condition that creates agents, since a launcher
starts a team together.

**This finding's own specimen is NOT exposed, and that matters.** The two
sessions recorded here (pids 74798 and 43993) were separate `claude --resume`
sessions started at different times, and they carry distinct instances
(`01M2M33CE6C8S8PP7T82XJBWZW` and its counterpart). Nothing about this specimen
is retroactively worse. The compound case needs a launcher casting both sessions
at once, which is exactly what `marvel` does when it applies a team and what
`marvel shift` does when it rotates a generation. So the specimen is safe and
the class it generalises to is not.

**A further consequence this finding could not have seen.** The R-49 collision
detector added in response to it is keyed on instance INEQUALITY:
`checkCollision` (`bus.go:725`) warns only on a row whose `instance` differs from
its own. Two sessions sharing both agent id and instance write ONE local presence
key, so the loop finds a single row, its own, and returns empty. In the compound
case, the detector built to catch this finding is silent. The fix in
finding-005 section 5 closes that too.
