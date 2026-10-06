package main

// The ask reader (design section 6): a loop over a broker's AGENT_AUDIT that
// feeds the ledger in askledger.go, and the on-demand `asks` read of its store.
//
// It reads messages by sequence and creates no consumer, so it can never move
// anyone's ack floor. It writes only its own store: the ASK_LEDGER bucket and
// a JSON file it rewrites atomically after each pass. Until the reader has a
// principal of its own (part A5, the operator's grant) it runs under
// director's own user and names every stream it could not read.
//
// Diagnostic, not a gate: it always exits 0 once running, and nothing waits
// on it.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	askBucket      = "ASK_LEDGER"
	askAuditStream = "AGENT_AUDIT"
	passReadMax    = 10000 // sequences read per pass; the rest wait for the next
	defaultPass    = askPassInterval
	rowKeyPrefix   = "row."
)

type askReaderCfg struct {
	Broker string
	File   string
}

type askReader struct {
	js      jetstream.JetStream
	cfg     askReaderCfg
	ledger  *askLedger
	NotRead []string

	loaded bool
	kv     jetstream.KeyValue
	cache  map[string][]byte // what each key last held, so a pass writes only changes
}

func newAskReader(js jetstream.JetStream, cfg askReaderCfg) *askReader {
	return &askReader{js: js, cfg: cfg, ledger: newAskLedger(), cache: map[string][]byte{}}
}

type askMeta struct {
	LastSeq  map[string]uint64    `json:"last_seq"`
	LastPass time.Time            `json:"last_pass"`
	Down     []readerGap          `json:"down"`
	Orphans  map[string]int       `json:"orphans"`
	Dropped  int                  `json:"dropped"`
	NotRead  []string             `json:"not_read"`
	Seen     map[string]time.Time `json:"seen"`
}

type askIDs struct {
	IDs   []idEntry `json:"ids"`
	Tombs []idEntry `json:"tombs"`
}

var keyChars = regexp.MustCompile(`^[-A-Za-z0-9_=]+$`)

// rowKey is a KV-safe key for an ask id.
func rowKey(ask string) string {
	if keyChars.MatchString(ask) {
		return rowKeyPrefix + ask
	}
	return rowKeyPrefix + "x" + hex.EncodeToString([]byte(ask))
}

// loadAskStore reads the ledger back from the ASK_LEDGER bucket. A missing
// bucket is not an error: it is an empty store. notRead is what the last pass
// recorded it could not read.
func loadAskStore(ctx context.Context, kv jetstream.KeyValue) (*askLedger, []string, error) {
	l := newAskLedger()
	var notRead []string
	if e, err := kv.Get(ctx, "meta"); err == nil {
		var m askMeta
		if err := json.Unmarshal(e.Value(), &m); err != nil {
			return nil, nil, fmt.Errorf("store meta unreadable: %w", err)
		}
		l.LastSeq, l.LastPass, l.Down, l.Orphans, l.Dropped, l.Seen = m.LastSeq, m.LastPass, m.Down, m.Orphans, m.Dropped, m.Seen
		notRead = m.NotRead
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil, err
	}
	if e, err := kv.Get(ctx, "ids"); err == nil {
		var ids askIDs
		if err := json.Unmarshal(e.Value(), &ids); err != nil {
			return nil, nil, fmt.Errorf("store ids unreadable: %w", err)
		}
		l.IDs, l.Tombs = ids.IDs, ids.Tombs
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil, err
	}
	keys, err := kv.Keys(ctx)
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil, err
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, rowKeyPrefix) {
			continue
		}
		e, err := kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var row askRow
		if json.Unmarshal(e.Value(), &row) == nil && row.Ask != "" {
			l.Rows[row.Ask] = &row
		}
	}
	l.reindex()
	return l, notRead, nil
}

func (r *askReader) open(ctx context.Context) error {
	kv, err := r.js.KeyValue(ctx, askBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		kv, err = r.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: askBucket})
	}
	if err != nil {
		return err
	}
	r.kv = kv
	l, _, err := loadAskStore(ctx, kv)
	if err != nil {
		return err
	}
	r.ledger = l
	r.loaded = true
	return nil
}

// gatherIDs copies every live presence key and every seat durable's filters.
// ok is false when a source could not be read: the table is then left alone,
// since a missing source would otherwise mark every entry expired.
func (r *askReader) gatherIDs(ctx context.Context) (obs []idObs, ok bool) {
	ok = true
	kv, err := r.js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		r.NotRead = append(r.NotRead, "AGENT_STATE: "+err.Error())
		ok = false
	} else {
		keys, err := kv.Keys(ctx)
		if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
			r.NotRead = append(r.NotRead, "AGENT_STATE keys: "+err.Error())
			ok = false
		}
		for _, k := range keys {
			e, err := kv.Get(ctx, k)
			if err != nil {
				continue
			}
			if o, good := idObsFromPresence(k, e.Value()); good {
				obs = append(obs, o)
			}
		}
	}
	s, err := r.js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		r.NotRead = append(r.NotRead, "AGENT_INBOX: "+err.Error())
		return obs, false
	}
	lister := s.ListConsumers(ctx)
	for info := range lister.Info() {
		filters := info.Config.FilterSubjects
		if len(filters) == 0 && info.Config.FilterSubject != "" {
			filters = []string{info.Config.FilterSubject}
		}
		if o, good := idObsFromDurable(info.Name, filters); good {
			obs = append(obs, o)
		}
	}
	if err := lister.Err(); err != nil {
		r.NotRead = append(r.NotRead, "AGENT_INBOX consumers: "+err.Error())
		ok = false
	}
	return obs, ok
}

