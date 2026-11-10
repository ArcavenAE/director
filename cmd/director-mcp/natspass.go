package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nats-io/nats.go"
)

// DIRECTOR_NATS_PASS_FILE names a file that holds the bus password, so a
// launcher need not put the secret in the environment it passes on a command
// line (aae-orc-xs6om, aae-orc-fcgpf part a). The file is read through
// nats.UserInfoHandler, so every connect and reconnect reads it again and a
// rotated file takes effect without a restart.
//
// The file must be a regular file with no group or other permission bits.
// When it is set it is the only source of the password: a missing, unreadable
// or loose file is an error, never a quiet fall back to DIRECTOR_NATS_PASS or
// to an empty password. DIRECTOR_NATS_PASS stays the source when no file is
// named, so a running seat keeps working. Errors name the path and the reason,
// never the password.

// checkNatsPassFile reads path and returns the password: trailing newlines
// stripped, as the seat password file is.
func checkNatsPassFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("DIRECTOR_NATS_PASS_FILE %s: %w", path, err)
	}
	defer f.Close()
	// Stat the open descriptor, so the mode checked is the file read.
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("DIRECTOR_NATS_PASS_FILE %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("DIRECTOR_NATS_PASS_FILE %s: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("DIRECTOR_NATS_PASS_FILE %s: mode %04o lets group or others read it; chmod it to 0600", path, st.Mode().Perm())
	}
	b, err := readAll(f)
	if err != nil {
		return "", fmt.Errorf("DIRECTOR_NATS_PASS_FILE %s: %w", path, err)
	}
	pass := strings.TrimRight(string(b), "\n")
	if pass == "" {
		return "", fmt.Errorf("DIRECTOR_NATS_PASS_FILE %s: file is empty", path)
	}
	return pass, nil
}

func readAll(f *os.File) ([]byte, error) {
	// A password file is a few dozen bytes; a bound keeps a wrong path from
	// reading a large file into memory.
	const limit = 4096
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("file is larger than 4096 bytes")
	}
	return b, nil
}

// natsAuthOption is the dial credential, from the environment:
//
//	DIRECTOR_NATS_CREDS      a .creds file, wins when set
//	DIRECTOR_NATS_USER       the user name
//	DIRECTOR_NATS_PASS_FILE  the password file, wins over DIRECTOR_NATS_PASS
//	DIRECTOR_NATS_PASS       the password
//
// A nil option with a nil error is the anonymous connect.
func natsAuthOption() (nats.Option, error) {
	if creds := os.Getenv("DIRECTOR_NATS_CREDS"); creds != "" {
		return nats.UserCredentials(creds), nil
	}
	user := os.Getenv("DIRECTOR_NATS_USER")
	file := os.Getenv("DIRECTOR_NATS_PASS_FILE")
	if file == "" {
		if user == "" {
			return nil, nil
		}
		return nats.UserInfo(user, os.Getenv("DIRECTOR_NATS_PASS")), nil
	}
	if user == "" {
		return nil, errors.New("DIRECTOR_NATS_PASS_FILE is set but DIRECTOR_NATS_USER is not; the file holds the password, not the user name")
	}
	// Fail at startup, not on the first reconnect.
	if _, err := checkNatsPassFile(file); err != nil {
		return nil, err
	}
	return nats.UserInfoHandler(func() (string, string) {
		pass, err := checkNatsPassFile(file)
		if err != nil {
			// The handler cannot return an error. An empty password makes the
			// broker refuse the connect, which is the loud outcome; the env
			// value is not tried.
			fmt.Fprintf(os.Stderr, "director-mcp: %v\n", err)
			return user, ""
		}
		return user, pass
	}), nil
}
