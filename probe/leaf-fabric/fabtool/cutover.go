package main

// The flag-day cutover from the single-broker AGENT_INBOX to the per-cluster
// INBOX on the agent.<cluster>. root (brief 11 section 5). Three commands:
//
//	unread    per address: messages above the highest ack floor of any durable
//	          filtering that address (a message any instance acked was read)
//	migrate   republish every unread legacy message onto the cluster-qualified
//	          subject, same Nats-Msg-Id, and write the id -> legacy sequence map
//	rollback  after the new INBOX is parked and AGENT_INBOX's subjects are
//	          restored: delete the legacy copy of every migrated message that
//	          was read after cutover, and republish every post-cutover message
//	          still unread onto its legacy subject

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const migratedHeader = "Fab-Migrated-From-Seq"

type legacyMsg struct {
	seq     uint64
	subject string
	msg     *jetstream.RawStreamMsg
}

// unreadLegacy returns, per legacy subject, the messages no durable has acked.
func unreadLegacy(ctx context.Context, js jetstream.JetStream, stream string) (map[string][]legacyMsg, error) {
	st, err := js.Stream(ctx, stream)
	if err != nil {
		return nil, err
	}
	floor := map[string]uint64{} // filter subject -> highest ack floor
	lister := st.ListConsumers(ctx)
	for ci := range lister.Info() {
		f := ci.Config.FilterSubject
		if ci.AckFloor.Stream > floor[f] {
			floor[f] = ci.AckFloor.Stream
		} else if _, ok := floor[f]; !ok {
			floor[f] = 0
		}
	}
	if err := lister.Err(); err != nil {
		return nil, err
	}
	info, err := st.Info(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]legacyMsg{}
	for seq := info.State.FirstSeq; info.State.Msgs > 0 && seq <= info.State.LastSeq; seq++ {
		m, err := st.GetMsg(ctx, seq)
		if err != nil {
			continue
		}
		if seq <= floor[m.Subject] {
			continue // some instance of this address acked it
		}
		out[m.Subject] = append(out[m.Subject], legacyMsg{seq, m.Subject, m})
	}
	return out, nil
}

// qualify maps a legacy subject onto the cluster-qualified grammar:
// agent.<ws>.<team>.<id>.inbox -> agent.<cluster>.<ws>.<team>.<id>.inbox, and
// the role form likewise. Anything else is refused, never guessed.
func qualify(cluster, legacy string) (string, error) {
	t := strings.Split(legacy, ".")
	if len(t) == 5 && t[0] == "agent" && t[4] == "inbox" {
		return "agent." + cluster + "." + strings.Join(t[1:], "."), nil
	}
	if len(t) == 6 && t[0] == "agent" && t[3] == "role" && t[5] == "inbox" {
		return "agent." + cluster + "." + strings.Join(t[1:], "."), nil
	}
	return "", fmt.Errorf("not a legacy inbox subject: %q", legacy)
}

func unqualify(cluster, subj string) (string, error) {
	p := "agent." + cluster + "."
	if !strings.HasPrefix(subj, p) {
		return "", fmt.Errorf("not on cluster %s: %q", cluster, subj)
	}
	return "agent." + strings.TrimPrefix(subj, p), nil
}

// copyHeaders carries the envelope's headers and Nats-Msg-Id, and drops the
// metadata a stream read adds (Nats-Subject, Nats-Sequence, Nats-Time-Stamp,
// Nats-Stream, Nats-Last-Sequence). Copying those onto a republished message
// makes a later direct read report the old subject.
func copyHeaders(dst, src nats.Header) {
	for k, v := range src {
		if strings.HasPrefix(k, "Nats-") && k != jetstream.MsgIDHeader {
			continue
		}
		dst[k] = v
	}
}

func connectJS(url string) (*nats.Conn, jetstream.JetStream, error) {
	if err := mustURL(url); err != nil {
		return nil, nil, err
	}
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, nil, err
	}
	js, _ := jetstream.New(nc)
	return nc, js, nil
}

