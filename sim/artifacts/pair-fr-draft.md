**Title:** Expose an Anthropic Messages `/v1/messages` endpoint on the PAIR proxy

**Area:** API compatibility

---

PAIR already routes to two engines that serve the Anthropic Messages API natively, and re-exposes them as OpenAI only. Adding `/v1/messages` alongside the existing endpoints would let coding agents that speak that dialect (Claude Code among them) use pooled local capacity, which is a large and growing class of client currently locked out of PAIR by protocol rather than by capability. PR #9 (vLLM as a third engine) would add a third: vLLM serves the same endpoint.

## User problem

Who is affected: people running an agentic coding client that speaks the Anthropic Messages API against local models, on a mix of machines where any single one is too small to hold a useful model roster. That is the case PAIR is built for, and it is the case we have: a team where a few machines have 64 GB or more and most have far less.

What cannot be accomplished today: such a client sends every request to one base URL and speaks only `/v1/messages`. PAIR presents Ollama-compatible and OpenAI-compatible proxy endpoints, so the client cannot reach PAIR at all. The gap is narrow rather than architectural: both supported engines already serve the Anthropic Messages API themselves. LM Studio added `POST /v1/messages` in 0.4.1 (released 2026-01-30, "use Claude Code with LM Studio"), and Ollama added it in v0.14.0 (published 2026-01-10, "Anthropic API compatibility: support for the `/v1/messages` API"), refining it since in v0.19.0 and v0.32.3. Verified on LM Studio 0.4.20 and Ollama 0.33.2: both return well-formed Anthropic Messages responses, content blocks and `stop_reason` and `usage` included. So a request arrives in a dialect the destination engine already understands, and the only component in the path that does not is the proxy in the middle.

## Desired outcome

`POST /v1/messages` on the PAIR proxy, alongside the existing endpoints, routed by the same scheduler and answered by the selected node's engine. Observable behavior:

* A client configured with PAIR's endpoint as its Anthropic base URL completes a normal request and receives a well-formed Messages response.
* Streaming returns the Anthropic SSE event grammar, not a translated approximation.
* `tool_use` and `tool_result` content blocks survive intact in both directions. This is the part that matters most for agent clients, because a dropped or reshaped tool block fails silently and looks like a confused model rather than a transport bug.
* `/v1/messages/count_tokens` behaves predictably. Agent clients call it, and neither engine implements it today: on LM Studio 0.4.20 it returns HTTP 200 carrying `{"error":"Unexpected endpoint or method."}`, and on Ollama 0.33.2 it returns a 404. Passing it through where an engine implements it and returning a clear error where none does would be an improvement on both, and PAIR is not the right place to invent a tokenizer.
* Model selection continues to work the way the existing endpoints do.

Passthrough rather than translation would be the smaller and safer change: when the selected node's engine speaks Anthropic Messages, forwarding the body preserves fidelity by construction and needs no per-field mapping to maintain as the API evolves.

## Alternatives considered

* Point the client directly at a single node's LM Studio or Ollama. This works and is what we do today, and it gives up everything PAIR exists to provide: no pooling, no scheduling, no use of idle machines.
* Put a translating gateway in front of PAIR. This reintroduces Anthropic to OpenAI conversion at exactly the point where the destination engine did not need it, and tool-call round trips are the known casualty of that conversion.
* Run a small Anthropic-to-OpenAI shim alongside PAIR. Same fidelity problem, plus another process on every machine.

Each alternative pays a translation cost to reach engines that already speak the dialect.

## Compatibility and security implications

Additive. Existing Ollama-compatible and OpenAI-compatible endpoints are unchanged, and clients that do not use `/v1/messages` see no difference. No change to discovery, pairing, or the trust model is implied. Routing an unfamiliar path to a node whose engine does not implement it should fail with a clear error rather than a partial or reshaped response, so that a capability gap is visible rather than silent.

## Validation approach

* A three-call check against each supported engine: an ordinary message, a message that must produce a `tool_use` block with valid arguments, and a streaming request whose SSE event sequence is compared against the Anthropic grammar.
* A `count_tokens` call that returns either a clear answer or a clear error, never a hang and never an HTTP 200 carrying an error body.
* The same three checks with the request routed to a remote node rather than the local one, confirming the shape survives the hop.

This is adjacent to two open threads rather than a fresh direction: PR #9 adds a third engine that serves the same endpoint, and issue #7 asks for richer placement signals, which is the same question of whether a node can serve a given request rather than merely being idle.

Happy to test on Apple Silicon and report back, and happy to follow up with a pull request if the direction is one you would take.
