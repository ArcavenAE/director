# The cluster-gone alert: lost after a bound, one parent for a hub move, one key with leaf-down

Status: design for review, 2026-10-20. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Ticket: aae-orc-cashp (ticket 8 of the cluster-address party).
- Rulings: the cluster-address party outcome, 2026-10-08 (K7, O4, O6), ruled by
  the operator the same day. Section 1 quotes what this note builds on.
- Prior art: `presence-watch.md` (#319, built as #331), `global-bus-tier.md`,
  `shim-timer-heartbeat.md`; marvel `docs/design/leaf-down-bus-event.md`
  (marvel#752) and its subscriber ticket aae-orc-mn10f.
- Checked against director `origin/main` 935e0f5 and marvel `origin/main`
  695fdff2. Every `file:line` below is at those shas. A claim with no citation
  is marked inferred.

## 1. What was ruled

- **K7 (panel, 7-0).** "The cluster-gone alert comes from director's hub-side
  reader, driven by ledger state, never by `/leafz`. A hub move gives one parent
  alert and suppresses the per-cluster alerts while it is open. Director checks
  its own hub link before it calls anything gone. Alerts are rate-capped. The
  collapse threshold is a probe number, not a design number."
- **O4 (operator, against a 5-2 panel vote).** One shared dedupe key for the
  hub-side alert and marvel's `bus.leaf.down`. The dissent: one path may be the
  one that is down, so a shared key can suppress the only page that would
  arrive. This note must show how the shared key avoids that (section 7).
- **O6 (operator), verbatim:** "make it a marvel admin preference, default to
  30 minutes". This note must name how director reads it without depending on
  marvel (section 5).
- **Carried from the outcome:** this design names which store is
  authoritative for "away" and "lost" (section 3).

## 2. What is already on main, and what is not

The ticket's premise ("nothing on director's side raises it") is partly false,
as the builder recorded on 2026-10-20.

On main:

| fact | where |
|---|---|
| The watch reads `GLOBAL_PRESENCE` on the director seat's 30 s heartbeat tick, behind `DIRECTOR_PRESENCE_WATCH=1`, only for the global director role | `cmd/director-mcp/presencewatch_shim.go:1-35`, `:216-218` |
| Per supervisor instance and per cluster: `present`, `absent` (two missed passes), `asleep`; flapping collapses after 4 transitions in an hour | `presencewatch.go:49`, `:54-56`, `:174-189`, `:381-458` |
| A failed keys call marks the watch blind and every unit unknown, so no absence is inferred | `presencewatch.go:245-255` |
| Host sleep and the watch's own downtime are gaps, and no transition is inferred across one | `presencewatch.go:207-235` |
| `AbsenceDuration` gives time absent less host sleep | `presencewatch.go:471-493` |
| Events go to an event file and, unless collapsed, to notices the director model drains | `presencewatch_shim.go:89-110`, `:174-197` |
| The threshold table: built-in defaults, overridden by `thresholds.json` in the state directory; a bad value is an error | `handled.go:66-71`, `:97-110` |
| The shim reaches the hub over its own broker's leaf link, with no second connection | `global.go:3-15` |
| marvel emits `bus.leaf.down` on the cluster's own ring, repeated every 30 minutes while a promised leaf stays down | marvel `internal/bus/supervisor.go:212-220`, `:1035-1062` |
| marvel stamps the seat's `DIRECTOR_*` env at launch | marvel `internal/runtime/adapter.go:414`, `:547` |

Not on main: a "lost" alert past a bound; a hub-move parent; a rate cap; a
check of director's own hub link beyond the keys call; the shared key with
`bus.leaf.down`; the O6 preference.

Two facts on main shape the rest of this note:

1. **An empty bucket reads as every cluster leaving.** `watchScan` returns an
   empty scan, not an error, when the hub answers "no keys"
   (`presencewatch_shim.go:365-368`). A hub that was replaced or flushed answers
   exactly that, and every known cluster then goes absent two passes later.
2. **The director's own row is read and then dropped.** `watchScan` skips keys
   for which `isDirectorKey` is true (`presencewatch_shim.go:372`,
   `global.go:285-291`). That row is the one piece of evidence that the hub
   this pass read is the hub this director writes to.

## 3. The source and the store

The alert is computed from the watch's state file and event file (the
`watchStore` of `presencewatch_shim.go`). They are authoritative for "away"
and "lost".

K7 says "driven by ledger state". The ask ledger (`askledger.go`) records
asks and their global ids; it holds no presence history. It was written before
the watch existed, and the watch's state is a per-cluster ledger of presence
in all but name. So this note reads K7's "ledger state" as the watch's state,
and never `/leafz` (which K7 rules out and which the hub does not expose by
cluster). That reading is a ruling request (card R1, section 10). The ask
ledger's 30-day prune (`askledger.go:30`, `:624`) then does not touch this alert.

## 4. Director's own hub link, before anything is called gone

A pass is **sighted** only when all three hold:

1. the keys call succeeded (today's blind check, unchanged);
2. the scan contains this director's own row (the key `isDirectorKey`
   accepts for this session's instance); and
3. that row's `ts` is the one this session wrote on the same tick, or later.

The heartbeat writes the row before the pass runs
(`presencewatch_shim.go:29-31`, cadence bound 4), so a sighted pass proves the
hub read is the hub written to. A pass that fails 2 or 3 is **self-unseen**:
every unit becomes unknown, as on a blind pass, and the watch records one
`self-unseen` event. No absence, lost alert or parent is raised from a
self-unseen pass.

This changes one thing on main: `watchScan` keeps this session's own row for
the check, and still excludes every director row from the units.

## 5. The bound (O6), and how director reads it without marvel

**Event.** A cluster unit that is `absent` and whose `AbsenceDuration` reaches
its bound raises `lost`, once per absence. A return closes it with `back`, as
today. Host sleep never counts toward the bound (`AbsenceDuration` already
subtracts it).

**Where the bound comes from, in order:**

1. **The cluster's own value, carried on its supervisor's presence record.**
   marvel, when it launches a seat, stamps `DIRECTOR_PRESENCE_LOST_AFTER` from
   a new admin preference in marvel's config (a key name for the builder to
   choose, default 30m), beside the `DIRECTOR_*` values it already stamps
   (marvel `adapter.go:547`). The shim on that cluster writes it as one more
   field, `lost_after`, in the record `writePresence` already puts
   (`global.go:788-801`). The watch stores the last value it saw per cluster in
   its state file, because the row expires when the cluster leaves.
2. **Director's own default**, the threshold-table row
   `presence_lost_after`, built in at 30 minutes and overridable in
   `thresholds.json` (`handled.go:66-71`).

**Why this keeps director independent of marvel.** Director never calls
marvel, reads no marvel file and links no marvel code. It reads one optional
field of a record it already reads. A cluster with no marvel, a hand-run
supervisor, or an older shim writes no field, and director uses its own
30-minute default. marvel without director is unchanged: the stamp is one more
env variable that nothing reads. The only shared thing is the field name,
which `docs/shim-reference.md` documents as part of the presence record.

**Bad or extreme values.** These are decisions of this design:

- A per-cluster `lost_after` that does not parse as a positive duration is
  ignored for that cluster, which uses director's row, with one event naming
  the cluster. It never fails the watch, so one cluster cannot stop it.
- A per-cluster value above `presence_lost_after_max` (a director-owned row,
  provisional default 24 hours) is clamped to that maximum, with one event
  naming the cluster and both values. A cluster cannot silence its own `lost`
  by setting a very long bound.
- A bad value in `thresholds.json` for any of the five rows this design adds
  (`presence_lost_after`, `presence_lost_after_max`, `presence_hub_move_min`,
  `presence_hub_move_window`, `presence_alert_cap`) keeps that row's built-in
  default and raises one notice naming the row. Today a bad row of any kind
  makes `loadThresholds` return an error (`handled.go:97-110`),
  `startPresenceWatch` returns it (`presencewatch_shim.go:224-227`), and main
  only logs "presence watch not started" (`main.go:168-170`), so no alert of
  any kind is raised. The new rows do not widen that path. The existing rows
  keep today's behavior; changing them is outside this note.

The cheaper alternative, a director-only row with no marvel stamp, is option
(b) on card R2.

## 6. The hub-move parent, and the rate cap

**Parent.** On a sighted pass, when at least `presence_hub_move_min` clusters
(provisional default 2) that were present go absent within
`presence_hub_move_window` (provisional default 2 minutes), and those are every
cluster that was present apart from this director's own, the watch opens one
`hub-moved` parent instead of per-cluster absences. While it is open:

- per-cluster `absent` and `lost` events are recorded in the event file,
  marked collapsed, and left out of the notices (the flap collapse's mechanism,
  `presencewatch.go:174-189`);
- each cluster that returns raises `back` as usual, and the parent lists the
  clusters still out;
- the parent closes when every listed cluster is back, or after
  `presence_hub_move_window` passes with none back, at which point it converts
  into per-cluster `lost` alerts at their own bounds (so a real multi-cluster
  loss is not hidden behind a parent).

Both numbers are threshold-table rows, so the probe K7 calls for sets them
without a code change. Until a probe reports, the defaults are marked
provisional in the table's comments.

**Rate cap.** At most `presence_alert_cap` notices of kind `lost` or
`hub-moved` per hour (provisional default 6). Past the cap, one line per hour
says how many were recorded and not shown, and points at the event file.
Nothing is dropped from the file. `back` is never capped, so a recovery always
reaches the reader who heard the outage.

## 7. One key with `bus.leaf.down` (O4), without suppressing the only page

**The key.** Both alerts carry `dedupe_key: cluster-gone:<cluster>`. The
hub-side `lost` and `hub-moved` notices carry it (a parent carries one key per
listed cluster). The director-side subscriber that turns marvel's
`bus.leaf.down` into a supervisor inbox item (aae-orc-mn10f, unbuilt) puts the
same key on that item.

**The two paths reach different places.** `bus.leaf.down` lives on the
cluster's local broker and cannot cross the link it reports (marvel
`leaf-down-bus-event.md` section 2). Its reader is that cluster's supervisor.
The hub-side alert is read by the director seat and reaches the operator
through director. So during an outage the two copies are almost never at the
same place.

