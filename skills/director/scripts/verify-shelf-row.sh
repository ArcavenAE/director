#!/usr/bin/env bash
# Prove `shelf-row` (sim/design/reply-shelf.md): it builds one Reply Shelf row, or a seed batch, as the write
# a director session applies with ArtifactData, and refuses what the page must never show. It writes nothing.
#   - a row holds exactly the schema's fields; state starts `new`; the id is r-<yyyymmdd>-<n>;
#   - `source` is an https URL, a repo path, or <host>:<absolute path> with the file's sha256 (a host name is
#     fine on this private page; a bare local path, a `..`, a query-less http URL or credentials are not);
#   - the summary is one paragraph of at most 1200 characters with no token, key, credential URL or name from
#     the private-names list (dash-withhold.txt, 0600; absent or wider than 0600 refuses every row);
#   - a refusal prints one fixed line and nothing on stdout, and never echoes the value;
#   - a seed file builds a batch of at most 50 rows, all or none.
# Every run is under `env -i` with a scratch HOME and DIRECTOR_STATE. Fixture values are placeholders.
# Written for bash 3.2 and bash 5.
#
# Usage: skills/director/scripts/verify-shelf-row.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
R="${SHELF_ROW:-$here/shelf-row}"
seed="$here/fixtures/shelf-seed.example.json"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
[[ -f "$R" ]] || { echo "FAIL shelf-row exists at $R"; echo "0 passed, 1 failed"; exit 1; }

root="$(mktemp -d "${SHELF_TMP:-${TMPDIR:-/tmp}}/shr.XXXXXX")" || { echo "FAIL scratch root"; exit 1; }
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/home" "$root/state"
PYDIR="$(dirname "$(command -v python3)")"
printf '%s\n' "canary-client" "# a comment" "second name" >"$root/state/dash-withhold.txt"
chmod 600 "$root/state/dash-withhold.txt"

run() { # run <args...>: stdout to $root/out, stderr to $root/err, status in $rc
  env -i HOME="$root/home" PATH="$PYDIR:/usr/bin:/bin" DIRECTOR_STATE="$root/state" DIRECTOR_TEST_CLOCK=1 \
    SHELF_NOW="2030-01-20T12:00:00Z" python3 "$R" "$@" >"$root/out" 2>"$root/err"
  rc=$?
}
printf 'Placeholder summary. The check ran and found nothing to change.\n' >"$root/sum.txt"
new() { run new --ask I-900001 --asked-by placeholder-supervisor --asked-of placeholder-builder --summary-file "$root/sum.txt" --source "$1" "${@:2}"; }
refused() { # refused <name> <reason-word>: nonzero, empty stdout, one line on stderr naming the reason, no canary
  local name="$1" why="$2"
  if [[ $rc -eq 1 && ! -s "$root/out" && "$(wc -l <"$root/err" | tr -d ' ')" == 1 ]] && grep -qF "$why" "$root/err"; then ok "$name"
  else bad "$name" "rc=$rc out=$(head -c 80 "$root/out") err=$(head -c 160 "$root/err" | tr '\n' ' ')"; fi
}
accepted() { [[ $rc -eq 0 && -s "$root/out" ]] && ok "$1" || bad "$1" "rc=$rc err=$(head -c 160 "$root/err" | tr '\n' ' ')"; }

# --- a row ----------------------------------------------------------------------------------------
new "https://example.org/placeholder/report"
accepted "a valid https source builds a row"
python3 - "$root/out" <<'PY' && ok "the write is one set op on replies with the schema's fields and nothing else" || bad "the write shape" "$(head -c 300 "$root/out")"
import json, sys
w = json.load(open(sys.argv[1]))
assert w["op"] == "set" and w["collection"] == "replies", w
assert w["doc_id"] == "r-20300120-1", w["doc_id"]
d = w["data"]
assert sorted(d) == sorted(["summary", "source", "ask", "asked_by", "asked_of", "state", "created_at", "read_at", "cleared_at", "more", "more_at"]), sorted(d)
assert d["state"] == "new" and d["created_at"] == "2030-01-20T12:00:00Z", d
assert d["read_at"] == d["cleared_at"] == d["more"] == d["more_at"] == "", d
assert d["ask"] == "I-900001" and d["summary"].startswith("Placeholder summary."), d
PY
new "https://example.org/x" --n 7; python3 -c "import json,sys;sys.exit(0 if json.load(open('$root/out'))['doc_id']=='r-20300120-7' else 1)" && ok "--n sets the id's sequence number" || bad "--n sets the id's sequence number"
new "docs/placeholder/finding.md"; accepted "a repo path is a source"
new "host-a:/srv/placeholder/report.md" --sha256 "$(printf 'a%.0s' $(seq 1 64))"
accepted "a host:absolute-path source with a sha256 is accepted"
python3 -c "import json;d=json.load(open('$root/out'))['data'];assert d['source']=='host-a:/srv/placeholder/report.md' and d['sha256']=='a'*64" && ok "the host source keeps its sha256 in the row" || bad "the host source keeps its sha256 in the row"
printf 'The report is on host-a under a path like /srv/placeholder; a host name is fine here.\n' >"$root/sum-host.txt"
run new --ask I-900001 --asked-by a --asked-of b --summary-file "$root/sum-host.txt" --source "https://example.org/x"
accepted "a host name and an absolute path in the summary pass (host detail is allowed on this page)"

