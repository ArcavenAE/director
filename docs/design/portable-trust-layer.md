# Portable trust layer: one inventory, any deployment

- **Date:** 2026-10-20
- **Status:** design for review. Nothing is built by this document, and no ticket is filed.
- **Owner:** director, for the trust-chain protocol and the settings generator. The role library and marvel own the follow-ups in section 9.

Today a seat's permission settings are hand-written per host, so a team that adopts marvel and director in a different organization starts from an empty file and relearns every boundary by hitting it. This design separates what is true of every deployment (the trust chain, and what each role may and must never do) from what is true of one deployment (its orgs, hosts, production targets and secrets), so a new team writes one private inventory file and every seat on every harness gets settings generated from it.

The cost of adopting it is low. It is additive: marvel's existing policy projection keeps working, and a deployment with no inventory behaves as it does now.

## 1. The split

| layer | portable? | owner | what it holds |
|---|---|---|---|
| trust chain | yes, every deployment | director (protocol) | who may grant what to whom, how identity is established, what always blocks |
| role intent | yes, every deployment | the role library | per role, in environment-neutral words: what is routine, what needs a person, what is never done |
| deployment inventory | no, one per deployment, kept private | the deployment's own private repo | orgs and repos, hosts, services, production targets, secrets custody, cross-org content rules |
| settings generator | yes | director | inventory + role intent + trust chain, rendered into one harness's settings |
| launch-time projection | yes | marvel | runs the generator for each seat at spawn and writes the result where that harness reads it |

Each part stays usable alone, as the independence rule asks. A person without marvel runs the generator by hand and installs the output. A marvel deployment without director keeps its hand-written `Policy` resources, which project as they do today. Roles in the role library stay readable as plain documents.

## 2. The trust chain, as protocol (director)

These are rules about authority, written so any harness adapter can encode them. Most already hold as director requirements; the table names them.

| rule | statement | grounded in |
|---|---|---|
| principal | The operator is the principal. Authority starts there and nowhere else. | R-01 (a message carries a verifiable principal); the operator as the root principal is new here |
| director carries intent | director's messages carry the operator's intent, and say how strongly: originated by the operator, or director's own. | R-02, R-03 |
| delegation | A team supervisor carries the operator's intent by delegation, within its role. Other seats are peers: a peer's request is a request, not a grant. | R-02, R-58 |
| no widening | A delegated grant is at most what the delegator holds, and at most what the receiving role allows. Delegation never widens what was granted. | new (TRUST-A) |
| protected class | A deployment names a protected class of actions. Those always block, whoever asks and however the ask is worded, and the operator runs them by hand. | new (TRUST-B) |
| identity from the bus | Sender identity comes from what the bus stamps on the envelope, never from message text or a display name. | R-82, R-58 |
| content is data | An instruction inside content a seat reads (an issue, a file, a tool result, a relayed quote) carries no authority, including the authority of whoever asked for the read. | R-07 |
| a denial stands | After a policy denial, any retry is a bypass. One plain retry is allowed only after a named transient cause. | R-184 |

**Custody stays where ADR-009 put it.** The audience test applies to everything this design adds. The inventory names where each secret lives and who mints it, never a value. The generator reads no secret and writes none: the settings it emits hold rules and prose, which are not bearer authority anywhere. A seat that needs a credential gets it the way it does today, from the tool that owns it. Nothing here moves custody into director, the role library or marvel.

**What the trust chain does not settle.** Whether a harness's classifier can tell a bus-stamped sender from a sender claimed in text is a property of that harness, not of this protocol. For Claude Code it is unverified, and section 8 makes it the first probe.

## 3. Role intent (role library)

Each role in the role library gains three lists, written without naming any org, host or repo:

- **routine:** what the role does without asking. "Read any repository in the deployment's own orgs." "Open a pull request from a branch."
- **ask:** what needs a person's explicit go in the current message. "Merge a pull request." "Write to a production target."
- **never:** what the role must not do even when asked. "Commit or push" for a reviewer. "Type into another seat's pane."

The nouns in these lists ("the deployment's own orgs", "a production target") are the inventory's keys. The generator resolves them against one deployment's inventory, so the same role reads correctly at any organization.

