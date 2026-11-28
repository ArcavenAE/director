# finding-director-ghlt: the director-mcp shim gives up about two minutes into a host bus outage and never dials again

- **Date:** 2026-11-28
- **Session:** read-only study for I-870, routed by the supervisor
- **Subject:** how a director-mcp shim's bus connection behaves when its host's broker goes down and returns
- **Confidence:** read from code at director 8154099 (origin 7f1d7eb differs only in the drain classifier) and marvel origin/main 6bcedac2. The outage case is from the mokuzai relay of 2026-11-28 (the relay log of that day, a local file that is not tracked in this repo). Nothing was run against a live host.
- **Filed here by the subject test:** the connection that gives up is the shim's. marvel's part (the purged secret files, the stop that started the outage) is named as an object below and is tracked under I-868.

## 0. The sentence

**A shim whose host broker is gone for more than about two minutes stays off the bus for the rest of its life, even after the broker returns, because its connection keeps the nats.go reconnect defaults and nothing redials once the connection closes. The process keeps serving MCP, so the seat looks alive to its harness and absent to the bus. Only a new shim process recovers it, and that works only if marvel's pass file for the seat still exists.**

## 1. Evidence

- `dialRing` (`cmd/director-mcp/bus.go:191-213`) sets `nats.Name`, an optional `ErrorHandler`, and the auth option, and nothing else. A `git grep` finds `MaxReconnects` only on the short-lived connections, each set to 0 on purpose (`hubuser_cmds.go:349`, `look.go:172`, `observer.go:100`).
- The nats.go v1.55.0 defaults are `DefaultMaxReconnect = 60` and `DefaultReconnectWait = 2 * time.Second`, with 100 ms jitter (`nats.go:55-59`). Against a refused port that is about two minutes (inferred arithmetic), and it matches the outage.
- The only handler set after startup is the reconnect handler (`main.go:337`), which sets a cue flag. The main loop waits on stdio or a signal (`main.go:345-352`), and no code outside tests checks `IsClosed`. A closed connection therefore leaves the process serving with tool calls failing (inferred), and the 30 s heartbeat logs only on transitions (`bus.go:2016-2052`).
- The pass file is re-read on every connect through `UserInfoHandler`. A failed read yields an empty password, which the broker refuses (`natspass.go:13-22, 100-108`). marvel writes the file only at launch, under `os.TempDir()/marvel-tokens-<tag>` (marvel `internal/session/manager.go:318-323, 643-652, 1884, 1957`), and macOS temp cleanup removed it for seats cast on 2026-11-24 (the same relay log).
- On 11-28, `/mcp reconnect director` recovered the seats whose file survived, and `marvel shift` recovered the rest with fresh files. That matches both points above.

## 2. What it implies

- Any outage longer than about two minutes needs a manual step on every seat of the host. Presence expiry (90 s, `askledger.go:22`) reports the loss, but nothing repairs it.
- A fix was already recommended: `RetryOnFailedConnect(true)`, `MaxReconnects(-1)` and a `ClosedHandler` (`director/_bmad-output/nats-topology-party-2026-09-17/recommendation.md:363-374`). It was not applied to `dialRing`. No director or marvel issue carried it when this was written (searches for MaxReconnects, PASS_FILE and reconnect found the related director#66, #127 and #269 only); it is now director#768.
- Unlimited reconnect alone does not cover a purged pass file. That half is marvel's (I-868, marvel#314).

## 3. Not in scope

The fix itself, which director#768 carries. Whether a closed connection should exit the process (so a harness that respawns can recover it) or keep retrying. The I-871 (6ptdd) redesign. The procedure for operators is in `procedure-dark-host.md` beside this file.