**The rule that keeps the only page.** Dedupe happens only at a sink, on
copies the sink already holds:

1. **No emitter consults the other path.** The watch never asks whether a
   `bus.leaf.down` exists before raising `lost`, and the subscriber never asks
   the hub. Each path raises its alert from its own evidence, always.
2. **A sink folds a copy only into an open item it already holds under the
   same key.** The fold adds the copy as a witness ("also seen from the
   cluster's side at <ts>") and does not page again. A sink that holds no open
   item under the key pages, whatever any other sink holds.

   **Only a shown item is a fold target**: one whose notice reached its
   reader. Exactly two items are open and shown:
   - a per-cluster `lost` whose notice went out;
   - an open `hub-moved` parent whose notice went out, for each cluster it
     lists (its notice names that cluster).

   Not fold targets:
   - a `lost` held back by the section 6 cap ("recorded and not shown");
   - a per-cluster `absent` or `lost` collapsed under a parent (only the
     parent was shown).

   A copy whose key matches only one of these pages. That page is exempt from
   the cap, because the cap would otherwise rebuild the hole this rule closes,
   and the held-back item becomes shown, so later copies fold into it. A
   parent that has closed or converted (section 6) is no longer a target for
   any cluster, and rule 4 applies.
3. **A fold never closes, delays or downgrades an item.** Only `back` (hub
   side) or `bus.leaf.up` (cluster side) closes one, each at its own sink.
4. **An episode ends at close.** A copy that arrives after the item closed
   opens a new item, so an old outage cannot swallow a new one.

**The dissent's case, in each direction:**

| what is down | hub-side alert | leaf-side alert | pages |
|---|---|---|---|
| the cluster's leaf link | raised (the cluster's rows expire) | raised on the cluster | each at its own sink |
| the hub, or director's own link | not raised: the pass is blind or self-unseen (section 4), and a `self-unseen` or `blind` notice goes out instead | raised on every cluster whose leaf dropped | each cluster's supervisor; director reports its own blindness |
| the cluster's subscriber (mn10f not running) | raised | none | the hub side |
| the director seat | none, and a watch-down gap is recorded | raised | the cluster side |