func cmdUnread(args []string) error {
	fs := flag.NewFlagSet("unread", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	stream := fs.String("stream", "AGENT_INBOX", "legacy stream")
	fs.Parse(args)
	nc, js, err := connectJS(*url)
	if err != nil {
		return err
	}
	defer nc.Close()
	u, err := unreadLegacy(context.Background(), js, *stream)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for s, ms := range u {
		counts[s] = len(ms)
	}
	emit(map[string]any{"stream": *stream, "unread": counts})
	return nil
}

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	from := fs.String("from", "AGENT_INBOX", "legacy stream")
	cluster := fs.String("cluster", "", "this cluster's token")
	mapFile := fs.String("map", "", "write the id -> legacy sequence map here")
	fs.Parse(args)
	if *cluster == "" || *mapFile == "" {
		return errors.New("-cluster and -map are required")
	}
	nc, js, err := connectJS(*url)
	if err != nil {
		return err
	}
	defer nc.Close()
	ctx := context.Background()
	u, err := unreadLegacy(ctx, js, *from)
	if err != nil {
		return err
	}
	idmap := map[string]uint64{}
	moved, failed := 0, 0
	var firstErr string
	for _, ms := range u {
		for _, lm := range ms {
			dst, err := qualify(*cluster, lm.subject)
			if err != nil {
				return err
			}
			m := nats.NewMsg(dst)
			copyHeaders(m.Header, lm.msg.Header)
			id := m.Header.Get(jetstream.MsgIDHeader)
			if id == "" {
				id = fmt.Sprintf("legacy-%d", lm.seq)
				m.Header.Set(jetstream.MsgIDHeader, id)
			}
			m.Header.Set(migratedHeader, fmt.Sprint(lm.seq))
			m.Data = lm.msg.Data
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, err = js.PublishMsg(pctx, m)
			cancel()
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = err.Error()
				}
				continue
			}
			idmap[id] = lm.seq
			moved++
		}
	}
	b, _ := json.MarshalIndent(idmap, "", " ")
	if err := os.WriteFile(*mapFile, b, 0o600); err != nil {
		return err
	}
	emit(map[string]any{"migrated": moved, "failed": failed, "first_error": firstErr, "map": *mapFile})
	return nil
}

func cmdRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	newStream := fs.String("new", "INBOX", "parked new stream")
	legacy := fs.String("legacy", "AGENT_INBOX", "restored legacy stream")
	cluster := fs.String("cluster", "", "this cluster's token")
	mapFile := fs.String("map", "", "map written by migrate")
	fs.Parse(args)
	if *cluster == "" || *mapFile == "" {
		return errors.New("-cluster and -map are required")
	}
	b, err := os.ReadFile(*mapFile)
	if err != nil {
		return err
	}
	idmap := map[string]uint64{}
	if err := json.Unmarshal(b, &idmap); err != nil {
		return err
	}
	nc, js, err := connectJS(*url)
	if err != nil {
		return err
	}
	defer nc.Close()
	ctx := context.Background()
	ns, err := js.Stream(ctx, *newStream)
	if err != nil {
		return err
	}
	ls, err := js.Stream(ctx, *legacy)
	if err != nil {
		return err
	}
	info, err := ns.Info(ctx)
	if err != nil {
		return err
	}
	stillUnread := map[string]bool{}
	republished, deleted := 0, 0
	for seq := info.State.FirstSeq; info.State.Msgs > 0 && seq <= info.State.LastSeq; seq++ {
		m, err := ns.GetMsg(ctx, seq)
		if err != nil {
			continue // acked in the new era (work-queue removed it)
		}
		id := m.Header.Get(jetstream.MsgIDHeader)
		stillUnread[id] = true
		if m.Header.Get(migratedHeader) != "" {
			continue // its legacy original is still there and still unread
		}
		dst, err := unqualify(*cluster, m.Subject)
		if err != nil {
			return err
		}
		out := nats.NewMsg(dst)
		copyHeaders(out.Header, m.Header)
		out.Data = m.Data
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err = js.PublishMsg(pctx, out)
		cancel()
		if err != nil {
			return fmt.Errorf("republish %s: %w", id, err)
		}
		republished++
	}
	for id, lseq := range idmap {
		if stillUnread[id] {
			continue
		}
		// Migrated, then read after cutover: remove the legacy original so
		// the rollback does not deliver it a second time.
		if err := ls.DeleteMsg(ctx, lseq); err != nil {
			return fmt.Errorf("delete legacy seq %d (%s): %w", lseq, id, err)
		}
		deleted++
	}
	emit(map[string]any{"republished_post_cutover": republished, "deleted_read_after_cutover": deleted})
	return nil
}
