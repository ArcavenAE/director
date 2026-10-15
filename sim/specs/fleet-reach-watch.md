# Director spec: fleet reach watch

Status: firm, evidence-backed. Written 2026-10-14 from the network-move
simulation (2026-10-10 to 2026-10-14), when three hosts changed networks
repeatedly and director watched reach from kinu with a hand-run script. This
document is the job that script did by hand, stated as the software's job.
Requirements: R-205 to R-212 in `../requirements.md`.

## Why this exists

Director reaches seats over two paths that fail independently: marvel's
remote calls, addressed by a literal host address, and the global bus, whose
leaf links recover on their own and can use a different address family. A
move, a cut route, a sleeping host or a damaged trust store each looked the
same from the outside: a peer that stopped answering. For five days the only
thing telling them apart was a shell loop that director restarted by hand.
Every stop, every misread verdict and every defect in that loop is recorded in
the friction log, and each one names part of this spec.

## Inputs

Per watching host, sampled on a fixed interval (the reference used 20
seconds):

1. **Own network identity:** the primary interface's address and default
   gateway.
2. **Bus links:** the global hub's leaf list, read from the hub's own monitor
   endpoint, named in config per tier. Never infer which server answered from a
   port number (R-164). For each link: its connection id, the remote address,
   the remote server key, the inbound message count and the round-trip time.
3. **Marvel reach:** one real remote call per peer cluster, on a slower
   interval (the reference used 5 minutes), because each failed call writes a
   handshake failure into the peer's log.
