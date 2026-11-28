# When a host's seats go dark on the director bus

On 2026-11-28 mokuzai's seats dropped off the director bus and stayed off after the bus came back. Recovery was worked out by hand, one seat at a time. This procedure gives the next outage an order to check things in and a rule for which recovery to use on each seat. It also separates the steps that belong in code from the steps a runbook can carry, so the code fixes can retire runbook steps one at a time.

Line numbers: director's are at 5354503 (main when this was written) and marvel's at 6bcedac2; nats.go's are at v1.55.0. They drift as those files change, so read them at the named revision.

Status: draft for the supervisor (I-870). It names who acts at each step and grants nothing: inject, shift and restart stay with the roles that already hold them.

## 1. Detection, cheapest and most remote first

1. **Roster absence (from any host).** Each live shim rewrites its presence row every 30 s, and the row expires after 90 s (director `cmd/director-mcp/main.go:323-332`, `askledger.go:22`). If a host's seats are missing from `list_roster` for more than 90 s, the host is dark or its shims are. A clean exit deletes the row at once (`bus.go:2071-2089`). A row flagged `durable-unchecked` with "no responders" is not by itself a sign of an outage: remote rows carry that flag on a healthy day.
2. **Connections on the host's broker (on the host, read only).** `lsof -nP -iTCP:4222 -sTCP:ESTABLISHED`, or the broker's `connz` page if monitoring is on. Zero shim connections with the broker up means the shims gave up (section 3 below), not that the bus is down.
3. **Each seat's MCP log (on the host).** Claude Code writes the shim's stderr to `~/Library/Caches/claude-cli-nodejs/<cwd-slug>/mcp-logs-director/<timestamp>.jsonl` as `{"error":"Server stderr: director-mcp: ..."}`. Two lines decide the recovery: a pass-file line such as `DIRECTOR_NATS_PASS_FILE .../marvel-tokens-<tag>/<hash>.pass: no such file or directory` (needs a shift, see 2b), or a closed or refused connection (reconnect, see 2a).
4. **marvel daemon events (on the host).**
   - `heartbeat.refused` with cause `no-token` means the seat's token file is gone, not that the bus is down. Cause `stale-token` means an orphan from an earlier daemon is still sending heartbeats (marvel `internal/daemon/daemon.go:2297-2321`).
   - `bus.stopped` and `bus.leaf.down` cover the broker and the leaf to kinu.
   - A session whose harness state reads `uncovered` or `other` may be sitting on a prompt (see 2c).

Signals 1 and 2 tell whether the host is dark. Signals 3 and 4 tell which recovery each seat needs.

## 2. Recovery, per seat, in rising cost

First, the host itself. If the marvel daemon is down, only the operator at the host can bring it back. Start it with `marvel daemon`. A stray argument to `marvel stop` was what stopped it on 11-28 (I-868). When the daemon starts, it adopts or respawns the broker (marvel `internal/bus/supervisor.go:382-445`) and adopts the recorded panes. Check the leaf to kinu next: marvel's admin guide says a daemon stop and start drops the leaf seed, yet on 11-28 the leaf returned. That conflict is unresolved (see section 6), so check the leaf rather than assume either way.

Then, for each seat:

