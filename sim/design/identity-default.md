# Design brief: no personal default identity, and one holder per address

Owner: arcaven-architect-g5-0. Commission: operator ruling via director and
arcaven-supervisor, 2026-09-27 (PR 2 of the identity change; PR 1 moves tests
and docs to the placeholder `agent://ops/operator` and leaves the runtime
alone). Status: design for skippy review. No build until the review passes
and the operator rules on section 6.

## Why

A session opened in the orc today joins the bus under the operator's
personal id, and every such session joins as the same person.
That puts a personal name on seats that are not that person, and it lets an
unrelated session occupy the human director's address. Both happened
(finding-003: an architect session heartbeat under the personal id beside
the real director).

## 1. Premises checked (one command each)

| claim | command | result |
|---|---|---|
| the runtime defaults identity to `$USER` | `git grep -nE '\$USER\|user\.Current\|os.Getenv\("USER"\)'` over code | **false.** `director-mcp` has no default: it exits when `DIRECTOR_AGENT_ID` is empty (`main.go:56-64`) |
| where the personal id comes from | read `~/.claude.json` (key names and agent id only) | a **local-scope Claude Code registration** for the orc directory, `director-mcp` with the personal `DIRECTOR_AGENT_ID` baked in, loaded by every Claude Code session started there. It is operator-local and in no git repo |
| instance ids can collide | `git grep ulid` in director-mcp | fixed for instances: `bus.go:165` uses `crypto/rand` (finding-005's fix). Message ids at `tools.go:124` still use `ulid.Make()`; out of scope here, noted in section 7 |
| the seat wrapper has a shared fallback | read `probe/nats-phase-0/director-mcp-seat:69` on main | **true.** `DIRECTOR_AGENT_ID="${DIRECTOR_AGENT_ID:-${MARVEL_SESSION:-director-seat}}"`: with neither set, every such seat joins as the literal `director-seat`. mokuzai's seats run this wrapper today |
| a launcher that assigns ids exists | `probe/nats-phase-0/id-a-demo/director-session.sh` | yes: it assigns a distinct id and passes `--strict-mcp-config`, so the baked registration is bypassed (ID-A) |

So the Go shim has no default, but one shipped wrapper does: the seat
wrapper's `director-seat` fallback. The change removes that fallback,
removes a hand-made registration, and gives every path that starts a
session a declared way to get a distinct, impersonal name.

## 2. The trap in the literal ask

Replacing the personal id with a role name such as `agent://ops/director` in the
same registration would remove the personal name but keep the collision
class. Every session opened in the orc would then join as the director, and
finding-003's failure (an architect session occupying the director's
address) would recur with a neutral name. A role name is safe as an address
only if something guarantees one holder.

## 3. The design

Three rules, each covering one path by which a session gets a name:

1. **A session's agent id is unique and impersonal, and something declared
   assigns it.**
   - **marvel seats:** unchanged. The launcher derives the id from the cast
     (`<team>-<role>-g<gen>-<idx>`, built in marvel's controller; R-81
     gives persistent seats a role word and ephemeral sessions a minted
     label, applied at spawn).
   - **Hand-run sessions:** they go through a declared launcher (ID-A,
     promoted from `id-a-demo/director-session.sh` into an installed
     `director-install` component, beside cast-launch). It assigns
     `hand-<host>-<4 base36>` and passes `--strict-mcp-config` with its own
     registration. Examples: `agent://ops/hand-kinu-7k2m`.
   - **No person's name, anywhere.** The team and workspace stay explicit
     flags, defaulting to `ops` and the current directory's workspace.
