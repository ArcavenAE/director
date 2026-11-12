package main

import (
	"context"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// earlierDurableProbe says whether the seat, its explicit predecessor or an
// earlier generation of its replica has left a durable on the hub stream,
// live or departed, acked or not (director#594). Only a clean "no" lets a new
// durable start at the stream head. It lists the hub stream's consumers; a
// cluster credential may not, and then it reads the local durables every
// instance also holds (mcp_<agent>_<instance> on AGENT_INBOX). A var so tests
// can make the lookup fail.
var earlierDurableProbe = func(ctx context.Context, g *globalTier, agentID, self string) (bool, error) {
	pred := predecessorID(agentID)
	me, hasGen := parseGenID(agentID)
	mine := func(prefix, name string) bool {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			return false
		}
		i := strings.LastIndexByte(rest, '_')
		if i <= 0 || i == len(rest)-1 || rest[i+1:] == self {
			return false
		}
		agent := rest[:i]
		return agent == agentID || (pred != "" && agent == pred) || (hasGen && earlierGeneration(name, prefix, me))
	}
	filters := []string{g.cfg.inboxSubject()}
	lctx, lcancel := context.WithTimeout(ctx, floorListTimeout)
	found, err := g.hubHasEarlier(lctx, mine, filters)
	lcancel()
	if err == nil {
		return found, nil
	}
	nctx, ncancel := context.WithTimeout(ctx, floorListTimeout)
	defer ncancel()
	st, serr := g.local.Stream(nctx, "AGENT_INBOX")
	if serr != nil {
		return false, serr
	}
	names := st.ConsumerNames(nctx)
	for name := range names.Name() {
		if mine("mcp_", name) {
			return true, nil
		}
	}
	return false, names.Err()
}

func (g *globalTier) hubHasEarlier(ctx context.Context, mine func(prefix, name string) bool, filters []string) (bool, error) {
	st, err := g.js.Stream(ctx, g.cfg.streamName())
	if err != nil {
		return false, err
	}
	lister := st.ListConsumers(ctx)
	found := false
	for info := range lister.Info() {
		if mine("mcp_global_", info.Name) && sameSubjects(filterSubjects(info.Config), filters) {
			found = true
		}
	}
	return found, lister.Err()
}

// earlierCount is how many messages the hub stream holds for the seat's inbox
// subject now, for the start note. ok is false when the credential cannot read
// the stream's subject counts, so the note says UNKNOWN and never 0.
func (g *globalTier) earlierCount(ctx context.Context) (n uint64, ok bool) {
	cctx, cancel := context.WithTimeout(ctx, floorListTimeout)
	defer cancel()
	st, err := g.js.Stream(cctx, g.cfg.streamName())
	if err != nil {
		return 0, false
	}
	subj := g.cfg.inboxSubject()
	info, err := st.Info(cctx, jetstream.WithSubjectFilter(subj))
	if err != nil {
		return 0, false
	}
	return info.State.Subjects[subj], true
}
