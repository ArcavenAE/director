#!/usr/bin/env bash
# Prove install.sh continues past a refusal (P0b, sim/design/board-current-state.md
# section 9): with one plain-file target in a scratch home it names that target,
# installs every other one, and exits nonzero. Also check the skill names the
# renderer by its installed path, so a stale copy elsewhere is never the one run.
#
# Usage: scripts/verify-install.sh   Exit nonzero on any miss. Touches only a temp dir.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/vi.XXXXXX")"
trap 'rm -rf "$root"' EXIT

run_install() { # $1 = scratch home
  CLAUDE_HOME="$1/claude" DIRECTOR_HOME="$1/director" DIRECTOR_STATE="$1/director/state" \
    "$here/install.sh" >"$1/out" 2>&1
}

targets() { # every target install.sh places, relative to the scratch home
  printf '%s\n' claude/skills/director claude/skills/stansfield claude/commands/director.md \
    director/bin/board-html director/bin/dws director/bin/dsi director/bin/dsx director/bin/merge-guard director/bin/relay-log director/bin/desk-snapshot director/bin/director-install director/bin/draft-sweep director/bin/card-ingest director/bin/dbi director/bin/introspect director/bin/dash-writer director/bin/freebusy
}

# Positive control: a clean home installs everything and exits 0.
clean="$root/clean"; mkdir -p "$clean"
if run_install "$clean"; then ok "clean home exits 0"; else bad "clean home exits 0" "$(cat "$clean/out")"; fi
while read -r t; do
  [[ -L "$clean/$t" ]] && ok "clean home installs $t" || bad "clean home installs $t"
done < <(targets)

# One plain file in the way: the renderer path holds an old regular file.
stale="$root/stale"; mkdir -p "$stale/director/bin"
echo "old renderer" >"$stale/director/bin/board-html"
if run_install "$stale"; then bad "a refusal exits nonzero" "exit 0"; else ok "a refusal exits nonzero"; fi
grep -q "board-html" "$stale/out" && grep -qi "refus" "$stale/out" \
  && ok "the refused target is named" || bad "the refused target is named" "$(cat "$stale/out")"
[[ "$(cat "$stale/director/bin/board-html")" == "old renderer" ]] \
  && ok "the plain file is left untouched" || bad "the plain file is left untouched"
while read -r t; do
  [[ "$t" == director/bin/board-html ]] && continue
  [[ -L "$stale/$t" ]] && ok "past the refusal, installs $t" || bad "past the refusal, installs $t"
done < <(targets)

# The skill runs the renderer by its installed path, never a relative one.
skill="$here/skills/director/SKILL.md"
grep -q '${DIRECTOR_HOME:-$HOME/.director}/bin/board-html' "$skill" \
  && ok "skill names the renderer by its installed path" || bad "skill names the renderer by its installed path"
grep -q 'run `scripts/board-html`' "$skill" \
  && bad "skill still runs a relative scripts/board-html" || ok "skill runs no relative renderer"

# Every script the skill calls is installed, executable, and called from the bin home.
# The list is the skill's: every installed-path call it makes. Each must exist in the checkout as the fallback.
BINHOME='${DIRECTOR_HOME:-$HOME/.director}/bin'
names="$(grep -o -E '\.director\}/bin/[A-Za-z0-9_-]+' "$skill" | sed 's#.*/bin/##' | sort -u | tr '\n' ' ')"
for n in dsi dsx dws merge-guard relay-log desk-snapshot draft-sweep card-ingest dbi introspect dash-writer freebusy; do
  case " $names " in *" $n "*) ok "the skill references $n";; *) bad "the skill references $n" "list: $names";; esac
done
for n in $names; do
  [[ -x "$here/skills/director/scripts/$n" || "$n" == board-html ]] && ok "every installed-path call resolves in the checkout: $n" || bad "every installed-path call resolves in the checkout: $n"