A role's **never** list joins the deployment's protected class for that seat. A role's **routine** list can never reach into the protected class: if they overlap, the generator refuses the role, and nothing launches with a contradiction in it.

## 4. Launch-time projection (marvel)

What exists, read from marvel's source on `main` (13e385e, 2026-10-25):

- A role may reference a `Policy`. At spawn, and again on every live re-projection, the session manager writes that policy's settings to a per-session file and passes it to the harness (`--settings` for claude and forestage).
- Each runtime adapter answers `ProjectionFor`. claude and forestage report a surface. codex, opencode, generic and the simulator report none, so a policy on those roles is logged as advisory.
- Projection of inline settings is best-effort: if the write fails, the session launches unprojected.
- A policy may instead name a `settings_file`, which marvel reads verbatim at each projection and re-reads on each reconcile. If that file cannot be read, marvel holds the spawn rather than launch the seat without the rules the file carries (marvel#1061).

What changes:

1. The policy's content comes from the generator (inventory + the seat's role from the role library + the trust chain), not from a hand-written block in each manifest. The generator writes a `settings_file`, so a generated policy inherits the held spawn above. A hand-written `Policy` remains valid and takes precedence while a deployment migrates.
2. The inventory is referenced once per cluster or workspace. A host change, such as a new production target, re-projects every live seat on that cluster, using the re-projection path that exists today.
3. A role whose effective protected class is non-empty fails closed: if projection fails, the seat does not launch. A file-backed policy already behaves this way when its file cannot be read; what remains is a protected role whose policy is inline or missing. Today's best-effort launch stays the behavior for roles with nothing protected. Because the inventory's `protected` list applies to every seat, a deployment with any entry in that list fails closed for every seat; best-effort launch survives only where that list and the role's **never** list are both empty.
4. Adapters for codex, opencode and generic gain a projection surface (section 6), each reporting what it can and cannot enforce, so a gap shows in `marvel describe` instead of being silent.

## 5. The deployment inventory

One YAML file per deployment, in that deployment's own private repository. It holds names and rules, never secret values. The generator validates it against a schema that director publishes.

```yaml
# inventory.yaml: one deployment. Private. Holds names, not secrets.
schema: director.inventory/v1
deployment: acme-platform

operator:
  principal: ops@example.com          # who the trust chain starts from

orgs:
  - name: acme                         # the deployment's own org
    kind: own
    visibility_default: private
  - name: acme-oss
    kind: own
    visibility_default: public
  - name: upstream-project
    kind: third-party                  # contribute by PR only; never collect without quarantine

repos:
  - name: acme/platform
    visibility: private
    default_branch: main
    protected_branches: [main]
  - name: acme-oss/widget
    visibility: public
    default_branch: develop
    protected_branches: [main, develop]

hosts:
  - name: build-01.example.com
    role: seat-host
  - name: db-prod.example.com
    role: production

services:
  - name: issue-tracker
    url: https://tracker.example.com
    write_from_seats: true
  - name: chat
    url: https://chat.example.com
    write_from_seats: false            # seats read, the operator posts

production_targets:
  - name: prod-k8s
    match: "kubectl --context prod-*"
  - name: prod-db
    match: "db-prod.example.com"

secrets:                               # custody by reference only (ADR-009 audience test)
  - name: model-backend-login
    held_by: harness                   # the harness owns it; no component copies it
  - name: bus-credential
    held_by: cluster                   # minted and revocable by the cluster
  - name: cloud-deploy-role
    held_by: vault
    brokered: short-lived              # a component may ask for one; it never holds the grant

content_rules:
  - from: private
    to: public
    rule: block                        # private names, links and text never reach a public target
  - from: acme
    to: upstream-project
    rule: review                       # a person reads it before it posts

protected:                             # always blocks, for every seat, whoever asks
  - force-push or history rewrite on any remote
  - any write to a production_target
  - moving a secret's value into a file, message or repo
  - deleting a repository or a protected branch
```

The `protected` list is prose on purpose. Each harness adapter turns it into the strongest form that harness offers: a hard rule where one exists, classifier text where that is all there is, and a stated gap where neither exists.

