# I-469: a read-only observer principal on the global hub

Architect seat, arcaven team, mokuzai, 2026-11-13. Draft for a builder PR in
director (proposed home: `sim/design/hub-observer.md`). Sources: director
origin/main cd77211, marvel origin/main e74a50e0, and director's read of kinu's
live hub conf as relayed 2026-11-13T13:14Z. Recommendations are valid until
2026-11-20; this architect seat re-checks them then.

Today director cannot see whether a seat on another cluster is sitting on mail,
so fleet health reports "0 mail" when the honest answer is "not read". The
operator ruled (I-469) that director, and later marvel's majordomo and possibly
a third reader, should read anyone's mail. This design does that with one new hub
user that can read every inbox and cannot take, send, ack or delete anything.

## 1. The principal and its grant

Why a new user and not a wider leaf: everything that crosses a leaf is bounded
by that leaf's hub user. Today the shim reaches the hub only through its local
broker's leaf (global.go: "the hub is reached through the local broker's leaf
link"). On kinu that leaf user grants JS API on GLOBAL_TO_DIRECTOR,
GLOBAL_TO_kinu and KV_GLOBAL_PRESENCE only. A role grant therefore needs its own
hub user and a direct client connection to the hub's client port, which no
client uses today (finding-019 item 3). Widening kinu's leaf would give the read
to every seat on kinu, which is the host grant the ruling rules out.

