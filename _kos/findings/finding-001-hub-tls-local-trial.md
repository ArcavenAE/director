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
3. **The leaf side takes TLS by reload, but only if the remote URL changes too.** Reload diffs leaf remotes by URL. A `tls { ca_file }` block added to a remote whose URL is unchanged is ignored without a word in the log: no `Reloaded: LeafNode Remote` line, and the leaf keeps failing with the old config (`certificate is not trusted`, the system roots). With the scheme changed from `nats-leaf://` to `tls://` the reload logs the remote removed and re-added, applies the block, and re-links over TLS with no leaf restart; the seed comes from the running process's environment as before. A fresh start applies the block with either scheme, so the quirk only bites the reload path, which is exactly the live path for mokuzai.
4. **Rollback is the same path reversed** and was run: hub restarted without the tls blocks, leaf reloaded without its tls block, link back in plaintext.

`allow_non_tls: true` (top level) admits plaintext and TLS clients side by side on the client listener. It is recorded as a transitional posture only, for a direct client that lags the hub; the leaf listener has no such switch, and no step below uses it.

## 6. The live cutover (RECOMMENDED, operator-gated, not done)

Nothing below has been applied. The live hub carries the director bus in active use; this is the operator's change to schedule, with skippy for the mokuzai half.

**Preconditions**

- `hub/tls-mint.sh` on kinu, once. Writes `~/.director/nats-global/tls/{ca.pem,ca-key.pem,hub.pem,hub-key.pem}`.
- Ship `ca.pem` to mokuzai (public material; any channel). Suggested path there: `~/.director/nats/hub-ca.pem`.
- `cp ~/.director/nats-global/nats-server.conf ~/.director/nats-global/nats-server.conf.pre-tls`.

**Hub diff** (`~/.director/nats-global/nats-server.conf`; `<H>` is the operator's home)

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

**mokuzai diff** (skippy's local `nats-server.conf`)

```diff
 leafnodes {
   remotes: [
-    { urls: ["nats-leaf://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY }
+    { urls: ["tls://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY,
+      tls { ca_file: "/home/<skippy>/.director/nats/hub-ca.pem" } }
   ]
 }
```

**Order** (fact 1 makes the leaf-first order the one with the least coordination)

1. skippy applies the mokuzai diff, scheme change included, and reloads (`nats-server --signal reload=<pid>`); a broker restart works too and does not need the scheme change. The link drops with `TLS Handshake Failure` once a second and local traffic on mokuzai continues; global sends from mokuzai refuse loudly after one presence TTL; director-to-mokuzai envelopes queue on the hub stream for up to 24 h.
2. The operator applies the hub diff, checks it (`nats-server --config ~/.director/nats-global/nats-server.conf -t`), stops the hub, and starts it (`hub/start.sh`). The mokuzai link comes up on its own within a second.
3. Verify: `curl -s 127.0.0.1:8242/varz | jq '{tls_required, leaf: .leaf.tls_required}'` shows both true; `/leafz` lists mokuzai; `/connz` shows `tls_version` on new client connections. From mokuzai, `HUB_CA=~/.director/nats/hub-ca.pem probe/nats-global-tier/verify-global-shim.sh` should pass 14 of 14 as before the change.
4. Direct clients: `hub/provision.sh` picks up `tls/ca.pem` on its own once it exists; hand-run nats CLI calls against 4242 add `--tlsca ~/.director/nats-global/tls/ca.pem`. `verify-global.sh` takes `HUB_CA=`.
5. Update `recipe-mokuzai.md` section 1 and section 9 of the design to describe the new posture; retire the "No TLS on 4242 or 7442" paragraph in both.

The reverse order (hub first, mokuzai second) is equally valid and leaves the link down for exactly as long as step 1 takes skippy; the leaf-first order lets his step happen any time before the restart.

**Rollback**

1. `cp ~/.director/nats-global/nats-server.conf.pre-tls ~/.director/nats-global/nats-server.conf`; stop and start the hub. The mokuzai link drops with `TLS Handshake Failure` until step 2.
2. skippy removes the tls block from the remote, puts the scheme back to `nats-leaf://` (a changed URL is what makes the reload apply the edit), and reloads. Link back in plaintext.

The JetStream store directory is not in either diff; streams, the presence bucket, and their contents are read back by the restart as they are by every restart. The NKey users and their permissions are not in either diff.

## 7. What the cloud path still owns

- **The fleet CA.** This CA is local, self-signed, and minted on kinu. The cloud phase replaces it (the infra component's cert-manager TLS); every leaf and direct client then swaps `ca_file`. The mechanics in section 5 apply unchanged.
- **Mutual TLS on the leaf listener** (`verify: true`, a client cert per leaf, `leafnode_tls_verify` in the infra component). Not exercised here; the leaf still authenticates by NKey only.
- **DNS and public reachability.** The SAN carries the hub name and the host name for that day; the LAN IP entry retires when the URL becomes a name.
- **Certificate rotation.** Only enabling TLS by reload was measured, not swapping a certificate under a live link.

## 8. Follow-ons (recorded here, not filed as tickets by this finding)

- **marvel's rendered leaf remote has no CA field.** `sim/design/local-broker-supervision.md` says so (`bus.hub.url`, "no CA field yet"), and `marvel/internal/bus/render.go` renders the remote without a tls block. A marvel-managed local broker cannot join a TLS hub until `bus.hub` gains a `ca_file` and the renderer emits `tls { ca_file }`. This lands in marvel's court beside `aae-orc-e9g8i`.
- **The kinu leaf** (`aae-orc-bxg5f`) joins after the cutover with the tls block from the start, the same shape as mokuzai's diff.
- **Section 9 of the design** marks the local TLS item addressed by this finding once the live cutover runs; until then the note there points here.

## Files

- `probe/nats-global-tier/verify-global-tls.sh`: the 24-check instrument (scratch hub, scratch leaves, the shim built from source; `KEEP=1` keeps the work dir).
- `probe/nats-global-tier/hub/tls-mint.sh`: the one-time CA and cert mint for the cutover.
- `probe/nats-global-tier/verify-global.sh`, `verify-global-shim.sh`, `hub/provision.sh`: gain a `HUB_CA` knob so they verify the hub after the cutover without edits.
- `probe/nats-global-tier/leaf-remote.conf.example`: the post-cutover remote, commented.