done
for n in dsi dsx dws merge-guard relay-log desk-snapshot draft-sweep card-ingest dbi introspect dash-writer freebusy; do
  [[ -x "$clean/director/bin/$n" ]] && ok "install puts $n in the bin home, executable" || bad "install puts $n in the bin home, executable"
  grep -qF "$BINHOME/$n" "$skill" && ok "skill calls $n from the bin home" || bad "skill calls $n from the bin home"
  [[ -x "$here/skills/director/scripts/$n" ]] && ok "the fallback scripts/$n exists in the checkout" || bad "the fallback scripts/$n exists in the checkout"
done
grep -qF 'only when the installed copy is missing' "$skill" && ok "skill states the fallback to scripts/ only when the installed copy is missing" || bad "skill states the fallback to scripts/ only when the installed copy is missing"
# No document installed with the skill may call an installed script by a bare relative
# scripts/<name> path (the replay reference was missed once, which read only SKILL.md).
# Any name counts, not a fixed list. The fallback sentence names skills/director/scripts/<name>, which this pattern skips.
bare="$(grep -rn -E '(^|[^/A-Za-z])scripts/[A-Za-z]' "$here/skills/director" "$here/skills/stansfield" "$here/commands" --include='*.md' || true)"
[[ -z "$bare" ]] && ok "no installed document calls a bare scripts/<name>" || bad "no installed document calls a bare scripts/<name>" "$bare"
# The widened check does catch a bare call: plant one in a scratch copy.
plant="$root/plant.md"; printf 'run scripts/dsi now\n' >"$plant"
grep -qE '(^|[^/A-Za-z])scripts/[A-Za-z]' "$plant" && ok "the widened pattern flags a planted bare scripts/dsi" || bad "the widened pattern flags a planted bare scripts/dsi"
# The relay template's Log field names the relay-log call from the bin home.
grep -qF "$BINHOME/relay-log append" "$here/skills/director/reference/relay-template.md" && ok "relay-template Log field calls relay-log from the bin home" || bad "relay-template Log field calls relay-log from the bin home"
printf 'run scripts/composer-check now\n' >"$plant"
grep -qE '(^|[^/A-Za-z])scripts/[A-Za-z]' "$plant" && ok "the widened pattern flags a bare call to a script outside the old list" || bad "the widened pattern flags a bare call to a script outside the old list"
grep -q 'replay.md' <(find "$here/skills/director" -name '*.md') && ok "replay.md is among the documents scanned" || bad "replay.md is among the documents scanned"

# The sweep mode runs the draft sweep after dsx and shows its lines in a Drafts
# section of the reply only; they are not appended to board.md and not fed to dws (5yupe).
grep -qF "$BINHOME/draft-sweep" "$skill" && grep -q '^Drafts' "$skill" \
  && ok "the sweep mode calls draft-sweep from the bin home and presents a Drafts section" || bad "the sweep mode calls draft-sweep from the bin home and presents a Drafts section"
grep -q 'shown in the sweep reply only' "$skill" && ok "the skill says the draft lines go to the reply only" || bad "the skill says the draft lines go to the reply only"
grep -qi 'GAP' <(grep -n -A3 'draft-sweep' "$skill") && ok "the skill says a missing config still shows its GAP line" || bad "the skill says a missing config still shows its GAP line"
grep -q 'exits 0 whether it finds nothing or a lot' "$skill" && ok "the skill says to read the lines, not the exit code" || bad "the skill says to read the lines, not the exit code"
grep -qF '$DIRECTOR_STATE/draft-sweep.conf' "$skill" && ! grep -qF '~/.director/state/draft-sweep.conf' "$skill" \
  && ok "the skill names the config where the script reads it, under DIRECTOR_STATE" || bad "the skill names the config where the script reads it, under DIRECTOR_STATE"
[[ ! -e "$here/scripts/draft-sweep" ]] && ok "the script lives with the other bin-home scripts, not in scripts/" || bad "the script lives with the other bin-home scripts, not in scripts/"