User `observer.director`: one NKey user in the hub's FLEET account. It
connects directly to the hub, so it uses the plain `$JS.API.` prefix (the
precedent is the template's "director seat connecting directly" user).

| publish allow | why |
|---|---|
| `$JS.API.INFO` | account info |
| `$JS.API.STREAM.NAMES`, `$JS.API.STREAM.LIST` | list the hub streams (`unread --global`'s first call) |
| `$JS.API.STREAM.INFO.*` | stream state, and the subject-filtered info that names presence keys without a consumer (unread.go readKeysUnder) |
| `$JS.API.CONSUMER.NAMES.*`, `$JS.API.CONSUMER.LIST.*` | list each stream's durables (unread.go ListConsumers) |
| `$JS.API.CONSUMER.INFO.*.*` | pending, ack-pending and floor of a named durable (the roster check) |
| `$JS.API.STREAM.MSG.GET.*`, `$JS.API.DIRECT.GET.*`, `$JS.API.DIRECT.GET.*.>` | read a message by sequence or subject, which changes no consumer |

Subscribe allow: `_INBOX_observer_director.>` only, with the shim setting
that custom inbox prefix on this connection.

Explicit deny on publish, so a later widening of the allow list cannot slip
these in: `$JS.API.CONSUMER.CREATE.>`, `$JS.API.CONSUMER.DURABLE.CREATE.>`,
`$JS.API.CONSUMER.DELETE.>`, `$JS.API.CONSUMER.MSG.NEXT.>`,
`$JS.API.CONSUMER.PAUSE.>`, `$JS.API.STREAM.MSG.DELETE.>`,
`$JS.API.STREAM.PURGE.>`, `$JS.ACK.>`, `global.>`, `$KV.>`.

The stream token is `*`, not `GLOBAL_TO_*`. NATS wildcards match whole tokens,
so `GLOBAL_TO_*` would be read as a literal stream name. `*` covers every stream
in FLEET (the GLOBAL_TO_ streams and KV_GLOBAL_PRESENCE). It is read-only, and a
new cluster needs no re-render.

Why reading never takes a message from its owner:
- The hub streams use limits retention (provision.sh `--retention limits`), so
  no read removes a message. Only max age does.
- A seat's mail leaves its view only when that seat's own durable acks it.
  The observer has no consumer, holds no MSG.NEXT, and cannot ack, so it cannot
  move any durable's floor.
- MSG.GET and DIRECT.GET return a stored message and change no consumer
  state.
- It creates no consumer at all. That keeps marvel's invariant that "every
  consumer on GLOBAL_TO_<cluster> is filtered to a single inbox subject"
  (marvel docs/design/bus-role-users.md, which already names
  `unread --global` as a reader that creates no consumer). It also rules out
  the ephemeral ordered consumer the ask allowed for, since one could be
  pulled from by anyone who can name it.

Residual, pre-existing and not widened: every FLEET user today subscribes
`_INBOX.>`, so it can see replies to others' requests. The custom prefix keeps
the observer out of that set. Narrowing the existing users is separate work.

## 2. Who holds it

`observer.director`: the director seat's shim on kinu, and the CLI read paths
that run as director there (`unread --global`, freebusy through it, and the
global durable check in `list_roster`). The key reaches the shim by path, in
three new variables set only in the director seat's launch environment:
`DIRECTOR_HUB_OBSERVER_URL`, `DIRECTOR_HUB_OBSERVER_NKEY` (seed file path) and
`DIRECTOR_HUB_OBSERVER_CA` (only if the client listener has TLS; finding-019 left
that half "reloadable for later", and I have not checked the live state). The
shim opens this connection only for those reads and never for sending, its
inbox or presence.

Reserved, not issued: `observer.majordomo` (marvel's majordomo, or a functionary
under its supervisor) and `observer.reserved-2` (a third reader; unnamed here). Each gets the same grant
template, with its own inbox prefix and its own key. The hub conf carries them
as commented-out blocks with no public key. Issuing one needs an operator ruling
and the majordomo design. The template is per role so that revoking one holder
leaves the others.

## 3. Custody (SOUL section 3, ADR-009)

This is issuance, not custody. The audience test: the key means something only
to the operator's own hub on kinu. The operator mints it on that host (`nk -gen
user`), and revokes it by deleting one public key from the hub conf and
reloading, with nobody at a third party's console. No vendor credential is
involved.

At rest: the seed is in `$DIRECTOR_GLOBAL_HOME/keys/observer-director.nk`, mode
0600, in the 0700 keys directory beside `admin.nk`, `director.nk` and the leaf
seeds (start.sh: "seeds never leave the keys dir"). The hub conf holds only the
public key. Nothing goes in a repo, in marvel, or on the bus.

Honest limit: file mode separates OS users, not seats. Any process on kinu under
the operator's own user can read this seed, as it already can read `admin.nk`.
Passing the path only in the director seat's environment keeps the key out of
other seats' env. It does not stop a determined local reader. The full fix is a
separate OS user for the director seat, out of scope here.

## 4. What the readers do with it

| reader | grant present and connected | no grant (an install without it) | grant set but connect or read fails |
|---|---|---|---|
| `unread --global` | full read of every GLOBAL_TO_ stream; no "partial" warning | today's leaf path: own cluster's stream by name, then rec 2's presence fallback for other clusters, marked self-reported | a GAP naming the observer error, then the no-grant path; never 0 |
| `list_roster` global durable check | CONSUMER.INFO through the observer for every row | today's check; other clusters' rows say durable-unchecked | the row's durable_note names the observer error |
| freebusy | unchanged; it reads `unread --global` | GAP plus rec 1's "not read" | same |

Rec 2 is still needed, as its own small PR. The observer exists only where the
operator issued it. A second user of director (BYOA), any seat reading off kinu
(a cluster supervisor's `list_roster`), and kinu during an outage of the direct
connection all fall back to the leaf path. There, presence is the only cross-
cluster signal. Rec 2 does not block on this design, and this design does not
block on rec 2.

Order: rec 1 (in the current PR), then the observer read path, then rec 2. Either
of the last two can go first.

Red tests for the builder PR, named here and written there:
- `TestObserverReadsEveryClusterStreamWithoutAConsumer`: consumer count on each
  stream is the same before and after.
- `TestObserverReadLeavesEverySeatDurableFloorUnchanged`: num_pending and the ack
  floor are unchanged.
- `TestObserverConnectFailureFallsBackAndNamesTheGap`.
- `verify-hub-observer.sh` on a scratch hub plus two leaves, under the four run
  conditions (relay-run-conditions.md):
  - positive: stream list, consumer info, MSG.GET;
  - negative: CONSUMER.CREATE, MSG.NEXT, CONSUMER.DELETE, STREAM.MSG.DELETE and a
    publish on `global.director.inbox` are each refused.

## 5. Reaching the live hub

Adding a FLEET user is a config reload, not a restart. finding-019 item 4: three
reloads that logged "Reloaded: authorization nkey users" kept both leaves
connected. Its caveat, that a changed grant waits for a leaf reconnect, does not
apply, because no existing user's grant changes. The operator runs it on kinu,
after the builder PR has merged and `verify-hub-observer.sh` has passed.

The script is `2026-11-13-hub-observer-user.sh` beside this file, written for
`~/operator-steps` on kinu and not run. It does these steps:
- refuses if the user already exists;
- backs up the conf;
- mints the seed;
- inserts the user block, generated from the template's observer.director block
  (`probe/nats-global-tier/hub/nats-server.conf`, the one copy of the grant);
- validates with `nats-server -t`, restoring the backup on failure;
- reloads by the hub's pid file;
- checks the log line;
- runs the positive reads;
- runs harmless negatives that cannot take mail even if wrongly allowed
  (MSG.NEXT on a durable that does not exist, an empty CONSUMER.CREATE), each
  passing only when the hub logs a permission violation naming that subject;
- compares each stream's consumer count before and after with the admin key;
- prints the rollback, and on any failure after the conf changed ends with a
  `ROLLBACK:` line carrying the one command that restores the backup and reloads.

`verify-hub-observer-script.sh` runs the script against scratch hubs: the happy path, a
wrongly allowed negative, and a failure after the reload.

Then the director seat needs the three variables at its next launch. That is a
launcher change for director's install, also the operator's.

## Not decided here

Issuing any key, applying the conf, the majordomo design, and whether
the third reader holds a slot. All are the operator's. The narrower `_INBOX` for
existing users is separate work.
