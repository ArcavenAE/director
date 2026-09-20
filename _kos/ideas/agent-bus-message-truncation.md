# Large-message truncation on the agent bus, and where it actually comes from

- **Status:** observation + open question, pending research in bd aae-orc-7yimg.
- **Date:** 2026-09-19
- **Subject:** director. How the comms layer carries a large message is
  director's concern; marvel (the inject doorbell) and NATS (the transport)
  are the two paths it reasons about.
- **Tracking:** bd aae-orc-7yimg (research + handling design).

## What was observed

Dispatching task briefs to marvel-managed seats this session, a 1747-byte
payload arrived TRUNCATED at the receiver (the receiver reported its copy
started mid-sentence, the first item missing), while a later 2192-byte payload
arrived intact. Not a clean byte threshold.

## The correction that matters

Both dispatches went via `marvel inject` (keystrokes/paste into the tmux pane),
NOT via director `send_message` over NATS. So the truncation observed is on the
inject/tmux-paste path, not the bus. Likely cause: tmux bracketed-paste
chunking, or the separate bare-Enter flush inject landing mid-paste. NATS's own
default `max_payload` is 1MB (configurable), so a director message over the bus
does not truncate at ~2KB. The two paths have different limits and different
failure modes, and conflating them ("it's a NATS limit") would misdirect the
fix.

## Why director must handle this explicitly

A real task brief trivially exceeds 2KB, and the sender gets no signal the
front was dropped (compounds R-08: accepted is not delivered or read). Director
should not assume either path carries arbitrary text. Candidate handling, to be
decided in aae-orc-7yimg: an explicit size limit + validation; chunking with
reassembly; or a reference-not-inline pattern (store the large content in a file
or NATS KV and send a pointer). The inject doorbell needs its own answer (a
chunk+flush protocol, or a cap plus a durable pointer).

## Related

- bd aae-orc-7yimg (the research ticket).
- `../../../docs/drafts/director-tasking-rough-edges-2026-09-19.md` edges 6
  (large inject arrives truncated) and 7 (input line cannot be cleared via
  inject).
- `nats-request-recovery-ledger.md` (durable memory of instructions; the
  reference-not-inline pattern overlaps).
- bd aae-orc-ebc32 (presence and under-used NATS features; KV bucket is a
  candidate carrier for large content).