In no row is a page suppressed because the other path "has it": suppression
needs a copy already in hand at the same sink. When the leaf returns and a
supervisor forwards its leaf-down item upward, or both copies reach the
operator's view, they fold into one item with two witnesses, which is the
operator's O4 outcome. A duplicate page across two sinks remains possible and
is the cheaper failure the panel named.

## 8. Build split (stacked-PR rule d: one chain)

All in director unless named. Each starts with a one-command premise check.

1. **C1. Self-check and empty bucket** (section 4): keep the own row for the
   check, a self-unseen pass, the event. Tests: a scan without the own row marks
   every unit unknown and raises no absence; an empty "no keys" scan is
   self-unseen, not every cluster absent.
2. **C2. `lost` at a bound** (section 5): the table row `presence_lost_after`
   (30m), the per-cluster `lost_after` field read and cached, host sleep not
   counted, the clamp, and the bad-row default. Tests: lost fires once at the
   bound; a cluster value overrides the default; a bad cluster value falls back
   with one event; a cluster value of 720h is clamped to the maximum with one
   event; a bad `presence_lost_after` row in `thresholds.json` keeps 30m, raises
   one notice, and the watch starts.
3. **C3. Parent and cap** (section 6), on C2.
4. **C4. The key** (section 7): `dedupe_key` on the hub-side notices, and the
   sink fold for director's own sink. Tests:
   - a copy with no open item pages;
   - a copy with an open, shown `lost` folds and pages nothing;
   - a copy after close opens a new item;
   - **capped:** seven clusters lose their links at separate times within one
     hour, so no parent opens, and X's `lost` is the seventh, past a cap of 6.
     A forwarded leaf-down copy for X pages, even though the cap is already
     reached, and X's item is then shown;
   - **collapsed:** with a shown parent listing X open, a copy for X folds into
     the parent and pages nothing. With the parent closed or converted, the
     same copy pages;
   - **collapsed, not listed:** a copy for a cluster the open parent does not
     list pages.
