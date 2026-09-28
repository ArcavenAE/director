# finding-009: a grant added to the hub by config reload does not reach a leaf connection that is already open; the leaf has to reconnect

- **Date:** 2026-09-27 (observed 2026-09-26)
- **Session:** observed by arcaven-supervisor-g5-0 on the live global tier; written by arcaven-author-g5-0 from the supervisor's harvest placement (item 2)
- **Subject:** the global bus tier (R-86): how a permission change on the hub reaches the clusters linked to it
- **Confidence:** one live observation, with its own before-and-after; not yet reproduced on a scratch hub

## The finding

**A permission added to the hub by config reload was not honored on a leaf
connection opened before the reload. It worked after that leaf reconnected.**

- The hub was reloaded by SIGHUP at 13:28:05 on 2026-09-26 and logged
  `Reloaded: authorization nkey users`.
- After the reload, `$JS.global.API.STREAM.INFO.GLOBAL_TO_kinu`, a request
  the new grant was meant to allow, answered `No responders are available`
  through kinu's leaf connection lid:29. That connection had been open since
  2026-09-25 16:17:43.
- Over that same connection, a request under the older `GLOBAL_TO_DIRECTOR`
  grant answered normally. The connection was up; only the newer grant was
  missing.
- The operator's broker restart made the leaf reconnect as lid:854 at
  15:31:31. The same `GLOBAL_TO_kinu` request then answered.

It looks like the leaf's permissions are fixed when it connects, and a later
authorization reload applies only to connections made after it. That is
inferred from one before-and-after, not from nats-server's code.

## Why it matters

- **Adding a cluster, or a grant, is two steps, not one.** Reloading the hub
  is not enough; each affected leaf also has to reconnect. Until it does,
  the new subject fails as `No responders`, which reads like a missing
  consumer rather than a missing permission. finding-008 recorded the same
  disguise for a refused publish, which arrives as a timeout.
- **It is the same shape as two siblings.** finding-001 found that the hub's
  leaf listener does not take TLS by reload, so that half needs a restart.
  Orc finding-178 found that marvel's `bus leaf connect|disconnect` is
  reload-only, while re-pushing the leaf seed restarts the broker. In each
  case "reload applied" and "the running connection changed" are different
  claims.

## Open questions

A. Reproduce on a scratch hub (finding-001's rig shape): add a grant by
reload, test an existing leaf and a new one, and confirm which nats-server
behavior this is.

B. Should the hub runbook for adding a grant end with a leaf reconnect (for
example `marvel bus leaf disconnect` then `connect` on each affected cluster)
and a named check that the new subject answers?

## Evidence

- arcaven-supervisor-g5-0 harvest 2026-09-27, section 1: the timestamps, lids
  and responses above.
- Supervisor placement plan 2026-09-27, item 2.
- Siblings: finding-001 (TLS by reload), finding-008 (refusal seen as a
  timeout), orc `_kos/findings/finding-178` (marvel leaf controls,
  reload-only versus restart).
