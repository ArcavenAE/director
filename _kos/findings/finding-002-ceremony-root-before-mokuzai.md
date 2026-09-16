# finding-002: the trial root does not ship; a ceremony root replaces it before the mokuzai cutover

- **Date:** 2026-09-16
- **Probe:** the marvel CA party (`_bmad-output/party-mode/marvel-ca-shape-2026-09-16/report.md`, section 6, part 1 items 1 and 2), commissioned after director#42 merged; bd `aae-orc-4vx98` REMAINING. Instruments: `hub/ca-ceremony.sh` in rehearsal mode and `verify-global-tls.sh` with `TLS_DIR`.
- **Subject:** the director hub's certificate authority: where its key lives and what it has signed.
- **Confidence:** bedrock for the measured mechanics (the rehearsal, the chain on a scratch hub, the two OpenSSL and macOS gotchas); frontier for the live shape until the real ceremony runs and the cutover follows.
- **Live hub:** untouched, still plaintext. The live `~/.director/nats-global/tls/` does not exist yet; the trial root was never minted there.

## 1. What #42 should have carried before anything shipped to mokuzai

finding-001 proved the TLS mechanics and wrote the cutover. It also left `hub/tls-mint.sh` as the source of the live CA: a ten-year root, generated on the live filesystem beside the server key, unencrypted, with no second copy and no record of what it signed. The party's losers' concession was exact: that root does not ship to mokuzai as-is. Once `ca.pem` is on another host and in another operator's broker config, the root behind it is a fleet commitment, and its custody has to be decided before that, not after.

| As #42 left it | Corrected here | Why |
|---|---|---|
| Root key written to `~/.director/nats-global/tls/ca-key.pem`, mode 600, on the boot volume | Root key born on a RAM disk, exists afterwards only as an AES-256 encrypted file on two offline volumes | a key on the live filesystem is in every backup, every agent's reach, and every laptop theft |
| No passphrase | Passphrase typed by a person at openssl's prompt; the script refuses a pipe; no file, no environment variable, no agent | the passphrase is the one secret an agent must never hold; the script shape makes that the default rather than a rule to remember |
| `-days 3650` | 1826 days (five years), `pathlen:1` | a root that outlives the platform's plans is unrevocable in practice; `pathlen:1` lets part 2 hang intermediates off it without a new root |
| One copy | Two offline copies, written and read back by digest, kept in step by the `sign` path | one volume is one failure |
| No record of signatures | `ceremony.log`: one line per root creation and per certificate signed (time, subject, SAN, serial, expiry, cert and CSR digests, who, ceremony id), on both copies and beside the served certificates | a certificate with no log line was not made by the ceremony, whatever its issuer says |
| CA key and server key minted together in one place | `hub-csr.sh` makes the server key on kinu and keeps it there; only the CSR goes to the ceremony; `hub.pem` comes back | the two keys have different custody and never need to meet |
| `tls-mint.sh` writes the live directory | `tls-mint.sh` refuses anything under `~/.director` and mints 30-day trial material only | the trial tool cannot become the live tool by accident again |
| No check that the CA on mokuzai is the CA the hub serves | Digest of `ca.pem` recorded at the ceremony and compared on mokuzai before the flip | a wrong file on the far side fails the same way as a wrong CA, and the digest tells them apart before the window opens |

finding-001 section 6.2 ("`hub/tls-mint.sh` on kinu, once") is superseded by section 3 below; a one-line note in that finding says so. Sections 6.1, 6.3 through 6.6 of finding-001 (the bus-loss windows, the order, the backout tables) stand unchanged.

## 2. What was proven (rehearsal, 2026-09-16)

`ca-ceremony.sh` in rehearsal mode (`CEREMONY_REHEARSAL=1`: a pipe is allowed, copies may share a device, the passphrase comes from a descriptor, the root is named `REHEARSAL`, `--out` may not be under `~/.director`), against scratch directories, three times over:

