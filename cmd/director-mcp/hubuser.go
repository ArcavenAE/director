package main

// director hub user and director hub settings: the lifecycle of the global hub's
// users as a director command (sim/design/hub-user-lifecycle.md). This file is
// the skeleton the red tests build against; the verbs are not written yet and
// every one of them reports that and exits 1.

import (
	"fmt"
	"io"
	"io/fs"
)

// hubClassFS holds one conf template per user class (leaf, director, observer,
// admin). Tests replace it to make a template wrong on purpose.
var hubClassFS fs.FS

// runHubCmd runs `director-mcp hub ...`. args are the words after "hub". getenv
// reads the command's settings, so a test can hand it a scratch hub home.
func runHubCmd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "hub: not implemented")
	return 1
}
