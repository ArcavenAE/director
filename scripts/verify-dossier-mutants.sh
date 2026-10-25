#!/usr/bin/env bash
# Re-run the red for the dossier's contract tests. Each mutant is a deliberate
# fault applied to a scratch copy of cmd/director-mcp; the named tests must FAIL
# against it, so a reader can see that T2, T7, T13 and the handoff guards bite and
# do not pass against a stub that does nothing.
#
#   scripts/verify-dossier-mutants.sh
#
# The working tree is never edited: the module is copied to a temp dir that a
# trap removes. Nothing reaches a live bus or the real home. Needs go and python3.
# Runs under bash 5 and bash 3.2. Exit nonzero if a mutant survives.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
src="$here/../cmd/director-mcp"
work="$(mktemp -d "${TMPDIR:-/tmp}/dossier-mutants.XXXXXX")"
trap 'rm -rf "$work"' EXIT
survived=0 killed=0

# mutate NAME TESTRE FILE ANCHOR REPLACEMENT
mutate() {
  local name="$1" tests="$2" file="$3" anchor="$4" repl="$5" out
  rm -rf "$work/m" && cp -R "$src" "$work/m"
  python3 - "$work/m/$file" "$anchor" "$repl" <<'PY'
import sys
path, anchor, repl = sys.argv[1:4]
s = open(path).read()
if anchor not in s:
    sys.exit("anchor not found: " + anchor)
open(path, "w").write(s.replace(anchor, repl, 1))
PY
  out="$(cd "$work/m" && go test -run "$tests" . 2>&1 || true)"
  if grep -q '^--- FAIL' <<<"$out"; then
    killed=$((killed + 1)); echo "killed   $name"
  else
    survived=$((survived + 1)); echo "SURVIVED $name" >&2
  fi
}

mutate "copies an envelope body into an ask item (T2)" 'TestDossierT2NoBody' dossier.go \
  'Performative: "REQUEST", InReplyTo: "", At: stamp(r.AskedAt), State: r.State}' \
  'Performative: "REQUEST", InReplyTo: "", At: stamp(r.AskedAt), State: r.State, Text: r.Line}'
mutate "creates a consumer (T7)" 'TestDossierT7AckFloorUntouched' dossier.go \
  '	prefix := "mcp_" + d.Predecessor + "_"' \
  '	_, _ = in.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "dossier_stray", AckPolicy: jetstream.AckExplicitPolicy})
	prefix := "mcp_" + d.Predecessor + "_"'
mutate "writes a file outside dossier/ (T13)" 'TestDossierT13NothingElseWritten' dossier.go \
  '	md, js2 := renderBoth(d)' \
  '	_ = os.WriteFile(filepath.Join(o.State, "stray.txt"), []byte("x"), 0o600)
	md, js2 := renderBoth(d)'
mutate "does nothing useful (T2, T7, T13)" 'TestDossierT2NoBody|TestDossierT7AckFloorUntouched|TestDossierT13NothingElseWritten' dossier.go \
  '	md, js2 := renderBoth(d)' \
  '	d = &dossier{ID: "n"}
	md, js2 := renderBoth(d)'
mutate "reads a non-regular handoff as absent" 'TestDossierNonRegularHandoffFailsClosed' dossier.go \
  '	if err != nil || !fi.Mode().IsRegular() {
		return handoffUnreadable
	}' \
  '	if err != nil || !fi.Mode().IsRegular() {
		return handoffNone
	}'
mutate "reads an open failure as absent" 'TestDossierUnreadableHandoffFileFailsClosed' dossier.go \
  '	if err != nil {
		return handoffUnreadable
	}
	defer f.Close()' \
  '	if err != nil {
		return handoffNone
	}
	defer f.Close()'
mutate "ignores the declared handoff path" 'TestDossierDeclaredHandoffPathAndMarker' dossier.go \
  '	if h.Path != "" {' '	if false {'

mutate "accepts a marker with surrounding whitespace" 'TestDossierMarkerFlagRefusals' dossier.go \
  ' || strings.TrimSpace(out.handoffMark) != out.handoffMark' ''
mutate "accepts a CR or NUL inside the marker" 'TestDossierMarkerFlagRefusals' dossier.go \
  'strings.ContainsAny(out.handoffMark, "\r\n\x00")' 'strings.ContainsAny(out.handoffMark, "\n")'
mutate "reads a handoff swapped after the stat (os.SameFile)" 'TestDossierHandoffSwappedBetweenStatAndOpen' dossier.go \
  '!fi2.Mode().IsRegular() || !os.SameFile(fi, fi2)' '!fi2.Mode().IsRegular() || false'
mutate "reads a failed tail read as a read (ReadAt)" 'TestDossierHandoffTailReadFailure' dossier.go \
  '; err != nil && !errors.Is(err, io.EOF) {
		return handoffUnreadable' '; err != nil && false {
		return handoffUnreadable'
mutate "reads an EOF that carries the data as a failure (ReadAt)" 'TestDossierHandoffTailReadFailure' dossier.go \
  '; err != nil && !errors.Is(err, io.EOF) {
		return handoffUnreadable' '; err != nil {
		return handoffUnreadable'
mutate "reads an unlistable fallback directory as no handoff (ReadDir)" 'TestDossierFallbackListFailure' dossier.go \
  '	ents, err := os.ReadDir(dir)
	if err != nil {
		return handoffUnreadable, "", true
	}' '	ents, err := os.ReadDir(dir)
	if err != nil {
		return handoffNone, "", false
	}'

echo "killed $killed, survived $survived"
test "$survived" -eq 0
