I built and ran the suite at a4b96a3 on macOS arm64, since this adds an engine and a second platform data point seemed worth having. The vLLM work passes; the two failures I hit are pre-existing on main and are not from this PR.

Environment: darwin/arm64 (Apple Silicon), Go 1.26.5, Node 25.9.0, clean tree at a4b96a3.

**Desktop** (`make test-desktop`): 39 files, 220 tests, all pass, including the new `vllm-engine.test.ts` (6) and `openai-proxy-engines.test.ts` (6).

**Go**: `make test-services` halts at the first failing module, so I ran each module separately. 18 modules, 16 pass, 2 fail:

| module | test | also fails at origin/main 13b6811 |
|---|---|---|
| `nvpair-engine-manager` | `TestUninstallTerminatesRunningInstance` | yes |
| `ollama-proxy` | `TestAliasSelfTargetMatchesBoundLoopbackAddressNotPortAlone` | yes |

Both reproduce at main with identical messages, so neither is caused by this change. `lmstudio-proxy`, which carries most of the new engine code plus `multiengine_test.go`, passes.

On those two, in case it saves anyone time:

- The alias test binds `127.0.0.2`. macOS configures only `127.0.0.1` on `lo0`, where Linux treats all of `127.0.0.0/8` as local, so the bind fails before the test's own logic runs. An `ifconfig lo0 alias` in the harness or a platform skip would cover it.
- `TestUninstallTerminatesRunningInstance` may reach a real macOS gap rather than a test-only one. `procImage` in `proc_unix.go` reads `/proc/<pid>/exe`, which macOS does not provide, so it returns `""`; `isOurEngineImage` returns false for an empty image; and the orphan-reclaim branch in `doStop` is skipped as a result. That would leave the path described in its own comment, letting a user's OFF take effect "instead of being refused forever", unreachable on macOS. That is my reading of the source rather than something I instrumented. Happy to file it separately if useful.

Two small notes on the manifest, neither of them asks:

- `manifests/vllm.json` declares `linux/amd64` and `linux/arm64` only, which I read as deliberate. It does mean an Apple Silicon tester can exercise the Go and TS layers but not the runtime path.
- The manifest exposes `/v1/models` and `/v1/chat/completions`. vLLM also serves the Anthropic Messages API (`vllm/entrypoints/anthropic/api_router.py`), which is the subject of #16. Nothing this PR needs to take on; noting it only because the two touch.
