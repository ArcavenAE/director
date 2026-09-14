# Design briefs (candidates, not specs)

These four briefs came out of the 2026-09-12 identity roundtable, after a live
incident: several Claude Code sessions on one host loaded the same local-scope
MCP config and all registered as `agent://ops/michael` (the OS user), so two
real sessions collided on one address and one presence key.

They are candidate designs for further development and probes, not firm specs.
`sim/specs/` is for requirements firm enough to constrain the software; these
are a level below that. Each brief develops a mechanism, runs an adversarial
pass, and proposes candidate requirements (provisional tags), which are
harvested into `../requirements.md` section I (R-49 onward) with their source
classes. Where a brief's conclusion is JUDGMENT rather than OBSERVED, it wants
a probe before it hardens.

- `identity-at-spawn.md` (ID-A..E): who assigns a session its address, and when
  cryptographic identity (A2A DID plus JWS-signed AgentCard) becomes necessary.
- `director-seat-lease.md` (SEAT-A..G): the human-director seat as a fenced KV
  lease on the NATS primitives the Phase 0 probe already runs.
- `continuous-custody-succession.md` (CUST-A..H): custody externalized
  continuously so an involuntary exit loses nothing; graceful and ungraceful
  succession converge on the same durable state.
- `authority-never-in-content.md` (INJ-A..C): why authority rides an
  out-of-band channel and never message or document content, and the R-07
  graduation.
- `shim-timer-heartbeat.md` (BEAT-A..F): the R-56 build plan; one shim timer
  serving presence and seat, observed state separated from declared state,
  the shutdown edge, failure accounting, and a watcher for shimless sessions.
- `marvel-twin-manifest-and-cutover.md`: the operator's stand-up-alongside
  program; the target manifest mapping the nine fleet functions to marvel roles
  and wardrobe casts, the FORWARD identity block (R-84), and the cutover
  criteria as checks (R-49/R-84, R-08/R-09, R-56, R-42, R-60/R-61) plus the
  cross-host trial stage (R-86, R-77).
- `bus-credential-enrollment.md` (brief 9, shape for review): marvel and
  marvel keys as the enrollment and distribution plane for the global bus
  credential; the flow, the transient `Credential` resource, the custody
  argument, the multi-user boundary, and the seams for marvel-builder.
