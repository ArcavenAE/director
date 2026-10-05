# Joining the global tier from mokuzai (skippy's cluster)

The hub runs on kinu (kinu.local), leaf port 7442. Your
supervisors keep talking to your own local broker; the leaf link carries the
director channel. Every supervisor on the cluster joins the global tier: the
supervisor and research-supervisor roles, both with
`DIRECTOR_GLOBAL_ROLE=supervisor`. Workers never do; the director holds the
one fleet address. Nothing in your supervisors
changes until the shim's global mode ships; then it is three environment
variables in each supervisor's cast.

## Before you start: a network path to the hub

The joining host needs a routed path to the hub's leaf port. Being online is
not enough. Check, before anything else, that the hub's address is on your
own LAN (the same subnet) or on a network you have a route to, and that
`nc -vz <hub-address> 7442` connects. A host on a different network sees the
connection time out, which reads like a firewall or a down hub.

Two things on the joining host can make a good path look broken:

- A VPN client that claims all the private ranges (10/8, 172.16/12,
  192.168/16) sends the hub's address into its tunnel unless the hub is on
  your own LAN, where the on-link route is more specific and wins. If the VPN
  reconnects on a timer, an off-LAN path can break again without warning.
- On macOS, a terminal without the Local Network permission gets
  `No route to host` for every LAN address. marvel's admin guide covers it
  under "Host prerequisites", with keeping the host awake.

## Naming and addresses: two rules from bring-up

**Name your workspaces as kinu names them.** A joining cluster's workspaces
take the names kinu already uses for the same teams. A name chosen to match the
cluster (the obvious guess) works until the first seat needs to be found by
workspace, and then every seat has to be removed and re-applied under the right
name, with new instance ids and stale presence keys until they expire. Ask for
the team-to-workspace list before you declare any team. One consequence to know
about: with matching workspace names, a bd actor does not say which cluster
wrote it, because it carries workspace, team, role, generation and index but not
the cluster.

**Use the hub's hostname, never a literal address.** The hub's address appears
in four places on a joining cluster: the hub URL in the bus config, the bd host,
the section header of the bd credentials file, and the local hub-address file.
A hostname in all four means a network move changes nothing you have to touch:
in one move, a leaf whose hub URL was a hostname reattached on its own, and one
configured with a literal address did not. When an address does have to change,
the hub URL is read only at daemon start, so the cost is a daemon restart and
then a seed push from the hub operator (the restart drops the transient seed;
the leaf shows `unenrolled` until it arrives), plus an edit to each of the other
three places.

## 0. What you receive out of band

One file from the operator, handed privately (never on GitHub, never on the
bus): the NKey seed for your cluster's leaf link. Store it at the standard
operator-side stage path `~/.director/nats/leaf-<label>.nk`, mode 0600; here
`<label>` is `mokuzai`, so the file is `~/.director/nats/leaf-mokuzai.nk`. It is a
bus credential the operator can revoke and re-mint; it is not your OAuth and it is
not a credential at any third party. This interim recipe reads the seed from that
file at each broker start (section 1); once your broker is marvel-supervised, the
seed instead lives in the daemon Store and the daemon keeps no seed path on disk (the
model in [bus-credential-enrollment.md](../../sim/design/bus-credential-enrollment.md)).

On a marvel-supervised cluster, your host need not hold the seed at all. Once
the hub's host is enrolled with your daemon, the hub operator pushes it from
there (`marvel --cluster <name> credential put bus/leaf --value-file <seed-file>`),
and pushes it again after every daemon stop, start or reexec. Declare the hub
in your bus config before the daemon starts; marvel's admin guide ("Connecting
to a shared hub") says why.

## 1. Broker config (local nats-server.conf)

Your broker needs a JetStream domain and one leaf remote. Adding the domain
to a running broker keeps its streams, buckets, and existing clients working.

```
jetstream {
  store_dir: "<your existing store dir>"
  domain: mokuzai
}
leafnodes {
  remotes: [
    { urls: ["nats-leaf://kinu.local:7442"], nkey: $DIRECTOR_LEAF_NKEY }
  ]
}
```

Start the broker with the seed in the environment, never in the file:

```sh
DIRECTOR_LEAF_NKEY="$(grep -m1 '^SU' ~/.director/nats/leaf-mokuzai.nk)" \
  nats-server -c <your nats-server.conf>
```

Your shims reconnect on their own after the restart; presence keys rewrite
within 30s.

If your broker runs the director#4 authorization block, add to the
supervisor user: publish allow `global.director.inbox`, `$JS.global.API.>`,
`$JS.ACK.>`; subscribe allow `global.mokuzai.>`, `_INBOX.>`.

## 2. Verify the link

Broker log shows `Leafnode connection created` and
`JetStream using domains: local "mokuzai", remote "global"`. Then, from any
client of your local broker:

```sh
nats --js-domain global kv put GLOBAL_PRESENCE presence.mokuzai.supervisor.test '{"role":"supervisor"}'
nats --js-domain global stream info GLOBAL_TO_mokuzai      # works
nats --js-domain global stream info GLOBAL_TO_DIRECTOR     # "no responders": the binding holding
```

## 3. The test message across kinu

Your side to the director:

```sh
nats --js-domain global pub global.director.inbox 'hello from mokuzai <nonce>' --jetstream
# expect: Stored in Stream: GLOBAL_TO_DIRECTOR ... Domain: "global"
```