# The card standard (card-ingest, #320): the skill writes the card file contract once, with a
# minimal valid card, and routes a card write through card-ingest; functions.md says what is
# enforced and what stays honor-only.
grep -q '^## .*[Cc]ard' "$skill" && grep -qF "$BINHOME/card-ingest parse" "$skill" && grep -qF "$BINHOME/card-ingest check" "$skill" \
  && ok "the skill states the card standard and calls card-ingest parse and check from the bin home" || bad "the skill states the card standard and calls card-ingest parse and check from the bin home"
grep -qF '**Valid until**' "$skill" && grep -qF 'Source:' "$skill" && grep -qF '**Terms.** None.' "$skill" \
  && ok "the skill carries the minimal valid card" || bad "the skill carries the minimal valid card"
grep -qF 'card-ingest' "$here/skills/director/reference/functions.md" && grep -qi 'direct writes' "$here/skills/director/reference/functions.md" \
  && ok "functions.md names card-ingest and says a Desk write made without it is honor-only" || bad "functions.md names card-ingest and says a direct write is honor-only"

# The ledger fill (#321): the skill tells director to name the workstream when it logs a relay, and
# what the fill and its list do; functions.md says what is enforced and what stays honor-only.
fn="$here/skills/director/reference/functions.md"
grep -F 'relay-log append' "$skill" | grep -qF -- '--ws' \
  && ok "the relay capture trigger names --ws" || bad "the relay capture trigger names --ws"
grep -qF 'refresh --local' "$skill" && grep -qF 'dws unlinked' "$skill" \
  && ok "the skill names refresh --local and dws unlinked in the sweep" || bad "the skill names refresh --local and dws unlinked"
grep -qF 'refresh --local' "$fn" && grep -qF 'dws unlinked' "$fn" && grep -qF -- '--ws' "$fn" \
  && ok "functions.md says what the fill reads, what it never matches, and that a missing --ws is honor-only" || bad "functions.md covers the fill"

# A copy install runs from the bin home with no checkout beside it.
cp_home="$root/copy"; mkdir -p "$cp_home"
CLAUDE_HOME="$cp_home/claude" DIRECTOR_HOME="$cp_home/director" DIRECTOR_STATE="$cp_home/state" "$here/install.sh" --copy >"$cp_home/out" 2>&1 || true
for n in dsi dsx dws merge-guard relay-log desk-snapshot draft-sweep card-ingest dbi introspect dash-writer freebusy; do
  [[ -f "$cp_home/director/bin/$n" && ! -L "$cp_home/director/bin/$n" && -x "$cp_home/director/bin/$n" ]] && ok "copy install puts a real executable $n in the bin home" || bad "copy install puts a real executable $n in the bin home"
done
# draft-sweep (5yupe) is installed for the sweep to call; it needs no checkout beside it.
[[ -f "$cp_home/director/bin/draft-sweep" && ! -L "$cp_home/director/bin/draft-sweep" && -x "$cp_home/director/bin/draft-sweep" ]] && ok "copy install puts a real executable draft-sweep in the bin home" || bad "copy install puts a real executable draft-sweep in the bin home"
set +e
"$cp_home/director/bin/merge-guard" >"$cp_home/mg" 2>&1; rc=$?
[[ $rc -eq 2 ]] && grep -q "merge-guard" "$cp_home/mg" && ok "installed merge-guard runs from the bin home and prints usage" || bad "installed merge-guard runs from the bin home and prints usage" "rc=$rc"
DIRECTOR_STATE="$cp_home/state" "$cp_home/director/bin/dsx" >"$cp_home/dsx" 2>&1; rc=$?
[[ $rc -eq 0 ]] && grep -q "no watch list" "$cp_home/dsx" && ok "installed dsx runs from the bin home" || bad "installed dsx runs from the bin home" "rc=$rc"
mkdir -p "$cp_home/home"; HOME="$cp_home/home" DIRECTOR_STATE="$cp_home/state" "$cp_home/director/bin/dsi" >"$cp_home/dsi" 2>&1; rc=$?
[[ $rc -eq 0 ]] && ok "installed dsi runs from the bin home" || bad "installed dsi runs from the bin home" "rc=$rc"
set -e