# --- source refusals ------------------------------------------------------------------------------
new "host-a:/srv/placeholder/report.md"; refused "a host path without a sha256 is refused" "source:sha256-required"
new "host-a:/srv/p.md" --sha256 abc; refused "a short sha256 is refused" "sha256:shape"
new "https://example.org/x" --sha256 "$(printf 'a%.0s' $(seq 1 64))"; refused "a sha256 with an https source is refused" "sha256:host-path-only"
new "/Users/placeholder/report.md"; refused "a bare local absolute path is refused" "source:local-path"
new "~/report.md"; refused "a tilde path is refused" "source:local-path"
new "docs/../../etc/passwd"; refused "a repo path with .. is refused" "source:shape"
new "host-a:/srv/../etc/p.md" --sha256 "$(printf 'a%.0s' $(seq 1 64))"; refused "a host path with .. is refused" "source:shape"
new "http://example.org/x"; refused "an http URL is refused" "source:shape"
new "https://user:pw@example.org/x"; refused "a URL with credentials is refused" "source:shape"
new "https://example.org/a b"; refused "a URL with a space is refused" "source:shape"
new ""; refused "an empty source is refused" "source:empty"
new "https://example.org/canary-client/x"; refused "a source holding a private name is refused" "source:withheld-name"

# --- field refusals -------------------------------------------------------------------------------
for a in "I-" "i-1" "900001" "I-1234567" "I-9x"; do
  run new --ask "$a" --asked-by x --asked-of y --summary-file "$root/sum.txt" --source "docs/a.md"; refused "ask '$a' is refused" "ask:shape"
done
run new --ask I-900001 --asked-by "Bad Name" --asked-of y --summary-file "$root/sum.txt" --source docs/a.md; refused "asked_by with a space is refused" "asked_by:shape"
run new --ask I-900001 --asked-by x --asked-of "" --summary-file "$root/sum.txt" --source docs/a.md; refused "an empty asked_of is refused" "asked_of:shape"
run new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/none.txt" --source docs/a.md; refused "a missing summary file is refused" "summary:unreadable"

# --- summary refusals -----------------------------------------------------------------------------
sumcase() { # sumcase <name> <reason> <text>
  printf '%s' "$3" >"$root/s.txt"
  run new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/s.txt" --source docs/a.md; refused "$1" "summary:$2"
}
sumcase "an empty summary is refused" empty ""
sumcase "a summary of only spaces is refused" empty "   
"
sumcase "a summary over 1200 characters is refused" length "$(printf 'x%.0s' $(seq 1 1201))"
printf '%s' "$(printf 'x%.0s' $(seq 1 1200))" >"$root/s.txt"; run new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/s.txt" --source docs/a.md
accepted "a summary of exactly 1200 characters passes"
sumcase "a control character is refused" control "$(printf 'a\001b')"
sumcase "a GitHub token prefix is refused" token "see ghp_placeholderplaceholder1234 for it"
sumcase "an AWS key prefix is refused" token "key AKIAPLACEHOLDER12345"
sumcase "a 40-hex run is refused" token "$(printf 'b%.0s' $(seq 1 40))"
sumcase "a mixed-case base64 run is refused" token "Zm9vYmFyQmF6UXV4Q29yZ2VHcmFwbGVYeTEyMw"
sumcase "a private key header is refused" secret "-----BEGIN RSA PRIVATE KEY-----"
sumcase "a password assignment is refused" secret "the password: hunter2 was set"
sumcase "a bearer value is refused" secret "Authorization: Bearer abcdefghijklmnop"
sumcase "a URL with credentials is refused" secret "clone https://user:pw@example.org/repo"
sumcase "a private name is refused, in any case" withheld-name "talked to the CANARY-Client team"
sumcase "a second listed name is refused" withheld-name "about second name today"
printf 'Prose with a line break.\n\nAnd a second paragraph.\n' >"$root/s.txt"; run new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/s.txt" --source docs/a.md
refused "more than one paragraph is refused (one paragraph each)" "summary:paragraphs"
printf 'A canary-clientele is not a listed name.\n' >"$root/s.txt"; run new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/s.txt" --source docs/a.md
accepted "a listed name only matches as a whole word"

