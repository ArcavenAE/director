#!/usr/bin/env bash
# The fleet root ceremony (CEREMONY.md). A human runs this at a terminal; it
# refuses a pipe. The root key is born on a RAM disk, never on the live
# filesystem; it leaves the RAM disk only as an AES-256 encrypted file on two
# offline copies; the passphrase is typed at openssl's prompt and held by no
# agent, no file, no environment variable; every signature appends one line
# to ceremony.log on both copies. The root lives five years, the hub server
# cert 825 days, and the root can sign one level of intermediates (pathlen 1)
# for the part-2 hierarchy without a new root. The passphrase prompts come
# from the console (/dev/tty); the key-reading openssl calls take stdin from
# /dev/null on purpose, because with an inherited non-tty stdin OpenSSL 3.6
# ignored -passin in rehearsal (measured 2026-09-16).
#
#   ca-ceremony.sh init --csr <hub.csr> --copy <dir1> --copy <dir2> [--out <tls dir>]
#       first ceremony: create the root, sign the hub CSR, write the copies
#   ca-ceremony.sh sign --csr <hub.csr> --copy <dir1> --copy <dir2> [--out <tls dir>]
#       later ceremony (renewal, a second relying party): load the root from
#       the copies, sign, log to both, write nothing new to the live host but
#       the certificate
#
# --out defaults to $DIRECTOR_GLOBAL_HOME/tls and receives ca.pem, hub.pem,
# and a copy of ceremony.log. The root key is never written there.
#
# Rehearsal (CEREMONY_REHEARSAL=1): allowed without a terminal, copies may
# share a device, the passphrase is read once from CEREMONY_PASS_FD into a
# file on the RAM disk, the root is named REHEARSAL, and --out may not be
# under ~/.director. That is how the script is tested; it is not how the
# fleet root is made.
set -euo pipefail

REHEARSAL="${CEREMONY_REHEARSAL:-0}"
cmd="${1:-}"; shift || true
[[ "$cmd" == init || "$cmd" == sign ]] || { sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
CSR=""; OUT="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}/tls"; COPIES=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --csr) CSR="$2"; shift 2 ;;
    --copy) COPIES+=("$2"); shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    *) echo "unknown argument $1" >&2; exit 2 ;;
  esac