// readAudit reads new sequences of AGENT_AUDIT, by sequence, and feeds them to
// the ledger. It keeps its own last sequence per broker.
func (r *askReader) readAudit(ctx context.Context) {
	s, err := r.js.Stream(ctx, askAuditStream)
	if err != nil {
		r.NotRead = append(r.NotRead, askAuditStream+": "+err.Error())
		return
	}
	info, err := s.Info(ctx)
	if err != nil {
		r.NotRead = append(r.NotRead, askAuditStream+": "+err.Error())
		return
	}
	b := r.cfg.Broker
	last := r.ledger.LastSeq[b]
	if info.State.LastSeq < last { // the stream was replaced; start over at its first message
		last = 0
	}
	start := last + 1
	if info.State.FirstSeq > start {
		start = info.State.FirstSeq
	}
	for seq, n := start, 0; seq <= info.State.LastSeq && n < passReadMax; seq, n = seq+1, n+1 {
		msg, err := s.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				r.ledger.LastSeq[b] = seq
				continue
			}
			r.NotRead = append(r.NotRead, fmt.Sprintf("%s seq %d: %v", askAuditStream, seq, err))
			return
		}
		rec := auditRec{Broker: b, Seq: seq, At: msg.Time, Refused: msg.Header.Get("Director-Outcome") == "refused"}
		if json.Unmarshal(msg.Data, &rec.Env) != nil {
			r.ledger.LastSeq[b] = seq // not an envelope: nothing to ledger
			continue
		}
		r.ledger.Ingest(rec)
		r.ledger.LastSeq[b] = seq
	}
}

// Pass runs one pass at now: ids, then new audit sequences, then resolution,
// aging, and the store.
func (r *askReader) Pass(ctx context.Context, now time.Time) error {
	if !r.loaded {
		if err := r.open(ctx); err != nil {
			return fmt.Errorf("ask store: %w", err)
		}
	}
	r.NotRead = nil
	r.ledger.NotePass(now)
	if obs, ok := r.gatherIDs(ctx); ok {
		r.ledger.ObserveIDs(now, obs)
	}
	r.readAudit(ctx)
	r.ledger.Resolve(now)
	r.ledger.Sweep(now)
	if err := r.persist(ctx, now); err != nil {
		return err
	}
	return r.writeFile(now)
}

func (r *askReader) put(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if string(r.cache[key]) == string(b) {
		return nil
	}
	if _, err := r.kv.Put(ctx, key, b); err != nil {
		return err
	}
	r.cache[key] = b
	return nil
}

func (r *askReader) persist(ctx context.Context, now time.Time) error {
	l := r.ledger
	if err := r.put(ctx, "meta", askMeta{LastSeq: l.LastSeq, LastPass: l.LastPass, Down: l.Down, Orphans: l.Orphans, Dropped: l.Dropped, NotRead: r.NotRead, Seen: l.Seen}); err != nil {
		return err
	}
	if err := r.put(ctx, "ids", askIDs{IDs: l.IDs, Tombs: l.Tombs}); err != nil {
		return err
	}
	live := map[string]bool{}
	for ask, row := range l.Rows {
		k := rowKey(ask)
		live[k] = true
		if err := r.put(ctx, k, row); err != nil {
			return err
		}
	}
	for k := range r.cache {
		if strings.HasPrefix(k, rowKeyPrefix) && !live[k] {
			if err := r.kv.Purge(ctx, k); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
				return err
			}
			delete(r.cache, k)
		}
	}
	return nil
}

