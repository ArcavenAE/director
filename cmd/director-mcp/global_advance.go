package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const globalAdvanceUsage = "director-mcp global-advance [--seat AGENT-ID] [--to-head | --to SEQ]"

// advanceBatch is how many messages one pull asks for while acking through.
const advanceBatch = 256

// advanceResult is what one advance did, for the printout.
type advanceResult struct {
	Durable       string
	Stream        string
	BeforeFloor   uint64
	AfterFloor    uint64
	BeforePending uint64
	AfterPending  uint64
	Acked         uint64
	Head          uint64
	HeadKnown     bool
}

// advanceGlobalDurable moves the caller's own global durable forward by acking
// through its backlog: it pulls the messages the durable has not delivered, up
// to the stream head read at the start (or to sequence to, when to is set), and
// acks each, so the durable's ack floor reaches that point. It deletes and
// renames nothing, touches no other consumer and no message, and refuses while
// the durable has deliveries in flight (director#596). A message it pulled and
// did not ack because the run failed is redelivered to the seat after the
// durable's ack wait.
func advanceGlobalDurable(ctx context.Context, g *globalTier, agentID string, to uint64) (advanceResult, error) {
	res := advanceResult{Stream: g.cfg.streamName()}
	name, err := g.ownDurable(ctx, agentID)
	if err != nil {
		return res, err
	}
	res.Durable = name
	cons, err := g.js.Consumer(ctx, res.Stream, name)
	if err != nil {
		return res, fmt.Errorf("read global durable %s: %w", name, err)
	}
	before, err := cons.Info(ctx)
	if err != nil {
		return res, fmt.Errorf("read global durable %s: %w", name, err)
	}
	if before.NumAckPending > 0 || before.NumWaiting > 0 {
		return res, fmt.Errorf("global durable %s has deliveries in flight (%d unacked, %d pulls waiting); a live seat is reading it, so nothing was changed. Run this while the seat is idle, not inside a wait", name, before.NumAckPending, before.NumWaiting)
	}
	res.BeforeFloor, res.BeforePending = before.AckFloor.Stream, before.NumPending
	// The point the advance stops at, read once: a message after it is the
	// seat's to read, never skipped.
	limit := to
	hctx, hcancel := context.WithTimeout(ctx, floorListTimeout)
	if st, serr := g.js.Stream(hctx, res.Stream); serr == nil {
		if info, ierr := st.Info(hctx); ierr == nil {
			res.Head, res.HeadKnown = info.State.LastSeq, true
		}
	}
	hcancel()
	if limit == 0 && res.HeadKnown {
		limit = res.Head
	}
	// With neither a target nor a readable head, the bound is the backlog the
	// durable showed at the start.
	budget := before.NumPending
	for {
		if limit == 0 && res.Acked >= budget {
			break
		}
		want := advanceBatch
		if limit == 0 && budget-res.Acked < uint64(want) {
			want = int(budget - res.Acked)
		}
		batch, ferr := cons.Fetch(want, jetstream.FetchMaxWait(2*time.Second))
		if ferr != nil {
			return finishAdvance(ctx, cons, res), fmt.Errorf("pull from %s: %w", name, ferr)
		}
		got, stop := 0, false
		for msg := range batch.Messages() {
			got++
			md, merr := msg.Metadata()
			if merr != nil {
				return finishAdvance(ctx, cons, res), fmt.Errorf("read a message's sequence on %s: %w", name, merr)
			}
			if limit != 0 && md.Sequence.Stream > limit {
				_ = msg.Nak() // past the point: the seat's to read
				stop = true
				continue
			}
			if aerr := msg.Ack(); aerr != nil {
				return finishAdvance(ctx, cons, res), fmt.Errorf("ack sequence %d on %s: %w (unacked messages return to the seat)", md.Sequence.Stream, name, aerr)
			}
			res.Acked++
		}
		if berr := batch.Error(); berr != nil && !errors.Is(berr, jetstream.ErrNoMessages) {
			return finishAdvance(ctx, cons, res), fmt.Errorf("pull from %s: %w", name, berr)
		}
		if stop || got == 0 {
			break
		}
	}
	return finishAdvance(ctx, cons, res), nil
}