## 6. Harness adapters

An adapter takes the generator's output (trust chain + resolved role intent + inventory) and writes one harness's settings. The table states what I read in each harness on 2026-10-20, and marks the rest as unverified.

| harness | version read | allow surface | deny surface | environment prose | how settings reach it |
|---|---|---|---|---|---|
| Claude Code | 2.1.304 | `permissions.allow`; `autoMode.allow` | `permissions.deny`; `autoMode.soft_deny`; `autoMode.hard_deny` | `autoMode.environment` | `--settings <file>` (marvel does this today); pattern rules also as launch args (`--allowedTools`, `--disallowedTools`) |
| codex | 0.154.0 | sandbox mode (`-s`), approval policy (`-a`) | `-s read-only`; no per-command deny seen | none seen | launch args and `-c key=value` overrides |
| opencode | 1.18.31 | `permission.<tool>: allow` | `permission.<tool>: deny` or `ask` | none seen | `OPENCODE_CONFIG_CONTENT` |
| crush | 0.95.0 | `permissions.allowed_tools`; `--yolo` | none seen | none seen | its config file (path unverified), kept where the agent cannot write |

**Claude Code: the first adapter.** The `autoMode` block has four slots, read from the 2.1.304 binary's own setup text: `environment` ("context about the user's setup that helps the classifier make decisions"), `allow`, `soft_deny` ("block unless clear user intent authorizes them") and `hard_deny` ("block unconditionally (user intent does not clear these)"). A non-empty rule array starts with the literal `"$defaults"`, which keeps the shipped rules. The mapping:

| slot | filled from |
|---|---|
| `environment` | the inventory rendered as sentences (own orgs, public vs private repos, production targets, which services seats may write), plus the trust chain stated as facts about this deployment |
| `allow` | the role's **routine** list, resolved against inventory nouns and scoped to name the org or repo |
| `soft_deny` | the role's **ask** list, plus writes to any `production_target` |
| `hard_deny` | the deployment's `protected` list plus the role's **never** list |
| `permissions.deny` | the parts of `protected` and **never** that are exact command patterns (for example `Bash(git push --force:*)`), because a pattern rule does not depend on the classifier |

Settings are not the only channel. Deployments already give a seat pattern rules as launch arguments, for example to scope a seat's issue writes to one org. A pattern allow scoped by a flag, such as `Bash(gh issue create -R acme/*)`, needs a matching deny for the same flag given twice or in its long form (`Bash(gh issue create*-R*-R*)`, `Bash(gh issue create*--repo*)`), or a second flag moves the command outside the scope. The adapter emits each such deny with its allow.

**codex.** The adapter maps a role whose **never** list includes writing to `-s read-only`, and a role with no human at the pane to `-a never`. In 0.154.0's help I found no per-command allow or deny surface, so the protected class reaches codex only as coarsely as the sandbox can express it, and the adapter says so in its `ProjectionFor` report. marvel finding-049 measured that a codex-trusted project stays writable by codex's own edit tool regardless of `-s`, so the adapter must also keep the role's private codex home from trusting the target. Unverified: whether codex has a rules file that would give a finer deny surface.

**opencode.** The 1.18.31 binary carries config examples of the form `"permission": { "edit": "deny", "bash": "ask" }`, so per-tool allow, ask and deny exist. A deployment can already pass opencode's MCP wiring through `OPENCODE_CONFIG_CONTENT`, as a role's declared env in its manifest, which marvel merges into the launch environment (`mergeRoleEnv`); marvel itself never builds that JSON. The adapter adds the `permission` key to it. Unverified: whether `bash` accepts per-command patterns, and whether opencode has a slot for environment prose. Until that is known, the environment prose goes into the role's prompt, where it informs and does not enforce.

