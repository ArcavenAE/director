#!/usr/bin/env bash
# Prove `director-install director-mcp --release TAG|latest-alpha` installs a
# published shim with no Go toolchain, checks the bytes and then the origin
# before it switches anything, and leaves the installed link alone on every
# refusal. Network-free: the release is a directory served from a Python HTTP
# server bound to 127.0.0.1 on a random port, the verifier is a fake `gh` that
# records its arguments, and every path is a temp directory. The server is
# stopped on exit.
#
# Usage: scripts/director-install/verify-director-install-release.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALLER="${DIRECTOR_INSTALL:-$here/director-install}"
[[ -x "$INSTALLER" ]] || { echo "director-install not executable at $INSTALLER"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is needed for the fake release server"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
srvpid=""
cleanup() { [[ -n "$srvpid" ]] && kill "$srvpid" 2>/dev/null || true; rm -rf "$root"; }
trap cleanup EXIT
home="$root/home"
out="$root/out"
srv="$root/srv"
fake="$root/fake"
mkdir -p "$out" "$srv/dl" "$srv/api" "$fake"

# --- the fake release server --------------------------------------------------
cat > "$root/serve.py" <<'PY'
import http.server, socketserver, sys, os
os.chdir(sys.argv[1])
class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a): pass
with socketserver.TCPServer(("127.0.0.1", 0), H) as s:
    open(sys.argv[2], "w").write(str(s.server_address[1]))
    s.serve_forever()
PY
python3 "$root/serve.py" "$srv" "$root/port" >/dev/null 2>&1 &
srvpid=$!
for _ in $(seq 1 50); do [[ -s "$root/port" ]] && break; sleep 0.1; done
[[ -s "$root/port" ]] || { echo "the fake release server did not start"; exit 2; }
port="$(cat "$root/port")"
BASE="http://127.0.0.1:$port/dl"
API="http://127.0.0.1:$port/api/releases"

case "$(uname -s)" in Darwin) hos=darwin ;; Linux) hos=linux ;; *) echo "unsupported host"; exit 2 ;; esac
case "$(uname -m)" in arm64|aarch64) harch=arm64 ;; x86_64|amd64) harch=amd64 ;; *) echo "unsupported host"; exit 2 ;; esac

# A release dir for TAG whose binaries report REV. Each target gets a script
# that answers --version the way the real shim does.
mkrelease() {
  local tag="$1" rev="$2" d="$srv/dl/$1" t
  mkdir -p "$d"
  for t in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
    printf '#!/bin/sh\necho "director-mcp 0.1.0-alpha.test.%s rev %s channel alpha"\n' "${rev:0:7}" "$rev" > "$d/director-mcp-$t"
  done
  (cd "$d" && shasum -a 256 director-mcp-* | sed 's/ \*/  /' | LC_ALL=C sort -k2 > checksums.txt)
  echo '{"bundle":"fake"}' > "$d/attestations.sigstore.json"
}
A_REV="aaaaaaa0000000000000000000000000000000aa"; A_TAG="director-mcp-alpha-20261113-100000-aaaaaaa"
B_REV="bbbbbbb0000000000000000000000000000000bb"; B_TAG="director-mcp-alpha-20261113-110000-bbbbbbb"
mkrelease "$A_TAG" "$A_REV"
mkrelease "$B_TAG" "$B_REV"
printf '[{"tag_name":"%s","prerelease":true},\n{"tag_name":"%s","prerelease":true}]\n' "$B_TAG" "$A_TAG" > "$srv/api/releases"

