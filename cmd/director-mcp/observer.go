package main

// The hub observer (I-469, sim/design/hub-observer.md). One NKey user on the
// global hub may read every inbox and cannot take, send, ack or delete
// anything. The shim reaches the hub through the local broker's leaf, whose
// grant is the host's own, so a read of another cluster's mail needs a
// direct connection with the observer's key. It is opened only for the two
// reads that want it, `unread --global` and the global durable check of
// list_roster, and never for send, the inbox or presence.
//
// The key reaches the shim by path, in three variables set in the director
// seat's launch environment:
//
//	DIRECTOR_HUB_OBSERVER_URL   the hub's client url (nats:// or tls://)
//	DIRECTOR_HUB_OBSERVER_NKEY  the path of the observer's NKey seed file
//	DIRECTOR_HUB_OBSERVER_CA    the CA file, only when the client port has TLS
//
// With the url unset there is no observer and the readers behave as before.
// With it set and unusable, the reader says so (a GAP naming the error) and
// uses the leaf path; it never prints an empty count in its place.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	hubObserverEnvURL  = "DIRECTOR_HUB_OBSERVER_URL"
	hubObserverEnvNKey = "DIRECTOR_HUB_OBSERVER_NKEY"
	hubObserverEnvCA   = "DIRECTOR_HUB_OBSERVER_CA"

	// observerInboxPrefix is the observer's reply prefix. Its grant subscribes to
	// this prefix only, so it never sees replies addressed to another user.
	observerInboxPrefix = "_INBOX_observer_director"

	observerConnectTimeout = 3 * time.Second
)

// observerConfig is the observer's connection, read from the environment.
type observerConfig struct {
	url, nkeyPath, caPath string
}

// observerFromEnv returns nil when no observer is configured.
func observerFromEnv() *observerConfig {
	url := strings.TrimSpace(os.Getenv(hubObserverEnvURL))
	if url == "" {
		return nil
	}
	return &observerConfig{
		url:      url,
		nkeyPath: os.Getenv(hubObserverEnvNKey),
		caPath:   os.Getenv(hubObserverEnvCA),
	}
}

// checkSeedFile refuses a seed that group or others can read, as the bus
// password file is (natspass.go). The error names the path and the reason and
// never the seed.
func checkSeedFile(path string) error {
	if path == "" {
		return fmt.Errorf("%s is not set", hubObserverEnvNKey)
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s %s: %w", hubObserverEnvNKey, path, err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s %s: not a regular file", hubObserverEnvNKey, path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s %s: mode %04o lets group or others read it; chmod it to 0600", hubObserverEnvNKey, path, st.Mode().Perm())
	}
	return nil
}

// dialObserver connects to the hub as the observer. It uses the plain JetStream
// API prefix, since it is a direct client of the hub, and the observer's own
// inbox prefix.
func dialObserver(cfg *observerConfig) (*nats.Conn, jetstream.JetStream, error) {
	if err := checkSeedFile(cfg.nkeyPath); err != nil {
		return nil, nil, err
	}
	nkeyOpt, err := nats.NkeyOptionFromSeed(cfg.nkeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", hubObserverEnvNKey, cfg.nkeyPath, err)
	}
	opts := []nats.Option{
		nkeyOpt,
		nats.Name("director-mcp/hub-observer"),
		nats.CustomInboxPrefix(observerInboxPrefix),
		nats.Timeout(observerConnectTimeout),
		nats.MaxReconnects(0),
	}
	if cfg.caPath != "" {
		opts = append(opts, nats.RootCAs(cfg.caPath))
	}
	nc, err := nats.Connect(cfg.url, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", cfg.url, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, js, nil
}

// observerGap words the GAP a reader prints when the observer is set and could
// not be used.
func observerGap(err error) string {
	return fmt.Sprintf("GAP observer: the hub observer was not usable (%v); read through the leaf path instead, which cannot see other clusters' mail in full", err)
}

// readUnreadGlobalObserved is readUnreadGlobalWith with the observer: when one
// is configured, the read goes through it and covers every hub stream in full.
// A connect or read failure is a GAP on every report, then the leaf path; if
// that fails too, one error names both.
func readUnreadGlobalObserved(ctx context.Context, nc *nats.Conn, domain string, now time.Time, durables []string) ([]unreadReport, error) {
	cfg := observerFromEnv()
	if cfg == nil {
		return readUnreadGlobalWith(ctx, nc, domain, now, durables)
	}
	onc, ojs, err := dialObserver(cfg)
	if err == nil {
		defer onc.Close()
		var reports []unreadReport
		reports, err = readUnreadGlobalJS(ctx, ojs, domain, now, nil, false)
		if err == nil {
			return reports, nil
		}
	}
	gap := observerGap(err)
	reports, lerr := readUnreadGlobalWith(ctx, nc, domain, now, durables)
	if lerr != nil {
		return nil, errors.New(gap + "; leaf path: " + lerr.Error())
	}
	for i := range reports {
		reports[i].Warnings = append([]string{gap}, reports[i].Warnings...)
	}
	return reports, nil
}

// flagGlobalRowsObserved is the global durable check: through the observer when
// one is configured, so a seat on another cluster reads present, missing or
// unknown on its own durable and not durable-unchecked for want of a grant. A
// failure to connect is named on every row and the leaf path checks the row
// instead; a failed lookup through the observer reads unknown with the error.
func (g *globalTier) flagGlobalRowsObserved(ctx context.Context, rows []map[string]any) {
	cfg := observerFromEnv()
	if cfg == nil {
		g.flagGlobalRowsOn(ctx, g.js, "", false, rows)
		return
	}
	onc, ojs, err := dialObserver(cfg)
	if err != nil {
		g.flagGlobalRowsOn(ctx, g.js, observerGap(err), false, rows)
		return
	}
	defer onc.Close()
	g.flagGlobalRowsOn(ctx, ojs, "", true, rows)
}
