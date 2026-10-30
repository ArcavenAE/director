# finding-director-gnr2: a seat cannot diagnose the traps that stop it taking a turn, and the checks that see them are built in five places no one command reaches

- **Date:** 2026-10-30
- **Session:** research pass, stream 3 of operator ruling I-29 (aae-orc-wria0)
- **Subject:** the director seat's startup and self-check path
- **Confidence:** read-only. Code read at director 35bf9fa and marvel 04c258f; host state read on one host; no seat launched, nothing published

## 0. The sentence

**The checks a seat doctor needs mostly exist already, across cast-launch,
director-session.sh, `director-mcp --preflight`, `director-install
--status`, freebusy and marvel's watchdog, but no one command reaches them,
and the traps the fleet has lost mail to (logged out, held at a dialog) are
the ones a seat cannot check from inside, because they stop it taking the
turn the check would need.**

## 1. What was observed

- Of the ticket's five launcher checks, two are built in director-session.sh
  (the cwd refusal, director#264 and #265; the shim check). The shim check
  is also in cast-launch. The global-tier-off case for a director role is a
  shim WARNING on the MCP server's stderr
  (`cmd/director-mcp/global.go:109`), and director-session.sh neither warns
  nor refuses. No launcher prints its own revision on the spawn line.
- Bus readiness is built and fails closed: `director-mcp --preflight` checks
  the local broker and, with the global tier on, the hub half
  (`bus.go:165`, `global.go:1057`). This is R-93's ratified pre-flight.
- The outside view is built in two components: marvel's watchdog
  (`(logged-out)`, claude 2.1.285 to 2.1.293, expired-session screen
  uncovered per marvel#1079), marvel's parked state, the PANE column in
  marvel#1143 (open), and director's freebusy (`auth-expired`,
  `other-dialog`).
- Self-only facts (registered id, roster reach, `global://` resolution, cue
  nonce state) are visible through MCP tools but are not checks: no tool
  answers "can this seat do its job" with pass, fail or unknown.
- On one host on 2026-10-30, the installed cast-launch is pinned at
  2dbfd14, with five later commits on main touching it (#229, #230, #231,
  #271, #279). `aq`, `ax` and `ae` are not installed there. The `/director`
  skill is installed at user scope, and there is no shared project-scope
  director-mcp registration. Of the five seat-startup traps in the plan
  behind aae-orc-wria0, two are absent on that host (trap 2, the shared
  project-scope MCP block, and trap 4, the `/director` skill not installed)
  and one is present (trap 5, a stale launcher checkout).

## 2. Why it matters

- A doctor built only as the ticket's two surfaces (launcher, in-seat)
  would report nothing for the logged-out and dialog cases. A doctor that
  cannot run reads the same as a seat that was never asked, the
  silent-trap pattern the ticket set out to remove.
- A warning on a server's stderr is a warning nobody reads. The director
  global-off case is caught and still silent.
- Five homes for checks means five vocabularies. diag already defines one
  (pass, fail, skip, instrument), and marvel's PANE defines `unknown` with a
  GAP reason; a sixth vocabulary would make "unknown" mean different things
  to different readers.

## 3. What follows

- The doctor needs three vantages over one check library: before exec, from
  inside the seat (an MCP tool on the shim), and from outside naming a seat.
  Outside-view pane rows read marvel's verdict when marvel is present and
  report n/a without it, so neither component requires the other.
- Results map onto diag's codes, and unknown is never pass.
- Refusal stays limited to structural rows with a written basis (R-93
  today; R-100/R-119 for the director role once ruled). Unread depth, time
  to first turn, reach latency and denial counts are info, never fail
  (`diagnostic-not-gate.md`).

Design draft: aae-orc-wria0 stream 3 report (2026-10-30), held by the
research-supervisor seat.

## 4. Edges proposed

- derives: question-director-seat-startup (sub-questions A and F)
- supports: question-seat-startup-notice-capture
- derives: finding-017-a-merged-launcher-change-is-not-live-until-the-shared-checkout-moves
- supports: brief-operational-diagnostic-battery