# --- the refusal never echoes the value -------------------------------------------------------------
printf 'talked to canary-client about ghp_placeholderplaceholder1234\n' >"$root/s.txt"
run new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/s.txt" --source docs/a.md
! grep -qE 'canary|ghp_' "$root/err" "$root/out" && ok "a refusal names the field and reason, never the value" || bad "a refusal echoes the value"

# --- the private-names list fails closed ------------------------------------------------------------
mv "$root/state/dash-withhold.txt" "$root/w.keep"
new "docs/a.md"; refused "no names list refuses every row" "withhold:absent"
cp "$root/w.keep" "$root/state/dash-withhold.txt"; chmod 644 "$root/state/dash-withhold.txt"
new "docs/a.md"; refused "a names list wider than 0600 refuses every row" "withhold:mode"
chmod 600 "$root/state/dash-withhold.txt"; : >"$root/state/dash-withhold.txt"
new "docs/a.md"; accepted "an empty names list (0600) filters nothing and passes"
cp "$root/w.keep" "$root/state/dash-withhold.txt"; chmod 600 "$root/state/dash-withhold.txt"

# --- the seed batch ---------------------------------------------------------------------------------
run seed "$seed"
accepted "the example seed file builds a batch"
python3 - "$root/out" <<'PY' && ok "the batch is {ops: [...]} of set ops with ids r-<date>-1 to -3 in order" || bad "the batch shape" "$(head -c 300 "$root/out")"
import json, sys
b = json.load(open(sys.argv[1]))
assert list(b) == ["ops"], list(b)
assert [o["doc_id"] for o in b["ops"]] == ["r-20300120-1", "r-20300120-2", "r-20300120-3"], b
assert all(o["op"] == "set" and o["collection"] == "replies" for o in b["ops"])
assert b["ops"][1]["data"]["sha256"] == "0" * 64 and "sha256" not in b["ops"][0]["data"]
PY
run seed "$seed" --start 5
python3 -c "import json;print(','.join(o['doc_id'] for o in json.load(open('$root/out'))['ops']))" | grep -qx 'r-20300120-5,r-20300120-6,r-20300120-7' && ok "--start numbers the batch from the given n" || bad "--start numbers the batch"
python3 - "$seed" "$root/bad-seed.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1])); s["rows"][1]["source"] = "/Users/placeholder/x.md"
json.dump(s, open(sys.argv[2], "w"))
PY
run seed "$root/bad-seed.json"; refused "one bad row refuses the whole batch, naming the row" "row 2: source:local-path"
python3 -c "import json;s=json.load(open('$seed'));s['rows']=s['rows']*17;json.dump(s,open('$root/big.json','w'))"
run seed "$root/big.json"; refused "more than 50 rows is refused" "seed:too-many"
printf '{"rows": "no"}' >"$root/shape.json"; run seed "$root/shape.json"; refused "a seed file of the wrong shape is refused" "seed:shape"
printf '{"rows":[{"ask":"I-900001","extra":"x","asked_by":"a","asked_of":"b","summary":"s","source":"docs/a.md"}]}' >"$root/extra.json"
run seed "$root/extra.json"; refused "a seed row with an unknown field is refused" "seed:fields"

# --- it writes nothing, and the clock is not overridable by accident ---------------------------------
before="$(cd "$root" && find state home -type f | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
new "docs/a.md"; run seed "$seed"
after="$(cd "$root" && find state home -type f | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
[[ "$before" == "$after" ]] && ok "no file under the state or home directory changed" || bad "a file changed"
env -i HOME="$root/home" PATH="$PYDIR:/usr/bin:/bin" DIRECTOR_STATE="$root/state" SHELF_NOW="2030-01-20T12:00:00Z" \
  python3 "$R" new --ask I-900001 --asked-by x --asked-of y --summary-file "$root/sum.txt" --source docs/a.md >"$root/out" 2>"$root/err"
python3 -c "import json;d=json.load(open('$root/out'))['data']['created_at'];assert d!='2030-01-20T12:00:00Z'" && ok "SHELF_NOW is ignored without DIRECTOR_TEST_CLOCK=1" || bad "SHELF_NOW overrides the clock unguarded"
run; [[ $rc -eq 2 ]] && ok "no subcommand is a usage error (exit 2)" || bad "no subcommand is a usage error" "rc=$rc"
run bogus; [[ $rc -eq 2 ]] && ok "an unknown subcommand is a usage error (exit 2)" || bad "an unknown subcommand is a usage error" "rc=$rc"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