2. **The director is a role held under the seat lease, not an agent id.**
   - The address is `role://ops/director`. It resolves to whichever session
     holds the seat lease (`director-seat-lease.md` SEAT-A; this answers that
     brief's open question on unifying the lease with `role://`: yes).
   - The holder keeps its own unique agent id.
   - If the operator wants `agent://ops/director` as a literal address, the
     shim must refuse to register it without winning the lease. That makes it
     the same lease, spelled as an agent address. Either spelling is safe;
     only the unguarded one is not.
3. **No shared fallback.**
   - `director-mcp` keeps refusing an empty `DIRECTOR_AGENT_ID`. It does not
     mint one: a mint inside the shim would hide a missing launcher.
   - The seat wrapper on main (`director-mcp-seat:69`) falls back to the
     literal `director-seat` today, and the resolution chain designed for
     the single seat shape (director#108 item 6, aae-orc-crukm) carried the
     same fallback. That is a shared name, the same collision class. Both
     change to refuse, with a message naming the hand-run launcher; see
     section 5.

**Retiring the registration.** The local-scope registration that bakes
personal `DIRECTOR_AGENT_ID` is removed by the operator (`claude mcp remove
director-mcp --scope local` from the orc directory). This PR cannot do it,
because the registration lives in the operator's `~/.claude.json`, not in
any repo. Until it is removed, the hand-run launcher's `--strict-mcp-config`
bypasses it. The design lists it as the first manual step, and
`director-install --status` WARNs while any Claude Code registration still
carries a `DIRECTOR_AGENT_ID` naming a person or equal to a role word.

## 4. Why this closes the collision class

A collision needs two live sessions claiming one address. After this design:
- marvel ids are unique by construction.
- Hand-run ids are unique by construction: host plus a random suffix, and on
  a clash the presence write finds the key taken and the launcher re-mints.
- The one role address that must be singular is fenced by the lease, so a
  second claimant is refused.
- A session with no assigned id cannot start.

R-50's per-session durable (`mcp_<agentID>_<instance>`) is then belt and
braces, not the only guard.

## 5. Changes for the builder (after review and rulings)

- **`director-install`:** a `director-session` component (the ID-A launcher,
  hardened from the demo): an id mint, `--strict-mcp-config`, a per-session
  MCP config written into a temp file that is removed on exit, and flags for
  team, workspace and a role hint. It shares the versioned install, stamp
  and rollback of director#108.
- **`director-mcp`:**
  - a role name (`director`, `supervisor`) as an agent id is refused unless
    the lease is held (rule 2, the literal-spelling case);
  - no fallback name (rule 3).
- **`director-mcp-seat` on main:** line 69 drops the `director-seat`
  fallback now, in this build: with neither `DIRECTOR_AGENT_ID` nor
  `MARVEL_SESSION` set, it exits and names the hand-run launcher. This
  does not wait for the single-shape move, so the live fallback cannot
  outlive this build if that move is late.
- **The #108 item 6 chain (aae-orc-crukm):** when the chain moves into
  `director-mcp`, it carries the same refusal, not the fallback.
- **`--status`:** WARN on any Claude Code or codex MCP registration that
  bakes a `DIRECTOR_AGENT_ID` naming a person or equal to a role word.
- **Tests:**
  - two hand-run launches on one host get distinct ids;
  - an empty id refuses;
  - a second claimant of the director role is refused while the lease is
    held, and succeeds after it expires;
  - the chain with no id refuses and names the launcher;
  - `director-mcp-seat` with neither `DIRECTOR_AGENT_ID` nor
    `MARVEL_SESSION` exits non-zero and never joins as `director-seat`.

## 6. Rulings needed

- **IDD-1:** the director address. Default: `role://ops/director`, resolved
  through the seat lease. The alternative is a lease-guarded literal
  `agent://ops/director`.
- **IDD-2:** the hand-run id shape. Default: `hand-<host>-<4 base36>`.
- **IDD-3:** remove the local-scope personal-id registration (an operator
  action on `~/.claude.json`), with the launcher's `--strict-mcp-config` as
  the bridge until then. Default: yes.

## 7. Not in scope, noted

- Message ids at `tools.go:124` use `ulid.Make()`, the same seeding
  finding-005 fixed for instances. Two shims sending in the same millisecond
  can mint equal message ids. Worth its own ticket.
- ID-B and ID-C (a DID bound to the name) remain the trajectory for
  off-host or multi-operator identity. This design is ID-A made default.
