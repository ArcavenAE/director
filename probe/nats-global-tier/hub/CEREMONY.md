# The fleet root ceremony

The certificate authority every hub client and leaf trusts is a key. Where
that key lives, who can use it, and what it has signed decide how much the
TLS on the hub is worth. This runbook is the part-1 shape ratified 2026-09-16
(the marvel CA party, finding-002): one fleet root, five years, generated off
the live filesystem, encrypted under a passphrase no agent holds, stored as
two offline copies, with one log line per signature. It replaces the trial
root `tls-mint.sh` used to write into the live directory with a ten-year life.

The general hierarchy (per-daemon intermediates, the URI name grammar) is
part 2 and is not this document. This root is made so that part 2 can hang
off it: `pathlen:1` lets it sign one level of intermediates without a new
root.

## Roles and rules

- **The person** runs the ceremony at a terminal. `ca-ceremony.sh` refuses a
  pipe. The passphrase is typed at openssl's prompt, twice at creation and
  once per later signing. It is written nowhere a process can read: not in a
  file, not in an environment variable, not in a password manager an agent
  session can query, not in a chat.
- **An agent** may prepare the CSR (`hub-csr.sh`), carry `ca.pem` and
  `hub.pem` to where they are served, run the rehearsal, and run the checks
  in this document. An agent never holds the root key, the passphrase, or a
  mounted copy.
