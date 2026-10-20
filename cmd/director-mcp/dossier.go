package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	thresholdDossierWindow = "dossier_window"
	thresholdDossierKeep   = "dossier_keep"
)

var dossierToken = regexp.MustCompile(`^[-A-Za-z0-9_=]+$`)

func parseDossierArgs(rest []string) (cliArgs, error) {
	out := cliArgs{mode: "dossier"}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		val := func() (string, error) {
			if k := strings.IndexByte(a, '='); strings.HasPrefix(a, "--") && k > 0 {
				return a[k+1:], nil
			}
			if i+1 >= len(rest) {
				return "", fmt.Errorf("dossier: %s needs a value (usage: %s)", a, dossierUsage)
			}
			i++
			return rest[i], nil
		}
		name := a
		if k := strings.IndexByte(a, '='); strings.HasPrefix(a, "--") && k > 0 {
			name = a[:k]
		}
		switch name {
		case "--json":
			out.json = true
		case "--role", "--predecessor", "--predecessor-state", "--pointer":
			v, err := val()
			if err != nil {
				return cliArgs{}, err
			}
			switch name {
			case "--role":
				out.role = v
			case "--predecessor":
				out.pred = v
			case "--predecessor-state":
				out.predState = v
			default:
				out.pointers = append(out.pointers, v)
			}
		default:
			return cliArgs{}, fmt.Errorf("dossier: unknown argument %q (usage: %s)", a, dossierUsage)
		}
	}
	parts := strings.Split(out.role, "/")
	if len(parts) != 2 || !dossierToken.MatchString(parts[0]) || !dossierToken.MatchString(parts[1]) {
		return cliArgs{}, fmt.Errorf("dossier: --role must be TEAM/ROLE (usage: %s)", dossierUsage)
	}
	if out.pred != "" && !dossierToken.MatchString(out.pred) {
		return cliArgs{}, fmt.Errorf("dossier: --predecessor %q is not an agent id (usage: %s)", out.pred, dossierUsage)
	}
	return out, nil
}

// The dossier (P1): what a successor can reconstruct about the seat it
// replaces when no handoff exists, read from the bus and the state directory.
//
// It is a read. It creates no consumer and acks nothing, so no ack floor moves
// (the same posture as askreader.go); it publishes nothing and writes no KV; it
// copies no envelope body, only pointers (message id, stream and sequence,
// performative, counterpart, in_reply_to, stream time); and the only files it
// writes are its own, under dossier/<successor>/, each carrying a nonce line so
// its own retention sweep removes nothing else. It is diagnostic, not a gate
// (SOUL section 8): once it runs it exits 0 with every gap shown, and exit 2 is
// for a usage error alone. It stands in for a handoff only where none exists,
// and it never carries the handoff terminal marker.

const (
	dossierStream   = "AGENT_AUDIT"
	dossierInbox    = "AGENT_INBOX"
	dossierMDCap    = 8192
	dossierJSONCap  = 65000 // the file adds two header lines; the cap is 64 KiB
	dossierReadMax  = 200000
	dossierNoncePfx = "director-dossier-id: "
	stUnread        = "UNREAD"
	stRead          = "read"
	stNotKept       = "not kept"
	stUnknown       = "predecessor unknown"
)

// dossierRename is a seam so a test can fail the rename after the temp write.
var dossierRename = os.Rename

// dossierOpts is everything a run needs, so a test sets each of them directly.
type dossierOpts struct {
	Role        string // TEAM/ROLE
	Predecessor string
	PredSource  string // flag, env or unknown
	PredState   string
	Pointers    []string
	JSON        bool
	State       string
	Successor   string
	DWS         string
	Now         time.Time
}

