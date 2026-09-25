// fabtool is the probe instrument for design brief 11 (the leaf fabric). It
// runs against the scratch brokers rig.sh starts; it takes the server URL on
// every call and has no default. It refuses the live fleet ports (see
// liveGuard) unless FABTOOL_LIVE=1 is set, the deliberate opt-in section 5.1
// of the brief needs for the real cutover, so it cannot reach a live broker
// by accident.
//
//	pub     publish N messages with Nats-Msg-Id, plus R repeats of earlier ids
//	verify  read a stream by sequence (never through a consumer, so a
//	        work-queue stream is not drained) and check count, distinctness,
//	        and order
//	mint    operator, accounts, and a scoped signing key with a per-seat
//	        permission template, for P3
//	perm    one JetStream publish under a credential; report stored, refused
//	        by name, or timed out
//	sweep   the section 2.5 sweeper: notify a message's sender when it has
//	        waited past each threshold, once per threshold
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fabtool pub|verify|mint|perm|sweep [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "pub":
		err = cmdPub(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "mint":
		err = cmdMint(os.Args[2:])
	case "perm":
		err = cmdPerm(os.Args[2:])
	case "sweep":
		err = cmdSweep(os.Args[2:])
	case "unread":
		err = cmdUnread(os.Args[2:])
	case "migrate":
		err = cmdMigrate(os.Args[2:])
	case "rollback":
		err = cmdRollback(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fabtool:", err)
		os.Exit(1)
	}
}

func emit(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

// livePorts are the fleet's live broker ports: client, hub client, leaf, and
// the two monitoring ports. rig.sh refuses the same set.
var livePorts = map[string]bool{"4222": true, "4242": true, "7442": true, "8222": true, "8242": true}

func mustURL(u string) error {
	if u == "" {
		return errors.New("-s is required; fabtool has no default server")
	}
	return liveGuard(u)
}

// liveGuard refuses a server list naming any live fleet port, including a URL
// with no port, which the client resolves to 4222. FABTOOL_LIVE=1 lifts it.
func liveGuard(servers string) error {
	if os.Getenv("FABTOOL_LIVE") == "1" {
		return nil
	}
	for _, s := range strings.Split(servers, ",") {
		s = strings.TrimSpace(s)
		if !strings.Contains(s, "://") {
			s = "nats://" + s
		}
		pu, err := url.Parse(s)
		if err != nil {
			return fmt.Errorf("server url %q: %w", s, err)
		}
		port := pu.Port()
		if port == "" {
			port = "4222"
		}
		if livePorts[port] {
			return fmt.Errorf("refusing %s: port %s is a live fleet port (set FABTOOL_LIVE=1 only for the section 5.1 cutover)", pu.Host, port)
		}
	}
	return nil
}

func cmdPub(args []string) error {
	fs := flag.NewFlagSet("pub", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	subj := fs.String("subj", "", "subject")
	n := fs.Int("n", 1, "distinct messages")
	repeat := fs.Int("repeat", 0, "repeats of the first R ids")
	prefix := fs.String("prefix", "m", "message id prefix")
	sender := fs.String("sender", "", "Fab-Sender header (where notices go)")
	fs.Parse(args)
	if err := mustURL(*url); err != nil {
		return err
	}
	nc, err := nats.Connect(*url)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	var stored, dup, failed int
	var firstErr string
	send := func(i int) {
		m := nats.NewMsg(*subj)
		m.Header.Set(jetstream.MsgIDHeader, fmt.Sprintf("%s-%05d", *prefix, i))
		if *sender != "" {
			m.Header.Set("Fab-Sender", *sender)
		}
		m.Data = []byte(strconv.Itoa(i))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ack, err := js.PublishMsg(ctx, m)
		switch {
		case err != nil:
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
		case ack.Duplicate:
			dup++
		default:
			stored++
		}
	}
	for i := 0; i < *n; i++ {
		send(i)
	}
	for i := 0; i < *repeat; i++ {
		send(i)
	}
	emit(map[string]any{"subject": *subj, "stored": stored, "duplicate": dup, "failed": failed, "first_error": firstErr})
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	stream := fs.String("stream", "", "stream")
	filter := fs.String("filter", "", "subject prefix to count (empty counts all)")
	prefix := fs.String("prefix", "", "message id prefix to count")
	fs.Parse(args)
	if err := mustURL(*url); err != nil {
		return err
	}
	nc, err := nats.Connect(*url)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx := context.Background()
	st, err := js.Stream(ctx, *stream)
	if err != nil {
		return err
	}
	info, err := st.Info(ctx)
	if err != nil {
		return err
	}
	seen := map[string]int{}
	count, dupIDs, outOfOrder := 0, 0, 0
	last := -1
	subjects := map[string]int{}
	for seq := info.State.FirstSeq; seq <= info.State.LastSeq && info.State.Msgs > 0; seq++ {
		m, err := st.GetMsg(ctx, seq)
		if err != nil {
			continue // deleted (work-queue ack) or interior gap
		}
		if *filter != "" && !strings.HasPrefix(m.Subject, *filter) {
			continue
		}
		id := m.Header.Get(jetstream.MsgIDHeader)
		if *prefix != "" && !strings.HasPrefix(id, *prefix) {
			continue
		}
		count++
		subjects[m.Subject]++
		seen[id]++
		if seen[id] > 1 {
			dupIDs++
		}
		if v, err := strconv.Atoi(string(m.Data)); err == nil {
			if v < last {
				outOfOrder++
			}
			last = v
		}
	}
	emit(map[string]any{"stream": *stream, "stream_msgs": info.State.Msgs, "counted": count, "distinct_ids": len(seen), "duplicate_ids": dupIDs, "out_of_order": outOfOrder, "subjects": subjects})
	return nil
}

// cmdMint writes an operator-mode server fragment and per-seat creds. The
// scoped signing key carries ONE permission template; each user JWT carries
// its seat as tags, so the same template expands per seat (brief 11 2.4).
func cmdMint(args []string) error {
	fs := flag.NewFlagSet("mint", flag.ExitOnError)
	dir := fs.String("dir", "", "output directory")
	fs.Parse(args)
	if *dir == "" {
		return errors.New("-dir is required")
	}
	okp, _ := nkeys.CreateOperator()
	opub, _ := okp.PublicKey()
	oc := jwt.NewOperatorClaims(opub)
	oc.Name = "FABOP"
	sysKP, _ := nkeys.CreateAccount()
	sysPub, _ := sysKP.PublicKey()
	oc.SystemAccount = sysPub
	ojwt, err := oc.Encode(okp)
	if err != nil {
		return err
	}
	sc := jwt.NewAccountClaims(sysPub)
	sc.Name = "SYS"
	sysJWT, err := sc.Encode(okp)
	if err != nil {
		return err
	}

	akp, _ := nkeys.CreateAccount()
	apub, _ := akp.PublicKey()
	ac := jwt.NewAccountClaims(apub)
	ac.Name = "FAB"
	ac.Limits.JetStreamLimits.DiskStorage = -1
	ac.Limits.JetStreamLimits.MemoryStorage = -1
	skKP, _ := nkeys.CreateAccount()
	skPub, _ := skKP.PublicKey()
	scope := jwt.NewUserScope()
	scope.Key = skPub
	scope.Role = "seat"
	scope.Template.Pub.Allow.Add(
		"agent.{{tag(cluster)}}.{{tag(ws)}}.{{tag(team)}}.>",
		"out.*.{{tag(ws)}}.{{tag(team)}}.>",
		"agent.{{tag(cluster)}}.{{tag(ws)}}.{{tag(supteam)}}.{{tag(sup)}}.inbox",
		"out.director.inbox",
	)
	scope.Template.Sub.Allow.Add("_INBOX.>")
	ac.SigningKeys.AddScopedSigner(scope)
	ajwt, err := ac.Encode(okp)
	if err != nil {
		return err
	}

	seats := map[string][]string{
		"worker":  {"cluster:pa", "ws:w", "team:alpha", "id:x", "supteam:lead", "sup:boss"},
		"worker2": {"cluster:pa", "ws:w", "team:beta", "id:y", "supteam:lead", "sup:boss"},
	}
	for name, tags := range seats {
		ukp, _ := nkeys.CreateUser()
		upub, _ := ukp.PublicKey()
		uc := jwt.NewUserClaims(upub)
		uc.Name = name
		uc.IssuerAccount = apub
		uc.SetScoped(true)
		uc.Tags.Add(tags...)
		ujwt, err := uc.Encode(skKP)
		if err != nil {
			return err
		}
		seed, _ := ukp.Seed()
		creds, err := jwt.FormatUserConfig(ujwt, seed)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*dir, name+".creds"), creds, 0o600); err != nil {
			return err
		}
	}
	// A system-account user so run.sh can stand streams up without a seat's grants.
	for _, pair := range []struct {
		name string
		kp   nkeys.KeyPair
		acct string
	}{{"admin", akp, apub}} {
		ukp, _ := nkeys.CreateUser()
		upub, _ := ukp.PublicKey()
		uc := jwt.NewUserClaims(upub)
		uc.Name = pair.name
		ujwt, err := uc.Encode(pair.kp)
		if err != nil {
			return err
		}
		seed, _ := ukp.Seed()
		creds, _ := jwt.FormatUserConfig(ujwt, seed)
		os.WriteFile(filepath.Join(*dir, pair.name+".creds"), creds, 0o600)
	}
	conf := fmt.Sprintf("operator: %q\nsystem_account: %s\nresolver: MEMORY\nresolver_preload: {\n  %s: %q\n  %s: %q\n}\n", ojwt, sysPub, sysPub, sysJWT, apub, ajwt)
	return os.WriteFile(filepath.Join(*dir, "operator.conf"), []byte(conf), 0o600)
}

func cmdPerm(args []string) error {
	fs := flag.NewFlagSet("perm", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	creds := fs.String("creds", "", "creds file")
	subj := fs.String("subj", "", "subject")
	fs.Parse(args)
	if err := mustURL(*url); err != nil {
		return err
	}
	var mu sync.Mutex
	var async []string
	nc, err := nats.Connect(*url, nats.UserCredentials(*creds), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		mu.Lock()
		async = append(async, e.Error())
		mu.Unlock()
	}))
	if err != nil {
		return err
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	t0 := time.Now()
	ack, err := js.Publish(ctx, *subj, []byte("perm"))
	el := time.Since(t0)
	time.Sleep(200 * time.Millisecond)
	out := map[string]any{"subject": *subj, "elapsed_ms": el.Milliseconds()}
	switch {
	case err == nil:
		out["result"] = "stored"
		out["stream"] = ack.Stream
	case errors.Is(err, context.DeadlineExceeded):
		out["result"] = "timeout"
		out["error"] = err.Error()
	default:
		out["result"] = "refused"
		out["error"] = err.Error()
	}
	mu.Lock()
	out["async_errors"] = async
	mu.Unlock()
	emit(out)
	return nil
}

func cmdSweep(args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	url := fs.String("s", "", "server url")
	stream := fs.String("stream", "", "inbox stream")
	after := fs.String("after", "", "comma-separated notice ages, e.g. 60s,120s")
	every := fs.Duration("every", 5*time.Second, "scan interval")
	dur := fs.Duration("for", time.Minute, "how long to run")
	fs.Parse(args)
	if err := mustURL(*url); err != nil {
		return err
	}
	var thresholds []time.Duration
	for _, s := range strings.Split(*after, ",") {
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return err
		}
		thresholds = append(thresholds, d)
	}
	nc, err := nats.Connect(*url)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	ctx := context.Background()
	st, err := js.Stream(ctx, *stream)
	if err != nil {
		return err
	}
	noticed := map[string]int{} // msg id -> thresholds already noticed
	end := time.Now().Add(*dur)
	for time.Now().Before(end) {
		info, err := st.Info(ctx)
		if err == nil && info.State.Msgs > 0 {
			for seq := info.State.FirstSeq; seq <= info.State.LastSeq; seq++ {
				m, err := st.GetMsg(ctx, seq)
				if err != nil {
					continue
				}
				id := m.Header.Get(jetstream.MsgIDHeader)
				sender := m.Header.Get("Fab-Sender")
				age := time.Since(m.Time)
				for noticed[id] < len(thresholds) && age >= thresholds[noticed[id]] {
					level := noticed[id]
					noticed[id]++
					if sender == "" {
						continue
					}
					n := nats.NewMsg(sender)
					n.Header.Set(jetstream.MsgIDHeader, fmt.Sprintf("notice-%s-%d", id, level))
					n.Data = []byte(fmt.Sprintf("undelivered %s: %s to %s", thresholds[level], id, m.Subject))
					pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
					_, perr := js.PublishMsg(pctx, n)
					cancel()
					emit(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "notice": level + 1, "id": id, "age_s": int(age.Seconds()), "to": sender, "err": fmt.Sprint(perr)})
				}
			}
		}
		time.Sleep(*every)
	}
	return nil
}
