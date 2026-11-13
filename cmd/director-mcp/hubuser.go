package main

// director hub user and director hub settings: the lifecycle of the global hub's
// users as a director command (sim/design/hub-user-lifecycle.md). This file is
// the skeleton the red tests build against; the verbs are not written yet and
// every one of them reports that and exits 1.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
)

// hubClassFS holds one conf template per user class (leaf, director, observer,
// admin). Tests replace it to make a template wrong on purpose.
var hubClassFS fs.FS

// hubAfterReload, when a test sets it, runs right after the command reloads the
// hub and before it verifies. It lets a test act while the new conf is live.
var hubAfterReload func()

// writeFileExcl creates path with mode, failing if the name exists (a dangling
// symlink included), and sets the mode on the open descriptor so the umask
// does not decide it.
func writeFileExcl(path string, data []byte, mode os.FileMode) error {
	return fmt.Errorf("not implemented")
}

// runHubCmd runs `director-mcp hub ...`. args are the words after "hub". getenv
// reads the command's settings, so a test can hand it a scratch hub home. PATH is
// read from the process environment, not from getenv.
func runHubCmd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "hub: not implemented")
	return 1
}
