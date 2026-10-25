#!/usr/bin/env bash
# Prove the diag runner does what its design promises, with no live fleet: the
# pair rule, INSTRUMENT distinct from FAIL, the expectation shown beside the
# observation, gate 0 first with every later case INSTRUMENT when it fails, the
# interventional case never run, evidence kept byte for byte, a clean
# environment for each case, and a run that always exits 0. It also proves the
# scratch-daemon helper keeps to its root and stops only the pid it started.
#
# Every binary the helper would run (marvel, simulator, gh) is a stub made
# here, every path is a temp directory, and no daemon or port of the real
# fleet is touched. The one listener it opens binds 127.0.0.1 on a port the
# kernel picks.
#
# Usage: scripts/diag/verify-diag.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIAG="$here/diag"
[[ -x "$DIAG" ]] || { echo "diag not executable at $DIAG"; exit 2; }
command -v jq >/dev/null || { echo "jq is required"; exit 2; }
command -v python3 >/dev/null || { echo "python3 is required (the stub daemon binds a unix socket)"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
has() { grep -q -- "$2" <<<"$1"; }              # has "$text" "fixed-ish pattern"
check() { # check "name" cmd...   passes when the command succeeds
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$name"; else bad "$name"; fi
}

root="$(mktemp -d "${TMPDIR:-/private/tmp}/dgv.XXXXXX")"
trap 'rm -rf "$root"' EXIT
cd "$root"
repo="$(cd "$here/../.." && pwd)"
repo_status_before="$(git -C "$repo" status --porcelain 2>/dev/null || true)"

# --- fixtures ------------------------------------------------------------------
mkcheck() { # mkcheck DIR GATE ID MODE "case lines..." body   -> DIR/GATE/ID.sh
  local dir="$1" gate="$2" id="$3" mode="$4" cases="$5" body="$6"
  mkdir -p "$dir/$gate"
  {
    echo "# diag-id: $id"; echo "# diag-gate: $gate"; echo "# diag-subject: test"; echo "# diag-mode: $mode"
    while IFS= read -r c; do [[ -n "$c" ]] && echo "# diag-case: $c"; done <<<"$cases"
    echo 'cs="$1"'; echo "$body"
  } > "$dir/$gate/$id.sh"
}

# A gate 0 that passes, and one that fails, as a stand-in for the real one.
g0ok="$root/g0ok.sh"; g0bad="$root/g0bad.sh"
cat > "$g0ok" <<'EOF'
# diag-id: G0
# diag-gate: 0
# diag-case: control expect=pass
echo "control ok"; exit 0
EOF
cat > "$g0bad" <<'EOF'
# diag-id: G0
# diag-gate: 0
# diag-case: control expect=pass
echo "control broken"; exit 1
EOF

run() { # run CHECKSDIR [diag args...]; sets $out, $rc, $rdir
  local checks="$1"; shift
  rc=0
  out="$(DIAG_GATE0="${G0:-$g0ok}" "$DIAG" run --checks "$checks" --report-root "$root/reports" "$@" 2>&1)" || rc=$?
  rdir="$(find "$root/reports" -name report.jsonl -print0 | xargs -0 ls -t | head -n 1 | xargs dirname)"
}
result_of() { jq -r --arg g "$1" --arg i "$2" --arg c "$3" 'select(.gate==$g and .id==$i and .case==$c) | .result' "$rdir/report.jsonl"; }
obs_of()    { jq -r --arg g "$1" --arg i "$2" --arg c "$3" 'select(.gate==$g and .id==$i and .case==$c) | .observed' "$rdir/report.jsonl"; }

# --- 1. the command line ---------------------------------------------------------
rc=0; "$DIAG" --help >/dev/null 2>&1 || rc=$?
[[ $rc -eq 0 ]] && ok "--help exits 0" || bad "--help exits 0" "rc=$rc"
for args in "" "frob" "run --gate Z" "run --nope" "run --gate"; do
  rc=0; # shellcheck disable=SC2086
  "$DIAG" $args >/dev/null 2>&1 || rc=$?
  [[ $rc -eq 2 ]] && ok "bad command line exits 2: '$args'" || bad "bad command line exits 2: '$args'" "rc=$rc"
done
rm -rf "$root/reports"

# --- 2. exit 0 whatever the cases found; the report shape -------------------------
c1="$root/c1"
mkcheck "$c1" A A1 scratch $'positive\nnegative' 'if [[ "$cs" == positive ]]; then echo p; exit 0; else echo n; exit 1; fi'
run "$c1" --gate A
[[ $rc -eq 0 ]] && ok "a run whose negative failed still exits 0" || bad "run exits 0 on a failing case" "rc=$rc"
want='run host gate id case expected observed result evidence subject_sha ts'
got="$(jq -r 'keys_unsorted|join(" ")' "$rdir/report.jsonl" | head -n 1)"
if [[ "$(tr ' ' '\n' <<<"$want" | sort | tr '\n' ' ')" == "$(tr ' ' '\n' <<<"$got" | sort | tr '\n' ' ')" ]]; then ok "report line has exactly the registered fields"; else bad "report fields" "$got"; fi
[[ "$(wc -l < "$rdir/report.jsonl" | tr -d ' ')" == 3 ]] && ok "one JSON line per case (gate 0 plus two)" || bad "line count" "$(wc -l < "$rdir/report.jsonl")"
[[ "$(result_of A A1 positive)" == PASS && "$(result_of A A1 negative)" == FAIL ]] && ok "pass and fail are reported as such" || bad "pass and fail"
has "$out" "gate A: not gate-ready: A1.negative" && ok "a failing case keeps the gate not-ready and names it" || bad "gate A line" "$out"
check "the table is also written to report.txt" test -s "$rdir/report.txt"

# --- 3. the pair rule ----------------------------------------------------------------
c2="$root/c2"
mkcheck "$c2" A A2 scratch $'positive\nnegative' 'if [[ "$cs" == positive ]]; then echo "positive broke"; exit 1; else echo "refused"; exit 0; fi'
run "$c2" --gate A
[[ "$(result_of A A2 negative)" == INSTRUMENT ]] && ok "a negative that passes beside a failing positive is INSTRUMENT" || bad "pair rule" "$(result_of A A2 negative)"
[[ "$(result_of A A2 positive)" == FAIL ]] && ok "the failing positive stays FAIL" || bad "positive stays FAIL"
check "the pair-rule note is kept in the evidence" grep -q 'refusal proves nothing' "$rdir/evidence/A.A2.negative.txt"
c2b="$root/c2b"
mkcheck "$c2b" A A2b scratch $'positive\nnegative' 'if [[ "$cs" == positive ]]; then exit 2; else exit 0; fi'
run "$c2b" --gate A
[[ "$(result_of A A2b negative)" == INSTRUMENT ]] && ok "a negative beside a skipped positive is INSTRUMENT too" || bad "pair rule, skipped positive"

# --- 4. both pass: gate-ready; exit codes map -------------------------------------
c3="$root/c3"
mkcheck "$c3" A A3 scratch $'positive\nnegative' 'exit 0'
run "$c3" --gate A
has "$out" "gate A: gate-ready" && ok "every case PASS reads gate-ready" || bad "gate-ready" "$out"
c4="$root/c4"
mkcheck "$c4" B B1 scratch $'s\ni\nodd\nf' 'case "$cs" in s) exit 2;; i) exit 3;; odd) exit 7;; f) exit 1;; esac'
run "$c4" --gate B
[[ "$(obs_of B B1 s)" == skip && "$(obs_of B B1 i)" == instrument && "$(obs_of B B1 odd)" == instrument && "$(obs_of B B1 f)" == fail ]] \
  && ok "exit codes 2, 3, an unlisted code and 1 map to skip, instrument, instrument and fail" || bad "exit code map"
check "an unlisted exit code says so in the evidence" grep -q 'not a case code' "$rdir/evidence/B.B1.odd.txt"

# --- 5. the expectation is registered, shown, and accepted by name ---------------
c5="$root/c5"
mkcheck "$c5" A A3b scratch $'positive\nnegative expect=fail' 'if [[ "$cs" == positive ]]; then exit 0; else exit 1; fi'
run "$c5" --gate A
line="$(grep -E '^A +A3b +negative' <<<"$out" || true)"
if [[ -n "$line" ]] && ! has "$line" "not as registered"; then ok "an expected FAIL is not marked as a surprise"; else bad "expected FAIL marker" "$line"; fi
has "$out" "gate A: not gate-ready: A3b.negative" && ok "a pre-registered FAIL still blocks gate-ready until accepted" || bad "known fail blocks" "$out"
run "$c5" --gate A --accept A3b.negative
has "$out" "gate A: gate-ready" && ok "naming the pre-registered FAIL with --accept reads gate-ready" || bad "accept" "$out"
c5b="$root/c5b"
mkcheck "$c5b" A A9 scratch $'positive\nnegative' 'if [[ "$cs" == positive ]]; then exit 0; else exit 1; fi'
run "$c5b" --gate A --accept A9.negative
has "$out" "not gate-ready: A9.negative" && ok "--accept does not cover a FAIL nobody registered" || bad "accept needs expect=fail" "$out"
has "$out" "not as registered" && ok "an unregistered FAIL is marked not as registered" || bad "surprise marker" "$out"

# --- 6. gate 0 first; a broken control makes every later case INSTRUMENT ---------
c6="$root/c6"; marker="$root/c6.ran"
mkcheck "$c6" A A6 scratch $'positive\nnegative' "touch '$marker'; exit 0"
G0="$g0bad" run "$c6" --gate A
[[ "$(result_of A A6 positive)" == INSTRUMENT && "$(result_of A A6 negative)" == INSTRUMENT ]] && ok "a failing gate 0 makes later cases INSTRUMENT, never FAIL" || bad "gate 0 blocks"
[[ ! -e "$marker" ]] && ok "a blocked case is not run" || bad "blocked case ran"
has "$(<"$rdir/evidence/A.A6.positive.txt")" "gate 0 did not pass" && ok "the evidence names why" || bad "gate 0 evidence"
rm -f "$marker"
run "$c6" --gate A
[[ -e "$marker" ]] && ok "with gate 0 passing the same case runs" || bad "case ran with gate 0 ok"
run "$c6" --gate A
first="$(head -n 1 "$rdir/report.jsonl" | jq -r .gate)"
[[ "$first" == 0 ]] && ok "gate 0 runs first, even when only gate A is asked for" || bad "gate 0 first" "$first"

# --- 7. a gate with no checks is not a pass ------------------------------------------
run "$c3" --gate C
has "$out" "gate C: no checks registered; this is not a pass" && ok "an empty gate says so and does not read gate-ready" || bad "empty gate" "$out"
has "$out" "gate C: gate-ready" && bad "empty gate read gate-ready"

# --- 8. interventional never runs --------------------------------------------------
c8="$root/c8"; marker8="$root/c8.ran"
mkcheck "$c8" A A8 interventional $'positive' "touch '$marker8'; exit 0"
run "$c8" --gate A
[[ ! -e "$marker8" && "$(result_of A A8 positive)" == SKIP ]] && ok "an interventional case is not run and reports SKIP" || bad "interventional"
check "the evidence says the case is proposed" grep -q 'proposed' "$rdir/evidence/A.A8.positive.txt"

# --- 9. --live-only and --host -----------------------------------------------------
c9="$root/c9"; m9s="$root/c9.scratch"; m9l="$root/c9.live"
mkcheck "$c9" A A9s scratch $'positive' "touch '$m9s'; exit 0"
mkcheck "$c9" A A9l live $'positive' "touch '$m9l'; exit 0"
run "$c9" --gate A --live-only
[[ ! -e "$m9s" && -e "$m9l" ]] && ok "--live-only runs the live check and skips the scratch one" || bad "--live-only"
rm -f "$m9s" "$m9l"
run "$c9" --gate A --host some-other-host
[[ -e "$m9s" && ! -e "$m9l" && "$(result_of A A9l positive)" == SKIP ]] && ok "a live check does not run when --host names another host" || bad "--host mismatch"

# --- 10. evidence byte for byte; a clean environment; a timeout; bad headers ----
c10="$root/c10"
mkcheck "$c10" A A10 scratch $'bytes' 'printf "out:\\001\\377 x\\n"; printf "err:\\tz\\n" >&2; exit 0'
run "$c10" --gate A
want="$(printf 'out:\001\377 x\nerr:\tz\n')"
[[ "$(<"$rdir/evidence/A.A10.bytes.txt")" == "$want" ]] && ok "stdout and stderr are kept byte for byte" || bad "evidence bytes"
c10b="$root/c10b"
mkcheck "$c10b" A A10b scratch $'env' 'env | cut -d= -f1 | sort; exit 0'
export DIAG_VERIFY_SECRET_NAME=value-never-shown ZZ_PRIVATE_TOKEN=abc
run "$c10b" --gate A
names="$(<"$rdir/evidence/A.A10b.env.txt")"
if has "$names" '^PATH$' && has "$names" '^DIAG_RUN$' && ! has "$names" 'ZZ_PRIVATE_TOKEN'; then ok "a case sees PATH and DIAG_*, and not an unlisted variable"; else bad "clean environment" "$names"; fi
DIAG_PASS_ENV="ZZ_PRIVATE_TOKEN" run "$c10b" --gate A
has "$(<"$rdir/evidence/A.A10b.env.txt")" 'ZZ_PRIVATE_TOKEN' && ok "DIAG_PASS_ENV passes a named variable" || bad "DIAG_PASS_ENV"
check "no variable value reaches the report" bash -c "! grep -rq 'abc\\|value-never-shown' '$rdir'"
unset DIAG_VERIFY_SECRET_NAME ZZ_PRIVATE_TOKEN
c10c="$root/c10c"
mkcheck "$c10c" A A10c scratch $'slow' 'sleep 20; exit 0'
DIAG_CASE_TIMEOUT=1 run "$c10c" --gate A
[[ "$(result_of A A10c slow)" == INSTRUMENT ]] && ok "a case past its timeout is INSTRUMENT" || bad "timeout" "$(result_of A A10c slow)"
mkdir -p "$root/c10d/A"; echo 'exit 0' > "$root/c10d/A/nohead.sh"
run "$root/c10d" --gate A
[[ "$(result_of A nohead header)" == INSTRUMENT ]] && ok "a check with no header is INSTRUMENT" || bad "no header"

# --- 11. reports stay out of the repo ------------------------------------------------
after="$(git -C "$repo" status --porcelain 2>/dev/null || true)"
[[ "$after" == "$repo_status_before" ]] && ok "a run leaves the repository tree as it was" || bad "repo tree changed"

# --- 12. the scratch-daemon helper, against stubs -------------------------------------
stubs="$root/stubs"; mkdir -p "$stubs"
cat > "$stubs/marvel" <<'EOF'
#!/usr/bin/env bash
# A stand-in for marvel. State lives under the --marvel-home it is given.
home=""; sock=""; args=()
while [[ $# -gt 0 ]]; do
  case "$1" in --marvel-home) home="$2"; shift 2 ;; --socket) sock="$2"; shift 2 ;; *) args+=("$1"); shift ;; esac
