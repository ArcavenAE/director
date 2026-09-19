# Party log: cluster identity and seed path for the leaf topology (2026-09-18)

Subject: DECIDE three things for the leaf-attach topology. (a) the standard
seed path; (b) the cluster-identity scheme, including how it disambiguates
several clusters on one host and how it handles a cluster leafing to a
different global NATS than its neighbor; (c) what changes in
`global-nats-leaf-attach.md`, and whether the marvel render and the
Terraform for the shared tier need the narrow bus.domain override
(marvel#309) or a wider cluster-identity primitive. Three rounds, then a
decision. Redaction held throughout: no origin organization, its short
environment tokens, or its infrastructure repository is named; the durable
global tier is "the shared infrastructure NATS."

## Grounding read before the room opened

I verified every load-bearing premise at the source before casting, because
a ticket is a claim about the world and this one prescribes a method
(narrow override versus wider primitive) I should not accept on faith.

- marvel `internal/bus/render.go`: `validToken` (line 110) enforces charset
  `[A-Za-z0-9_-]` and rejects rather than rewrites (R-76); `Spec.Domain` is
  the cluster name and becomes the JetStream domain; the leaf seed is read
  from `DIRECTOR_LEAF_NKEY`, unquoted and whole-value, and the leafnodes
  block renders only when a hub URL and a leaf seed both exist and the
  operator has the leaf attached.
- marvel `internal/config/config.go:773`: `ValidateClusterName` enforces the
  same charset and rejects rather than rewrites (R-94); `AddCluster(name,
  addr, identity)` carries a name, an address, and a separate SSH-key
  identity that backs trust, distinct from the typeable name.
- director `sim/design/bus-credential-enrollment.md`: the leaf seed is a
  Store credential `bus/leaf`, kind `nats-nkey-seed`, `Persist: false`,
  delivered by enrollment over `mrvl://` and read into the broker's
  environment at start. "The seed exists in the daemon's memory and the
  broker's process environment, nowhere on disk." `local-broker-supervision.md:124`
  says it outright: "No credential path field."
- director `probe/nats-global-tier/recipe-mokuzai.md:12`: the one on-disk
  seed today is the operator-side transient stage `~/.director/nats/leaf-mokuzai.nk`,
  mode 0600, consumed by `credential put` then removable. The hub tier home
  is `~/.director/nats-global`; the phase-0 broker home is `~/.director/nats`
  via `DIRECTOR_PHASE0_HOME`.
- Hostname and NATS-charset facts (a web-searching researcher, cited in the
  recommendation): a raw hostname is not a safe NATS token (dots, the
  `.local` suffix, spaces and apostrophes in default macOS names), hostnames
  are not unique (default names collide; mDNS renumbers the advertised name
  but not the OS hostname), and NATS subject tokens are case-sensitive while
  hostnames are not. A short, sanitized, lowercase label is safe; a bare
  hostname is not.

So the single token already does quadruple duty (JetStream domain, the
`global.<label>.>` subject partition, the `leaf-<label>` NKey user and seed
file, and the Store credential binding), the charset is already narrower
than a hostname on purpose, and the daemon deliberately holds no seed path.
The room started from those facts, not from the ticket's framing.

## Cast

Five seats. Each is a distinct discipline; each web-checked any claim past
its own knowledge.

- Ident: identity and addressing architecture. Champions the single token.
- Tobi: NATS naming and charset. Domain, subject, and case coherence.
- Rem: marvel render and the shared-tier Terraform. The implementation cost.
- Wend: operator experience and typeable handles. The human types this.
- Skarp: the keep-it-simple skeptic. Pushes hard against new machinery.

## Round 1: opening positions

**Ident:** The token is already the identity primitive. One value is the
domain, the subject partition, the enrollment id, and the seed filename. That
is not an accident to be refactored; it is the property that keeps four
surfaces from disagreeing. My position on (b): keep the single token, keep
the charset gate, do not split it.

**Tobi:** I will hold you to why the charset is what it is, because it is
load-bearing. A NATS subject token cannot contain a dot; the dot is the
separator. A JetStream domain folds into `$JS.<domain>.API`, so it inherits
the same constraint. Subjects are case-sensitive. A hostname breaks all
three: `kinu.local` has a dot, "Avi's MacBook Pro" has a space and an
apostrophe, and the hostname layer treats `Kinu` and `kinu` as the same
machine while NATS treats them as two subjects. So `[A-Za-z0-9_-]` is not
arbitrary; it is the intersection that is safe as a domain and a subject at
once. I am with Ident, and I want lowercase on top.

**Rem:** Then tell me what I build. The ticket asks whether this is the
narrow bus.domain override, marvel#309, or a wider primitive. I read #309 as
letting the bus domain be set independently of the cluster name. If the
token already is the domain and nobody has shown me two clusters that need a
domain different from their name, #309 solves a problem we do not have and
gives me a second field that can drift from the first. My opening lean: no
override, no new primitive, and I will defend that in round two.

**Wend:** I care about the person at the keyboard. They will type
`mrvl://<label>/...` and they will name the cluster once, at creation. If the
platform derives the label from the hostname behind their back, the first
time two machines answer to "MacBook Pro" the addresses collide and they
cannot tell which is which. So the label must be operator-chosen and it must
be legible. Hostname as a starting suggestion, yes; hostname as an automatic
value, no.

**Skarp:** Good, because every automatic derivation is a rule I have to
explain when it surprises someone. My whole position this session is one
sentence: the token exists, it is validated, it is already the domain and the
subject; the work here is a naming convention and two documentation modes,
not a code change. Prove me wrong.

## Round 2: the clashes

**Ident vs Skarp, on whether anything new gets built.**

**Ident:** I do not want #309 either, but I want to be honest that "no code
change" is only true if the uniqueness of the label is guaranteed somewhere.
The token is the `global.<label>.>` partition and the `GLOBAL_TO_<label>`
stream on the tier. Two clusters with the same label on the same tier is a
subject and stream collision. Something has to stop that.

**Skarp:** Something already does, and it is not new code. Where does the
tier get provisioned?

**Rem:** The Terraform for the shared tier. The clusters are a map there, one
entry per cluster, and the entry provisions that cluster's stream and its
`leaf-<label>` NKey user. A Terraform map cannot hold two identical keys. The
map key is the label. So per-tier uniqueness is enforced structurally, by
HCL, the moment two entries would collide. I did not plan that; it falls out.
The only gap is that an HCL map key allows characters the NATS token does
not, so the Terraform needs a validation that the key matches `[A-Za-z0-9_-]`
and, I would add, is lowercase. That is a precondition block, not a redesign.

**Skarp:** So the uniqueness I was hand-waving is real and free, and the only
addition is a charset check on a map key. That is a convention with a lint,
not a primitive. I rest.

**Ident:** Agreed, with one honest edge: the map only enforces uniqueness
within one tier's Terraform. Marvel on a daemon can reject a label that
collides with another cluster it already knows, but it cannot see clusters on
a tier it does not run. Cross-tier uniqueness stays a convention plus the
hub-side reject when a duplicate NKey user or stream is provisioned. I can
live with that; I just will not claim marvel enforces what only the tier can.

**Tobi vs Wend, on lowercase: convention or enforcement.**

**Tobi:** I want lowercase enforced. Case-sensitivity is a genuine trap:
someone labels a cluster `Kinu`, the subject namespace is `global.Kinu.>`,
and the next person types `global.kinu.>` and hears silence. Lowercase closes
it.

**Wend:** And the moment you enforce it in code, you rewrite the operator's
input, and this codebase made a deliberate ruling not to do that. `validToken`
and `ValidateClusterName` reject, they do not rewrite (R-76, R-94). If I type
`Kinu` and the tool silently stores `kinu`, that is exactly the surprise the
reject-not-rewrite posture exists to prevent. Reject an uppercase label with
a message that says "labels are lowercase," fine. Silently downcase it, no.

**Tobi:** I will take reject-with-a-message. That keeps the posture and still
closes the trap. Uppercase is legal in the charset today, so this is a
convention the docs state and, if we want the guard, a validation that warns
or rejects, never a rewrite.

**Wend:** Then we agree. Lowercase is the convention; the charset stays as it
is; if a guard lands it rejects, it does not launder the input.

**Wend, on the two host cases.**

**Wend:** Two cases hide inside "multi-cluster-per-host," and they are not the
same. First: one host, two marvel clusters, on purpose. That is the
operator's own fleet goal, two local marvels. The answer is easy because the
label is operator-chosen: give them distinct labels, `kinu-a` and `kinu-b`,
or `kinu-dev` and `kinu-ci`. The host segment is convention for a human's
eye; the uniqueness is the whole label. Second, and worse: two different
physical hosts that happen to share a hostname. Default names collide, and
the hostname does not renumber the way the advertised name does.

**Ident:** The second case has no derivation that saves it. If we auto-derived
the label from the hostname, two machines would produce the same label and we
would be forced to auto-suffix, which is a rewrite and pollutes the subject
namespace with a number nobody chose.

**Wend:** So we state it unsupported by derivation. The operator must assign
distinct labels; the platform will not disambiguate two same-named hosts for
them. And it is safe to say so, because trust does not ride the label. The
SSH-key identity in `AddCluster` backs trust separately, so two same-named
hosts are never a security ambiguity, only an addressing collision the
operator resolves by labeling. That distinction is the reason the "unsupported"
is honest rather than a hole.

**Rem, on multi-global.**

**Rem:** The last sub-case, different global NATS per cluster, I already have.
Each cluster carries its own hub URL in the render spec and its own `bus/leaf`
credential minted by that tier's operator. Nothing in render.go assumes one
tier. Two people, two tiers, is the existing Mode D. One person with two
tiers is the same mechanism without the gateway. The missing piece was never
code; it was a documented mode and the rule that label uniqueness is scoped
to the tier. I will add the caveat that if two tiers are ever gateway-joined,
labels that were only tier-unique would then collide, so the safe convention
is globally distinct labels even across tiers that do not touch yet.

**On the seed path, all five.**

**Rem:** The seed path question half-dissolves once you read what is built. On
the daemon there is no path by design; the seed is a Store credential, held
in memory, read into `DIRECTOR_LEAF_NKEY` at broker start, `Persist: false`.
Adding a daemon-side path would reintroduce on-disk custody the design
deliberately removed. So the daemon answer is: no path, and that is the
feature.

**Skarp:** Then what is "the standard seed path" even asking?

**Rem:** The one place a seed touches disk: the operator stages the minted
seed before pushing it. Today that is `~/.director/nats/leaf-mokuzai.nk`, mode
0600, in one recipe. The decision is to promote that one recipe's path to the
standard: `~/.director/nats/leaf-<label>.nk`, 0600, transient, consumed by
`credential put` then removable. The NKey user is `leaf-<label>`, the same
token again.

**Ident:** Which keeps the token doing its fourth duty and gives the operator
one path to remember, under the director home that already holds the phase-0
broker.

## Round 3: converge and decide

**Skarp:** Say the decision in four lines so I can object if it grew.

**Ident:** (a) Seed path: none on the daemon, by design; the operator stages
`~/.director/nats/leaf-<label>.nk` at 0600, transient. (b) Identity: keep the
single token, charset `[A-Za-z0-9_-]`, operator-chosen not hostname-derived,
lowercase by convention, unique per tier (structurally enforced by the
Terraform map key), same-hostname-two-hosts unsupported by derivation and
resolved by the operator labeling. (c) Docs: add a multi-cluster-per-host
mode and a multi-global mode to `global-nats-leaf-attach.md`, plus a short
identity-and-seed section. (d) Code: neither #309's override nor a wider
primitive; the token already is the primitive. The only additions are a
charset validation on the Terraform map key and the standardized stage path,
both conventions with a guard, not a redesign.

**Skarp:** It did not grow. I have no objection.

**Tobi:** Charset and case are right and grounded, not asserted.

**Wend:** The operator names it once, legibly, and the two host cases are
answered without a silent rewrite. Agreed.

**Rem:** I know exactly what I build, and most of it is documentation. #309 is
the wrong lever and I will say so in the recommendation with the reason, not
just the verdict.

**Ident:** Unanimous. The decision is the single token, affirmed, with a
naming convention around it and two documentation modes; the marvel and
Terraform impact is a map-key charset guard and a standardized operator-side
stage path, not the bus.domain override.

## Convergence

The room did not split on the core and I did not force it. The one real
argument, lowercase enforcement versus convention, resolved cleanly onto the
reject-not-rewrite posture the codebase already holds: lowercase is the
convention, a guard may reject an uppercase label with a message, nothing
silently rewrites the operator's input. The decision, the per-tier
uniqueness mechanism, the two host cases, the multi-global handling, the doc
changes, and the explicit rejection of marvel#309 with its reason are in
`recommendation.md`.

## Sources the panel cited

The hostname and NATS-charset facts from a web-searching researcher (macOS
Computer Name versus LocalHostName sanitization, the RFC 1123 label rule,
NATS subject-token and JetStream-domain constraints, subject case
sensitivity), anchored to the NATS documentation and the platform's own
verified render and config code. Every load-bearing external claim is
anchored; the code claims are pinned to the files and lines read above.