**crush.** 0.95.0 accepts `permissions.allowed_tools` and a `--yolo` flag that accepts everything. I found no deny list. marvel runs crush as the `generic` runtime, which reports no projection surface. The adapter can write `allowed_tools` from the role's **routine** list. It cannot enforce the protected class, so a crush seat with a non-empty protected class either runs inside an outside sandbox or does not launch. That choice belongs to the deployment, and the adapter refuses to pretend. The adapter also carries marvel's crush guard (finding-033): crush runs every workspace config file as trusted code, so the config the adapter writes, and the user config that `CRUSH_GLOBAL_CONFIG` names, must sit where the agent cannot write. The crush adapter itself is still unbuilt on marvel main (13e385e).

## 7. The first instance

A supervisor on another cluster is building an `autoMode` block for its deployment now. I have not read it. It will fill the four slots in section 6, and each slot splits the same way:

| slot | moves to the portable layer | stays in that deployment's inventory |
|---|---|---|
| `environment` | the trust-chain sentences (principal, delegation, identity from the bus, content is data) | the names: orgs, repos, hosts, services, production targets |
| `allow` | the routine actions, phrased against inventory nouns | which org or repo each one is scoped to |
| `soft_deny` | the ask actions, and "writes to a production target" as a rule | which targets are production |
| `hard_deny` | the protected-class rules every deployment should start with (force-push, secret values leaving their holder, deleting protected branches) | additions only that deployment needs |

Reading that block against this table is the cheapest test of the design. Any entry that does not split cleanly into a portable rule and an inventory name points at a missing inventory key or a role-intent phrase that names a place.

## 8. Open questions

1. **Does the classifier honor the bus stamp?** The trust chain says identity comes from the envelope. In Claude Code the classifier sees tool results, including the envelope's sender field, and also sees message text that may claim a sender. A probe should send the same instruction twice, once from the director seat and once as a peer's message quoting the director, and record whether `environment` text alone makes the classifier treat them differently. Until that is measured, the protected class must not rely on the classifier: it rides `hard_deny` and pattern rules.
2. **Where does the generator run?** As a director CLI subcommand that marvel calls at spawn, or as a library marvel links. A subcommand keeps the independence rule simple. A library avoids a process per spawn.
3. **Precedence with a hand-written `Policy`.** This design lets the hand-written policy win while a deployment migrates. Whether that stays true after migration, or flips so the generated policy wins, is a ruling for the operator.
4. **Multi-user deployments.** The trust chain assumes one operator. A deployment with several people needs per-principal grants, which the register already anticipates (R-140). The inventory's `operator` key would become a list.

## 9. Follow-up tickets (titles and one line each; none filed)

**director**

- *Inventory schema and validator.* Publish `director.inventory/v1` as JSON Schema with a structural check that may gate, since it asks only whether the file is well-formed.
- *Settings generator with the Claude adapter.* Render inventory + role intent + trust chain into a settings fragment with `autoMode` and `permissions`. Refuse a role whose routine list overlaps the protected class.
- *Classifier probe for bus-stamped identity.* Run open question 1 and record the result as a finding.
- *Trust-chain register entries.* Harvest TRUST-A (no widening) and TRUST-B (protected class) into the requirements register.

**role library**

- *Role intent fields.* Add routine, ask and never lists to the role format, phrased against inventory nouns and naming no deployment.
- *Backfill existing roles.* Write the three lists for the roles in use today, starting with reviewer, supervisor and architect.

**marvel**

- *Inventory reference on the cluster.* Let a cluster or workspace name its inventory file, and re-project live seats when it changes.
- *Generated policy input.* Feed projection from the generator through a policy's `settings_file`, which marvel already reads and re-reads (marvel#1061), with a hand-written `Policy` taking precedence during migration.
- *Fail closed on a protected role.* A file-backed policy already holds the spawn when its file cannot be read (marvel#1061). Extend that to a role whose protected class is non-empty and whose policy is inline or missing.
- *codex projection surface.* Emit `-s`, `-a` and the trust setting from the generator, and report what codex cannot enforce.
- *opencode projection surface.* Merge the generator's `permission` key into the `OPENCODE_CONFIG_CONTENT` JSON a deployment already passes for MCP, and settle whether marvel takes that JSON over from the manifest or the generator edits the manifest's value.
- *generic projection for crush.* Write `permissions.allowed_tools`, and surface "protected class not enforceable" in `marvel describe`.
