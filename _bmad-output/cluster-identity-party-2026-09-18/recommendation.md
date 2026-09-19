# Cluster identity and seed path for the leaf topology: decision

Status: DECIDED by a three-round design party, held for the operator.
Nothing committed. Date: 2026-09-18. Party record in `party-log.md`.
Redaction held: no origin organization, its short environment tokens, or its
infrastructure repository is named; the durable global tier is "the shared
infrastructure NATS."

## The decision in one paragraph

Keep the single typeable token as the cluster's identity. It already does
four jobs at once (the JetStream domain, the `global.<label>.>` subject
partition, the `leaf-<label>` NKey user and seed filename, and the Store
credential binding), and its charset `[A-Za-z0-9_-]` is deliberately narrower
than a hostname because a raw hostname is not a safe NATS token. Do not add
marvel#309's bus.domain override and do not build a wider cluster-identity
primitive: the token already is the primitive, and splitting it reintroduces
the two-source-of-truth drift the single token prevents. The label is
operator-chosen (never derived from the hostname), lowercase by convention,
and unique per tier, a uniqueness the shared-tier Terraform enforces
structurally because its clusters map cannot hold two identical keys. The
seed has no path on the daemon by design (a Store credential read into
`DIRECTOR_LEAF_NKEY` at broker start, on disk nowhere); the one on-disk seed
is the operator-side transient stage, standardized as
`~/.director/nats/leaf-<label>.nk` at mode 0600. The work is a naming
convention, a charset guard on the Terraform map key, and two new
documentation modes; it is not a code redesign.

## (a) The standard seed path

Two surfaces, and naming both is the answer.

**Cluster daemon (production): no seed path, and that is the feature.** The
leaf seed is a Store credential named `bus/leaf`, kind `nats-nkey-seed`,
`Persist: false`, delivered by enrollment over `mrvl://` and read into the
broker's environment variable `DIRECTOR_LEAF_NKEY` at broker start. It exists
in the daemon's memory and the broker's process environment, nowhere on disk
(`bus-credential-enrollment.md` E2 and E3; `local-broker-supervision.md:124`,
"No credential path field"). Do not add a daemon-side path. A path would
reintroduce the on-disk custody the design removed on purpose, and it would
add a second place the seed can be stale relative to the Store.

**Operator mint and stage (and the interim hand-start before broker
supervision exists): `~/.director/nats/leaf-<label>.nk`, mode 0600,
transient.** This promotes the one path already in use
(`recipe-mokuzai.md:12` stages `~/.director/nats/leaf-mokuzai.nk` at 0600) to
the standard. The seed is minted with `nats auth nkey gen user` for NKey user
`leaf-<label>`, staged at that path, consumed by
`marvel --cluster <label> credential put bus/leaf --kind nats-nkey-seed --stdin < ~/.director/nats/leaf-<label>.nk`,
then removable. It lives under `~/.director/nats`, the director home that
already holds the phase-0 broker; the hub tier home stays
`~/.director/nats-global`. The filename embeds the label, so the seed file,
the NKey user, and the subject partition all read the same token.

## (b) The cluster-identity scheme

**Label source and value.** The label is a single token, charset
`[A-Za-z0-9_-]`, operator-chosen at cluster creation (`AddCluster(name, addr,
identity)`), never auto-derived from the hostname. It is the value in
`Spec.Domain` (so it is the JetStream domain), the value in the
`global.<label>.>` subject partition and the `GLOBAL_TO_<label>` stream, the
`leaf-<label>` NKey user and seed filename, and the display binding on the
`bus/leaf` credential. One token, four surfaces, no second source of truth.

**Why the charset is narrower than a hostname, and why that is correct.** A
raw hostname is not a safe NATS token. A NATS subject token cannot contain a
dot (the separator), so `kinu.local` is illegal; a JetStream domain folds
into `$JS.<domain>.API` and inherits the same rule. Default macOS Computer
Names carry spaces and apostrophes ("Avi's MacBook Pro"). NATS subject tokens
are case-sensitive while hostnames are not, so `Kinu` and `kinu` are one host
but two subjects. And hostnames are not unique: default names collide and the
OS hostname does not renumber the way an mDNS advertised name does. marvel
already rejects an unsafe token rather than rewriting it (`validToken`,
render.go:110, R-76; `ValidateClusterName`, config.go:773, R-94). "The
hostname is the natural choice" stays a convention for the human picking a
label (global-bus-tier.md), not an automatic derivation.