4. **Sleep accounting:** the watching host's own slept time, computed as a
   monotonic clock minus an uptime clock that stops in sleep, and each peer's
   declared sleep and wake events or self-reported sleep window, when the peer
   publishes them (marvel#866).

Health is never read from ping, a name lookup or a side endpoint (R-208).

## Derived signals

- **Stall:** a link whose inbound count has not moved for 60 seconds. In the
  reference run a stall led every hub-side close by about 1.5 to 4 minutes.
- **No pong:** a link whose round-trip time has not changed for 180 seconds.
  This fired falsely on a healthy link, so it goes to its own log and is not a
  verdict input.
- **Relinked:** a link id seen this round and not last round. Every per-link
  clock is keyed by link id, never by remote address, so a new connection from
  the same address does not inherit the old one's stall clock.
- **First-failure class:** the error class at the first failed call of an
  outage, held until the next success: no route, network unreachable, host
  down, refused, handshake, or timeout. The class decays during an outage (host down becomes a bare
  timeout when the neighbour cache expires), so only the first one is kept.
- **Last good addresses:** per peer and per path (marvel, bus), recorded at
  the last success on that path: the watching host's own address, and the
  peer's address as that path saw it. On the bus path that is the remote
  address of the peer's bus link; on the marvel path it is the address marvel
  reached. A published address (director#289), when there is one, is kept as
  well. Each is compared only with a current value of the same kind, because
  the bus can use a different address family from marvel's literal host
  address, and a cross-kind comparison reads as a move with nothing having
  moved.
- **Peer's current address:** the address the peer publishes for itself when
  it does (director#289), else the remote address of its bus link, matched by
  server key, while that link is up, else unknown. A down bus link, or a hub
  whose monitor endpoint cannot be read, with nothing published leaves it
  unknown. A bus-link address is compared with the last good address on the
  bus path, and a published address with the last published one; across kinds
  the answer is unknown.

## Verdicts

One per peer, every round. Rows are evaluated top to bottom and the first
match wins, so the most specific cause is reported before any general one.
Each row's condition includes "no row above matched", which makes the rows
disjoint.

| order | verdict | condition |
|---|---|---|
| 1 | `ok` | the marvel call succeeded |
| 2 | `trust-store-damaged` | the first class is a handshake failure that names the local host-key file |
| 3 | `not-listening` | the peer's daemon publishes that it takes no remote calls |
| 4 | `asleep` | the peer declared a sleep that covers now |
| 5 | `this-host-moved-from-<addr>` | the watching host's address differs from its last good address on the marvel path |
| 6 | `peer-moved-from-<addr>` | the peer's current address is known (published, or its bus link is up) and differs from the last good address of the same kind: a bus-link address against the last good address on the bus path, a published address against the last published one |
| 7 | `path-down` | the watching host's address is unchanged, the peer's current address is known and unchanged (published, or its bus link is up), and the first class is no route or network unreachable |
| 8 | `moved-or-marvel-down` | the hub's monitor endpoint is readable and the peer's bus link, matched by server key, is still up |
| 9 | `down-or-asleep` | the hub's monitor endpoint is readable and the peer's bus link is also down or absent |
| 10 | `bus-state-unknown` | the hub's monitor endpoint cannot be read (its host is down or the route to it is cut), so whether the bus link is up is not known |

Rows 5 and 6 can both be true when both hosts moved; row 5 wins, because the
watching host's own change is the one it can confirm. The verdict then names
both: `this-host-moved-from-<addr>` with `peer-also-moved` appended. In the same
way, when row 4 matches and the watching host's address also differs from its
last good address, the verdict is `asleep` with `this-host-also-moved`
appended, so a host move while every peer sleeps is not hidden.

When no comparison can be made for the peer's address, rows 6 and 7 cannot
match. That is so when the address is unknown and nothing is published (the
bus link is down or absent, no bus link is matched to the peer by server key,
or the hub's monitor endpoint cannot be read), and when only the other kind of
address is on record, since the two kinds are never compared. The verdict then
falls to row 8 (the endpoint is readable and a matched bus link is up), row 9
(readable, and the link is down or absent) or row 10 (not readable). A verdict
that reaches any of rows 8 to 10 this way carries the provisional mark. Every
state of the bus link, up, down, absent or not readable, therefore has a row,
so a watcher that cannot reach the hub still reports a verdict.

Limits that the verdict carries with it:

- It holds within one link. Fleet names here resolve only by mDNS, so in a
  cross-segment move the bus link drops too and the truth (moved) reads as
  down-or-asleep. A verdict reached across segments says it is provisional.
- A bus link is held continuously and has two sources; a marvel probe is
  sampled and has one. They are reported with that difference visible.
- Debounce is per path. A change on the bus path counts after it holds for
  three bus samples (60 seconds at the reference 20-second rate). A change on
  the marvel path counts after two consecutive probes agree (5 minutes at the
  reference rate). A path that answers once between failures inside its
  window is reported as flapping, and no repair is triggered from it.

## Output

- **Transitions only:** one log line when any field changes, not one per round.
- **Heartbeat:** one record overwritten every round, holding the round
  counter, the bytes read from the hub, the link count, the last probe outcome
  and verdict per peer, the first-failure class per peer, and slept time. A
  stale heartbeat explains itself across a sleep, and "ran and could not probe"
  reads differently from "ran and found nothing wrong".
- **Errors:** each failed call's error text is kept, and a repeated cause is
  stored once with a count and time range. The reference error log reached
  about 9.6 MB because the CLI prints its full help text after each connection
  error.
- **Events to director:** a verdict change, a recovery, and the watch's own
  start and stop. On recovery, director re-rings the seats with work stranded
  on that cluster and reads back from the last event it saw (R-212).

## Lifetime

The watch is a supervised service with no run limit. It reports its own stop
as an event (R-205). The reference loop had a fixed iteration count, so its
duration shrank when calls failed fast; it stopped silently at least eight
times, including once six hours into an outage, which hid the host's return.

## Reference behaviour

The hand-run script stays on kinu as the reference until this spec is built.
It reads the hub's leaf list, probes marvel every 15th round, keys every
per-link clock by link id, keeps the last good address per peer, and writes the
heartbeat above. Its four fixed defects are the test cases for the built
version: a header row counted as a link, stall clocks keyed by address,
measuring the wrong broker, and a verdict with no case for the watching host
moving.

## What stays outside director

Repointing a cluster entry (marvel#863), the hub's advertised address and
reload behaviour (the hub leaf-advertise design, marvel#855), host naming
(director#289), and hosts publishing sleep events (marvel#866) are owned
elsewhere. This spec only consumes them.
