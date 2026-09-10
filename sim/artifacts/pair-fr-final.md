PAIR pools capacity so that a machine too small to hold a useful model can still use one. Coding agents that speak the Anthropic Messages API are a large and growing class of client that cannot reach PAIR at all today, and the reason is protocol rather than capability: both engines PAIR already routes to serve `POST /v1/messages` natively, and PAIR re-exposes them as OpenAI only. Adding the endpoint alongside the existing ones would open pooled local capacity to those clients without changing anything for current ones.

### Area

API compatibility

### User problem

Who is affected: people running an agentic coding client that speaks the Anthropic Messages API against local models, across a mix of machines where no single one is large enough to hold a useful model roster. That is the case PAIR is built for, and it is ours: a team where a few machines have 64 GB or more and most have far less.

What cannot be accomplished today: such a client sends every request to one base URL and speaks only `/v1/messages`. PAIR presents Ollama-compatible and OpenAI-compatible proxy endpoints, so the client cannot reach PAIR at all.

The gap is narrow rather than architectural, because the destination engines already understand the dialect:

- Ollama's v0.14.0 release notes read "Anthropic API compatibility: support for the `/v1/messages` API".
- LM Studio's post "Use your LM Studio Models in Claude Code" (2026-01-30) reads "Run Claude Code with any local model using LM Studio's Anthropic-compatible API".

Checked directly rather than taken from the notes: on LM Studio 0.4.20 and Ollama 0.33.2, both return well-formed Anthropic Messages responses, content blocks and `stop_reason` and `usage` included.

So a request arrives in a dialect the destination engine already speaks, and the only component in the path that does not is the proxy in the middle.

### Desired outcome

`POST /v1/messages` on the PAIR proxy, alongside the existing endpoints, routed by the same scheduler and answered by the selected node's engine. Observable behavior:

- A client configured with PAIR's endpoint as its Anthropic base URL completes a normal request and receives a well-formed Messages response.
- Streaming returns the Anthropic SSE event grammar rather than a translated approximation.
- `tool_use` and `tool_result` content blocks survive intact in both directions. This is the part that matters most for agent clients, because a dropped or reshaped tool block fails silently and looks like a confused model rather than a transport bug.
- `/v1/messages/count_tokens` behaves predictably. Agent clients call it, and neither engine implements it: at LM Studio 0.4.20 it returns HTTP 200 carrying `{"error":"Unexpected endpoint or method."}`, and at Ollama 0.33.2 it returns 404. Passing it through where an engine implements it and returning a clear error where none does would improve on both, and PAIR is not the right place to invent a tokenizer.
- Model selection continues to work the way it does on the existing endpoints.

Passthrough rather than translation looks like the smaller and safer change: when the selected node's engine speaks Anthropic Messages, forwarding the body preserves fidelity by construction and needs no per-field mapping to maintain as the API evolves.

### Alternatives considered

- Point the client directly at a single node's LM Studio or Ollama. This works, it is what we do today, and it gives up everything PAIR exists to provide: no pooling, no scheduling, no use of idle machines.
- Put a translating gateway in front of PAIR. This reintroduces Anthropic to OpenAI conversion at exactly the point where the destination engine did not need it, and tool-call round trips are the known casualty of that conversion.
- Run a small Anthropic-to-OpenAI shim alongside PAIR. Same fidelity problem, plus another process on every machine.

Each alternative pays a translation cost to reach engines that already speak the dialect.

### Compatibility and security implications

Additive. Existing Ollama-compatible and OpenAI-compatible endpoints are unchanged, and clients that do not use `/v1/messages` see no difference. No change to discovery, pairing, or the trust model is implied.

Routing an unfamiliar path to a node whose engine does not implement it should fail with a clear error rather than a partial or reshaped response, so that a capability gap stays visible instead of silent.

### Validation approach

- A three-call check against each supported engine: an ordinary message, a message that must produce a `tool_use` block with valid arguments, and a streaming request whose SSE event sequence is compared against the Anthropic grammar.
- A `count_tokens` call that returns either a clear answer or a clear error, never a hang and never an HTTP 200 carrying an error body.
- The same three checks with the request routed to a remote node rather than the local one, confirming the shape survives the hop.

### Scope of what I checked

I read the proxy service only, not every service, and I did not locate in source the Ollama-native surface the README describes. The engine behavior above was executed at the pinned versions named; the claim that PAIR has no Anthropic surface today comes from finding no `/v1/messages` or Anthropic reference in the repository.

This sits next to two open threads rather than pointing in a fresh direction: #9 adds vLLM as a third engine, which serves the same endpoint, and #7 asks for richer placement signals, which is the same question of whether a node can serve a given request rather than merely being idle.

Happy to test on Apple Silicon and report back, and happy to follow up with a pull request if this is a direction you would take.

### Confirmations

- [x] I searched existing issues for duplicates.
- [x] I agree to follow the Code of Conduct.