**Lowercase by convention, not by rewrite.** Recommend lowercase labels,
because case-sensitivity is a real trap: a `Kinu` label makes the namespace
`global.Kinu.>` and the next person's `global.kinu.>` hears silence. The
charset permits uppercase today, so this is a documented convention; if a
guard is wanted it may reject an uppercase label with a message ("labels are
lowercase"), but it must never silently downcase the input, which would
violate the reject-not-rewrite posture the codebase deliberately holds.

**Uniqueness scope is the tier the cluster leafs to.** Because the label is
the subject partition and the stream name on the tier, it must be unique among
all clusters attached to the same global NATS. This one rule is the
disambiguation primitive for both sub-cases below.

**Disambiguating several clusters on one host.** One host may run several
marvel clusters (the operator's own goal, two local marvels). Since the label
is operator-chosen, give each a distinct label. Convention: bare `<host>`
when one cluster per host (`kinu`, `mokuzai`); a `<host>-<qualifier>` compound
when several (`kinu-a` and `kinu-b`, or `kinu-dev` and `kinu-ci`), staying
inside the charset. The host segment is convention for human legibility;
uniqueness is the whole label, not the host part.

**The same-hostname-two-hosts case is unsupported by derivation.** Two
physically distinct hosts that share a hostname cannot both use the bare
hostname as their label on the same tier. We do not auto-suffix to resolve
it, because auto-suffixing is a rewrite and pollutes the subject namespace
with a number nobody chose. The operator must assign distinct labels
(`avi-laptop`, `avi-laptop-2`). This is safe to state as unsupported because
trust does not ride the label: the separate SSH-key identity in `AddCluster`
backs trust, so two same-named hosts are never a security ambiguity, only an
addressing collision the operator resolves by labeling.

**Handling different global NATS per cluster.** A cluster leafing to a
different global NATS than its neighbor is already representable: each cluster
carries its own hub URL in the render spec and its own `bus/leaf` credential
minted by that tier's operator; nothing in render.go assumes one tier. Two
people with two tiers is the existing Mode D (joined by a gateway); one
person with two tiers is the same mechanism without the gateway. The label's
uniqueness scope is per-tier, so a label may in principle repeat across two
tiers that never gateway-join. The safe convention is globally distinct
labels even across tiers that do not touch yet, because a future gateway
join would make two tier-unique-but-colliding labels clash. The missing piece
was never code; it was the documented mode and the per-tier uniqueness rule.

## (c) What changes in global-nats-leaf-attach.md

Additive. The existing Modes A through D and the finding-004 material stand.

1. **New "Cluster identity and the seed" section**, placed before Operating
   Modes. States: the single token and its charset; the four surfaces it
   serves; operator-chosen not hostname-derived, with hostname as a
   convention; lowercase by convention with reject-not-rewrite; the per-tier
   uniqueness rule; the same-hostname-two-hosts unsupported statement with the
   SSH-key-backs-trust reason. Then the seed: no daemon-side path (Store plus
   `DIRECTOR_LEAF_NKEY`), the operator-side transient stage
   `~/.director/nats/leaf-<label>.nk` at 0600, the `leaf-<label>` NKey user,
   the `bus/leaf` credential.

2. **New Mode E: several clusters on one host.** Two local brokers on one
   host, each its own marvel cluster with a distinct label (`kinu-a`,
   `kinu-b`), each leafing up to the same shared-tier NATS with its own
   `leaf-<label>` seed. The point the diagram makes: the labels are the
   disambiguator, the host is convention, and the two clusters share nothing
   but the tier they leaf to.

3. **New Mode F: multiple global tiers, same operator.** Two clusters whose
   hub URLs point at two different tiers the same operator runs; each cluster
   leafs to its own tier with its own seed; label uniqueness is scoped
   per-tier, with the globally-distinct-labels convention noted for a future
   gateway join. This is distinct from Mode D, which is the cross-person
   gateway case; Mode F is one operator, no gateway, two tiers.

4. **Update the "Open questions" per-person-isolation bullet** to cross-
   reference Mode F as the concrete multi-tier answer, and note that the
   account-per-person versus server-per-person choice does not change the
   identity scheme (the label is unique per tier either way).

## (d) marvel render.go and shared-tier Terraform impact

**This is neither the narrow bus.domain override (marvel#309) nor a wider
cluster-identity primitive.**

**Reject marvel#309 for this, with the reason.** #309 would let the bus
domain be set independently of the cluster name. The token already is the
domain, and no case has been shown that needs a domain different from the
cluster name. Adding the override gives a second field that can drift from the
first and reintroduces exactly the two-source-of-truth risk the single token
prevents. It solves a problem this design does not have. If a genuine need for
a domain distinct from the name ever appears, reopen #309 then, on that
evidence; do not land it speculatively now.

**Reject a wider cluster-identity primitive.** The token, gated by
`validToken` and `ValidateClusterName`, already is the identity primitive.
A new abstraction over it is machinery without an established need.

**What does change (small, and mostly convention):**

- **Shared-tier Terraform (the nats-director configuration): a charset guard
  on the clusters map key.** The map key is the label and it flows straight
  through to the `GLOBAL_TO_<label>` stream and the `leaf-<label>` NKey user
  the tier provisions. An HCL map key allows characters the NATS token does
  not, so add a validation (a precondition or a variable validation block)
  that each map key matches `^[a-z0-9_-]+$`. This also gives per-tier label
  uniqueness for free: a Terraform map cannot hold two identical keys, so two
  clusters colliding on a label is a plan-time error, not a runtime subject
  clash. This is the structural enforcement the identity scheme relies on.

- **Enrollment and recipe docs: standardize the operator-side stage path.**
  `~/.director/nats/leaf-<label>.nk` at 0600 becomes the documented standard
  in `bus-credential-enrollment.md` and the recipes, replacing the one-off in
  `recipe-mokuzai.md`. No render.go change; the daemon already reads the seed
  from the Store into `DIRECTOR_LEAF_NKEY`.

- **render.go: no change to the identity model.** `Spec.Domain` is the token,
  `validToken` is the gate, the leaf seed is Store-plus-env with no path. The
  only optional addition is a lowercase guard consistent with reject-not-
  rewrite (reject an uppercase or dotted label with a message), and even that
  is optional over the existing charset reject, which already blocks dots,
  spaces, and the dangerous characters; the lowercase rule can start as a
  documented convention and gain a guard later if drift appears.

- **marvel daemon: reject a label that collides with a cluster it already
  knows.** Marvel can enforce uniqueness among the clusters one daemon runs;
  it cannot see clusters on a tier it does not run, so cross-tier uniqueness
  stays the Terraform map key plus the hub-side reject when a duplicate NKey
  user or stream is provisioned. State this boundary honestly: marvel does
  not enforce what only the tier can.

## What stays true

- One token, four surfaces, no second source of truth. This is the property
  the whole decision protects.
- Trust rides the SSH-key identity in `AddCluster`, never the typeable label,
  which is what lets the same-hostname-two-hosts case be "unsupported by
  derivation" without being a security hole.
- The seed's custody model is unchanged: Store, memory, env-only at the
  broker, `Persist: false`; the only disk touch is a transient operator-side
  stage the operator removes.
- The topology decisions (leaf now, gateway later) and the identity decision
  are independent: this changes how a cluster is named and how its seed is
  staged, and touches neither the leaf-versus-gateway layering nor the A2A
  envelope.

## Note on a dedup from the coordinator

The marvel Role env-field feature gap (the one belonging with the
Cluster.Bus and service-record work) is already filed as marvel#311 by the
marvel builder, premise-verified in code and cross-linking #308. This
decision does not refile it; if the service-record linkage adds anything, it
belongs as a comment on marvel#311, not a new issue.
