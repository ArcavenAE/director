# Design brief 10: the local broker as marvel's first supervised non-agent workload

Status: shape for review, 2026-09-15. Rulings in section 9 are proposed with
a default each; the operator's word makes them binding. Filed because brief 9
seam S6 is blocked: there is no broker start for marvel to inject the seed
into, and the two tickets it rides on (aae-orc-e9g8i, aae-orc-xy1dh) carried
an undecided fork and two fields brief 9 made stale.

## 0. Why now

Brief 9 landed S1 through S5 in one day (marvel #258, #259, #261, #262,
#263). A daemon can now receive the leaf seed over `mrvl://`, hold it in
memory, reveal it on the local socket only, and survive a reexec without
losing it silently. The seed's consumer does not exist. Nothing in marvel
starts, renders, or watches a nats-server; the phase-0 broker on this host
runs anonymous on 4222 from a hand-written conf, and the twin launcher
hardcodes its URL. The gaps between here and a cluster whose supervisor
reaches the director on another host are listed in section 8; this brief
takes the marvel-side ones and rules the fork that stopped the builder.

## 1. Running state (verified in the trees, this date)

- marvel `Cluster` is four fields: Name, Socket, Server, Identity
  (`internal/config/config.go:173`). Name is unvalidated; `my.cluster` loads.
- The only startup probe is tmux (`internal/daemon/daemon.go:228`,
  `tmux.NewDriver` with `exec.LookPath`). No code supervises a non-agent
  process. `internal/otel` is a stub.
- Session environment is built in `baseEnv` (`internal/runtime/adapter.go`):
  MARVEL_SESSION, ROLE, TEAM, WORKSPACE, DIRECTOR_AGENT_ID (R-84 S0),
  BEADS_ACTOR, and when a socket exists MARVEL_SOCKET plus the heartbeat
  token. No bus URL, no bus credential.
- The shim reads NATS_URL and, for authentication, DIRECTOR_NATS_CREDS or
  DIRECTOR_NATS_USER with DIRECTOR_NATS_PASS (`director-mcp/bus.go:61-64`).
- The phase-0 authorization block (director#4, `probe/nats-phase-0/
  authorization.conf`) is written and proven on 4223, not active on 4222;
  aae-orc-umw8p is open. Its shape: an admin user, one user per team confined
  to `agent.<workspace>.<team>.>` plus the JetStream and KV plumbing.
- The leaf example (`probe/nats-global-tier/leaf-remote.conf.example`) adds a
  JetStream domain and one remote with `nkey: $DIRECTOR_LEAF_NKEY`, and the
  supervisor's extra grants for the global prefix.
- A fresh broker is bare (finding-166). The twin runbook's precondition 6
  provisions AGENT_INBOX, AGENT_AUDIT, and AGENT_STATE by hand.
- Per-HOME isolation is ruled (marvel `docs/design/daemon-isolation.md`
  decisions 1 and 4): every daemon-owned artifact lives under `~/.marvel`,
  and the tmux socket name derives from the HOME.
- nats-server 2.14.6 is installed from brew on this host; marvel pins tools
  in `mise.toml`.

## 2. The fork, ruled: a daemon-owned child process

xy1dh asked marvel to decide between a first-class non-agent workload kind
and an interim ruling to run the broker through the generic adapter as a
single-replica role. I rule for a third shape that is smaller than the
first and honest where the second is not.

**The broker is a child process of the daemon, supervised by a small
`internal/bus` package. It is not a Role, has no pane, and is not a Store
resource.** What the daemon does with it: render its conf, start it, wait
for its listener, provision its streams and bucket, watch its pid and its
monitoring endpoint, restart it under the existing crash-loop backoff, and
adopt it across a daemon reexec.

Why not a role in a pane (the generic-adapter interim):

- A role lives inside a workspace and a team, and the broker predates every
  team; the first `marvel apply` would find its own dependency missing.
- Decision 4 gives each daemon its own tmux server. Killing that server (a
  reset, a reexec that fails to adopt) would take the bus down with the
  panes, and the bus is what a successor session reconnects to.
- Role health is pane liveness or a heartbeat. A broker's health is
  listener-up plus provisioned. A pane holding a broker that never bound
  its port reads healthy, which is finding-166 again with marvel as the
  author.
- baseEnv stamps MARVEL_SESSION and a heartbeat token into the pane. That
  is wrong-shaped for infrastructure and gives the broker a credential it
  has no use for.

Why not a general workload kind now: the Credential resource cost five
files because it rode the Store. A general Service kind would need
manifest surface, reconciliation, and a scheduling story, and the second
consumer (the OTEL collector, aae-orc-h6ck) is still a probe. One supervised
child with a clear interface is the shape (d) of the sidecar study
("configuration generation, not a new competence"); if a second component
arrives, `internal/bus` is the template to generalize from, not a design to
undo. Gradual elaboration, SOUL section 7.

The interim is not the generic adapter. It is `bus.managed = false`: the
daemon renders nothing and starts nothing, sessions still receive the bus
URL and team credential from config (section 4), and the operator runs the
broker by hand as today. This is what lets 1qyo3 land before xy1dh and keeps
the phase-0 fleet running through the transition.

## 3. Cluster config: the `bus` section (e9g8i, 962oy)

Topology and credentials live in `~/.marvel/config.yaml` on the daemon's
host, never in a workspace manifest. A manifest names no broker, host, port,
or credential; the same manifest applies on kinu and on mokuzai (the
portability principle of R-92 and marvel #255, proposed as R-96 below).

```yaml
clusters:
  - name: kinu                     # subject token, validated (962oy)
    socket: ~/.marvel/run/marvel.sock
    bus:
      managed: true                # false: adopt an existing broker
      listen: 127.0.0.1:4222       # explicit; refused if held by a stranger
      url: nats://127.0.0.1:4222   # what sessions receive; defaults from listen
      store_dir: ~/.marvel/state/nats   # default; Layout.StateDir()/nats
      hub:
        url: nats-leaf://192.168.100.110:7442   # optional; sets the leaf remote
```

Rules:

- `name` is validated to `[A-Za-z0-9_-]` at `cluster add` and at load,
  rejected with the offending byte named, never rewritten (962oy, R-94,
  the R-76 validToken shape). It becomes the JetStream domain and the
  global subject token.
- `listen` is explicit. No derivation from the HOME tag: two daemons in one
  HOME (the twin runs on `~/.marvel/run/twin.sock`) would derive the same
  port. If the port is held at start by a process marvel did not record in
  its pidfile, start refuses loudly, the same posture as the daemon pidfile
  guard.
- No credential path field. The leaf seed is the Store's transient
  `bus/leaf` credential (brief 9). No CA field yet: TLS is deferred under
  the ratified interim LAN posture (brief 8 section 9); the field is added
  when TLS is.
- `hub.url` absent means a local-only cluster. Present with no `bus/leaf`
  credential in the Store means the broker starts without the leaf remote
  and the daemon says so (section 6).

## 4. Session environment (1qyo3, the R-85 spawn half)

`baseEnv` gains, from the cluster's bus section:

- `NATS_URL` from `bus.url`. The twin launcher's hardcoded default retires.
- `DIRECTOR_NATS_USER` = the session's team name; `DIRECTOR_NATS_PASS` = the
  team password the daemon generated (section 5). When the bus section
  carries no authorization (an adopted anonymous broker), neither is set and
  the shim connects as today.
- Per-session users, one per session with the R-85 mint-at-spawn pattern,
  are a follow-on on the same mechanism: the daemon adds a user to the
  authorization file at spawn and reloads. Not in this brief's tickets;
  filed when the team credential is live.

This lands with `managed: false` against the phase-0 broker, which is why it
is its own ticket and not a clause of e9g8i.

## 5. The rendered conf and the authorization file (e9g8i, apeoc)

Marvel writes `~/.marvel/state/nats/nats-server.conf` the way policy
projection writes a settings file (`internal/session/projection.go`), from
the bus section and the applied teams:

```
listen: <bus.listen>
http: 127.0.0.1:<listen port + 4000>      # monitoring, loopback only (/leafz)
jetstream { store_dir: "<bus.store_dir>/store", domain: <cluster name> }
include "authorization.conf"
leafnodes { remotes: [ { urls: ["<hub.url>"], nkey: $DIRECTOR_LEAF_NKEY } ] }   # only with hub.url and a seed
```

`authorization.conf` sits beside it at mode 0600 (the mode `internal/paths`
enforces for keys) and carries: the admin user (marvel's own, for
provisioning and break-glass, never handed to a session); one user per
applied team, confined exactly as the director#4 block confines `ops`, with
the workspace and team substituted; and for the `supervisor` role's team,
when `hub.url` is set, the global grants from the leaf example (publish
`global.director.inbox`, `$JS.global.API.>`; subscribe `global.<cluster>.>`).
Passwords are generated by the daemon, held in memory for injection, and
written only to this file. When a team is applied or removed the file is
regenerated and the broker reloaded with SIGHUP, which nats-server honors
for authorization without dropping unaffected clients.

Why a file and not environment variables for the team passwords: the server
reads its environment once at start, so a team applied later could not be
given a password without a broker restart. The leaf seed alone stays
environment-only, because it is the one artifact that is authority at
another party (the hub) and brief 9 ruled it never touches disk. The team
passwords are marvel-issued, mean nothing outside this broker, and are
revoked by rewrite and reload: issuance under ADR-009, the same class as the
heartbeat token.

Provisioning (apeoc) runs as the admin user after the listener answers and
before any session may spawn: the three objects of precondition 6 with its
exact parameters, idempotent, exists is success. This is finding-166 fix
shape 3 made automatic and retires the runbook step for managed clusters.

## 6. Supervision (xy1dh, vqq2c)

Start order in the daemon: tmux probe (as today), then bus, then team
reconcile.

1. Resolve `nats-server` with LookPath. Absent is a loud start failure
   naming the runtime dependency (mise or brew), the tmux posture.
2. Render (section 5). Read the `bus/leaf` credential from the Store; when
   present, set `DIRECTOR_LEAF_NKEY` in the child's environment and render
   the remote. Nothing else carries the seed.
3. Start the child in its own process group with a pidfile under
   `~/.marvel/run/`, stdout and stderr to `~/.marvel/log/nats-server.log`.
4. Wait for the listener with a bounded connect loop; then provision
   (section 5); then mark the bus ready and emit `bus.started`.
5. Until ready, every role holds. The hold is the RoleHold posture the
   controller already has (`internal/team/controller.go`, the cxdf gate):
   actual below desired, nothing spawned this tick, one `bus.unavailable`
   event per role per transition. This is R-93 at the control plane: no
   session spawns against a bus that is down or bare.
6. Watch. Pid exit is `bus.crashed`; restart under the existing crash-loop
   backoff; sessions are not restarted, their shims reconnect (nats.go
   reconnects by default; presence keys expire and are rewritten on the
   next beat). The monitoring endpoint's `/leafz` is polled on the R-56
   cadence (30 s) and the leaf connection count emits `bus.leaf.up` and
   `bus.leaf.down` on transition. Leaf state is never a health state: a
   down hub restarts nothing (marvel-builder's R-85 answer, kept).
7. `hub.url` set with no seed: start without the remote, emit
   `bus.leaf.unenrolled` once. A later `credential put bus/leaf` re-renders
   and restarts the broker, since the environment cannot change on reload;
   clients reconnect. `credential delete` clears memory only; the hub-side
   revoke stays authoritative (brief 9 E4).
8. Daemon reexec adopts the running broker: pidfile alive and listener
   answering means no restart, the same adopt-on-restart discipline the
   panes have. Daemon stop stops the broker unless `--keep-bus` is given,
   so a routine daemon restart does not drop the fleet's bus by accident
   while an operator who means to keep it can.

Events added: `bus.started`, `bus.stopped`, `bus.crashed`, `bus.provisioned`,
`bus.unavailable`, `bus.reloaded`, `bus.leaf.up`, `bus.leaf.down`,
`bus.leaf.unenrolled`. CLI: `marvel bus status` prints managed or adopted,
listener, domain, provisioned, leaf state, pid, and the conf path.

## 7. What this discharges

- Brief 9 S6 (vqq2c): the seed has a consumer, and the interim `--reveal`
  start line retires for managed clusters.
- aae-orc-umw8p (director#4): the authorization block is active by
  construction on every managed cluster; the coordinated relaunch it asked
  for becomes the flip to `managed: true` (section 8, bxg5f rewritten).
- finding-166 shapes 1 and 3 at the control plane, and R-93's spawn gate.
- Twin runbook precondition 6 for managed clusters.
- R-85's spawn half (bus URL and team credential at launch) and R-94's name
  validation.

## 8. Gaps to a functional service, in order

"Functional" here means: a supervisor session inside a marvel cluster on
mokuzai exchanges envelopes with the director seat on kinu through the
global tier, with no hand-edited conf and no seed on disk.

Marvel side, this brief:

1. 962oy, Cluster.Name validation. Small, no dependencies.
2. e9g8i, the bus section and the rendered conf plus authorization file.
3. 1qyo3, session environment from the bus section (`managed: false`
   works). In parallel with 4.
4. xy1dh, supervision: start, hold, watch, adopt.
5. apeoc, provisioning at start.
6. vqq2c, S6, the seed into the child's environment.

Director side, existing tickets:

7. gvf6k, shim global mode (domain-qualified JetStream, the global inbox,
   GLOBAL_PRESENCE, `global://` addresses). Without it a supervisor cannot
   address the director even once its broker is a leaf. P1, no dependencies,
   can start now.
8. bxg5f, the phase-0 relaunch on kinu, rewritten: once 1 through 6 land,
   this is `managed: true` with `hub.url` in the kinu cluster's config and a
   `credential put bus/leaf`, run at a coordinated window because the
   authorization block goes live with it.
9. qu88n, the hub as a launchd service on kinu. Hygiene for a demo,
   required for a service.
10. juesa, the tap release; skippy upgrades before enrolling. Unaffected by
    this brief.
11. av2v1, the cross-host stage. Depends on 7 and 8 and on skippy's cluster
    reaching 6.

Not required for functional, but in the same neighborhood: z63a4 shape 2
(the shim-fed marvel heartbeat, so the health column carries bus liveness),
8mcnf (stranded consumers), lebdu (presence delete on exit).

## 9. Decisions for the operator (defaults stated)

- D1. The fork: daemon-owned child process, not a Role and not a Store
  resource (section 2). Default: as written.
- D2. Provisioning client: marvel takes `nats.go` as a Go dependency for the
  admin connection (provision, reload-free checks), rather than shelling to
  the `nats` CLI. Default: nats.go; the CLI would be a second runtime
  dependency with a version to pin, and marvel will want the client for the
  heartbeat and events work later.
- D3. Team passwords in a 0600 authorization file with SIGHUP reload, the
  leaf seed alone environment-only (section 5). Default: as written.
- D4. Broker lifetime bound to the daemon except across reexec; `--keep-bus`
  on stop. Default: as written.
- D5. Monitoring on loopback at listen port plus 4000 (the convention the
  hub already uses, 4242 to 8242). Default: as written.
- D6. Candidate requirement R-96: bus topology and credentials live in the
  cluster config and the daemon, never in the workspace manifest; a
  manifest names no broker, host, port, or credential. Ratify or fold into
  R-85.

## 10. Tickets

Filed 2026-09-15 with labels aae-orc, marvel, director, source:session:
aae-orc-962oy (validation), aae-orc-1qyo3 (session environment),
aae-orc-apeoc (provisioning). Refreshed with this brief's rulings in their
notes: aae-orc-e9g8i, aae-orc-xy1dh, aae-orc-vqq2c. Dependency edges:
e9g8i and xy1dh on 962oy; xy1dh on e9g8i; 1qyo3 on e9g8i; apeoc on xy1dh;
vqq2c on e9g8i, xy1dh, and qecha. bxg5f is rewritten when 6 lands.
