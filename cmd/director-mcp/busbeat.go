package main

// aae-orc-z63a4: the shim feeds marvel a bus-liveness beat.
//
// A seat's statusline keeps marvel's heartbeat fresh whether or not the seat's
// bus works, so a seat with a dead bus read as healthy. The shim
// sends marvel one `bus.beat` on its existing presence timer, only after that
// tick's local presence Put returned nil, so the beat means "this shim reached
// the bus and its presence bucket within the last tick". The feed is optional:
// without MARVEL_SOCKET, MARVEL_SESSION or a token it is off, with one log line
// that names the absent keys, and an older marvel that answers "unknown method"
// turns it off for the life of the process.
//
// director does not import marvel (the independence rule). The request is
// re-implemented here and pinned by testdata/bus-beat-request.json, which
// marvel pins byte for byte. The token never reaches a log or an error string.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	busBeatMethod  = "bus.beat"
	busBeatSource  = "director-shim"
	busBeatTimeout = 2 * time.Second
	// the reply is one small JSON object; anything larger is not marvel's
	busBeatMaxReply = 64 << 10

	marvelSocketEnv    = "MARVEL_SOCKET"
	marvelSessionEnv   = "MARVEL_SESSION"
	heartbeatTokenEnv  = "MARVEL_HEARTBEAT_TOKEN"
	heartbeatTokenFile = "MARVEL_HEARTBEAT_TOKEN_FILE"
)

// beatParams and beatRequest are marvel's wire shape: a daemonrpc.Request whose
// params carry these three fields, in this order.
type beatParams struct {
	SessionKey   string `json:"session_key"`
	SessionToken string `json:"session_token"`
	Source       string `json:"source"`
}

type beatRequest struct {
	Method string     `json:"method"`
	Params beatParams `json:"params"`
}

// beatReply is the part of marvel's reply the shim reads.
type beatReply struct {
	Error    string `json:"error,omitempty"`
	Starting bool   `json:"starting,omitempty"`
}

// marvelToken is marvel's rule (internal/api/heartbeat.go, HeartbeatTokenFrom),
// re-implemented: the file named by MARVEL_HEARTBEAT_TOKEN_FILE wins when set,
// and an unreadable named file gives an empty token, never the variable.
func marvelToken(getenv func(string) string) string {
	if path := getenv(heartbeatTokenFile); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(b), "\r\n")
	}
	return getenv(heartbeatTokenEnv)
}

// busBeat sends the beat. A nil *busBeat is the feed being off.
type busBeat struct {
	getenv func(string) string
	logf   func(string, ...any)
	dial   func(ctx context.Context, socket string) (net.Conn, error)

	mu      sync.Mutex
	off     bool   // an older marvel answered unknown method
	failing bool   // the last send failed, for the transition-only log
	lastMsg string // what the failure said, with the token removed
}

// newBusBeat returns the feed, or nil when it is off. It logs one line naming
// which keys were absent, and never a value.
func newBusBeat(getenv func(string) string, logf func(string, ...any)) *busBeat {
	var absent []string
	if getenv(marvelSocketEnv) == "" {
		absent = append(absent, marvelSocketEnv)
	}
	if getenv(marvelSessionEnv) == "" {
		absent = append(absent, marvelSessionEnv)
	}
	if marvelToken(getenv) == "" {
		if getenv(heartbeatTokenFile) != "" {
			absent = append(absent, heartbeatTokenFile+" (unreadable or empty)")
		} else {
			absent = append(absent, heartbeatTokenEnv+" or "+heartbeatTokenFile)
		}
	}
	if len(absent) > 0 {
		if logf != nil {
			logf("bus beat to marvel is off: not set: %s", strings.Join(absent, ", "))
		}
		return nil
	}
	return &busBeat{
		getenv: getenv,
		logf:   logf,
		dial: func(ctx context.Context, socket string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
}

func (bb *busBeat) log(format string, a ...any) {
	if bb.logf != nil {
		bb.logf(format, a...)
	}
}

// send makes one beat: one request on a fresh connection, 2 s at most. It
// returns nothing; every outcome is a log line or silence.
func (bb *busBeat) send(ctx context.Context) {
	if bb == nil {
		return
	}
	bb.mu.Lock()
	off := bb.off
	bb.mu.Unlock()
	if off {
		return
	}
	token := marvelToken(bb.getenv)
	if token == "" {
		bb.fail("the heartbeat token is empty or unreadable", token)
		return
	}
	reply, err := bb.exchange(ctx, beatRequest{
		Method: busBeatMethod,
		Params: beatParams{SessionKey: bb.getenv(marvelSessionEnv), SessionToken: token, Source: busBeatSource},
	})
	switch {
	case err != nil:
		bb.fail(err.Error(), token)
	case strings.HasPrefix(reply.Error, "unknown method"):
		bb.mu.Lock()
		bb.off = true
		bb.mu.Unlock()
		bb.log("bus beat to marvel is off for this process: marvel does not know %s (an older daemon)", busBeatMethod)
	case reply.Starting:
		// the daemon is adopting: transient, and a verdict on nothing
	case reply.Error != "":
		bb.fail("marvel refused the beat: "+reply.Error, token)
	default:
		bb.ok()
	}
}

// exchange dials, sends one request line and reads one reply, all under 2 s.
func (bb *busBeat) exchange(ctx context.Context, req beatRequest) (beatReply, error) {
	cctx, cancel := context.WithTimeout(ctx, busBeatTimeout)
	defer cancel()
	conn, err := bb.dial(cctx, bb.getenv(marvelSocketEnv))
	if err != nil {
		return beatReply{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(busBeatTimeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return beatReply{}, err
	}
	var rep beatReply
	if err := json.NewDecoder(io.LimitReader(conn, busBeatMaxReply)).Decode(&rep); err != nil {
		if errors.Is(err, io.EOF) {
			return beatReply{}, errors.New("marvel closed the connection without a reply")
		}
		return beatReply{}, err
	}
	return rep, nil
}

// fail logs a failure on the transition only, the way reportGlobalWarn does,
// and retries on the next tick. The token is removed from the text first.
func (bb *busBeat) fail(msg, token string) {
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "<token>")
	}
	bb.mu.Lock()
	was := bb.failing
	bb.failing, bb.lastMsg = true, msg
	bb.mu.Unlock()
	if !was {
		bb.log("WARNING: bus beat to marvel failed, retrying each tick: %s", msg)
	}
}

func (bb *busBeat) ok() {
	bb.mu.Lock()
	was := bb.failing
	bb.failing, bb.lastMsg = false, ""
	bb.mu.Unlock()
	if was {
		bb.log("bus beat to marvel recovered")
	}
}