// finishAdvance reads the durable's position after the acks. An Ack is sent
// without a reply, so the floor is read until it has taken every ack this run
// sent, for up to a few seconds; what it reads is what the durable reports.
func finishAdvance(ctx context.Context, cons jetstream.Consumer, res advanceResult) advanceResult {
	for i := 0; i < 20; i++ {
		after, err := cons.Info(ctx)
		if err != nil {
			return res
		}
		res.AfterFloor = after.AckFloor.Stream
		res.AfterPending = after.NumPending + uint64(after.NumAckPending)
		if after.NumAckPending == 0 {
			return res
		}
		time.Sleep(250 * time.Millisecond)
	}
	return res
}

// ownDurable finds the one global durable of agentID on this cluster's hub
// stream: names that carry exactly this agent id and filter the seat's inbox.
// When the instance is live in the presence bucket that decides between
// several; with none live, a single durable is the seat's. A cluster
// credential may not list the hub's consumers, so the local durables every
// instance also holds name the instances and each hub durable is read by name.
func (g *globalTier) ownDurable(ctx context.Context, agentID string) (string, error) {
	filters := []string{g.cfg.inboxSubject()}
	prefix := "mcp_global_" + agentID + "_"
	var names []string
	lctx, lcancel := context.WithTimeout(ctx, floorListTimeout)
	st, err := g.js.Stream(lctx, g.cfg.streamName())
	if err == nil {
		lister := st.ListConsumers(lctx)
		for info := range lister.Info() {
			inst, ok := strings.CutPrefix(info.Name, prefix)
			if ok && inst != "" && !strings.Contains(inst, "_") && sameSubjects(filterSubjects(info.Config), filters) {
				names = append(names, info.Name)
			}
		}
		err = lister.Err()
	}
	lcancel()
	if err != nil {
		names = names[:0]
		nctx, ncancel := context.WithTimeout(ctx, floorListTimeout)
		defer ncancel()
		lst, lerr := g.local.Stream(nctx, "AGENT_INBOX")
		if lerr != nil {
			return "", fmt.Errorf("cannot look up this seat's global durables: listing hub consumers failed (%v) and so did reading the local ones (%v)", err, lerr)
		}
		cn := lst.ConsumerNames(nctx)
		for local := range cn.Name() {
			inst, ok := strings.CutPrefix(local, "mcp_"+agentID+"_")
			if !ok || inst == "" || strings.Contains(inst, "_") {
				continue
			}
			cons, cerr := g.js.Consumer(nctx, g.cfg.streamName(), prefix+inst)
			if cerr != nil {
				continue
			}
			if info := cons.CachedInfo(); info != nil && sameSubjects(filterSubjects(info.Config), filters) {
				names = append(names, prefix+inst)
			}
		}
		if cerr := cn.Err(); cerr != nil {
			return "", fmt.Errorf("cannot look up this seat's global durables: %w", cerr)
		}
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no global durable of %s on stream %s: nothing to advance", agentID, g.cfg.streamName())
	case 1:
		return names[0], nil
	}
	var live []string
	for _, n := range names {
		if _, err := g.kv.Get(ctx, g.cfg.presenceKey(strings.TrimPrefix(n, prefix))); err == nil {
			live = append(live, n)
		}
	}
	if len(live) == 1 {
		return live[0], nil
	}
	return "", fmt.Errorf("%s has %d global durables on stream %s (%d live): %s; refusing to choose between them", agentID, len(names), g.cfg.streamName(), len(live), strings.Join(names, ", "))
}

