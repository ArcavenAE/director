I offered to test on Apple Silicon, so here is that, run against #27 at 5840b2b on darwin/arm64 with Go 1.26.5. Short version: #27 passes and introduces no failures, and the `count_tokens` 404 looks like it is coming from the engine rather than from PAIR.

**Tests.** `services/lmstudio-proxy` and `services/ollama-proxy` both pass at #27, including its new `failover_test.go` in each. The one red test in `ollama-proxy`, `TestAliasSelfTargetMatchesBoundLoopbackAddressNotPortAlone`, also fails at #27's merge base and is unrelated: it binds `127.0.0.2`, and macOS configures only `127.0.0.1` on `lo0` where Linux treats all of `127.0.0.0/8` as local.

**On `/v1/messages/count_tokens`.** Checked against Ollama 0.33.3 directly, with no PAIR in the path:

```
POST http://127.0.0.1:11434/v1/messages/count_tokens   ->  404  "404 page not found"
POST http://127.0.0.1:11434/v1/messages                ->  a well-formed Anthropic
                                                            error envelope
```

The bare `404 page not found` is the engine's own mux answering, so the route does not exist upstream. `inferenceEndpoints` is an exact-match map and the proxy handler is a catch-all rather than a registered route set, which reads to me as PAIR forwarding `count_tokens` and the engine 404ing it, rather than PAIR declining to expose it. If that is right, this is already the "pass it through where an engine implements it, clear error where none does" outcome, and nothing further is needed for it. I did not stand up the full proxy to confirm the hop, so treat that half as a source reading.

**What #27 changes, and what neither of us has tested.** The diff adds `/v1/messages` to `inferenceEndpoints`, which is the classification that decides whether a request is a cluster workload: model-eligible candidate selection, and failover to the next candidate. Since the handler already forwards any path, a single-node setup exercises the passthrough but not the part this PR actually changes. Your end-to-end run and this one are complementary, and neither covers remote-node routing. I have one node too, so I could not close that gap.

One thing I checked and found fine, in case it comes up in review: Ollama answers an unknown model with HTTP 404 on `/v1/messages`, and failover treats a 404 on an inference route as a reason to try the next candidate. It answers `/v1/chat/completions` and `/api/chat` the same way, so `/v1/messages` now behaves exactly like the routes already in that map. Consistent, not a new edge.
