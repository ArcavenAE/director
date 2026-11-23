package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A failed connect prints a reason on stderr. The reason must not carry the
// address's user or password: an error from the url package quotes the raw
// URL, and the readers in skills/director/scripts copy the first stderr line
// into their own output. The values here are fake.

const (
	fakeUser = "zuser9"
	fakePass = "sekrit9"
)

func TestRosterConnectErrorLineCarriesNoCredential(t *testing.T) {
	cases := []struct{ name, url string }{
		{"a single URL that does not parse", "nats://" + fakeUser + ":" + fakePass + "@a:xx"},
		{"the second part of a list that does not parse", "nats://127.0.0.1:1,nats://" + fakeUser + ":" + fakePass + "@a:xx"},
		{"a bad escape in the host", "nats://" + fakeUser + ":" + fakePass + "@a%zz:4222"},
		{"no broker at a URL with a credential", "nats://" + fakeUser + ":" + fakePass + "@127.0.0.1:1"},
	}
	for _, c := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var out, errb bytes.Buffer
		code := runRosterCmd(ctx, c.url, cliArgs{mode: "roster", json: true}, &out, &errb)
		cancel()
		if code == 0 {
			t.Errorf("%s: exit 0, want a failure", c.name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: stdout %q, want nothing", c.name, out.String())
		}
		for _, secret := range []string{fakeUser, fakePass} {
			if strings.Contains(out.String(), secret) || strings.Contains(errb.String(), secret) {
				t.Errorf("%s: output carries %q: stdout %q stderr %q", c.name, secret, out.String(), errb.String())
			}
		}
		if !strings.HasPrefix(errb.String(), "director-mcp roster: ") {
			t.Errorf("%s: stderr %q does not start with the command's name", c.name, errb.String())
		}
	}
}

// connectReason is the general path: whatever error the dial returns, the text
// it gives has no user or password of any part of the address.
func TestConnectReasonStripsTheCredentialFromAnyError(t *testing.T) {
	list := "nats://127.0.0.1:1,nats://" + fakeUser + ":" + fakePass + "@b:4222"
	cases := []struct {
		name string
		err  error
		keep string // text that is not secret and must survive, or ""
	}{
		{"a url.Error quoting the raw URL", &url.Error{Op: "parse", URL: "nats://" + fakeUser + ":" + fakePass + "@a:xx", Err: errors.New("invalid port")}, ""},
		{"an error quoting the whole list", fmt.Errorf("dial %s: connection refused", list), "connection refused"},
		{"an error quoting only the userinfo", fmt.Errorf("auth for %s:%s failed: refused", fakeUser, fakePass), "refused"},
		{"an error with no address in it", errors.New("nats: no servers available for connection"), "no servers available"},
	}
	for _, c := range cases {
		got := connectReason(c.err, list)
		for _, secret := range []string{fakeUser, fakePass} {
			if strings.Contains(got, secret) {
				t.Errorf("%s: connectReason = %q, which carries %q", c.name, got, secret)
			}
		}
		if got == "" {
			t.Errorf("%s: connectReason is empty; a reason must be given", c.name)
		}
		if c.keep != "" && !strings.Contains(got, c.keep) {
			t.Errorf("%s: connectReason = %q, want it to keep %q", c.name, got, c.keep)
		}
	}
}
