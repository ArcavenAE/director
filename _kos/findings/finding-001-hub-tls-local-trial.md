# finding-001: server TLS on the global hub, proven on a scratch hub; the live cutover is one hub restart plus one leaf reload

- **Date:** 2026-09-15
- **Probe:** bd `aae-orc-4vx98` (bring TLS forward onto the interim hub); the instrument is `probe/nats-global-tier/verify-global-tls.sh`
- **Subject:** the director global hub (R-86, `sim/design/global-bus-tier.md`), so this is the first finding in director's own graph
- **Confidence:** bedrock for the mechanics measured on nats-server 2.14.6 (what links, what refuses, what reloads); frontier for the live shape until the operator-gated cutover runs and soaks
- **Live hub:** untouched. `~/.director/nats-global/nats-server.conf` still has its 2026-09-14 mtime and sha `a07593b9`, the server pid is 1 day 7 h old, `/leafz` still lists mokuzai, `/varz` still reports no TLS. Every check ran against a scratch hub on random free ports in a temp dir with fresh NKeys and a CA minted for the run.

## 1. The question

The infra component (the upstream nats-io chart under the infra ADR) defaults to server TLS on both the client and the leaf listeners. The interim hub on kinu runs plaintext; section 9 of the design accepted that for the LAN and deferred TLS to the cloud phase. The ticket asks whether that client-side shape can be brought forward now so the shim's global-mode path and the leaf link are exercised over TLS before the placement moves, and what the live cutover and its rollback look like.

## 2. What was proven (24 of 24)

The run, in the order it executed. Each line is one check that exits nonzero on a miss.

```
PASS hub cert SAN covers global-hub, kinu, localhost, 192.168.100.110, 127.0.0.1
PASS hub config with tls on both listeners passes nats-server -t
PASS /varz reports tls_required on the client listener and on the leaf listener
PASS admin connects over TLS with the CA; /connz records TLS 1.3 TLS_AES_128_GCM_SHA256
PASS a client with the system roots is refused: x509: "global-hub" certificate is not trusted
PASS a client trusting a CA that did not sign the hub cert is refused
PASS a nats:// client without the CA is refused (the listener upgrades to TLS, then the CA check fails)
PASS the director NKey user reads its stream over TLS; users and streams are the live hub's
PASS leaf remote on tls:// with ca_file links; hub /leafz lists it
PASS a leaf trusting a CA that did not sign the hub cert never links: x509: certificate signed by unknown authority
PASS director-mcp --preflight in global mode passes through the TLS leaf link (domain g3f3b)
PASS preflight still refuses an unprovisioned cluster over the TLS link, naming the stream
PASS a message published on the leaf side is stored in the hub's GLOBAL_TO_DIRECTOR over the TLS link
PASS pre-staging does NOT work: a remote carrying tls { ca_file } insists on TLS against a plaintext hub (leaf: TLS Handshake Failure, retried 4 times in 3s; hub: authentication error); it links on its own once the hub flips
PASS cutover step 0: a plaintext leaf is linked to the plaintext hub
PASS cutover step 1a: the client listener takes its tls block by config reload (Reloaded: tls = enabled); the plaintext leaf stays linked
PASS cutover step 1a: a plaintext client connection opened before the reload stays open and keeps receiving
PASS cutover step 1b: the leaf listener does NOT take its tls block by reload (config reload not supported for LeafNode: field "AuthTimeout"); that half is a restart
PASS cutover step 1c: after the restart both listeners require TLS
PASS cutover step 2: the plaintext leaf drops when the hub flips and says why: x509: "global-hub" certificate is not trusted
PASS cutover step 3a: a tls block added to a remote whose URL is unchanged is IGNORED by reload (remotes are diffed by URL); the leaf stays down with the old config
PASS cutover step 3b: with the URL changed to tls:// the reload applies the tls block and the leaf re-links over TLS, no leaf restart
PASS rollback: the hub drops its tls blocks (restart), the leaf drops its tls block (reload), and the link is back in plaintext
PASS allow_non_tls on the client listener admits a plaintext client and a TLS client side by side (transitional only)
24 passed, 0 failed (leaf pre-staging: no; hub leaf listener takes tls by reload: no)
```