done
[[ -n "$home" ]] || { echo "stub marvel: no --marvel-home" >&2; exit 9; }
echo "${args[*]}" >> "$home/stub.calls"
case "${args[0]:-}" in
  daemon)
    mkdir -p "$home/state"; : > "$home/state/seats"
    exec python3 -c 'import socket,sys,os,time
p=sys.argv[1]
s=socket.socket(socket.AF_UNIX); s.bind(p); s.listen(1)
time.sleep(600)' "$sock" ;;
  get) printf 'WORKSPACE TEAM ROLE STATE\n'; cat "$home/state/seats" 2>/dev/null ;;
  run) name=""; i=0; for a in "${args[@]}"; do i=$((i+1)); [[ "$a" == --name ]] && name="${args[$i]}"; done
       echo "diag seat $name running" >> "$home/state/seats" ;;
  stop) rm -f "$home/state/seats" ;;
  *) exit 9 ;;
esac
EOF
printf '#!/usr/bin/env bash\nexit 0\n' > "$stubs/simulator"
printf '#!/usr/bin/env bash\ncase "$*" in *rate_limit*) echo 5000 ;; *) exit 1 ;; esac\n' > "$stubs/gh"
chmod +x "$stubs/marvel" "$stubs/simulator" "$stubs/gh"