- **2c. The seat is on the dev-channels prompt** ("WARNING: Loading development channels"). Send the confirm key the prompt names. `marvel inject` refuses a bare digit unless it is given `--allow-bare-digit`. This costs nothing and is safe: the prompt appears because the cast passes `--dangerously-load-development-channels server:director` (director#491), so answering it grants nothing new. marvel recognizes the prompt only for claude 2.1.310 (`internal/panestate/patterns/claude/2.1.310/`), and on other versions it reads as `uncovered`. That is likely why two seats waited about 22 h (inferred).
- **2a. The broker is up, the shim is dead, and the pass file exists.** Inject `/mcp reconnect director` into the seat while it is idle at its prompt, never mid-tool. The cost is one injected turn, and the context is kept. It worked on 11-28 for every seat cast on 2026-11-25 or later. To verify, the seat's roster row reappears within 30 s.
- **2b. The pass file or token file is gone.** A reconnect cannot work here. The shim re-reads the file on every connect, and a missing file gives an empty password that the broker refuses (director `natspass.go:13-22, 98-108`). Nothing rewrites the file except a new launch (marvel `internal/session/manager.go:1884, 1957`). Use `marvel shift --role <role>`. The cost is a fresh context that starts from the handoff only. It is safe when the seat can write its handoff. `--no-handoff` loses whatever the seat held, so escalate before using it. On 11-28 this was every seat cast on 11-24.

To tell 2a from 2b without touching the seat, look for the seat's `.pass` file in the daemon's token directory (`os.TempDir()/marvel-tokens-<tag>`, marvel `manager.go:318-323, 643-652`), or read the MCP log line from 1.3.

## 3. Why the shims stayed dead

The shim's bus connection sets no reconnect options (director `cmd/director-mcp/bus.go:196-218`), so the nats.go defaults apply: 60 attempts about 2 s apart (nats.go v1.55.0 `nats.go:55-59`). After about 2 minutes the connection closes for good. The process keeps serving MCP over stdio and never dials again (`main.go:340, 349-356`). An outage longer than about 2 minutes therefore needs a new shim process on every seat. See finding-director-ghlt.

## 4. Reaching a dark host's seats from outside the bus

- **What exists now.** kinu's marvel config declares the corporate and mokuzai clusters over SSH. From kinu, `marvel capture`, `marvel inject` and `marvel shift --cluster <name>` reach a host's seats with the bus down, as long as that host's daemon is up and the declared address is current. Address drift has broken this before: injects failed silently until the address was corrected by hand (recorded in director's friction notes). This is the out-of-band channel, and it was available on 11-28.
- **When the daemon is down,** nothing remote reaches the seats. The operator at the host is the only path.
- **SendMessage** lists only sessions on the local host or connected by Remote Control. To reach mokuzai, each mokuzai seat would need Remote Control connected under the operator's account. That is a per-seat setup and an account-level grant, for a channel that duplicates the marvel cluster path.
- **Switchboard** is an early MVP that works on the LAN only.
- **Recommendation:** rely on the marvel cluster path and keep its addresses current. Adding Remote Control is not worth it while the marvel path exists. This recommendation is valid until 2026-12-05, and this seat re-checks it then.

## 5. Code or runbook

| Step | Where it belongs | State |
|---|---|---|
| Shim retries forever (`RetryOnFailedConnect`, `MaxReconnects(-1)`) and reports a closed connection | director code | recommended in `_bmad-output/nats-topology-party-2026-09-17/recommendation.md:363-374`; tracked as director#768 |
| `marvel stop` refuses any positional argument and names `marvel delete session` (a custom `Args` function, as ArcavenAE/marvel#732 did for `marvel daemon`) | marvel code | merged as ArcavenAE/marvel#1625 (squash f584b9a0) |
| Secret files kept somewhere macOS does not purge, or rewritten on adopt | marvel code | I-868, fix in progress; marvel#314 open |
| Dev-channels prompt detected on every claude version | marvel code | pattern for 2.1.310 only |
| A missing bus beat or a dark host raises an alarm, not only a ring event | marvel and director code | marvel#580, director#659 open |
| A recover verb that runs section 2 per seat | marvel code, proposal only (inferred) | no issue; related to `docs/design/network-move-recovery.md` |
| Detection order, the per-seat choice, daemon restart, leaf check | runbook | this document |

Once the shim retries forever, 2a drops out of the runbook. Once the secret files survive, 2b shrinks to seats with a real fault. A recover verb would leave only the operator's daemon restart in the runbook.

## 6. Open question: does a daemon stop and start drop the leaf seed?

marvel's admin guide (docs/admin-guide.md, at marvel 6bcedac2, lines 648-650, and again in the shared-hub section) says a stop and start of the daemon drops the leaf seed, so a hub leaf needs the seed pushed again. On 2026-11-28 the leaf to kinu came back after the daemon was stopped and started. Both are recorded here as observed. This document does not correct the guide, and marvel is not edited from it.

What would settle it: after a daemon stop and start on a scratch cluster, `marvel bus status --require-leaf` says whether the leaf is up without a new push. Until someone runs that, check the leaf by hand after any daemon restart, as section 2 says.
