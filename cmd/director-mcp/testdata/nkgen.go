//go:build ignore

// nkgen writes a new NKey user seed to the file named by its one argument (mode
// 0600, refusing to overwrite) and prints the public key; `nkgen -pub SEEDFILE`
// prints the public key of an existing seed. verify-hub-observer.sh and
// verify-hub-observer-script.sh run it with `go run` from the shim's module, so a
// scratch run needs no `nk` binary.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nats-io/nkeys"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-pub" {
		b, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "nkgen:", err)
			os.Exit(1)
		}
		kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(b))))
		if err != nil {
			fmt.Fprintln(os.Stderr, "nkgen:", err)
			os.Exit(1)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			fmt.Fprintln(os.Stderr, "nkgen:", err)
			os.Exit(1)
		}
		fmt.Println(pub)
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: nkgen SEEDFILE | nkgen -pub SEEDFILE")
		os.Exit(2)
	}
	kp, err := nkeys.CreateUser()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nkgen:", err)
		os.Exit(1)
	}
	seed, err := kp.Seed()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nkgen:", err)
		os.Exit(1)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nkgen:", err)
		os.Exit(1)
	}
	f, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nkgen:", err)
		os.Exit(1)
	}
	if _, err := f.Write(append(seed, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "nkgen:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "nkgen:", err)
		os.Exit(1)
	}
	fmt.Println(pub)
}