# A fake gh: record the arguments, then answer by FAKE_GH_RC, and reject any
# bundle that names another workflow (the way a real verify would).
cat > "$fake/gh" <<'GH'
#!/bin/sh
echo "$*" >> "$FAKE_GH_LOG"
bundle=""
while [ $# -gt 0 ]; do [ "$1" = "--bundle" ] && bundle="$2"; shift; done
[ -n "$bundle" ] && grep -q OTHER-WORKFLOW "$bundle" 2>/dev/null && exit 1
exit "${FAKE_GH_RC:-0}"
GH
chmod +x "$fake/gh"

BASEPATH="/usr/bin:/bin"
ghlog="$root/gh.log"
# di PATHPREFIX args...: run the installer with a controlled environment.
di() {
  local pre="$1"; shift
  env -i HOME="$root/h" DIRECTOR_HOME="$home" PATH="${pre:+$pre:}$BASEPATH" LANG=en_US.UTF-8 \
      DIRECTOR_RELEASE_BASE="$BASE" DIRECTOR_RELEASES_API="$API" \
      DIRECTOR_MANIFESTS_DIR="$root/manifests" FAKE_GH_LOG="$ghlog" ${FAKE_GH_RC:+FAKE_GH_RC="$FAKE_GH_RC"} \
      "$INSTALLER" "$@" >"$out/stdout" 2>"$out/stderr"
}
mlink() { readlink "$home/bin/director-mcp" 2>/dev/null || echo none; }
want() { echo "$home/lib/director-mcp/$1/director-mcp"; }
mkdir -p "$root/h"

# Positive controls: this PATH has neither gh nor go, the fake one has gh only.
if PATH="$BASEPATH" command -v gh >/dev/null 2>&1 || PATH="$BASEPATH" command -v go >/dev/null 2>&1; then
  echo "control failed: $BASEPATH carries gh or go, so the no-toolchain runs prove nothing"; exit 2
fi
PATH="$fake:$BASEPATH" command -v gh >/dev/null 2>&1 || { echo "control failed: the fake gh is not found"; exit 2; }

# --- 1. install a release with a verifier and no Go ---------------------------
: > "$ghlog"
if di "$fake" director-mcp --release "$A_TAG"; then
  [[ "$(mlink)" == "$(want "$A_REV")" ]] \
    && ok "--release installs the tag and points the link at the commit the binary reports" \
    || bad "--release link" "$(mlink)"
  cmp -s "$srv/dl/$A_TAG/director-mcp-$hos-$harch" "$home/lib/director-mcp/$A_REV/director-mcp" \
    && ok "the installed bytes are the downloaded binary for this host's os and arch" \
    || bad "installed bytes differ from the host's binary"
  stamp="$home/lib/director-mcp/$A_REV/STAMP"
  miss=""
  grep -qx "sha=$A_REV" "$stamp" 2>/dev/null          || miss+=" sha"
  grep -qx "release=$A_TAG" "$stamp" 2>/dev/null      || miss+=" release"
  grep -qx "origin=verified" "$stamp" 2>/dev/null     || miss+=" origin"
  grep -q '^version=0.1.0-alpha' "$stamp" 2>/dev/null || miss+=" version"
  grep -q '^source=http://127.0.0.1' "$stamp" 2>/dev/null || miss+=" source"
  [[ -z "$miss" ]] && ok "the STAMP records sha, version, release tag, origin and source" \
                   || bad "STAMP" "missing:$miss -- $(cat "$stamp" 2>/dev/null)"
  line="$(head -1 "$ghlog" 2>/dev/null || true)"
  [[ "$line" == "attestation verify "* && "$line" == *"--repo ArcavenAE/director"* \
     && "$line" == *"--signer-workflow ArcavenAE/director/.github/workflows/release-director-mcp.yml"* \
     && "$line" == *"--bundle "* ]] \
    && ok "the verify names this repository, this release workflow and the downloaded bundle" \
    || bad "gh arguments" "$line"
else
  bad "--release $A_TAG with a verifier and no go" "$(cat "$out/stderr")"
fi

# --- 2. a download that does not match checksums.txt changes nothing ----------
TAMP="director-mcp-alpha-20261113-120000-ccccccc"; T_REV="ccccccc0000000000000000000000000000000cc"
mkrelease "$TAMP" "$T_REV"
echo "tampered" >> "$srv/dl/$TAMP/director-mcp-$hos-$harch"
: > "$ghlog"
before="$(mlink)"
if di "$fake" director-mcp --release "$TAMP"; then
  bad "a binary that fails its checksum was installed"
else
  [[ "$(mlink)" == "$before" && ! -e "$home/lib/director-mcp/$T_REV" ]] \
    && ok "a checksum mismatch refuses and leaves the link and the lib alone" \
    || bad "checksum mismatch: state changed" "$(mlink)"
  grep -qi checksum "$out/stderr" && ok "the checksum refusal says so" || bad "checksum refusal text" "$(cat "$out/stderr")"
  [[ ! -s "$ghlog" ]] && ok "the origin check does not run on bytes that fail the checksum" || bad "gh ran after a checksum failure"
fi

# --- 3. no verifier on the host: refuse, name the ways on, change nothing ------
D_TAG="director-mcp-alpha-20261113-130000-ddddddd"; D_REV="ddddddd0000000000000000000000000000000dd"
mkrelease "$D_TAG" "$D_REV"
before="$(mlink)"
if di "" director-mcp --release "$D_TAG"; then
  bad "an install with no verifier on PATH succeeded"
else
  [[ "$(mlink)" == "$before" && ! -e "$home/lib/director-mcp/$D_REV" ]] \
    && ok "with no gh the install refuses and leaves the link and the lib alone" \
    || bad "no verifier: state changed" "$(mlink)"
  grep -q 'gh' "$out/stderr" && grep -q -- '--ref' "$out/stderr" \
    && ok "the refusal names gh, and building from source with --ref, as the ways on" \
    || bad "no-verifier refusal text" "$(cat "$out/stderr")"
  # Only a verifier shown to accept this bundle may be named (design, section 3).
  ! grep -qiE 'cosign|sigstore|checksum-only' "$out/stderr" \
    && ok "the refusal names no verifier or flag the installer does not support" \
    || bad "the refusal names an unsupported way on" "$(cat "$out/stderr")"
fi

# --- 4. the verifier rejects the bundle ----------------------------------------
before="$(mlink)"
if FAKE_GH_RC=1 di "$fake" director-mcp --release "$D_TAG"; then
  bad "an install the verifier rejected succeeded"
else
  [[ "$(mlink)" == "$before" && ! -e "$home/lib/director-mcp/$D_REV" ]] \
    && ok "a verifier refusal leaves the link and the lib alone" \
    || bad "verifier refusal: state changed" "$(mlink)"
  grep -qi 'attestation\|origin' "$out/stderr" && ok "the refusal names the origin check" || bad "verifier refusal text" "$(cat "$out/stderr")"
fi

# refuses_for PATTERN: the last refusal was for this reason, and not for an argument the installer does not know.
refuses_for() { grep -qiE "$1" "$out/stderr" && ! grep -q 'unknown argument' "$out/stderr"; }

# --- 5. a bundle from another workflow is refused -------------------------------
O_TAG="director-mcp-alpha-20261113-140000-eeeeeee"; O_REV="eeeeeee0000000000000000000000000000000ee"
mkrelease "$O_TAG" "$O_REV"
echo '{"bundle":"OTHER-WORKFLOW"}' > "$srv/dl/$O_TAG/attestations.sigstore.json"
before="$(mlink)"
if di "$fake" director-mcp --release "$O_TAG"; then
  bad "a bundle from another workflow was accepted"
else
  [[ "$(mlink)" == "$before" && ! -e "$home/lib/director-mcp/$O_REV" ]] \
    && ok "a bundle the pinned verify rejects leaves the link and the lib alone" \
    || bad "other-workflow bundle: state changed"
  refuses_for 'attestation|origin' && ok "the other-workflow refusal names the origin check" || bad "other-workflow refusal text" "$(cat "$out/stderr")"
fi

# --- 6. the binary must report the commit its tag names ------------------------
W_TAG="director-mcp-alpha-20261113-150000-fffffff"
mkrelease "$W_TAG" "9999999000000000000000000000000000000099"
before="$(mlink)"
if di "$fake" director-mcp --release "$W_TAG"; then
  bad "a binary that reports another commit than its tag was installed"
else
  [[ "$(mlink)" == "$before" && ! -e "$home/lib/director-mcp/9999999000000000000000000000000000000099" ]] \
    && ok "a first run that reports another commit than the tag refuses and changes nothing" \
    || bad "wrong-rev: state changed"
  refuses_for 'rev|commit' && ok "the wrong-commit refusal names the commit" || bad "wrong-rev refusal text" "$(cat "$out/stderr")"
fi

# --- 7. a missing asset refuses ------------------------------------------------
M_TAG="director-mcp-alpha-20261113-160000-1111111"
mkrelease "$M_TAG" "1111111000000000000000000000000000000011"
rm "$srv/dl/$M_TAG/attestations.sigstore.json"
before="$(mlink)"
if di "$fake" director-mcp --release "$M_TAG"; then
  bad "a release with no bundle was installed"
else
  [[ "$(mlink)" == "$before" ]] && ok "a release with a missing asset refuses and leaves the link alone" || bad "missing asset: link moved"
  refuses_for 'download|fetch|attestations' && ok "the missing-asset refusal names the download" || bad "missing-asset refusal text" "$(cat "$out/stderr")"
fi

# --- 8. latest-alpha resolves once and records the tag --------------------------
if di "$fake" director-mcp --release latest-alpha; then
  [[ "$(mlink)" == "$(want "$B_REV")" ]] \
    && ok "latest-alpha resolves to the newest alpha in the listing" \
    || bad "latest-alpha link" "$(mlink)"
  grep -qx "release=$B_TAG" "$home/lib/director-mcp/$B_REV/STAMP" \
    && ok "latest-alpha is pinned: the STAMP names the tag it resolved to" \
    || bad "latest-alpha STAMP" "$(cat "$home/lib/director-mcp/$B_REV/STAMP")"
else
  bad "--release latest-alpha" "$(cat "$out/stderr")"
fi

# --- 9. --rollback returns to the first release ---------------------------------
if di "$fake" director-mcp --rollback; then
  [[ "$(mlink)" == "$(want "$A_REV")" ]] && ok "--rollback returns to the previous release" || bad "--rollback link" "$(mlink)"
else
  bad "--rollback" "$(cat "$out/stderr")"
fi

# --- 10. --status names the release and the origin result -----------------------
di "$fake" --status || true
grep -q "director-mcp: ${A_REV:0:12}" "$out/stdout" && grep -q "$A_TAG" "$out/stdout" && grep -q 'origin verified' "$out/stdout" \
  && ok "--status shows the release tag and that its origin was verified" \
  || bad "--status" "$(cat "$out/stdout")"

# --- 11. argument checks ---------------------------------------------------------
if di "$fake" director-mcp --release "$A_TAG" --ref origin/main; then
  bad "--release with --ref was accepted"
else
  refuses_for '--release.*--ref|--ref.*--release|not both' && ok "--release and --ref together are refused" || bad "--release with --ref refusal text" "$(cat "$out/stderr")"
fi
if di "$fake" director-mcp --release "../etc/passwd"; then
  bad "a tag with a path in it was accepted"
else
  refuses_for 'tag' && ok "a release tag with a path in it is refused before any download" || bad "bad tag text" "$(cat "$out/stderr")"
fi
if di "$fake" director-mcp --release; then
  bad "--release with no value was accepted"
else
  refuses_for 'needs a value' && ok "--release with no value is refused" || bad "--release no-value refusal text" "$(cat "$out/stderr")"
fi

echo
echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