// formatAdvance is the printout: the stream and durable, the position before
// and after, and the range skipped. A head that could not be read says
// UNKNOWN, never 0.
func formatAdvance(r advanceResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "global-advance: stream %s, durable %s\n", r.Stream, r.Durable)
	fmt.Fprintf(&b, "  before: ack floor %d, %d message(s) pending\n", r.BeforeFloor, r.BeforePending)
	if r.Acked > 0 && r.AfterFloor > r.BeforeFloor {
		fmt.Fprintf(&b, "  skipped: sequences %d to %d on the stream, %d message(s) for this seat acked without reading\n", r.BeforeFloor+1, r.AfterFloor, r.Acked)
	} else {
		fmt.Fprintf(&b, "  skipped: nothing, %d message(s) acked\n", r.Acked)
	}
	fmt.Fprintf(&b, "  after: ack floor %d, %d message(s) pending\n", r.AfterFloor, r.AfterPending)
	if r.HeadKnown {
		fmt.Fprintf(&b, "  stream head when read: sequence %d\n", r.Head)
	} else {
		b.WriteString("  stream head when read: UNKNOWN (the credential cannot read the stream's state); the advance stopped at the backlog the durable showed at the start\n")
	}
	b.WriteString("  read the skipped messages from that stream with the operator's stream tools\n")
	return b.String()
}

// runGlobalAdvanceCmd advances the caller's own global durable. It runs from
// the seat's own environment: --seat, if given, must equal DIRECTOR_AGENT_ID
// (or MARVEL_SESSION, as the shim reads it), and nothing here takes a hub
// credential beyond the seat's own (director#596).
func runGlobalAdvanceCmd(ctx context.Context, cli cliArgs, out, errw io.Writer) int {
	fail := func(format string, a ...any) int {
		_, _ = fmt.Fprintf(errw, "director-mcp global-advance: "+format+"\n", a...)
		return 1
	}
	own := env("DIRECTOR_AGENT_ID", os.Getenv("MARVEL_SESSION"))
	if own == "" {
		return fail("no DIRECTOR_AGENT_ID and no MARVEL_SESSION: run it from the seat's own environment (usage: %s)", globalAdvanceUsage)
	}
	if cli.advSeat != "" && cli.advSeat != own {
		return fail("seat %q is not your own (%s); this command advances only the caller's own durable", cli.advSeat, own)
	}
	se, err := resolveSeatEnv()
	if err != nil {
		return fail("%v", err)
	}
	if se.natsUser != "" {
		_ = os.Setenv("DIRECTOR_NATS_USER", se.natsUser)
		_ = os.Setenv("DIRECTOR_NATS_PASS", se.natsPass)
	}
	gcfg, err := loadGlobalConfig()
	if err != nil {
		return fail("%v", err)
	}
	if gcfg == nil {
		return fail("the global tier is off for this seat (DIRECTOR_GLOBAL_DOMAIN is unset), so it has no global durable")
	}
	url, err := busURL(os.Getenv)
	if err != nil {
		return fail("%v", err)
	}
	nc, _, err := dial(url, se.self)
	if err != nil {
		return fail("connect %s: %v", url, err)
	}
	defer nc.Close()
	js, err := jetstream.NewWithDomain(nc, gcfg.Domain)
	if err != nil {
		return fail("global JetStream domain %q: %v", gcfg.Domain, err)
	}
	kv, err := js.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		return fail("global presence bucket %s in domain %q: %v (%s)", globalPresenceBucket, gcfg.Domain, err, hubHint(*gcfg))
	}
	local, err := jetstream.New(nc)
	if err != nil {
		return fail("local JetStream: %v", err)
	}
	g := &globalTier{cfg: *gcfg, js: js, local: local, kv: kv}
	res, err := advanceGlobalDurable(ctx, g, own, cli.advTo)
	if res.Durable != "" {
		_, _ = io.WriteString(out, formatAdvance(res))
	}
	if err != nil {
		return fail("%v", err)
	}
	return 0
}
