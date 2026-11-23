package main

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// NATS_URL may be a comma-separated list, each server with its own credential.
// The address the roster names, in the document and in the error line, carries
// none of them: every part is redacted, not only the first.

func TestBrokerAddressRedactsEveryPartOfAList(t *testing.T) {
	cases := []struct{ in, want string }{
		{"nats://u:p@a:4222", "nats://a:4222"},
		{"nats://u:p@a:4222,nats://u2:p2@b:4222", "nats://a:4222,nats://b:4222"},
		{"nats://a:4222,nats://u2:p2@b:4222", "nats://a:4222,nats://b:4222"},
		// a part that does not parse becomes "unparseable" alone; the other stays
		{"nats://u:p@a:4222,nats://u2:p2@b:xx", "nats://a:4222,unparseable"},
		{"nats://u:p@a:xx,nats://u2:p2@b:4222", "unparseable,nats://b:4222"},
	}
	for _, c := range cases {
		got := brokerAddress(c.in)
		if got != c.want {
			t.Errorf("brokerAddress(%q) = %q, want %q", c.in, got, c.want)
		}
		for _, secret := range []string{"p@", "p2", "u2:"} {
			if strings.Contains(got, secret) {
				t.Errorf("brokerAddress(%q) = %q still carries %q", c.in, got, secret)
			}
		}
	}
}

func TestRosterListURLDocumentAndErrorLineCarryNoCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := startScratchServerNoStreamList(t) // nats://admin:admin@127.0.0.1:<port>
	provision(t, ctx, srv)
	u, err := url.Parse(srv)
	if err != nil {
		t.Fatal(err)
	}
	list := "nats://dead:secret@127.0.0.1:1," + srv
	doc, code, stderr := runRosterForContract(t, list)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	b, _ := doc["broker"].(map[string]any)
	got, _ := b["url"].(string)
	if want := "nats://127.0.0.1:1,nats://" + u.Host; got != want {
		t.Errorf("broker.url = %q, want %q", got, want)
	}
	raw, _ := json.Marshal(doc)
	for _, secret := range []string{"secret", "admin", "dead"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the document carries %q: %s", secret, raw)
		}
	}

	// no server at either address: the error line names both, with no credential
	_, code, stderr = runRosterForContract(t, "nats://u:p@127.0.0.1:1,nats://u2:p2@127.0.0.1:2")
	if code != 3 {
		t.Errorf("exit %d, want 3; stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "nats://127.0.0.1:1,nats://127.0.0.1:2") {
		t.Errorf("stderr %q does not name both addresses", stderr)
	}
	for _, secret := range []string{"u2:p2", "p@", "u:p"} {
		if strings.Contains(stderr, secret) {
			t.Errorf("stderr carries %q: %q", secret, stderr)
		}
	}
}