type dItem struct {
	Kind         string `json:"kind,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	Stream       string `json:"stream,omitempty"`
	Sequence     uint64 `json:"sequence,omitempty"`
	Performative string `json:"performative,omitempty"`
	Counterpart  string `json:"counterpart,omitempty"`
	InReplyTo    string `json:"in_reply_to,omitempty"`
	At           string `json:"at,omitempty"`
	State        string `json:"state,omitempty"`
	Text         string `json:"text,omitempty"`
}

type dSec struct {
	N       int            `json:"n"`
	Title   string         `json:"title"`
	State   string         `json:"state"`
	Why     string         `json:"why,omitempty"`
	Total   int            `json:"total"`
	Counts  map[string]int `json:"counts,omitempty"`
	Items   []dItem        `json:"items"`
	Omitted int            `json:"omitted,omitempty"`
}

type dossier struct {
	ID            string   `json:"director_dossier_id"`
	Reconstructed bool     `json:"reconstructed"`
	Role          string   `json:"role"`
	Successor     string   `json:"successor"`
	Predecessor   string   `json:"predecessor"`
	PredSource    string   `json:"predecessor_source"`
	Candidates    []string `json:"candidates,omitempty"`
	GeneratedAt   string   `json:"generated_at"`
	Window        string   `json:"window"`
	Outside       int      `json:"sequences_before_window"`
	Notices       []string `json:"notices,omitempty"`
	Sections      []*dSec  `json:"sections"`
}

func (d *dossier) sec(n int) *dSec {
	for _, s := range d.Sections {
		if s.N == n {
			return s
		}
	}
	return nil
}

var dossierTitles = map[int]string{
	2: "Standing terms and grants", 3: "Open asks on the Desk",
	4: "Asks the predecessor sent with no terminal reply",
	5: "Asks to the predecessor or its role with no reply",
	6: "Other mail to the predecessor", 7: "Parked and rejected items",
	8: "The predecessor's durable", 9: "Pointers", 10: "Could not recover",
}

func (d *dossier) set(n int, state, why string) *dSec {
	s := d.sec(n)
	if s == nil {
		s = &dSec{N: n, Title: dossierTitles[n], Items: []dItem{}}
		d.Sections = append(d.Sections, s)
	}
	s.State, s.Why = state, why
	return s
}

func (d *dossier) unread(n int, why string) { d.set(n, stUnread, why) }

func (d *dossier) add(n int, it dItem) {
	s := d.sec(n)
	s.Items = append(s.Items, it)
	s.Total++
}

// loadDossierThresholds reads dossier_window and dossier_keep. A row that is
// not a positive duration keeps its default and is named in the notices; it
// never stops the command (the presence watch's all-or-nothing load would).
func loadDossierThresholds(dir string) (window, keep time.Duration, notices []string) {
	window, keep = defaultThresholds[thresholdDossierWindow], defaultThresholds[thresholdDossierKeep]
	b, err := os.ReadFile(filepath.Join(dir, "thresholds.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		return window, keep, []string{"threshold table not read, defaults used: " + err.Error()}
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return window, keep, []string{"threshold table is not a JSON object, defaults used"}
	}
	for _, k := range []string{thresholdDossierWindow, thresholdDossierKeep} {
		v, ok := raw[k]
		if !ok {
			continue
		}
		str, _ := v.(string)
		dur, err := time.ParseDuration(str)
		if err != nil || dur <= 0 {
			notices = append(notices, fmt.Sprintf("threshold %s = %v is not a positive duration, default %s kept", k, v, defaultThresholds[k]))
			continue
		}
		if k == thresholdDossierWindow {
			window = dur
		} else {
			keep = dur
		}
	}
	return
}

func runDossierCmd(ctx context.Context, url string, cli cliArgs, out, errw io.Writer) int {
	st, err := stateDir()
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp dossier: %v\n", err)
		return 0
	}
	o := dossierOpts{Role: cli.role, Predecessor: cli.pred, PredSource: "flag", PredState: cli.predState,
		Pointers: cli.pointers, JSON: cli.json, State: st, DWS: os.Getenv("DIRECTOR_DWS"), Now: time.Now()}
	if o.Predecessor == "" {
		if p := os.Getenv("MARVEL_PREDECESSOR"); dossierToken.MatchString(p) {
			o.Predecessor, o.PredSource = p, "env MARVEL_PREDECESSOR"
		} else {
			o.PredSource = "unknown"
		}
	}
	o.Successor = os.Getenv("DIRECTOR_AGENT_ID")
	if !dossierToken.MatchString(o.Successor) {
		o.Successor = "unknown-successor"
	}
	if o.DWS == "" {
		o.DWS = "dws"
	}
	return runDossier(ctx, url, o, out, errw)
}

func runDossier(ctx context.Context, url string, o dossierOpts, out, errw io.Writer) int {
	team, role, _ := strings.Cut(o.Role, "/")
	window, keep, notices := loadDossierThresholds(o.State)
	for _, n := range notices {
		_, _ = fmt.Fprintf(errw, "director-mcp dossier: %s\n", n)
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	d := &dossier{ID: hex.EncodeToString(nonce), Reconstructed: true, Role: o.Role, Successor: o.Successor,
		Predecessor: o.Predecessor, PredSource: o.PredSource, GeneratedAt: stamp(o.Now), Window: window.String(), Notices: notices}
	if d.Predecessor == "" {
		d.Predecessor, d.PredSource = "", "unknown"
	}
	for n := 2; n <= 10; n++ {
		d.set(n, stRead, "")
	}
	dir := filepath.Join(o.State, "dossier", o.Successor)
	if err := sweepDossiers(dir, keep, o.Now); err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp dossier: retention sweep: %v\n", err)
	}

	collectDWS(ctx, d, o, role)
	nc, js, err := dial(url, Sender{AgentID: "dossier"})
	if err != nil {
		for _, n := range []int{4, 5, 6, 8} {
			d.unread(n, "connect "+url+": "+err.Error())
		}
		if d.Predecessor == "" {
			d.Candidates = nil
		}
	} else {
		defer nc.Close()
		collectBus(ctx, d, js, o, team, role, window)
	}
	collectHandled(d, o)
	collectPointers(d, o)
	finishCannotRecover(d)

	md, js2 := renderBoth(d)
	if err := writeDossier(dir, o, d, md, js2); err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp dossier: %v\n", err)
	}
	if o.JSON {
		_, _ = out.Write(js2)
		_, _ = io.WriteString(out, "\n")
	} else {
		_, _ = io.WriteString(out, md)
	}
	return 0
}

// sweepDossiers removes this directory's own files that carry the nonce line
// and are older than keep. It follows no link and touches nothing else.
func sweepDossiers(dir string, keep time.Duration, now time.Time) error {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || now.Sub(fi.ModTime()) <= keep {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		line, _ := bufio.NewReaderSize(f, 128).ReadString('\n')
		_ = f.Close()
		if strings.HasPrefix(line, dossierNoncePfx) {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectDWS(ctx context.Context, d *dossier, o dossierOpts, role string) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, o.DWS, "inherit", role, "--json")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		why := "dws inherit: " + err.Error()
		if m := firstLine(se.String()); m != "" {
			why += ": " + m
		}
		d.unread(2, why)
		d.unread(3, why)
		return
	}
	var r map[string]any
	if json.Unmarshal(so.Bytes(), &r) != nil {
		d.unread(2, "dws inherit: output is not JSON")
		d.unread(3, "dws inherit: output is not JSON")
		return
	}
	src, _ := r["sources"].(map[string]any)
	why := func(name string) string {
		m, _ := src[name].(map[string]any)
		if m == nil || m["state"] == stUnread {
			w, _ := m["why"].(string)
			return name + " " + stUnread + ": " + w
		}
		return ""
	}
	if w := why("rulings"); w != "" {
		d.unread(2, w)
	} else {
		for _, k := range []string{"terms", "grants", "pending_grants", "expired_grants", "unchecked_grants", "replaced_terms"} {
			list, _ := r[k].([]any)
			for _, x := range list {
				m, _ := x.(map[string]any)
				id, _ := m["id"].(string)
				text, _ := m["text"].(string)
				if text == "" {
					text, _ = m["subject"].(string)
				}
				d.add(2, dItem{Kind: k, Text: id + " " + firstLine(text)})
			}
		}
	}
	oa, isList := r["open_asks"].([]any)
	if !isList {
		w := why("desk")
		if w == "" {
			w = why("ledger")
		}
		if w == "" {
			w = "open asks not readable"
		}
		d.unread(3, w)
		return
	}
	for _, x := range oa {
		m, _ := x.(map[string]any)
		card, _ := m["card"].(string)
		ws, _ := m["ws"].(string)
		d.add(3, dItem{Kind: "open_ask", Text: card + " on " + ws})
	}
}

func collectBus(ctx context.Context, d *dossier, js jetstream.JetStream, o dossierOpts, team, role string, window time.Duration) {
	fail := func(why string) {
		for _, n := range []int{4, 5, 6} {
			d.unread(n, why)
		}
	}
	s, err := js.Stream(ctx, dossierStream)
	if err != nil {
		fail(dossierStream + ": " + err.Error())
	} else if info, err := s.Info(ctx); err != nil {
		fail(dossierStream + ": " + err.Error())
	} else {
		readWindow(ctx, d, s, info, o, team, role, window)
	}
	if d.Predecessor == "" {
		d.set(8, stUnknown, "")
		return
	}
	in, err := js.Stream(ctx, dossierInbox)
	if err != nil {
		d.unread(8, dossierInbox+": "+err.Error())
		return
	}
	prefix := "mcp_" + d.Predecessor + "_"
	l := in.ListConsumers(ctx)
	for ci := range l.Info() {
		if strings.HasPrefix(ci.Name, prefix) {
			d.add(8, dItem{Kind: "durable", Text: fmt.Sprintf("%s pending=%d ack_pending=%d ack_floor=%d", ci.Name, ci.NumPending, ci.NumAckPending, ci.AckFloor.Stream)})
		}
	}
	if err := l.Err(); err != nil {
		d.items8reset(err)
	}
}

func (d *dossier) items8reset(err error) {
	s := d.sec(8)
	s.Items, s.Total = []dItem{}, 0
	d.unread(8, "consumer list: "+err.Error())
}

func readWindow(ctx context.Context, d *dossier, s jetstream.Stream, info *jetstream.StreamInfo, o dossierOpts, team, role string, window time.Duration) {
	cutoff := o.Now.Add(-window)
	first, last := info.State.FirstSeq, info.State.LastSeq
	// The first sequence at or after the cutoff: stream time grows with the
	// sequence, so a binary search finds it without reading the older ones.
	lo, hi := first, last+1
	for lo < hi {
		mid := lo + (hi-lo)/2
		m, err := s.GetMsg(ctx, mid)
		if err != nil || m.Time.Before(cutoff) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	start := lo
	if info.State.Msgs > 0 && start > first {
		d.Outside = int(start - first)
	}
	type at struct {
		seq uint64
		t   time.Time
	}
	where := map[string]at{}
	l := newAskLedger()
	pred := d.Predecessor
	predAddr := "agent://" + team + "/" + pred
	roleAddr := "role://" + team + "/" + role
	cands := map[string]bool{}
	var mail []dItem
	counts := map[string]int{}
	n := 0
	for seq := start; seq <= last; seq++ {
		if n++; n > dossierReadMax {
			for _, k := range []int{4, 5, 6} {
				d.sec(k).Why = fmt.Sprintf("read stopped at sequence %d after %d reads", seq, dossierReadMax)
			}
			break
		}
		m, err := s.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				continue
			}
			for _, k := range []int{4, 5, 6} {
				d.unread(k, fmt.Sprintf("%s seq %d: %v", dossierStream, seq, err))
			}
			return
		}
		r := auditRec{Broker: "local", Seq: seq, At: m.Time, Refused: m.Header.Get("Director-Outcome") == "refused"}
		if json.Unmarshal(m.Data, &r.Env) != nil || r.Env.MessageID == "" {
			continue
		}
		where[r.Env.MessageID] = at{seq, m.Time}
		l.Ingest(r)
		e := r.Env
		if pred == "" {
			if e.Sender.AgentID != "" && (e.Sender.Role == role || strings.HasPrefix(e.Sender.AgentID, team+"-"+role+"-")) {
				cands[e.Sender.AgentID] = true
			}
			continue
		}
		if !r.Refused && e.Recipient.Address == predAddr {
			counts[e.Performative]++
			mail = append(mail, dItem{Kind: "mail", MessageID: e.MessageID, Stream: dossierStream, Sequence: seq,
				Performative: e.Performative, Counterpart: e.Sender.AgentID, InReplyTo: e.InReplyTo, At: stamp(m.Time)})
		}
	}
	if pred == "" {
		for c := range cands {
			d.Candidates = append(d.Candidates, c)
		}
		sort.Strings(d.Candidates)
		for _, k := range []int{4, 5, 6} {
			d.set(k, stUnknown, "")
		}
		return
	}
	rows := make([]*askRow, 0, len(l.Rows))
	for _, r := range l.Rows {
		if isOpen(r.State) {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].AskedAt.After(rows[j].AskedAt) })
	for _, r := range rows {
		w := where[r.Ask]
		it := dItem{Kind: "ask", MessageID: r.Ask, Stream: dossierStream, Sequence: w.seq, Performative: "REQUEST", InReplyTo: "", At: stamp(r.AskedAt), State: r.State}
		switch {
		case r.Asker.AgentID == pred:
			it.Counterpart = r.Owner.Address
			d.add(4, it)
		case r.Owner.Address == predAddr || r.Owner.Address == roleAddr:
			it.Counterpart = "agent://" + team + "/" + r.Asker.AgentID
			d.add(5, it)
		}
	}
	sort.Slice(mail, func(i, j int) bool { return mail[i].Sequence > mail[j].Sequence })
	s6 := d.sec(6)
	s6.Counts = counts
	for _, it := range mail {
		s6.Total++
		if len(s6.Items) < 10 {
			s6.Items = append(s6.Items, it)
		}
	}
}

func collectHandled(d *dossier, o dossierOpts) {
	if d.Predecessor == "" {
		d.set(7, stUnknown, "")
		return
	}
	if o.PredState == "" {
		d.unread(7, "no predecessor state directory given (--predecessor-state)")
		return
	}
	b, err := os.ReadFile(filepath.Join(o.PredState, "handled.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		d.set(7, stNotKept, "the seat kept no handled ledger")
		return
	}
	if err != nil {
		d.unread(7, "handled.jsonl: "+err.Error())
		return
	}
	f := foldLines(b)
	var items []dItem
	for id, e := range f.ByID {
		if e.Disposition == "parked" {
			items = append(items, dItem{Kind: "parked", MessageID: id, Stream: e.Stream, Sequence: e.Seq, At: e.At, Text: firstLine(e.Reason)})
		}
	}
	for _, e := range f.Rejected {
		items = append(items, dItem{Kind: "rejected", MessageID: e.MessageID, Stream: e.Stream, Sequence: e.Seq, At: e.At, Text: firstLine(e.Reason)})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].At > items[j].At || (items[i].At == items[j].At && items[i].MessageID < items[j].MessageID)
	})
	for _, it := range items {
		d.add(7, it)
	}
}

func collectPointers(d *dossier, o dossierOpts) {
	for _, p := range o.Pointers {
		fi, err := os.Stat(p)
		if err != nil {
			d.add(9, dItem{Kind: "pointer", State: "missing", Text: p})
			continue
		}
		d.add(9, dItem{Kind: "pointer", State: "exists", At: stamp(fi.ModTime()), Text: fmt.Sprintf("%s size=%d", p, fi.Size())})
	}
}

func finishCannotRecover(d *dossier) {
	for _, t := range []string{
		"in-flight work that was never sent on the bus",
		"an action that was started and not confirmed",
		"the harness session id, for any harness but a fresh Claude launch",
		"the predecessor's own notes",
	} {
		d.add(10, dItem{Kind: "gap", Text: t})
	}
	for _, s := range d.Sections {
		if s.N != 10 && s.State == stUnread {
			d.add(10, dItem{Kind: "unread", Text: fmt.Sprintf("section %d: %s", s.N, s.Why)})
		}
	}
}

// truncated is a copy of d with at most cap items per section.
func truncated(d *dossier, cap int) *dossier {
	c := *d
	c.Sections = nil
	for _, s := range d.Sections {
		x := *s
		if len(x.Items) > cap {
			x.Omitted = len(x.Items) - cap
			x.Items = x.Items[:cap]
		}
		c.Sections = append(c.Sections, &x)
	}
	return &c
}

func (it dItem) line() string {
	var p []string
	for _, v := range []string{it.Kind, it.State, it.MessageID, it.Performative, it.Counterpart, it.InReplyTo, it.At, it.Stream} {
		if v != "" {
			p = append(p, v)
		}
	}
	if it.Sequence != 0 {
		p = append(p, fmt.Sprintf("seq=%d", it.Sequence))
	}
	if it.Text != "" {
		p = append(p, it.Text)
	}
	return strings.Join(p, " ")
}

func renderMD(d *dossier) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\nreconstructed: true\n\n# Dossier for %s\n\n", dossierNoncePfx, d.ID, d.Role)
	pred := d.Predecessor
	if pred == "" {
		pred = "unknown (no --predecessor and no MARVEL_PREDECESSOR)"
	}
	fmt.Fprintf(&b, "- successor: %s\n- predecessor: %s (from %s)\n- generated: %s\n- window: %s, %d sequences before it\n", d.Successor, pred, d.PredSource, d.GeneratedAt, d.Window, d.Outside)
	for _, c := range d.Candidates {
		fmt.Fprintf(&b, "- candidate instance: %s\n", c)
	}
	for _, n := range d.Notices {
		fmt.Fprintf(&b, "- notice: %s\n", n)
	}
	for _, s := range d.Sections {
		fmt.Fprintf(&b, "\n## %d. %s\n\n%s", s.N, s.Title, s.State)
		if s.Why != "" {
			fmt.Fprintf(&b, ": %s", s.Why)
		}
		fmt.Fprintf(&b, " (%d)\n", s.Total)
		if len(s.Counts) > 0 {
			keys := make([]string, 0, len(s.Counts))
			for k := range s.Counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "- %s: %d\n", k, s.Counts[k])
			}
		}
		for _, it := range s.Items {
			fmt.Fprintf(&b, "- %s\n", it.line())
		}
		if s.Omitted > 0 {
			fmt.Fprintf(&b, "- %d more, see the .json\n", s.Omitted)
		}
	}
	return b.String()
}

// renderBoth returns the markdown within 8 KiB and the JSON within 64 KiB,
// shrinking the per-section item cap until each fits.
func renderBoth(d *dossier) (string, []byte) {
	md := ""
	for _, c := range []int{40, 20, 10, 5, 3, 2, 1, 0} {
		md = renderMD(truncated(d, c))
		if len(md) <= dossierMDCap {
			break
		}
	}
	if len(md) > dossierMDCap {
		cut := strings.LastIndexByte(md[:dossierMDCap-40], '\n')
		md = md[:cut+1] + "- more, see the .json\n"
	}
	var js []byte
	for c := 2048; ; c /= 2 {
		js, _ = json.MarshalIndent(truncated(d, c), "", "  ")
		if len(js) <= dossierJSONCap || c == 0 {
			break
		}
	}
	return md, js
}

func writeDossier(dir string, o dossierOpts, d *dossier, md string, js []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, p := range []string{filepath.Dir(dir), dir} {
		_ = os.Chmod(p, 0o700)
	}
	name := d.Predecessor
	if name == "" {
		name = "unknown"
	}
	base := name + "-" + o.Now.UTC().Format("20060102T150405Z")
	head := fmt.Sprintf("%s%s\nreconstructed: true\n", dossierNoncePfx, d.ID)
	var errs []string
	for ext, data := range map[string][]byte{".md": []byte(md), ".json": append([]byte(head), js...)} {
		if err := writeAtomic(dir, base+ext, data); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New("write: " + strings.Join(errs, "; "))
	}
	return nil
}

func writeAtomic(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, ".tmp-dossier-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := dossierRename(tmp, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