done
[[ -r "$CSR" ]] || { echo "--csr <hub.csr> is required and must be readable" >&2; exit 2; }
[[ ${#COPIES[@]} -eq 2 ]] || { echo "exactly two --copy <dir> are required (two offline copies)" >&2; exit 2; }
for c in "${COPIES[@]}"; do [[ -d "$c" && -w "$c" ]] || { echo "copy dir not writable: $c" >&2; exit 2; }; done
command -v openssl >/dev/null || { echo "openssl not on PATH" >&2; exit 2; }
command -v diskutil >/dev/null || { echo "this ceremony script is written for macOS (diskutil RAM disk)" >&2; exit 2; }

dev_of() { df -P "$1" | awk 'NR==2{print $1}'; }
if [[ "$REHEARSAL" == 1 ]]; then
  case "$OUT" in "$HOME/.director"*) echo "a rehearsal may not write under ~/.director" >&2; exit 2 ;; esac
  [[ -n "${CEREMONY_PASS_FD:-}" ]] || { echo "rehearsal needs CEREMONY_PASS_FD" >&2; exit 2; }
else
  [[ -t 0 && -t 1 ]] || { echo "the ceremony runs at a terminal, by a person; it refuses a pipe (CEREMONY.md)" >&2; exit 2; }
  [[ "$(dev_of "${COPIES[0]}")" != "$(dev_of "${COPIES[1]}")" ]] || { echo "the two copies must be on different volumes" >&2; exit 2; }
  for c in "${COPIES[@]}"; do
    [[ "$(dev_of "$c")" != "$(dev_of "$HOME")" ]] || { echo "a copy may not live on the boot volume: $c" >&2; exit 2; }
  done
fi

NONCE="$(date -u +%Y%m%dT%H%M%SZ)"
BY="$(id -un)@$(hostname -s)"
ram=""; mnt=""
cleanup() {
  cd / || true
  if [[ -n "$mnt" && -d "$mnt" ]]; then rm -rf "${mnt:?}"/* 2>/dev/null || true; fi
  if [[ -n "$ram" ]]; then diskutil eject "$ram" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
ram="$(hdiutil attach -nomount ram://32768 | tr -d ' \t')"
diskutil eraseVolume APFS "ceremony-$NONCE" "$ram" >/dev/null
mnt="/Volumes/ceremony-$NONCE"
[[ -d "$mnt" ]] || { echo "RAM disk did not mount at $mnt" >&2; exit 1; }
chmod 700 "$mnt"
cd "$mnt"

PASS=()
if [[ "$REHEARSAL" == 1 ]]; then
  cat <&"$CEREMONY_PASS_FD" > pass.txt; chmod 600 pass.txt
  [[ -s pass.txt ]] || { echo "rehearsal passphrase is empty" >&2; exit 2; }
  PASS=(-passin file:pass.txt); GENPASS=(-pass file:pass.txt)
else
  GENPASS=()
fi
ROOT_CN="director fleet root $(date -u +%Y)"
[[ "$REHEARSAL" == 1 ]] && ROOT_CN="REHEARSAL $ROOT_CN $NONCE"

log() { printf '%s %s by=%s ceremony=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$BY" "$NONCE" >> ceremony.log; }
sha() { shasum -a 256 "$1" | awk '{print $1}'; }

case "$cmd" in
  init)
    for c in "${COPIES[@]}"; do
      [[ -e "$c/director-root/root-key.pem" ]] && { echo "$c already holds a root; use sign, or move it aside by hand" >&2; exit 1; }
    done
    echo "== creating the root key on $mnt (you will be asked for the passphrase; choose it now, write it nowhere an agent can read)"
    openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-384 -aes-256-cbc "${GENPASS[@]}" -out root-key.pem
    chmod 600 root-key.pem
    echo "== self-signing the root (five years, pathlen 1)"
    openssl req -x509 -key root-key.pem "${PASS[@]}" -days 1826 -subj "/CN=$ROOT_CN" \
      -addext "basicConstraints=critical,CA:TRUE,pathlen:1" \
      -addext "keyUsage=critical,keyCertSign,cRLSign" \
      -addext "subjectKeyIdentifier=hash" -out ca.pem </dev/null
    : > ceremony.log
    log "root created cn=\"$ROOT_CN\" serial=$(openssl x509 -in ca.pem -noout -serial | cut -d= -f2) notafter=\"$(openssl x509 -in ca.pem -noout -enddate | cut -d= -f2)\" sha256=$(sha ca.pem)"
    ;;
  sign)
    src="${COPIES[0]}/director-root"
    for f in root-key.pem ca.pem ca.srl ceremony.log; do
      [[ -r "$src/$f" ]] || { echo "missing $src/$f; the first copy must hold the root" >&2; exit 1; }
      cp "$src/$f" .
    done
    [[ "$(sha ceremony.log)" == "$(sha "${COPIES[1]}/director-root/ceremony.log")" ]] \
      || { echo "the two copies' ceremony.log differ; reconcile them by hand before signing" >&2; exit 1; }
    chmod 600 root-key.pem
    ;;
esac

echo "== signing $CSR"
cp "$CSR" hub.csr
printf 'basicConstraints=CA:FALSE\nauthorityKeyIdentifier=keyid\nsubjectKeyIdentifier=hash\n' > sign.ext
openssl x509 -req -in hub.csr -CA ca.pem -CAkey root-key.pem "${PASS[@]}" -CAserial ca.srl -CAcreateserial \
  -days 825 -copy_extensions copy -extfile sign.ext -out hub.pem </dev/null 2>&1 | grep -v -E '^(Certificate request self-signature ok|subject=)' || true
[[ -s hub.pem ]] || { echo "signing produced no certificate" >&2; exit 1; }
openssl verify -CAfile ca.pem hub.pem >/dev/null
san="$(openssl x509 -in hub.pem -noout -ext subjectAltName | tail -n +2 | tr -d ' ')"
log "sign subject=\"$(openssl x509 -in hub.pem -noout -subject | cut -d= -f2-)\" san=$san serial=$(openssl x509 -in hub.pem -noout -serial | cut -d= -f2) notafter=\"$(openssl x509 -in hub.pem -noout -enddate | cut -d= -f2)\" sha256=$(sha hub.pem) csr_sha256=$(sha hub.csr)"

echo "== writing the two offline copies"
for c in "${COPIES[@]}"; do
  mkdir -p "$c/director-root"; chmod 700 "$c/director-root"
  for f in root-key.pem ca.pem ca.srl ceremony.log; do
    cp "$f" "$c/director-root/$f"
    [[ "$(sha "$f")" == "$(sha "$c/director-root/$f")" ]] || { echo "copy verify failed: $c/director-root/$f" >&2; exit 1; }
  done
  chmod 600 "$c/director-root/root-key.pem"
  sync
  echo "   $c/director-root: root-key.pem (encrypted) ca.pem ca.srl ceremony.log, verified"
done

echo "== writing the live host outputs to $OUT (no key)"
mkdir -p "$OUT"; chmod 700 "$OUT"
cp ca.pem "$OUT/ca.pem"; cp hub.pem "$OUT/hub.pem"; cp ceremony.log "$OUT/ceremony.log"
echo "   $OUT: ca.pem hub.pem ceremony.log"
openssl x509 -in "$OUT/hub.pem" -noout -subject -issuer -ext subjectAltName -enddate
echo "== last log line:"; tail -1 ceremony.log
echo "== ejecting the RAM disk; the root key now exists only on the two copies"
