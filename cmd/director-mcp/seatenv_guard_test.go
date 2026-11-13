package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shim runs in every seat and seat.env is the director host's file, so the shim must not read it
// (I-476, design clause 4): a DIRECTOR_CLUSTER written only in seat.env leaves the shim's cluster unset.
func TestTheShimDoesNotReadSeatEnv(t *testing.T) {
	dir := t.TempDir()
	dh := filepath.Join(dir, ".director")
	if err := os.MkdirAll(dh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dh, "seat.env"), []byte("DIRECTOR_CLUSTER=from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("DIRECTOR_HOME", dh)
	t.Setenv("DIRECTOR_CLUSTER", "")
	if got := sessionCluster(); got != "" {
		t.Fatalf("sessionCluster() = %q with the cluster only in seat.env, want empty", got)
	}
}

// And no non-test source of the shim names the file at all.
func TestNoShimSourceNamesSeatEnv(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "seat.env") {
			t.Errorf("%s names seat.env; the shim must not read the director host's file", f)
		}
	}
}
