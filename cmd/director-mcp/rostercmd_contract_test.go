package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// The roster output is a published interface: marvel's seat-gap check reads it
// (marvel#1572, docs/design/native-checks.md section 3.3). These tests pin the
// three things that check needs, so a change to the output cannot drop them
// unseen: a schema_version, a distinct exit status for "no broker reachable",
// and a field naming the broker that was read.

func runRosterForContract(t *testing.T, url string) (map[string]any, int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	code := runRosterCmd(ctx, url, cliArgs{mode: "roster", json: true}, &out, &errb)
	var doc map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatalf("stdout is not JSON: %v: %q", err, out.String())
		}
	}
	return doc, code, errb.String()
}

// The document says which version of its shape it is, as an integer, so a
// reader can refuse a shape it does not know.
func TestRosterJSONCarriesSchemaVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := startScratchServerNoStreamList(t)
	provision(t, ctx, srv)
	doc, code, stderr := runRosterForContract(t, srv)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	v, ok := doc["schema_version"].(float64)
	if !ok || v != 1 {
		t.Fatalf("schema_version = %v (%T), want the number 1; document keys %v", doc["schema_version"], doc["schema_version"], rosterDocKeys(doc))
	}
}

// The document names the broker it read: the URL without any credential, and
// the server's own name, so a host with two clusters can tell whose rows these
// are.
func TestRosterJSONNamesTheBrokerWithoutItsCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := startScratchServerNoStreamList(t) // the URL carries a user and password
	provision(t, ctx, srv)
	doc, code, stderr := runRosterForContract(t, srv)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	b, ok := doc["broker"].(map[string]any)
	if !ok {
		t.Fatalf("broker = %v (%T), want an object; document keys %v", doc["broker"], doc["broker"], rosterDocKeys(doc))
	}
	u, err := url.Parse(srv)
	if err != nil {
		t.Fatal(err)
	}
	want := "nats://" + u.Host
	if got, _ := b["url"].(string); got != want {
		t.Errorf("broker.url = %q, want %q (the address, no credential)", got, want)
	}
	if name, _ := b["server_name"].(string); name == "" {
		t.Errorf("broker.server_name = %v, want the connected server's name", b["server_name"])
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "admin") {
		t.Errorf("the document carries the URL's credential: %s", raw)
	}
}

// No broker at the address is exit 3 with nothing on stdout, apart from exit 1,
// which stays a read that failed once a broker answered. A caller can then say
// "director not present" and not "unreadable".
func TestRosterNoBrokerExitsThreeAndAReadFailureExitsOne(t *testing.T) {
	_, code, stderr := runRosterForContract(t, "nats://127.0.0.1:1")
	if code != 3 {
		t.Errorf("no broker: exit %d, want 3; stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "no broker") {
		t.Errorf("no broker: stderr %q does not say so", stderr)
	}

	// a broker that answers but holds no presence bucket is a failed read
	srv := startScratchServerNoStreamList(t)
	_, code, stderr = runRosterForContract(t, srv)
	if code != 1 {
		t.Errorf("a broker without the presence bucket: exit %d, want 1; stderr %q", code, stderr)
	}
}

// The shape is documented where a reader can find it, and the document names
// the three things above.
func TestRosterJSONContractIsDocumented(t *testing.T) {
	b, err := os.ReadFile("../../docs/roster-json.md")
	if err != nil {
		t.Fatalf("the contract is not documented: %v", err)
	}
	doc := string(b)
	for _, want := range []string{"schema_version", "broker", "server_name", "exit status 3", "exit status 1", "director-mcp roster --json"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/roster-json.md does not mention %q", want)
		}
	}
}

func rosterDocKeys(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