# The default idea roots (ld16k): written when absent, never over an operator's file.
cf="$clean/director/state/idea-roots.conf"
[[ -s "$cf" ]] && grep -q '_kos/ideas' "$cf" && ok "a clean install writes the default idea-roots.conf" || bad "a clean install writes the default idea-roots.conf"
echo "# mine" >"$cf"
run_install "$clean" && [[ "$(cat "$cf")" == "# mine" ]] && ok "a second install leaves an edited idea-roots.conf alone" || bad "a second install leaves an edited idea-roots.conf alone"
cp "$here/skills/director/idea-roots.conf" "$root/roots.conf"
[[ "$(grep -vc -E '^(#|$)' "$root/roots.conf")" == 7 ]] && ok "the shipped default names the design's seven roots" || bad "the shipped default names the design's seven roots"

# A dangling symlink at the conf path must stop the install, not be written through.
sl="$root/sl"; mkdir -p "$sl/director/state"
ln -s "$root/elsewhere.conf" "$sl/director/state/idea-roots.conf"
if run_install "$sl"; then bad "a symlink at idea-roots.conf stops the install" "exit 0"; else ok "a symlink at idea-roots.conf stops the install"; fi
[[ ! -e "$root/elsewhere.conf" ]] && ok "nothing was written through the dangling symlink" || bad "nothing was written through the dangling symlink"
grep -q 'idea-roots.conf' "$sl/out" && ok "the refusal names the conf path" || bad "the refusal names the conf path" "$(tail -3 "$sl/out")"
# The skill's stalled-ideas prose (review round 1 of ld16k).
grep -q 'do not run `standing-hook scan-beat`' "$skill" && ok "the skill skips scan-beat when dbi did not run" || bad "the skill skips scan-beat when dbi did not run"
# freebusy (idle-scheduler B3): run in sweep step 3 after dbi; a missing cluster list still shows its GAP line;
# its count line is printed outside the pinned fence, like the Away line, so the printed contract is unchanged.
fb_at="$(grep -n 'bin/freebusy' "$skill" | head -1 | cut -d: -f1 || true)"; dbi_at="$(grep -n 'bin/dbi --json' "$skill" | head -1 | cut -d: -f1 || true)"
{ [[ -n "$fb_at" && -n "$dbi_at" ]] && [[ "$fb_at" -gt "$dbi_at" ]]; } && ok "the sweep runs freebusy after dbi" || bad "the sweep runs freebusy after dbi" "freebusy line ${fb_at:-none}, dbi line ${dbi_at:-none}"
grep -q 'GAP clusters' <(grep -n -A10 'bin/freebusy' "$skill") && ok "the skill says a missing cluster list still shows its GAP clusters line" || bad "the skill says a missing cluster list still shows its GAP clusters line"
grep -q 'outside the fence' <(grep -n -A10 'bin/freebusy' "$skill") && ok "the skill prints freebusy's count line outside the pinned fence" || bad "the skill prints freebusy's count line outside the pinned fence"
grep -q '^Stalled ideas: none' "$skill" && ok "the shape has a none line for a scan with no rows" || bad "the shape has a none line"
grep -q 'No cap is ruled' "$skill" && ! grep -q 'at most five lines' "$skill" && ok "the skill applies no cap until one is ruled" || bad "the skill applies no cap until one is ruled"
grep -q 'under "Standing checklist"' "$skill" && ! grep -q 'as in the Standing checklist' "$skill" && ok "the scan-beat pointer names where it is described" || bad "the scan-beat pointer"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
