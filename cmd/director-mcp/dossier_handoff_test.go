package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Follow-up to the dossier's handoff reading: the marker flag's refusals, and the three
// branches of handoffFile and findHandoff that fail closed (a file swapped between the
// stat and the open, a failed tail read, and a fallback directory that cannot be listed).

func TestDossierMarkerFlagRefusals(t *testing.T) {
	refused := map[string]string{
		"leading space":     " DONE",
		"trailing space":    "DONE ",
		"leading tab":       "\tDONE",
		"trailing tab":      "DONE\t",
		"only a space":      " ",
		"no-break space":    " DONE",
		"embedded CR":       "a\rb",
		"trailing CR":       "DONE\r",
		"leading CR":        "\rDONE",
		"embedded NUL":      "a\x00b",
		"only NUL":          "\x00",
		"trailing NUL":      "DONE\x00",
		"embedded newline":  "a\nb",
		"trailing newline":  "DONE\n",
		"201 bytes":         strings.Repeat("a", 201),
		"CRLF line ending":  "DONE\r\n",
		"vertical tab edge": "DONE\v",
	}
	for name, m := range refused {
		if _, err := parseArgs([]string{"dossier", "--role", "ops/builder", "--handoff-marker", m}); err == nil {
			t.Errorf("%s: marker %q was accepted", name, m)
		}
	}
	// controls: what a manifest may declare still passes
	for name, m := range map[string]string{
		"the standing marker": "HANDOFF-COMPLETE",
		"an inner space":      "ALL DONE",
		"200 bytes":           strings.Repeat("a", 200),
		"punctuation":         "-- done --",
	} {
		cli, err := parseArgs([]string{"dossier", "--role", "ops/builder", "--handoff-marker", m})
		if err != nil || cli.handoffMark != m {
			t.Errorf("%s: marker %q refused or changed: %+v %v", name, m, cli, err)
		}
	}
}

// A path swapped for another regular file between the stat and the open is unreadable,
// not read: the opened file must be the one that was stat'd.
func TestDossierHandoffSwappedBetweenStatAndOpen(t *testing.T) {
	dir := t.TempDir()
	p := putHandoff(t, dir, "handoff.md", hdOK, time.Now())
	other := putHandoff(t, dir, "other.md", hdOK, time.Now())
	if got := handoffFile(p, ""); got != handoffPresent {
		t.Fatalf("control: an unswapped valid handoff read as %v", got)
	}
	old := handoffAfterLstat
	t.Cleanup(func() { handoffAfterLstat = old })
	handoffAfterLstat = func(path string) {
		if err := os.Rename(other, path); err != nil {
			t.Errorf("swap: %v", err)
		}
	}
	if got := handoffFile(p, ""); got != handoffUnreadable {
		t.Errorf("a file swapped after the stat read as %v, want unreadable", got)
	}
}

// A tail read that fails is unreadable; an EOF that comes with the data is not a failure.
func TestDossierHandoffTailReadFailure(t *testing.T) {
	p := putHandoff(t, t.TempDir(), "handoff.md", hdOK, time.Now())
	old := handoffReadAt
	t.Cleanup(func() { handoffReadAt = old })
	handoffReadAt = func(f *os.File, b []byte, off int64) (int, error) { return 0, errors.New("input/output error") }
	if got := handoffFile(p, ""); got != handoffUnreadable {
		t.Errorf("a failed tail read gave %v, want unreadable", got)
	}
	handoffReadAt = func(f *os.File, b []byte, off int64) (int, error) {
		n, _ := f.ReadAt(b, off)
		return n, io.EOF
	}
	if got := handoffFile(p, ""); got != handoffPresent {
		t.Errorf("EOF with the full tail gave %v, want present", got)
	}
}

// With no declared path, a fallback directory that cannot be listed is unreadable and
// reported as the fallback; it is never read as "no handoff".
func TestDossierFallbackListFailure(t *testing.T) {
	root := t.TempDir()
	pred := "builder-old"
	good := filepath.Join(root, "good")
	putHandoff(t, good, pred+"-2026-10-10.md", hdOK, time.Now())
	if st, _, fb := findHandoff(handoffSpec{Dir: good, Pred: pred}); st != handoffPresent || !fb {
		t.Fatalf("control: a listable fallback gave %v fallback=%v", st, fb)
	}
	notDir := putHandoff(t, root, "plain-file", "x", time.Now())
	for name, dir := range map[string]string{"a regular file": notDir, "a missing directory": filepath.Join(root, "absent")} {
		if st, _, fb := findHandoff(handoffSpec{Dir: dir, Pred: pred}); st != handoffUnreadable || !fb {
			t.Errorf("%s: gave %v fallback=%v, want unreadable and fallback", name, st, fb)
		}
	}
}
