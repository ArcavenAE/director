package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ArcavenAE/director/cmd/director-mcp/marry"
)

const marryUsage = "director-mcp marry --input FILE"

// runMarryCmd reads one marry.Input from the file, runs the marrying step and
// prints the answer as JSON. It is read-only and shadow only: it sends nothing,
// connects to no bus and needs no identity, so a host without marvel can pair
// seats with items. An unknown field in the input is an error, as in the pinned
// vectors.
func runMarryCmd(cli cliArgs, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	// "-" reads the input from stdin, as marvel's verb does, so the pinned
	// vectors can be piped through it.
	var b []byte
	var err error
	if cli.file == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(cli.file)
	}
	if err != nil {
		return fail(err)
	}
	var in marry.Input
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return fail(fmt.Errorf("marry input: %w", err))
	}
	out, err := marry.Run(in)
	if err != nil {
		return fail(err)
	}
	enc := json.NewEncoder(stdout)
	if err := enc.Encode(out); err != nil {
		return fail(err)
	}
	return 0
}