- **The root key** exists in three places and no others: the RAM disk while a
  ceremony runs (gone at eject), and the two offline copies, encrypted
  (AES-256, PKCS#8). It is never on the boot volume, never in a repo, never
  in a backup that is not one of the two copies.
- **Every signature is logged.** `ceremony.log` on both copies gets one line
  per root creation and one per certificate signed: time, subject, SAN,
  serial, expiry, certificate and CSR digests, who, and the ceremony id. A
  copy of the log (no secrets in it) sits beside the served certificates.
- **The two copies stay in step.** `sign` reads the root from the first copy,
  refuses if the two logs differ, and writes the log and serial file back to
  both.

## What you need

- A Mac with `openssl` 3.x (the scripts use `-copy_extensions` and
  `-addext`) and `diskutil` (the RAM disk).
- Two removable volumes, each dedicated to this, each on its own device.
  Label them. They are the offline copies; between ceremonies they live
  somewhere a laptop theft does not reach both.
- The hub host's CSR: `hub-csr.sh` on kinu writes
  `~/.director/nats-global/tls/hub.csr` and keeps the server key there. Only
  the CSR travels.
- Ten minutes. Nothing here is slow; the time is the passphrase and the
  labels.

## First ceremony (`init`)

On the ceremony machine (kinu is fine; the root never touches its
filesystem), with both volumes mounted:

```sh
probe/nats-global-tier/hub/ca-ceremony.sh init \
  --csr ~/.director/nats-global/tls/hub.csr \
  --copy /Volumes/<copy one> --copy /Volumes/<copy two>
```

What happens, in order:

1. Checks: a terminal, two writable copy directories on two different
   devices, neither on the boot volume, neither already holding a root.
2. A RAM disk is created and mounted at `/Volumes/ceremony-<utc time>`; all
   work happens there.
3. `openssl genpkey` makes a P-384 key encrypted with the passphrase you
   type. `openssl req -x509` self-signs the root: five years (1826 days),
   `CA:TRUE, pathlen:1`, `keyCertSign, cRLSign`.
4. The hub CSR is signed: 825 days, `CA:FALSE`, the SAN and key usages copied
   from the CSR (`hub-csr.sh` puts the hub name, the host name, `localhost`,
   the LAN IP, and `127.0.0.1` there). `openssl verify` checks the chain
   before anything is written out.
5. `root-key.pem`, `ca.pem`, `ca.srl`, and `ceremony.log` are copied to
   `<copy>/director-root/` on both volumes and read back by digest.
6. `ca.pem`, `hub.pem`, and a copy of `ceremony.log` are written to
   `--out` (default `~/.director/nats-global/tls/`). The root key is not.
7. The RAM disk is wiped and ejected. That also happens on any failure, so a
   ceremony that stops halfway leaves nothing behind except what step 5 had
   already written; start over.

Then, still at the terminal: eject both volumes, label the copies with the
ceremony id printed in the last log line, and put them away. Check
`openssl x509 -in ~/.director/nats-global/tls/ca.pem -noout -subject -enddate`
once for the record.

## Later ceremonies (`sign`)

A renewal of the hub certificate (825 days from issue), a second relying
party (the dolt server, `aae-orc-95qn5`), or a part-2 intermediate. Bring
both copies. Make the CSR on the host whose key it is (`RENEW=1 hub-csr.sh`
for the hub; it reuses the key), then:

```sh
probe/nats-global-tier/hub/ca-ceremony.sh sign \
  --csr <the csr> --copy /Volumes/<copy one> --copy /Volumes/<copy two> \
  --out <where the certificate is served from>
```

`sign` loads the root from the first copy onto the RAM disk, refuses if the
two copies' logs differ, signs, appends the log line, writes log and serial
back to both copies, writes the certificate and `ca.pem` to `--out`, and
ejects. One passphrase prompt.

## The log

One line per event, append-only, identical on both copies, copied without
secrets to the served directory:

```
2026-09-16T06:51:09Z root created cn="director fleet root 2026" serial=573E... notafter="Sep 16 06:51:09 2031 GMT" sha256=693c... by=<user>@<host> ceremony=20260916T065109Z
2026-09-16T06:51:10Z sign subject="CN=global-hub" san=DNS:global-hub,DNS:kinu,DNS:localhost,IPAddress:192.168.100.110,IPAddress:127.0.0.1 serial=79F8... notafter="Dec 19 06:51:10 2028 GMT" sha256=12e4... csr_sha256=e7d7... by=<user>@<host> ceremony=20260916T065109Z
```

A certificate the fleet trusts that has no line here was not made by this
root's ceremony, whatever its issuer field says. That is what the log is for.

## Rehearsal

Run before the first real ceremony, and again whenever the script changes:

```sh
R=$(mktemp -d); mkdir -p "$R/copy1" "$R/copy2" "$R/home"
DIRECTOR_GLOBAL_HOME="$R/home" probe/nats-global-tier/hub/hub-csr.sh
printf 'rehearsal-only\n' > "$R/p"
CEREMONY_REHEARSAL=1 CEREMONY_PASS_FD=3 DIRECTOR_GLOBAL_HOME="$R/home" \
  probe/nats-global-tier/hub/ca-ceremony.sh init \
  --csr "$R/home/tls/hub.csr" --copy "$R/copy1" --copy "$R/copy2" 3<"$R/p"
TLS_DIR="$R/home/tls" probe/nats-global-tier/verify-global-tls.sh
```

Rehearsal mode allows a pipe, lets the copies share a device, reads the
passphrase from a descriptor into a file on the RAM disk, names the root
`REHEARSAL`, and refuses to write under `~/.director`. The last line stands
up a scratch hub and leaf on the rehearsal chain (25 checks). Everything
under `$R` is throwaway; delete it.

## What can go wrong

| Symptom | Meaning | Do |
|---|---|---|
| `the ceremony runs at a terminal` | stdin or stdout is not a tty | run it from a terminal, not through an agent or a pipe |
| `the two copies must be on different volumes` / `may not live on the boot volume` | a copy path is on the wrong device | mount the second volume; do not point a copy at the Mac's disk |
| `already holds a root; use sign` | `init` on a volume that has a root | you wanted `sign`, or you are making a second root on purpose: move the old `director-root/` aside by hand and say so in a finding |
| `the two copies' ceremony.log differ` | a signing was logged on one copy only, or a copy was edited | diff the two logs, reconcile by hand, record what happened |
| `bad decrypt` then `signing produced no certificate` | wrong passphrase | run again; nothing was written to the copies or `--out` |
| a RAM disk named `ceremony-*` is still in `/Volumes` after a failure | the eject in the exit trap failed (rare: a shell still inside it) | `cd /; diskutil eject <the device>`; the key on it was never copied out, so there is nothing to recover |

## Relation to the cutover

finding-001 section 6 is the cutover; finding-002 amends its preconditions:
the CA and hub certificate come from this ceremony, `ca.pem` shipped to
mokuzai is this root's, and the pre-cutover check compares its digest on both
hosts. `tls-mint.sh` now mints only 30-day trial material and refuses the
live directory.
