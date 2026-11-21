package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// A relay entry for a send with an ask records the task it served (the ask id,
// the fleet's form of a2a.task.id) and the session that took it, as marvel keys
// a session: <workspace>/<name>. For a role recipient no session has taken the
// send yet, so the entry says unresolved and the send is remembered here until
// the first reply, or report_status, that names it arrives; that reply appends
// a second entry naming the session. An agent recipient is named at send time
// and is never remembered.

const sessionUnresolved = "unresolved"

// relaySession is the session a send's entry records. The workspace is the one
// the send was routed to, and the name is the agent id of an agent://<team>/<id>
// address. Any other address (a role, or an agent whose workspace is unknown)
// is unresolved.
func relaySession(to, workspace string) string {
	rest, ok := strings.CutPrefix(to, "agent://")
	if !ok || workspace == "" {
		return sessionUnresolved
	}
	i := strings.LastIndex(rest, "/")
	if i < 0 || i == len(rest)-1 {
		return sessionUnresolved
	}
	return workspace + "/" + rest[i+1:]
}

// unresolvedRec is one line of the unresolved-send store: a send that waits for
// its first reply, or a note that one was answered.
type unresolvedRec struct {
	MessageID string `json:"message_id,omitempty"`
	Ask       string `json:"ask,omitempty"`
	To        string `json:"to,omitempty"`
	Resolved  string `json:"resolved,omitempty"`
}

func unresolvedPath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "relay-unresolved.jsonl"), nil
}

func appendUnresolved(rec unresolvedRec) error {
	path, err := unresolvedPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	_, err = f.Write(append(b, '\n'))
	return err
}

// rememberUnresolved records a send whose session is not known yet.
func rememberUnresolved(messageID, ask, to string) error {
	return appendUnresolved(unresolvedRec{MessageID: messageID, Ask: ask, To: to})
}

// pendingSend returns the remembered send with this message id that no reply has
// resolved, or false. A missing store is an empty one; torn lines are skipped.
func pendingSend(messageID string) (unresolvedRec, bool) {
	path, err := unresolvedPath()
	if err != nil {
		return unresolvedRec{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return unresolvedRec{}, false
	}
	defer f.Close()
	var found unresolvedRec
	ok := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var r unresolvedRec
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.MessageID == messageID {
			found, ok = r, true
		}
		if r.Resolved == messageID {
			return unresolvedRec{}, false
		}
	}
	return found, ok
}

// resolveRelaySession is called with each message a wait hands over. When it is
// the first reply to a send whose session was unresolved, it appends a relay
// entry that names the replier's session, <workspace>/<agent id>, and notes the
// send as resolved so a later reply appends nothing. It never fails the wait:
// a relay-log that cannot write leaves the send pending, and the next reply
// tries again.
func resolveRelaySession(ctx context.Context, env *Envelope) {
	if env == nil || env.InReplyTo == "" || env.Sender.AgentID == "" || env.Sender.Workspace == "" {
		return
	}
	rec, ok := pendingSend(env.InReplyTo)
	if !ok {
		return
	}
	session := env.Sender.Workspace + "/" + env.Sender.AgentID
	text := "session resolved for message " + rec.MessageID + ": " + session + " answered it"
	if _, _, err := relayAppend(ctx, relayArgs{ask: rec.Ask, kind: "relay", to: rec.To, session: session,
		outcome: "resolves the session of message " + rec.MessageID + "; reply " + env.MessageID, text: text}); err != nil {
		return
	}
	_ = appendUnresolved(unresolvedRec{Resolved: rec.MessageID})
}
