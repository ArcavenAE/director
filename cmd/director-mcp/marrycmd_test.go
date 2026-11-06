package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marryCmdInput = `{"run_id":"r1","now":"2026-11-06T12:00:00Z",
 "items":[{"id":"item-1","priority":1,"ready_at":"2026-11-06T10:00:00Z","classes":{"label":"pr-review"},"component":"marvel","duties":["first-turn"],"status":"open"}],
 "seats":[{"address":"agent://t1/rev-a","team":"t1","cluster":"c1","availability":"free","classes":["pr-review"],"scopes":["marvel"],
  "cells":{"duty.first-turn":{"value":"proven","observed_at":"2026-11-06T11:50:00Z","valid_until":"2026-11-06T12:30:00Z","source":"pane:turn"}}}],
 "stop":{},"placement":{"sender_cluster":"c1"},"rulings":{"work_class_source":"label"},"lease_cap":1,"lease_minutes":30}`

func TestParseArgsMarry(t *testing.T) {
	got, err := parseArgs([]string{"marry", "--input", "/x/in.json"})
	if err != nil || got.mode != "marry" || got.file != "/x/in.json" {
		t.Fatalf("parseArgs = %+v, %v", got, err)
	}
	for _, bad := range [][]string{{"marry"}, {"marry", "--input"}, {"marry", "--input", "a", "b"}, {"marry", "--bogus"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("parseArgs(%v) accepted", bad)
		}
	}
}

// The subcommand reads its input from a file, prints the run's JSON answer and
// needs no bus, no marvel and no identity.
func TestRunMarryCmdPairsFromAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(f, []byte(marryCmdInput), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runMarryCmd(cliArgs{mode: "marry", file: f}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	var got struct {
		Pairs []struct{ Item, Seat, Lease string } `json:"pairs"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v: %q", err, out.String())
	}
	if len(got.Pairs) != 1 || got.Pairs[0].Item != "item-1" || got.Pairs[0].Seat != "agent://t1/rev-a" || got.Pairs[0].Lease != "r1/item-1" {
		t.Fatalf("pairs = %+v", got.Pairs)
	}
}

func TestRunMarryCmdRefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"unknown field": strings.Replace(marryCmdInput, `"run_id"`, `"bogus":1,"run_id"`, 1),
		"not json":      "not json",
		"zero minutes":  strings.Replace(marryCmdInput, `"lease_minutes":30`, `"lease_minutes":0`, 1),
	}
	for name, body := range cases {
		f := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if code := runMarryCmd(cliArgs{mode: "marry", file: f}, &out, &errb); code == 0 || out.Len() != 0 || !strings.HasPrefix(errb.String(), "Error: ") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := runMarryCmd(cliArgs{mode: "marry", file: filepath.Join(dir, "missing.json")}, &out, &errb); code == 0 {
		t.Error("a missing file exited 0")
	}
}

// "--input -" reads the input from stdin.
func TestRunMarryCmdReadsStdinForDash(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(marryCmdInput); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	var out, errb bytes.Buffer
	if code := runMarryCmd(cliArgs{mode: "marry", file: "-"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"lease":"r1/item-1"`) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}