The ticket's three success signals all hold: the shim's preflight in global mode passes with every hub-bound byte on a TLS link, the leaf link establishes over TLS, and a certificate the CA did not sign is refused on both listeners. Users, streams, and subjects are the live hub's, so this is the reconfiguration section 3 promised, not a rebuild.

## 3. The shape

Hub, both listeners. The paths are absolute on purpose: nats-server substitutes an environment variable only as a whole unquoted value (the rule `hub/start.sh` already records), so `"$DIRECTOR_GLOBAL_HOME/tls/hub.pem"` would stay literal.

```
tls {
  cert_file: "/Users/<operator>/.director/nats-global/tls/hub.pem"
  key_file:  "/Users/<operator>/.director/nats-global/tls/hub-key.pem"
  timeout: 2
}
leafnodes {
  listen: 0.0.0.0:7442
  tls {
    cert_file: "/Users/<operator>/.director/nats-global/tls/hub.pem"
    key_file:  "/Users/<operator>/.director/nats-global/tls/hub-key.pem"
    timeout: 2
  }
}
```

Leaf remote, on each cluster's local broker. On a fresh start the tls block alone makes the leaf negotiate TLS and verify the hub against the CA, with either URL scheme (checked both ways). On a config reload the scheme must change to `tls://` as well; section 5 fact 3 says why.

```
leafnodes {
  remotes: [
    { urls: ["tls://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY,
      tls { ca_file: "<path to the hub CA>" } }
  ]
}
```

Direct clients (the admin and `director` NKey users, which today means the nats CLI and `hub/provision.sh`): `--tlsca <hub CA>`. `nats://` still works because the listener announces `tls_required` and the client upgrades; the CA is the only new input.

Certificate: EC P-256, CA and server cert minted by `hub/tls-mint.sh` into `$DIRECTOR_GLOBAL_HOME/tls/` (mode 700; keys 600), SAN `DNS:global-hub, DNS:<host>, DNS:localhost, IP:<LAN IP>, IP:127.0.0.1`, server cert 825 days, CA 10 years. mokuzai dials the LAN IP, so the IP SAN is the one that matters today; the DNS names are for the cloud phase's URL and for the loopback admin path.

Wire: TLS 1.3, `TLS_AES_128_GCM_SHA256`, per `/connz`.

## 4. What the shim's global-mode path actually is, and why TLS on the hub covers it

The shim holds one NATS connection, to its local broker on loopback, and reaches the hub's JetStream domain through that broker's leaf link (`jetstream.NewWithDomain` in `global.go`). There is no second connection and no hub URL in the shim. Every shim byte that leaves the host therefore rides the leaf link, and TLS on the hub's leaf listener covers all of it. The preflight check in section 2 is that path: the shim never saw a certificate and did not need to.

Two consequences worth stating plainly:

- `dial()` in `bus.go` has no CA lever and no NKey-seed option. A shim cannot dial the hub directly against a private CA today, and it could not authenticate as the `director` NKey user either. Neither is a gap for the ratified topology: the seat's local broker leafs up (bd `aae-orc-bxg5f`) and the seat's shim stays on loopback. If a direct shim-to-hub dial is ever wanted, that is `nats.RootCAs` plus `nats.NkeyOptionFromSeed`, both additive; nothing here builds them.
- The loopback hop between shim and local broker stays plaintext. That is inside one host and one user, which section 9 never counted as the LAN exposure.

## 5. Cutover mechanics, measured

These four facts decide the order of the live change.

