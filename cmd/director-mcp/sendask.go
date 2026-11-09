package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// send_message takes an optional ask (an intake id, I-<n>) and kind. When the
// send is accepted, the shim has relay-log write the entry, so the entry is the
// one `relay-log append --ask` writes by hand and the step that was skipped under
// load cannot be. relay-log owns the numbering, the stamp and the lock; the shim
// only calls it. A send without ask never reaches this file.

var askIDPattern = regexp.MustCompile(`^I-[0-9]+$`)

// relayKinds are the kinds relay-log accepts, in its own order.
var relayKinds = []string{"relay", "ruling", "build", "research", "question", "answer"}

// relayLogTimeout bounds the relay-log call. The send has already left by then.
const relayLogTimeout = 5 * time.Second

// checkSendAsk refuses, before anything is published, an ask or kind that cannot
// be written to the relay log: a malformed or unrecorded ask id, a kind with no
// ask, a kind relay-log would not take, or a broadcast address (an entry names
// one recipient). With neither field it is a no-op and reads nothing.
func checkSendAsk(ask, kind, to string) error {
	if ask == "" && kind == "" {
		return nil
	}
	if ask == "" {
		return errors.New("send_message: kind needs ask; nothing sent")
	}
	if !askIDPattern.MatchString(ask) {
		return fmt.Errorf("send_message: ask %q is not an ask id (want I-<n>); nothing sent", ask)
	}
	if kind != "" {
		ok := false
		for _, k := range relayKinds {
			ok = ok || k == kind
		}
		if !ok {
			return fmt.Errorf("send_message: kind %q is not one of %s; nothing sent", kind, strings.Join(relayKinds, ", "))
		}
	}
	if strings.HasPrefix(to, "broadcast://") {
		return errors.New("send_message: ask is for one recipient; a broadcast address is not allowed; nothing sent")
	}
	dir, err := stateDir()
	if err != nil {
		return fmt.Errorf("send_message: cannot check ask %q: %v; nothing sent", ask, err)
	}
	known, err := askRecorded(filepath.Join(dir, "asks.jsonl"), ask)
	if err != nil {
		return fmt.Errorf("send_message: cannot check ask %q: %v; nothing sent", ask, err)
	}
	if !known {
		return fmt.Errorf("send_message: ask %q is not recorded in asks.jsonl; nothing sent", ask)
	}
	return nil
}

// askRecorded reports whether asks.jsonl holds a line whose ask_id is id. A missing
// file is an empty record, so every id is unknown; torn lines are skipped, as dws does.
func askRecorded(path, id string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e struct {
			AskID string `json:"ask_id"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.AskID == id {
			return true, nil
		}
	}
	return false, sc.Err()
}

// sendRelayFields reads ask and kind from the raw send arguments. toolSend reads
// them as it reads fail_fast, so buildSendEnvelope keeps returning the envelope only.
func sendRelayFields(raw json.RawMessage) (ask, kind string) {
	var a struct {
		Ask  string `json:"ask"`
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(raw, &a)
	if a.Ask != "" && a.Kind == "" {
		a.Kind = "relay"
	}
	return a.Ask, a.Kind
}

// directorHome is where the director scripts are installed: DIRECTOR_HOME, else ~/.director.
func directorHome() string {
	if d := os.Getenv("DIRECTOR_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".director")
}

var relayHeading = regexp.MustCompile(`^## (R-[0-9]+) `)

// writeRelayEntry has relay-log append the entry for an accepted send and returns
// its number (R-<n>). The text goes in on stdin, verbatim; the message id rides on
// the Outcome line.
func writeRelayEntry(ctx context.Context, ask, kind, to, text, messageID string) (string, error) {
	home := directorHome()
	if home == "" {
		return "", errors.New("no DIRECTOR_HOME and no home directory")
	}
	bin := filepath.Join(home, "bin", "relay-log")
	ctx, cancel := context.WithTimeout(ctx, relayLogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "append", "--to", to, "--ask", ask, "--kind", kind,
		"--outcome", "sent; message "+messageID, "--text-file", "-")
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return "", errors.New(msg)
	}
	m := relayHeading.FindStringSubmatch(strings.TrimSpace(stdout.String()))
	if m == nil {
		return "", fmt.Errorf("relay-log printed no entry heading: %q", strings.TrimSpace(stdout.String()))
	}
	return m[1], nil
}
