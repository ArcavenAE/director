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