- **`init`**: a P-384 root encrypted under the rehearsal passphrase, self-signed five years with `CA:TRUE, pathlen:1`; the hub CSR (from `hub-csr.sh`, SAN `global-hub, kinu, localhost, 192.168.100.110, 127.0.0.1`) signed 825 days with the SAN and key usages copied from the CSR; `openssl verify` passes; both copies written and read back by digest; `ca.pem`, `hub.pem`, `ceremony.log` in the served directory and no key beside them; the RAM disk wiped and ejected (`/Volumes` clean after every run).
- **`sign`** (a renewal): `RENEW=1 hub-csr.sh` reuses the server key; `sign` loads the root from the first copy, refuses if the two logs differ, signs, appends one line, writes log and serial back to both copies. Three log lines after one `init` and one `sign`, identical on both copies.
- **Wrong passphrase**: `bad decrypt`, `signing produced no certificate`, exit nonzero, no log line, nothing written to the copies or the served directory.
- **The chain on a real broker**: `TLS_DIR=<rehearsal tls> verify-global-tls.sh` uses the ceremony-issued `ca.pem`, `hub.pem`, `hub-key.pem` as the scratch hub's material instead of minting: 25 of 25, the 24 checks of finding-001 plus the chain check. The leaf links against the ceremony root, the shim's preflight passes through it, a rogue CA is still refused.

Two things the rehearsal had to find out:

- **OpenSSL 3.6 ignored `-passin` when the process inherited a non-tty stdin.** The decoder fell through to prompting on the console and failed with `unable to get passphrase`; the same command with stdin from `/dev/null` honoured `-passin`. The two key-reading calls now take `</dev/null`; the real ceremony's prompts come from `/dev/tty`, which is unaffected. Measured with `openssl ec -passin file:` in both shapes, three runs each.
- **A RAM disk without sudo on macOS**: `hdiutil attach -nomount ram://N` then `diskutil eraseVolume APFS <name> <dev>` mounts at `/Volumes/<name>`; `newfs_hfs` plus `hdiutil attach` reported no mountable file systems. `diskutil eject <dev>` from outside the mount removes it; ejecting from inside it fails, so the exit trap does `cd /` first.

## 3. The corrected cutover (amends finding-001 section 6.2; the rest of section 6 applies as written)

Nothing below has been applied. The operator gates the ceremony and the flip; skippy does the mokuzai half. The bus-loss window per host, the order, and the backout tables are finding-001 sections 6.1 and 6.3 to 6.6 and are not restated; what follows is what changes and what is added.

**Preconditions (replace finding-001 6.2)**

1. On kinu: `probe/nats-global-tier/hub/hub-csr.sh`. Writes `~/.director/nats-global/tls/hub-key.pem` (0600, stays) and `hub.csr`. Check the SAN it prints carries `IP Address:192.168.100.110`.
2. The operator, at a terminal, with the two labelled volumes mounted: `ca-ceremony.sh init --csr ~/.director/nats-global/tls/hub.csr --copy /Volumes/<one> --copy /Volumes/<two>` (CEREMONY.md). Passphrase typed twice. Eject and put the volumes away.
3. Check what the ceremony left on kinu, once, for the record:
   ```sh
   ls ~/.director/nats-global/tls          # ca.pem  ceremony.log  hub-key.pem  hub.csr  hub.pem   (no ca-key.pem)
   openssl verify -CAfile ~/.director/nats-global/tls/ca.pem ~/.director/nats-global/tls/hub.pem
   openssl x509 -in ~/.director/nats-global/tls/hub.pem -noout -issuer -enddate -ext subjectAltName
   shasum -a 256 ~/.director/nats-global/tls/ca.pem        # record this digest
   ```
   The issuer is `CN=director fleet root 2026`, not `TRIAL` and not `REHEARSAL`; the expiry is about 825 days out; the SAN carries the LAN IP.
4. Ship `ca.pem` to mokuzai (public material; any channel), to `~/.director/nats/hub-ca.pem`. Skippy runs `shasum -a 256 ~/.director/nats/hub-ca.pem` and the digest matches step 3. A mismatch stops here.
5. On mokuzai, the broker conf must already have a durable home. Measured 2026-09-16: the running broker is started with `-c <an ephemeral per-session directory>/nats-mokuzai.conf`, and `store_dir` and `logfile` point into the same directory, so there is no file for step 6 to back up and nothing for the 6.6 rollback to restore. Skippy relocates conf, store and log under `~/.director/nats/` and restarts the broker once, before the window, not inside it. The leaf seed is already durable at `~/.director/nats/leaf-mokuzai.nk` and still reaches the broker only as `$DIRECTOR_LEAF_NKEY`.
6. Backups: `cp ~/.director/nats-global/nats-server.conf ~/.director/nats-global/nats-server.conf.pre-tls` on kinu; the same for skippy's broker conf, which step 5 has made possible.
7. Agree the time and the 5-minute backout deadline (finding-001 6.2).