# A sentinel the helper must never touch: an unrelated process of the same name.
sleep 300 & sentinel=$!

helper_test() ( # a subshell, so the helper's environment edits do not leak
  set -u
  export DIAG_TMP="$root"; export DIAG_MARVEL="$stubs/marvel" DIAG_SIMULATOR="$stubs/simulator"
  export MARVEL_SOCKET=/real/live.sock MARVEL_WORKSPACE=live TMUX=/live/tmux,1,0
  real_home="$HOME"
  # shellcheck source=lib/scratch.sh
  source "$here/lib/scratch.sh"
  diag_scratch_new || { echo "new failed"; exit 1; }
  [[ "$HOME" != "$real_home" && "$MARVEL_HOME" == "$DIAG_SCRATCH"/* && "$DIRECTOR_HOME" == "$DIAG_SCRATCH"/* ]] || { echo "env not scratch"; exit 1; }
  [[ -z "${MARVEL_SOCKET:-}" && -z "${MARVEL_WORKSPACE:-}" && -z "${TMUX:-}" ]] || { echo "live names survive"; exit 1; }
  r="$DIAG_SCRATCH"
  diag_scratch_daemon_up || { echo "daemon_up failed"; exit 1; }
  pid="$DIAG_SCRATCH_DAEMON_PID"
  kill -0 "$pid" || { echo "daemon not running"; exit 1; }
  diag_scratch_seat_up t1 || { echo "seat_up failed"; exit 1; }
  grep -q 't1' <(diag_scratch_marvel get sessions) || { echo "seat not listed"; exit 1; }
  diag_scratch_down || { echo "down failed"; exit 1; }
  ! kill -0 "$pid" 2>/dev/null || { echo "daemon survived down"; exit 1; }
  [[ ! -e "$r" ]] || { echo "root survived down"; exit 1; }
  echo helper-ok
)
if hout="$(helper_test 2>&1)" && has "$hout" helper-ok; then ok "helper: scratch env, daemon, seat, and teardown by recorded pid"; else bad "helper lifecycle" "$hout"; fi
kill -0 "$sentinel" 2>/dev/null && ok "helper: an unrelated process was left running" || bad "helper killed an unrelated process"
kill "$sentinel" 2>/dev/null || true

# Refusals: a root that is not one the helper made, and a root under a real state dir.
refuse_test() (
  set -u
  export DIAG_TMP="$root" DIAG_REAL_HOME="$root/fakehome"
  # shellcheck source=lib/scratch.sh
  source "$here/lib/scratch.sh"
  mkdir -p "$root/fakehome/.marvel/dg.x" "$root/notmine"
  DIAG_SCRATCH="$root/notmine"; diag_scratch_assert && { echo "accepted a foreign root"; exit 1; }
  DIAG_SCRATCH="$root/fakehome/.marvel/dg.x"; diag_scratch_assert && { echo "accepted a root under a real state dir"; exit 1; }
  DIAG_SCRATCH="$root/notmine"; diag_scratch_down && { echo "down removed a foreign root"; exit 1; }
  [[ -d "$root/notmine" ]] || { echo "foreign root was removed"; exit 1; }
  echo refuse-ok
)
if rout="$(refuse_test 2>&1)" && has "$rout" refuse-ok; then ok "helper: refuses a foreign root and one under a real state directory, and removes neither"; else bad "helper refusals" "$rout"; fi

missing_test() (
  set -u
  export DIAG_TMP="$root"; export DIAG_MARVEL="$root/nope/marvel" DIAG_SIMULATOR="$root/nope/simulator"
  # shellcheck source=lib/scratch.sh
  source "$here/lib/scratch.sh"
  diag_scratch_new || exit 1
  rc=0; diag_scratch_daemon_up 2>/dev/null || rc=$?; [[ $rc -eq 3 ]] || { echo "daemon_up rc=$rc"; exit 1; }
  export DIAG_MARVEL="$stubs/marvel"; rc=0; diag_scratch_seat_up 2>/dev/null || rc=$?; [[ $rc -eq 3 ]] || { echo "seat_up rc=$rc"; exit 1; }
  diag_scratch_down
  echo missing-ok
)
if mout="$(missing_test 2>&1)" && has "$mout" missing-ok; then ok "helper: a missing marvel or simulator binary returns 3 (INSTRUMENT), not 1"; else bad "helper missing binaries" "$mout"; fi

# --- 13. the real gate 0, against stubs and loopback listeners ----------------------------
lport="$root/lport"
python3 -c 'import socket,sys,time
s=socket.socket(); s.bind(("127.0.0.1",0)); s.listen(5)
print(s.getsockname()[1], flush=True); time.sleep(60)' > "$lport" & lpid=$!
i=0; while [[ ! -s "$lport" && $i -lt 40 ]]; do sleep 0.25; i=$((i+1)); done
bport="$(<"$lport")"
rc=0
mkdir -p "$root/none"
out="$(PATH="$stubs:$PATH" DIAG_TMP="$root" DIAG_MARVEL="$stubs/marvel" DIAG_SIMULATOR="$stubs/simulator" \
  DIAG_BROKER_ADDR="127.0.0.1:$bport" DIAG_HUB_ADDR="127.0.0.1:$bport" \
  "$DIAG" run --gate 0 --checks "$root/none" --report-root "$root/reports" 2>&1)" || rc=$?
rdir="$(find "$root/reports" -name report.jsonl -print0 | xargs -0 ls -t | head -n 1 | xargs dirname)"
all5=1; for c in daemon seat broker hub gh; do [[ "$(result_of 0 G0 $c)" == PASS ]] || { all5=0; echo "  gate 0 case $c: $(result_of 0 G0 $c)"; }; done
[[ $all5 -eq 1 && $rc -eq 0 ]] && ok "gate 0: daemon, seat, broker, hub and gh all PASS against stubs" || bad "gate 0 against stubs" "$out"
has "$out" "gate 0: gate-ready" && ok "gate 0 reads gate-ready" || bad "gate 0 gate-ready"

# Negative control: a closed port must read FAIL, so a PASS above means something.
kill "$lpid" 2>/dev/null || true; sleep 0.5
out="$(PATH="$stubs:$PATH" DIAG_TMP="$root" DIAG_MARVEL="$stubs/marvel" DIAG_SIMULATOR="$stubs/simulator" \
  DIAG_BROKER_ADDR="127.0.0.1:$bport" DIAG_HUB_ADDR="127.0.0.1:$bport" \
  "$DIAG" run --gate 0 --checks "$root/none" --report-root "$root/reports" 2>&1)" || true
rdir="$(find "$root/reports" -name report.jsonl -print0 | xargs -0 ls -t | head -n 1 | xargs dirname)"
[[ "$(result_of 0 G0 broker)" == FAIL && "$(result_of 0 G0 hub)" == FAIL ]] && ok "gate 0 control: a closed port reads FAIL for the broker and the hub" || bad "closed port control" "$(result_of 0 G0 broker) $(result_of 0 G0 hub)"
out="$(PATH="$stubs:$PATH" DIAG_TMP="$root" DIAG_MARVEL="$stubs/marvel" DIAG_SIMULATOR="$stubs/simulator" \
  DIAG_BROKER_ADDR="127.0.0.1:$bport" \
  "$DIAG" run --gate 0 --checks "$root/none" --report-root "$root/reports" 2>&1)" || true
rdir="$(find "$root/reports" -name report.jsonl -print0 | xargs -0 ls -t | head -n 1 | xargs dirname)"
[[ "$(result_of 0 G0 hub)" == SKIP ]] && ok "gate 0: no DIAG_HUB_ADDR means the hub case is SKIP, not PASS" || bad "hub skip" "$(result_of 0 G0 hub)"
out="$(PATH="$stubs:$PATH" DIAG_TMP="$root" DIAG_MARVEL="$root/nope" DIAG_SIMULATOR="$root/nope" DIAG_BROKER_ADDR="127.0.0.1:$bport" \
  "$DIAG" run --gate 0 --checks "$root/none" --report-root "$root/reports" 2>&1)" || true
rdir="$(find "$root/reports" -name report.jsonl -print0 | xargs -0 ls -t | head -n 1 | xargs dirname)"
[[ "$(obs_of 0 G0 daemon)" == instrument && "$(obs_of 0 G0 seat)" == instrument ]] && ok "gate 0: a missing marvel binary is INSTRUMENT for the scratch cases" || bad "missing marvel" "$(obs_of 0 G0 daemon) $(obs_of 0 G0 seat)"

echo
echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