1. **A leaf cannot carry its tls block ahead of the hub.** A remote with `tls { ca_file }` insists on TLS; against a plaintext hub the leaf logs `Leafnode connection closed: TLS Handshake Failure` once a second and the hub logs `authentication error`. It does link on its own the moment the hub flips. So a leaf edited early is loud and unlinked until the hub restarts, then needs no further action.
2. **The hub's client listener takes TLS by config reload; its leaf listener does not.** `nats-server --signal reload` with the top-level tls block logs `Reloaded: tls = enabled`, new plaintext clients are refused, and a plaintext connection opened before the reload stays open and keeps receiving (measured with a subscriber held across the reload). Adding the leafnode tls block is refused: `config reload not supported for LeafNode: field "AuthTimeout"` (the TLS timeout changes the derived auth timeout). The leaf listener half is a restart.
3. **The leaf side takes TLS by reload, but only if the remote URL changes too.** Reload diffs leaf remotes by URL. A `tls { ca_file }` block added to a remote whose URL is unchanged is ignored without a word in the log: no `Reloaded: LeafNode Remote` line, and the leaf keeps failing with the old config (`certificate is not trusted`, the system roots). With the scheme changed from `nats-leaf://` to `tls://` the reload logs the remote removed and re-added, applies the block, and re-links over TLS with no leaf restart; the seed comes from the running process's environment as before. A fresh start applies the block with either scheme, so the quirk only bites the reload path, which is exactly the live path for mokuzai. Narrower than it first looks: once a tls block exists, a change to its `ca_file` alone IS picked up on the next reconnect after a reload (measured with a wrong CA corrected in place); it is the block appearing where none was that reload does not apply.
4. **Rollback is the same path reversed** and was run: hub restarted without the tls blocks, leaf reloaded without its tls block, link back in plaintext.

`allow_non_tls: true` (top level) admits plaintext and TLS clients side by side on the client listener. It is recorded as a transitional posture only, for a direct client that lags the hub; the leaf listener has no such switch, and no step below uses it.

## 6. The live cutover and backout (RECOMMENDED, operator-gated, not done)

Nothing below has been applied. The live hub carries the director bus in active use; this is the operator's change to schedule, with skippy for the mokuzai half. Each host gets its own cutover and its own backout, and the bus-loss window is stated per host first, because the order of the steps follows from it.

### 6.0 What is on the hub today (read 2026-09-16, monitoring endpoints only)

- The hub has exactly one connection: mokuzai's leaf link (12 subscriptions, up 11 h). No direct client is connected to 4242.
- The kinu phase-0 broker (`director-phase0`, 4222) has no JetStream domain and no leaf link; `aae-orc-bxg5f` is still open. Its clients are eleven fleet shims and the seat, all in local mode. Nothing on kinu reaches the hub today.
- `GLOBAL_PRESENCE` holds no rows. `GLOBAL_TO_mokuzai` holds 3 messages (last 11 h ago); `GLOBAL_TO_DIRECTOR` and `GLOBAL_TO_kinu` hold none.

So the bus that is lost during the flip is the global tier only, and its only live party is mokuzai's leaf. The local tiers on both hosts are outside the change.

### 6.1 The bus-loss window, per host

**kinu**

| What | During the flip |
|---|---|
| Local bus (phase-0 broker, 4222) | Unaffected. Every kinu shim and the seat keep sending and receiving. Nothing on kinu holds a hub connection today. Once bxg5f makes this broker a leaf, kinu inherits the mokuzai row below and the same cutover shape. |
| The hub process | Down for the stop and start: one to two seconds on the scratch hub. No direct client exists today; a hand-run nats CLI session would be disconnected and would need `--tlsca` to come back. |
| Director to mokuzai channel | Dark from the hub stop until mokuzai re-links. A send to `global://mokuzai/supervisor` refuses before publish once the mokuzai presence row has expired (90 s after its last write; the bucket is empty today anyway), and writes nothing. A send that lands while a row is still live is stored on `GLOBAL_TO_mokuzai` and delivered when the link returns. |
| Stored state | Streams, the presence bucket, their messages, and the durable consumers live in the file store under `~/.director/nats-global/store`, which neither diff touches; the restart reads it back. The 3 messages on `GLOBAL_TO_mokuzai` survive. |

**mokuzai**