The director answers on `global.mokuzai.supervisor.inbox`; read it:

```sh
nats --js-domain global consumer add GLOBAL_TO_mokuzai sup --pull --deliver new --ack explicit \
  --filter global.mokuzai.supervisor.inbox --defaults
nats --js-domain global consumer next GLOBAL_TO_mokuzai sup --raw
```

A publish to any other cluster's prefix, or a consumer on the director's
stream, fails with "no responders" and stores nothing; that is expected.

## 3b. The shim launcher: `director-mcp-seat`

`probe/nats-phase-0/director-mcp-seat` launches `director-mcp` for either a
marvel-managed agent or a hand-run seat, so one registration serves both:

```sh
codex mcp add director -- /path/to/director-mcp-seat
```

Note the registration carries **no identity**. That is the point. A literal
`--env DIRECTOR_AGENT_ID=… --env DIRECTOR_NATS_USER=…` is correct in exactly one
of the two modes: it is what a hand-run seat needs, and it OVERRIDES what marvel
already stamped into a managed session, so the agent joins the bus under a
hand-chosen id on the wrong broker user. The launcher defers to the environment
and falls back to the seat only when there is nothing to defer to.

The seat half needs a seat to fall back to. Declare one on the cluster's bus
(`~/.marvel/config.yaml`), which renders a broker user named `director` and
writes its password to `<StateDir>/nats/director.pass`:

```yaml
bus:
  managed: true
  seat: { workspace: <workspace>, team: <team> }
```

Without it, a hand-run shim against a managed broker fails with
`nats: Authorization Violation`, because a managed broker renders an
authorization block and a hand-launched shim gets no credentials from marvel.

### codex needs one more line

codex does not pass its environment to an MCP server: the child gets a fixed
allowlist (`HOME LANG LOGNAME PATH PWD SHELL SHLVL TERM TMPDIR USER`) and none
of marvel's stamps. `shell_environment_policy.inherit` does not change it —
that policy governs shell children, not MCP launches. Forward them per-server,
in the marvel manifest beside the command registration:

```yaml
runtime:
  image: codex
  command: codex
  args:
    - "-c"
    - 'mcp_servers.director.command="/path/to/director-mcp-seat"'
    - "-c"
    - 'mcp_servers.director.env_vars=["MARVEL_SESSION","MARVEL_TEAM","MARVEL_WORKSPACE","DIRECTOR_NATS_USER","DIRECTOR_NATS_PASS","NATS_URL","DIRECTOR_GLOBAL_DOMAIN","DIRECTOR_CLUSTER","DIRECTOR_GLOBAL_ROLE"]'
    - "-c"
    - 'mcp_servers.director.default_tools_approval_mode="approve"'
```

The `-c` registration is required rather than a convenience: marvel gives each
codex session a private `CODEX_HOME` seeded symlink-only, so a global
`codex mcp add` never reaches it. `default_tools_approval_mode` defaults to
`auto`, which still prompts on every director call and parks a standing agent
forever; `approve` is the value that lets it run unattended.

**Diagnostic.** If a marvel-managed agent appears on the roster as `director-seat`
instead of its session name, the `env_vars` line is missing from its role.

The last three names carry the global tier (section 4). Without them a codex
supervisor's shim never sees them and stays on the local tier, with no error.
Only a supervisor's role needs them; leave them off a worker's list.

## 4. Casting the supervisors onto the global tier

The shim's global mode is built (aae-orc-gvf6k). Cast every supervisor on the
cluster (the supervisor and research-supervisor roles; both take
`DIRECTOR_GLOBAL_ROLE=supervisor`) with three more environment variables, and
nothing else changes. One caveat for a research-supervisor: marvel's broker
renderer grants the global subjects only to a role literally named
`supervisor` (`GlobalAddressRoles` in marvel's `internal/config/config.go`).
So a research-supervisor reaches the global tier only through its team's
broker user, when that team also has a `supervisor` role. marvel#518
tracks this.

```sh
DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=mokuzai \
  DIRECTOR_GLOBAL_ROLE=supervisor director-mcp
```

The shim keeps its one connection to your local broker, consumes
`GLOBAL_TO_mokuzai` through the domain, beats presence into `GLOBAL_PRESENCE`
on the same 30s timer as its local presence, and accepts `global://director`
as a send address. `wait_for_message` polls both inboxes and names the tier it
found; `list_roster` shows both with a tier column. All three variables are
validated at spawn: the role is `supervisor` or `director` and nothing else,
and the cluster and domain are subject tokens.

Leave `DIRECTOR_GLOBAL_DOMAIN` unset and the shim behaves exactly as it did
before: local tier only, no hub traffic, no global addresses.

Run `director-mcp --preflight` with those variables set before casting: it
verifies the stream and the bucket through the domain and exits nonzero if the
link or the provisioning is missing, which is what keeps a supervisor from
coming up healthy and unreachable.

To prove the whole path on your host, `probe/nats-global-tier/verify-global-shim.sh`
runs 14 checks against the real hub with a throwaway leaf broker of its own,
leaving your live broker alone.

## What the interim LAN posture does not protect

No TLS on 4242 or 7442: anyone on the LAN can read envelopes on the wire and
can attempt a connection. Without a valid NKey they cannot publish or
subscribe (anonymous connections get an authorization violation), and the
seed never crosses the wire (the client signs a server nonce). Keep secrets
out of message bodies; authority never rides content anyway. TLS and DNS
arrive with the cloud placement.