**Hub diff** (unchanged from finding-001 6.3; the file names are the ones the ceremony writes)

```diff
 # director global bus tier (R-86), interim placement on kinu (operator ruling 2026-09-14).
 # Second NATS on this host: the phase-0 local broker stays on 127.0.0.1:4222.
 # Client 4242 and leaf 7442 bind the LAN interface; monitoring stays loopback.
-# Transport TLS is deferred to the cloud phase; principal authorization (R-77) is not.
+# Server TLS on both listeners since <cutover date> (finding-001, root per finding-002); mutual TLS and part 2 stay ahead.
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

**mokuzai diff** (unchanged from finding-001 6.5; the `ca_file` is the ceremony root's `ca.pem`, digest-matched in precondition 4)

```diff
-    { urls: ["nats-leaf://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY }
+    { urls: ["tls://192.168.100.110:7442"], nkey: $DIRECTOR_LEAF_NKEY,
+      tls { ca_file: "/Users/skippy/.director/nats/hub-ca.pem" } }
```

**Order**: finding-001 6.3 and 6.5, leaf-first recommended. Step for step: skippy applies the remote diff and reloads (the link drops with `TLS Handshake Failure` once a second; his local bus continues); the operator applies the hub diff, `nats-server --config ... -t`, stops the hub, starts it; the link is back within a second or two.

**Verification** (finding-001 6.3 step 4, plus two lines that are new here)

```sh
curl -s 127.0.0.1:8242/varz | jq '{tls_required, leaf: .leaf.tls_required}'     # both true
curl -s 127.0.0.1:8242/leafz | jq '.leafs[].name'                                  # "mokuzai"
nats -s tls://127.0.0.1:4242 --tlsca ~/.director/nats-global/tls/ca.pem \
  --nkey ~/.director/nats-global/keys/admin.nk stream ls                          # the three streams, counts as before
nats -s tls://127.0.0.1:4242 --tlsca <any other CA, e.g. a tls-mint.sh trial> \
  --nkey ~/.director/nats-global/keys/admin.nk account info                       # refused: the hub serves the ceremony root, nothing else
grep -c ' sign ' ~/.director/nats-global/tls/ceremony.log                          # 1: the served hub.pem is the logged signature
```

From mokuzai: `HUB_CA=~/.director/nats/hub-ca.pem probe/nats-global-tier/verify-global-shim.sh`, 14 of 14.

**Rollback**: finding-001 6.4 (kinu) and 6.6 (mokuzai), unchanged. A backout restores the plaintext conf and restarts the hub; skippy restores the pre-TLS remote line and reloads. The ceremony's outputs stay in `tls/` for the next attempt; the root, on its two volumes, is not involved in a backout at all.

## 4. Out of scope, on purpose

Part 2 of the party's motion: per-daemon intermediates, the `marvel://` URI name grammar, `verify_and_map` on the hub's leaf listener, the dolt server certificate (`aae-orc-95qn5`, a later `sign`), the YubiKey question (open question 2: until answered, the root is a file with a ceremony). marvel's `bus.hub.ca_file` is marvel#278. None of it gates this cutover; all of it hangs off this root.

## Files

- `probe/nats-global-tier/hub/CEREMONY.md`: the runbook.
- `probe/nats-global-tier/hub/ca-ceremony.sh`: `init` and `sign`; rehearsal mode.
- `probe/nats-global-tier/hub/hub-csr.sh`: the server key and CSR on the hub host.
- `probe/nats-global-tier/hub/tls-mint.sh`: trial material only, 30 days, refuses `~/.director`.
- `probe/nats-global-tier/verify-global-tls.sh`: `TLS_DIR` runs the 24 checks on externally issued material, plus the chain check.
- `_kos/findings/finding-001-hub-tls-local-trial.md`: one-line supersession note at 6.2.
