# finding-015: respawned seats replayed their inbox after the resume fix merged, and the successor cannot tell handled mail from new

- **Date:** 2026-10-02
- **Session:** the arcaven builder seat, placing the 2026-10-02 team harvest
- **Subject:** director-mcp durable resume across a seat respawn
- **Confidence:** six replay counts from at least five seats, observed from 2026-09-26 to 2026-10-02; no replaying shim's revision is known, so whether the fix was running is not established; the candidates are read from the code, not tested on a scratch broker

## 0. The sentence

**After #84 merged, a new instance should start after the departed durables'
ack floor, yet respawned seats kept receiving their whole retained inbox with
the note "no earlier durable for this seat", and nothing marks which of those
messages the predecessor already handled. Whether the replaying shims carried
#84 is not known.**

## 1. What was observed

- 2026-09-26 22:05Z, about 18 h after #84 merged (2026-09-26T03:49Z): a
  restart re-delivered 9 messages. A comment on director#83 reports a
  restarted seat receiving 9 messages, with asks its prior instance had
  answered and the note above, and another seat replaying 39; the 9 may be
  that same event seen from two seats.
- Reported in team arcaven's 2026-10-02 harvest, observed between 2026-09-28
  and 2026-10-02, four seats independently:
  - one respawned seat received 15 messages, every one older than its
    handoff, with the same note;
  - after a shim upgrade, one seat's `wait_for_message` returned 30
    already-handled messages as new;
  - after another shim respawn, `inbox_summary` showed 53 drained messages
    as waiting again;
  - a respawned seat inherited 43 unacked messages, and a drain with
    `max=50` returned about 68k characters, past the tool's output cap.

That is six counts (9, 39, 15, 30, 53, 43), or five events if the two 9s are
one. Each successor sorted handled from new by hand against its handoff. Where a
handoff was stale or absent, the only defense was the seat's own judgment.

## 2. Mechanism, as far as it is read

`seatAckFloor` (bus.go) takes the floor only from durables that carry the
seat's name prefix, filter exactly the seat's current subjects, and have no
live presence row. A miss on any of the three returns zero, and zero means
"read everything". Four candidates, none confirmed for any instance above:

0. **The shim predates #84.** No instance records its shim's revision, and
   finding-017 and finding-018 show that a merge is not a deploy and that
   the `rev` stamp can be wrong. A shim built before #84 replays by design.
1. **The presence row is still live.** A crashed or killed session's row
   lingers up to the bucket's 90 s TTL, and the code and
   docs/shim-reference.md both say a reconnect inside that window replays.
   A marvel respawn after a kill or shift is usually faster than 90 s, so
   the documented edge may be the common path, not the rare one.
2. **The subject set changed.** A seat that took or dropped a role since
   reads from the start, by design. Shims rebuilt across #86 (role inbox,
   merged 2026-09-26T04:00Z) changed their filter set, which fits the instances that
   followed an upgrade.
3. **The agent id changed.** A successor with a new id has a new prefix and
   a new inbox subject, which is finding-012's case, not this one: it would
   see no old mail at all.

The replay itself is deliberate ("a replay, never a loss"). What is missing
is any mark a successor can read: the replayed messages arrive exactly as
new ones do.

## 3. What this does not establish

Which candidate produced each instance. A few reads per respawn would decide
it, before the new shim binds: `go version -m` on the shim binary for its
revision, the predecessor's presence row expiry, and its durable's filter
subjects (`nats consumer info AGENT_INBOX <name>`).

## Related

- director#83 (open): the original replay and starvation report; #84 fixed
  the general case.
- finding-012: a reply to a dead id; the adjacent addressing failure.
- question-director-seat-startup, sub-question C (what carries across a
  restart).

## Addendum 2026-10-04: two more sightings

- The sideshow-builder seat reported that, on respawn, `wait_for_message` replayed 11 messages it had already handled (team harvest of 2026-10-04).
- The arcaven builder seat saw the same on its own respawn on 2026-10-04: the first drain reported "local inbox has no earlier durable for this seat, so it reads everything the stream still holds; 18 message(s) waiting", and the retained messages included asks answered days earlier.

Neither shim's revision was checked, so this still does not establish whether the resume fix was running (section 3 stands).