5. **marvel, separate ticket:** the admin preference and the
   `DIRECTOR_PRESENCE_LOST_AFTER` stamp, plus the shim writing `lost_after`
   (director, folded into C2). Independent of C1, C3 and C4.
6. **mn10f** puts the same key on its supervisor item; it is already filed and
   blocked by the tap (aae-orc-zhx6x). Nothing here waits on it.

## 9. Candidate requirements (provisional, not ratified)

- **P1 (provisional; source: K7).** Director raises no absence, lost or parent
  alert from a pass that does not contain its own current presence row.
- **P2 (provisional; source: O4).** A shared dedupe key suppresses a page only
  at a sink that already holds an open, shown item under that key; no emitter checks
  another path before raising.
- **P3 (provisional; source: O6).** The away-to-lost bound is read per cluster
  from that cluster's presence record when present, else from director's own
  threshold table, default 30 minutes; director takes no dependency on marvel
  to read it, and clamps a cluster's value to a maximum it owns.
- **P4 (provisional; source: O4 and K7).** A held-back item (capped, or
  collapsed under a parent) is never a fold target; a copy matching only such
  an item pages, exempt from the cap.

## 10. Rulings this note needs

**R1. Does "ledger state" in K7 mean the watch's state?**
In short: K7 was written before the presence watch existed; the ask ledger
holds no presence history. Options: (a) the watch's state file is the
authoritative store for away and lost, cost: none beyond this note; (b) build
a presence history into the ask ledger, cost: a second store of the same
facts, with the ledger's 30-day prune. Recommendation: (a). This
recommendation is valid until 2026-11-03; the architect role re-checks it
then.

**R2. Where does the O6 value live?**
In short: the operator asked for a marvel admin preference. Options: (a) the
marvel preference, stamped into the seat env and carried on the presence
record, with director's own 30-minute row as the fallback (section 5), cost:
one marvel ticket and one record field; (b) director's own threshold row only,
documented in marvel's admin guide, cost: the admin edits a director file and
the value is fleet-wide, not per cluster. Recommendation: (a), since it is
what the operator asked for and (b) is already its fallback. This
recommendation is valid until 2026-11-03; the architect role re-checks it
then.

The parent and cap defaults are probe numbers under K7 and are not asked
here.

## 11. Not covered

- How director forwards a hub-side notice to the operator: today's practice is
  unchanged.
- The probe that sets the parent and cap numbers.
- Lifting `bus.leaf.down` to the hub, which marvel's design rules out of
  scope.