| What | During the flip |
|---|---|
| Local bus (skippy's broker) | Unaffected. `--signal reload` re-reads the file without dropping client connections (the same reload on the scratch hub kept a held subscriber receiving). Skippy's supervisor and team traffic continue. |
| Leaf link | Down from skippy's reload until the hub restarts with TLS (leaf-first order), or from the hub restart until skippy's reload (hub-first). The leaf retries once a second, so recovery after the second step is automatic within a second or two. |
| A supervisor shim in global mode, link down | A global send returns an error to the model (`no responders`, with the hint that a down link and a refusing credential look the same); nothing is stored, and the local audit mirror is written only after a successful hub publish, so a refused send leaves no record that reads as delivery. The presence write fails and the shim logs `WARNING: global tier unavailable, local tier unaffected` once, then `global tier recovered` once. The global inbox poll returns a warning beside local results; local receive continues. Its `GLOBAL_PRESENCE` row expires 90 s after the last successful write and is rewritten within 30 s of the link returning. The global durable on the hub survives (25 h inactivity threshold) and is rebuilt on the next poll if it does not; anything stored on `GLOBAL_TO_mokuzai` in the window is delivered after re-link. |
| Launching a new global-mode session | Refused. `--preflight` in global mode fails loudly while the hub is unreachable (by design, finding-166 one tier up), so a cast-launch of a supervisor on mokuzai crashes its pane and marvel backs off. Existing sessions are unaffected. Skippy should not relaunch a global-mode supervisor inside the window. |

The window's length is the gap between the two steps, whichever order. With the leaf-first order the second step (the hub restart) is the only moment that needs both people's attention, and recovery is automatic when it lands.

### 6.2 Preconditions (both hosts)

Superseded on 2026-09-16 by finding-002 section 3: the CA and the hub certificate come from the root ceremony (`hub/CEREMONY.md`), not from `tls-mint.sh`, and `ca.pem` is digest-matched on mokuzai before the flip. The rest of this section 6 stands.

- On kinu: `hub/tls-mint.sh`, once. Writes `~/.director/nats-global/tls/{ca.pem,ca-key.pem,hub.pem,hub-key.pem}`. Check the SAN it prints carries `IP Address:192.168.100.110`; that is the name mokuzai dials.
- Ship `ca.pem` to mokuzai (public material; any channel). Suggested path there: `~/.director/nats/hub-ca.pem`, readable by the broker's user.
- On kinu: `cp ~/.director/nats-global/nats-server.conf ~/.director/nats-global/nats-server.conf.pre-tls`. On mokuzai: the same for skippy's broker conf.
- Agree a time and a backout deadline: if `/leafz` on kinu does not list mokuzai within 5 minutes of the hub restart, back out (6.4 case C).

### 6.3 kinu: cutover

**Hub diff** (`~/.director/nats-global/nats-server.conf`; `<H>` is the operator's home; absolute paths on purpose, section 3)

```diff
 # director global bus tier (R-86), interim placement on kinu (operator ruling 2026-09-14).
 # Second NATS on this host: the phase-0 local broker stays on 127.0.0.1:4222.
 # Client 4242 and leaf 7442 bind the LAN interface; monitoring stays loopback.
-# Transport TLS is deferred to the cloud phase; principal authorization (R-77) is not.
+# Server TLS on both listeners since <cutover date> (finding-001); mutual TLS and the fleet CA stay with the cloud phase.
 server_name: global-hub
 listen: 0.0.0.0:4242
 http: 127.0.0.1:8242
+tls {
+  cert_file: "<H>/.director/nats-global/tls/hub.pem"
+  key_file: "<H>/.director/nats-global/tls/hub-key.pem"
+  timeout: 2
+}
 jetstream {
   ...
 }
 leafnodes {
   listen: 0.0.0.0:7442
+  tls {
+    cert_file: "<H>/.director/nats-global/tls/hub.pem"
+    key_file: "<H>/.director/nats-global/tls/hub-key.pem"
+    timeout: 2
+  }
```

The live file is also behind the repo template on the `STREAM.MSG.DELETE` grants from director#41. The restart is a natural moment to bring those in, but it is a separate decision; the diff above is against the live file as it is.

**Steps**

1. Apply the diff. Check it before anything stops: `nats-server --config ~/.director/nats-global/nats-server.conf -t`. A refusal here changes nothing; the running hub is untouched.
2. Confirm skippy has done 6.5 step 1 (or agreed to do it right after; see the order note below).
3. Stop the hub: `kill "$(cat ~/.director/nats-global/nats-server.pid)"`. Start it the way it runs today (`probe/nats-global-tier/hub/start.sh` in its own session or backgrounded; it runs `-t` again, then execs with the pidfile and log). Expect one to two seconds.
4. Verify, in this order:
   - `curl -s 127.0.0.1:8242/varz | jq '{tls_required, leaf: .leaf.tls_required}'` shows both `true`.
   - `curl -s 127.0.0.1:8242/leafz | jq '.leafs[].name'` lists `mokuzai` within a second or two of skippy's side being done.
   - `nats -s tls://127.0.0.1:4242 --tlsca ~/.director/nats-global/tls/ca.pem --nkey ~/.director/nats-global/keys/admin.nk stream ls` shows the three streams with their message counts as before.
   - `curl -s '127.0.0.1:8242/connz?state=closed' | jq '.connections[-1].tls_version'` shows `1.3` for that CLI connection.
5. From mokuzai, skippy runs `HUB_CA=~/.director/nats/hub-ca.pem probe/nats-global-tier/verify-global-shim.sh`; 14 of 14 as before the change.

Order: leaf-first (mokuzai 6.5 first, then this) is recommended because the leaf retries on its own and the hub restart is then the single moment of coordination. Hub-first is equally valid and leaves the link down for exactly as long as skippy's step takes. Either way the window is the gap between the two steps.

### 6.4 kinu: backout and recovery

| Case | Signal | Action | Bus impact |
|---|---|---|---|
| A. The config check refuses | `nats-server -t` exits nonzero (a path, a permission, a typo) | Fix the file or stop here. Nothing has changed. | None. |
| B. The hub does not start | `start.sh` exits, or `nats-server.log` ends in an error (unreadable key, bad cert) | `cp ~/.director/nats-global/nats-server.conf.pre-tls ~/.director/nats-global/nats-server.conf` and `start.sh`. Tell skippy to run 6.6 case D. | Hub down for the time to notice plus two seconds. State intact: the store was not written by the failed start. |
| C. The hub is up, mokuzai does not link by the deadline | `/leafz` empty; hub log shows `TLS leafnode handshake error` or nothing; mokuzai log says why (6.6 cases A to C) | Read both logs first; the common causes are on the mokuzai side and are fixed there without touching the hub. If not fixed by the deadline: restore `.pre-tls`, `start.sh`, and skippy runs 6.6 case D. | Global tier dark from the hub stop until the link returns; local tiers unaffected throughout. |
| D. Everything links, something else is wrong (a client cannot reach 4242, a stream is missing) | The 6.3 step 4 checks | A missing stream after a restart is the finding-166 shape and `hub/provision.sh` is idempotent; run it (it picks up `tls/ca.pem` on its own). A client refusal is the CA: add `--tlsca`. Neither is a reason to back out. | None beyond the restart. |

Recovery after a backout: the hub is plaintext again exactly as before; the mokuzai link is back once skippy has reverted (6.6 case D); the store, streams, and presence bucket were never in the diff. The minted CA and cert stay in `tls/` for the next attempt.

### 6.5 mokuzai: cutover

**Remote diff** (skippy's local `nats-server.conf`; the scheme change is load-bearing on the reload path, section 5 fact 3)

```diff
 leafnodes {
   remotes: [
-    { urls: ["nats-leaf://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY }
+    { urls: ["tls://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY,
+      tls { ca_file: "/home/<skippy>/.director/nats/hub-ca.pem" } }
   ]
 }
```

**Steps**

1. Apply the diff and reload: `nats-server --signal reload=<broker pid>`. The broker log should show `Reloaded: LeafNode Remote ... removed` and then, until the hub flips, `Leafnode connection closed: TLS Handshake Failure` once a second. That is the expected state of the window, not a fault. Local clients are untouched. (A broker restart works too and does not need the scheme change, but it does drop local clients, which reconnect on their own.)
2. Do not launch a new global-mode supervisor until step 3.
3. When the operator reports the hub is back (6.3 step 4), confirm `Leafnode connection created` and `JetStream using domains: local "mokuzai", remote "global"` in the broker log. A running global-mode shim logs `global tier recovered` within 30 s.
4. Run `HUB_CA=~/.director/nats/hub-ca.pem probe/nats-global-tier/verify-global-shim.sh`; 14 of 14.

### 6.6 mokuzai: backout and recovery

| Case | Signal | Action | Bus impact |
|---|---|---|---|
| A. The reload did nothing | No `Reloaded:` line, or a reload error in the log | A config error keeps the old config running; fix and reload. If the log is silent, the URL was left unchanged (fact 3): change the scheme to `tls://` and reload, or restart the broker. | None until fixed; the link is still plaintext against the old hub. |
| B. After the hub flip the link fails with `certificate is not trusted` or `unknown authority` | Broker log, once a second | Wrong CA file or path. Fix `ca_file` and reload: a path change inside an existing tls block is picked up on the next reconnect (measured; the URL does not need to change again). | Link down until fixed; local traffic unaffected. |
| C. After the hub flip the link fails with a name or IP mismatch | `x509: certificate is valid for ...` in the broker log | The hub cert's SAN does not carry the address in `urls`. Fix is on kinu (re-mint with `HUB_LAN_IP` set) or change `urls` to a name the SAN carries; then reload here. | Same as B. |
| D. The operator backs the hub out (6.4 B or C) | The operator says so; the log shows `TLS Handshake Failure` against the plaintext hub | Restore the pre-TLS remote line: scheme back to `nats-leaf://`, no tls block (the URL change is what makes the reload apply it), and reload. `Leafnode connection created` follows within a second. | Link down between the hub backout and this reload; local traffic unaffected. |

Nothing in this section touches skippy's local streams, presence bucket, or sessions; the remote block is the only edit, and a reload is the only action.

### 6.7 After the cutover

- Direct clients on kinu: `hub/provision.sh` picks up `tls/ca.pem` on its own once it exists; hand-run nats CLI calls against 4242 add `--tlsca ~/.director/nats-global/tls/ca.pem`; `verify-global.sh` takes `HUB_CA=`.
- Update `recipe-mokuzai.md` section 1 and section 9 of the design to describe the new posture; retire the "No TLS on 4242 or 7442" paragraph in both.
- When bxg5f lands, the kinu leaf joins with the tls block and `tls://` from the start; no second window.

## 7. What the cloud path still owns

- **The fleet CA.** This CA is local, self-signed, and minted on kinu. The cloud phase replaces it (the infra component's cert-manager TLS); every leaf and direct client then swaps `ca_file`. The mechanics in section 5 apply unchanged.
- **Mutual TLS on the leaf listener** (`verify: true`, a client cert per leaf, `leafnode_tls_verify` in the infra component). Not exercised here; the leaf still authenticates by NKey only.
- **DNS and public reachability.** The SAN carries the hub name and the host name for that day; the LAN IP entry retires when the URL becomes a name.
- **Certificate rotation.** Only enabling TLS by reload was measured, not swapping a certificate under a live link.

## 8. Follow-ons

- **marvel's rendered leaf remote has no CA field.** `sim/design/local-broker-supervision.md` says so (`bus.hub.url`, "no CA field yet"), and `marvel/internal/bus/render.go` renders the remote without a tls block. A marvel-managed local broker cannot join a TLS hub until `bus.hub` gains a `ca_file` and the renderer emits `tls { ca_file }`. Filed as ArcavenAE/marvel#278 (GH-issue layer; the narrow field, with the general marvel CA shape under separate design).
- **The kinu leaf** (`aae-orc-bxg5f`) joins after the cutover with the tls block from the start, the same shape as mokuzai's diff.
- **Section 9 of the design** marks the local TLS item addressed by this finding once the live cutover runs; until then the note there points here.

## Files

- `probe/nats-global-tier/verify-global-tls.sh`: the 24-check instrument (scratch hub, scratch leaves, the shim built from source; `KEEP=1` keeps the work dir).
- `probe/nats-global-tier/hub/tls-mint.sh`: the one-time CA and cert mint for the cutover.
- `probe/nats-global-tier/verify-global.sh`, `verify-global-shim.sh`, `hub/provision.sh`: gain a `HUB_CA` knob so they verify the hub after the cutover without edits.
- `probe/nats-global-tier/leaf-remote.conf.example`: the post-cutover remote, commented.
