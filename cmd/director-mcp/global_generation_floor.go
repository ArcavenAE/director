package main

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// agentGen splits an agent id of the form <base>-g<generation>-<replica>, the
// shape marvel names a seat by. An id that does not end that way has no
// generation and so no earlier generation to take a position from.
var agentGen = regexp.MustCompile(`^(.+)-g([0-9]+)-([0-9]+)$`)

type genID struct {
	base         string
	gen, replica int
}

func parseGenID(agentID string) (genID, bool) {
	m := agentGen.FindStringSubmatch(agentID)
	if m == nil {
		return genID{}, false
	}
	gen, err1 := strconv.Atoi(m[2])
	rep, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil {
		return genID{}, false
	}
	return genID{base: m[1], gen: gen, replica: rep}, true
}

// earlierGeneration reports whether durable (named prefix+<agent id>_<instance>)
// belongs to the same team, role and replica as me, from an earlier generation.
// The instance is a ULID and holds no '_', so the last '_' ends the agent id
// even when the id itself holds one.
func earlierGeneration(durable, prefix string, me genID) bool {
	rest, ok := strings.CutPrefix(durable, prefix)
	if !ok {
		return false
	}
	i := strings.LastIndexByte(rest, '_')
	if i <= 0 || i == len(rest)-1 {
		return false
	}
	other, ok := parseGenID(rest[:i])
	return ok && other.base == me.base && other.replica == me.replica && other.gen < me.gen
}

// generationFloor is the highest ack floor among the departed durables of this
// seat's earlier generations on the hub stream (the cluster's own stream, so
// the same cluster), and the name of the durable it came from. It is used only
// when the seat has no floor of its own and no predecessor link: a respawn
// after a crash or a team apply gets a new generation in its agent id, so
// seatFloor's prefix never names the durable the old generation left.
//
// Only the same replica counts. Every replica of a role filters the same role
// inbox and reads its own copy of each message, so a sibling replica's position
// says nothing about what this one has read. A live durable is skipped, as in
// seatAckFloor. Best effort: any failure returns no floor, which is the replay
// the seat would have had.
func (g *globalTier) generationFloor(ctx context.Context, agentID, self string) (uint64, string) {
	me, ok := parseGenID(agentID)
	if !ok {
		return 0, ""
	}
	filters := []string{g.cfg.inboxSubject()}
	live := func(c context.Context) func(string) bool {
		return func(inst string) bool {
			_, err := g.kv.Get(c, g.cfg.presenceKey(inst))
			return err == nil
		}
	}
	var best uint64
	var name string
	take := func(durable string, floor uint64) {
		if floor > best {
			best, name = floor, durable
		}
	}
	lctx, lcancel := context.WithTimeout(ctx, floorListTimeout)
	st, err := g.js.Stream(lctx, g.cfg.streamName())
	if err == nil {
		lister := st.ListConsumers(lctx)
		isLive := live(lctx)
		for info := range lister.Info() {
			if !earlierGeneration(info.Name, "mcp_global_", me) || !sameSubjects(filterSubjects(info.Config), filters) {
				continue
			}
			if isLive(info.Name[strings.LastIndexByte(info.Name, '_')+1:]) {
				continue
			}
			take(info.Name, info.AckFloor.Stream)
		}
		err = lister.Err()
	}
	lcancel()
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		// A cluster credential may not list the hub's consumers. Read the
		// departed instances' hub durables by name, found through the local
		// durables every instance also holds.
		nctx, ncancel := context.WithTimeout(ctx, floorListTimeout)
		defer ncancel()
		names, nerr := g.generationFloorByName(nctx, me, self, filters, live(nctx))
		for d, f := range names {
			take(d, f)
		}
		if nerr != nil && best == 0 {
			return 0, ""
		}
	}
	if best > 0 {
		log.Printf("director-mcp: global durable took over %s at floor %d", name, best)
	}
	return best, name
}

// generationFloorByName is the by-name read for generationFloor: it lists the
// local durables of the earlier generations (listing local consumers is
// allowed) and reads each one's hub durable by name.
func (g *globalTier) generationFloorByName(ctx context.Context, me genID, self string, filters []string, live func(string) bool) (map[string]uint64, error) {
	st, err := g.local.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		return nil, err
	}
	out := map[string]uint64{}
	names := st.ConsumerNames(ctx)
	for local := range names.Name() {
		if !earlierGeneration(local, "mcp_", me) {
			continue
		}
		rest := strings.TrimPrefix(local, "mcp_")
		i := strings.LastIndexByte(rest, '_')
		instance := rest[i+1:]
		if instance == self || live(instance) {
			continue
		}
		durable := "mcp_global_" + rest
		cons, err := g.js.Consumer(ctx, g.cfg.streamName(), durable)
		if errors.Is(err, jetstream.ErrConsumerNotFound) {
			continue
		}
		if err != nil {
			return out, err
		}
		info := cons.CachedInfo()
		if info == nil || !sameSubjects(filterSubjects(info.Config), filters) {
			continue
		}
		out[durable] = info.AckFloor.Stream
	}
	return out, names.Err()
}

// donorAgent is the agent id inside a hub durable name, for the resume note.
func donorAgent(durable string) string {
	rest := strings.TrimPrefix(durable, "mcp_global_")
	return rest[:strings.LastIndexByte(rest, '_')]
}