// writeFile rewrites the JSON file atomically: a temp file in the same
// directory, then a rename.
func (r *askReader) writeFile(now time.Time) error {
	if r.cfg.File == "" {
		return nil
	}
	b, err := json.MarshalIndent(r.ledger.Report(now, r.NotRead, true), "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.cfg.File)
	tmp, err := os.CreateTemp(dir, filepath.Base(r.cfg.File)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	serr := tmp.Sync() // the bytes are on disk before the rename makes them the file
	cerr := tmp.Close()
	if werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(name)
		return errors.Join(werr, serr, cerr)
	}
	if err := os.Rename(name, r.cfg.File); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// readAskReport reads the ASK_LEDGER bucket and builds the report. It runs no
// pass of its own.
func readAskReport(ctx context.Context, js jetstream.JetStream, now time.Time, all bool) (askReport, error) {
	kv, err := js.KeyValue(ctx, askBucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return newAskLedger().Report(now, nil, all), nil
		}
		return askReport{}, err
	}
	l, notRead, err := loadAskStore(ctx, kv)
	if err != nil {
		return askReport{}, err
	}
	return l.Report(now, notRead, all), nil
}

func printAsks(w io.Writer, rep askReport) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	head := fmt.Sprintf("asks at %s: %d rows", rep.GeneratedAt, len(rep.Rows))
	if rep.Partial {
		head += " (PARTIAL: not every stream was read; see gaps)"
	}
	p("%s", head)
	p("1. alarms")
	if len(rep.Alarms) == 0 {
		p("   none")
	}
	for _, a := range rep.Alarms {
		p("   %s", a.Text)
	}
	p("2. blocked on whom")
	if len(rep.Chains) == 0 {
		p("   none")
	}
	for _, c := range rep.Chains {
		tail := ""
		if c.Cycle {
			tail = " (cycle)"
		}
		p("   %s%s", strings.Join(c.Asks, " -> "), tail)
	}
	for _, group := range []struct {
		title string
		rows  []rollupRow
	}{{"3. rollup per owner role", rep.ByOwner}, {"   rollup per asker role", rep.ByAsker}} {
		p("%s", group.title)
		if len(group.rows) == 0 {
			p("   none")
		}
		for _, g := range group.rows {
			p("   %s: open %d, unacked %d, blocked %d, oldest open %s", g.Key, g.Open, g.Unacked, g.Blocked, humanAge(time.Duration(g.OldestOpenSec)*time.Second))
		}
	}
	p("4. gaps")
	g := rep.Gaps
	anyGap := false
	line := func(format string, a ...any) { anyGap = true; p("   "+format, a...) }
	for _, s := range g.StreamsNotRead {
		line("not read: %s", s)
	}
	for b, n := range g.Orphans {
		if n > 0 {
			line("orphan replies on %s: %d (the ledger missed their asks)", b, n)
		}
	}
	if len(g.NoReplyBy) > 0 {
		line("open asks with no reply_by: %s", strings.Join(g.NoReplyBy, ", "))
	}
	for _, u := range g.Unresolved {
		line("%s: %s unresolved (%s)", u.Ask, u.Party, u.Reason)
	}
	if g.NamesSB4 {
		line("a seat declared no role: the fix is marvel passing the role to the shim (SB-4)")
	}
	if g.Stale > 0 {
		line("stale (no message for 72h): %d, oldest %s; --all lists them", g.Stale, humanAge(time.Duration(g.StaleOldestSec)*time.Second))
	}
	if g.Dropped > 0 {
		line("dropped this pass: %d", g.Dropped)
	}
	for _, d := range g.ReaderDown {
		line("%s", d)
	}
	if !anyGap {
		p("   none")
	}
}

func runAsksCmd(ctx context.Context, url string, cli cliArgs, out, errw io.Writer) int {
	if cli.help {
		_, _ = io.WriteString(out, asksHelp())
		return 0
	}
	nc, js, err := dial(url, Sender{AgentID: "asks-reader"})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp asks: connect %s: %v\n", url, err)
		return 0
	}
	defer nc.Close()
	rep, err := readAskReport(ctx, js, time.Now(), cli.all)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp asks: %v\n", err)
		return 0
	}
	if cli.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return 0
	}
	printAsks(out, rep)
	return 0
}

// runAskReaderCmd is the long-running loop: a pass at least every 30s until a
// signal arrives, or one pass with --once.
func runAskReaderCmd(ctx context.Context, url string, cli cliArgs, errw io.Writer) int {
	nc, js, err := dial(url, Sender{AgentID: "ask-reader"})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp ask-reader: connect %s: %v\n", url, err)
		return 1
	}
	defer nc.Close()
	file := cli.file
	if file == "" {
		file = os.Getenv("DIRECTOR_ASK_LEDGER_FILE")
	}
	if file == "" {
		if home, err := os.UserHomeDir(); err == nil {
			file = filepath.Join(home, ".director", "ask-ledger.json")
		}
	}
	if file != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			_, _ = fmt.Fprintf(errw, "director-mcp ask-reader: %v\n", err)
			return 1
		}
	}
	every := cli.interval
	if every <= 0 {
		every = defaultPass
	}
	r := newAskReader(js, askReaderCfg{Broker: "local", File: file})
	for {
		started := time.Now()
		if err := r.Pass(ctx, started); err != nil {
			_, _ = fmt.Fprintf(errw, "director-mcp ask-reader: pass: %v\n", err)
		}
		for _, n := range r.NotRead {
			_, _ = fmt.Fprintf(errw, "director-mcp ask-reader: not read: %s\n", n)
		}
		if cli.once {
			return 0
		}
		// Sleep to the next tick, not for a full interval after the pass, so a
		// slow pass does not stretch the gap the resolution margin assumes.
		wait := every - time.Since(started)
		if wait < 0 {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(wait):
		}
	}
}

// askReaderContext is a context cancelled by SIGINT or SIGTERM.
func askReaderContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
