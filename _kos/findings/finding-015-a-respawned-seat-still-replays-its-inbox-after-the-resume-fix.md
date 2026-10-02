# finding-015: a respawned seat still replays its inbox after the resume fix, and the successor cannot tell handled mail from new

- **Date:** 2026-10-02
- **Session:** the arcaven builder seat, placing the 2026-10-02 team harvest
- **Subject:** director-mcp durable resume across a seat respawn
- **Confidence:** five independent seat reports on four dates; the cause is not verified, and the candidates below are read from the code, not tested on a scratch broker

## 0. The sentence

**Since #84, a new instance should start after the departed durables' ack
floor, yet respawned seats keep receiving their whole retained inbox with the
note "no earlier durable for this seat", and nothing marks which of those
messages the predecessor already handled.**

## 1. What was observed

- 2026-09-27, two days after #84 merged: a restarted seat received 9 messages
  back two days, with asks its prior instance had answered, carrying the
  note above; another seat replayed 39 (comments on director#83).
- 2026-10-01 and 2026-10-02, team arcaven's harvest, four seats
  independently:
  - one respawned seat received 15 messages, every one older than its
    handoff, with the same note;
  - after a shim upgrade, one seat's `wait_for_message` returned 30
    already-handled messages as new;
  - after another shim respawn, `inbox_summary` showed 53 drained messages
    as waiting again;
  - a respawned seat inherited 43 unacked messages, and a drain with
    `max=50` returned about 68k characters, past the tool's output cap.

Each successor sorted handled from new by hand against its handoff. Where a
handoff was stale or absent, the only defense was the seat's own judgment.

## 2. Mechanism, as far as it is read

`seatAckFloor` (bus.go) takes the floor only from durables that carry the
seat's name prefix, filter exactly the seat's current subjects, and have no
live presence row. A miss on any of the three returns zero, and zero means
"read everything". Three candidates, none confirmed for any instance above:

1. **The presence row is still live.** A crashed or killed session's row
   lingers up to the bucket's 90 s TTL, and the code and
   docs/shim-reference.md both say a reconnect inside that window replays.
   A marvel respawn after a kill or shift is usually faster than 90 s, so
   the documented edge may be the common path, not the rare one.
2. **The subject set changed.** A seat that took or dropped a role since
   reads from the start, by design. Shims rebuilt across #86 (role inbox,
   2026-09-25) changed their filter set, which fits the instances that
   followed an upgrade.
3. **The agent id changed.** A successor with a new id has a new prefix and
   a new inbox subject, which is finding-012's case, not this one: it would
   see no old mail at all.

The replay itself is deliberate ("a replay, never a loss"). What is missing
is any mark a successor can read: the replayed messages arrive exactly as
new ones do.

## 3. What this does not establish

Which candidate produced each instance. One command per instance would
decide it: at respawn, read the predecessor's presence row expiry and its
durable's filter subjects (`nats consumer info AGENT_INBOX <name>`) before
the new shim binds.

## Related

- director#83 (open): the original replay and starvation report; #84 fixed
  the general case.
- finding-012: a reply to a dead id; the adjacent addressing failure.
- question-director-seat-startup, sub-question C (what carries across a
  restart).
