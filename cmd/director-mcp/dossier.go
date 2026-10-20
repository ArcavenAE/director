package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	thresholdDossierWindow = "dossier_window"
	thresholdDossierKeep   = "dossier_keep"
)

var dossierToken = regexp.MustCompile(`^[-A-Za-z0-9_=]+$`)

func parseDossierArgs(rest []string) (cliArgs, error) {
	out := cliArgs{mode: "dossier"}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		val := func() (string, error) {
			if k := strings.IndexByte(a, '='); strings.HasPrefix(a, "--") && k > 0 {
				return a[k+1:], nil
			}
			if i+1 >= len(rest) {
				return "", fmt.Errorf("dossier: %s needs a value (usage: %s)", a, dossierUsage)
			}
			i++
			return rest[i], nil
		}
		name := a
		if k := strings.IndexByte(a, '='); strings.HasPrefix(a, "--") && k > 0 {
			name = a[:k]
		}
		switch name {
		case "--json":
			out.json = true
		case "--role", "--predecessor", "--predecessor-state", "--pointer":
			v, err := val()
			if err != nil {
				return cliArgs{}, err
			}
			switch name {
			case "--role":
				out.role = v
			case "--predecessor":
				out.pred = v
			case "--predecessor-state":
				out.predState = v
			default:
				out.pointers = append(out.pointers, v)
			}
		default:
			return cliArgs{}, fmt.Errorf("dossier: unknown argument %q (usage: %s)", a, dossierUsage)
		}
	}
	parts := strings.Split(out.role, "/")
	if len(parts) != 2 || !dossierToken.MatchString(parts[0]) || !dossierToken.MatchString(parts[1]) {
		return cliArgs{}, fmt.Errorf("dossier: --role must be TEAM/ROLE (usage: %s)", dossierUsage)
	}
	if out.pred != "" && !dossierToken.MatchString(out.pred) {
		return cliArgs{}, fmt.Errorf("dossier: --predecessor %q is not an agent id (usage: %s)", out.pred, dossierUsage)
	}
	return out, nil
}

// dossierOpts is everything a run needs, so a test sets each of them directly.
type dossierOpts struct {
	Role        string // TEAM/ROLE
	Predecessor string
	PredSource  string // flag, env or unknown
	PredState   string
	Pointers    []string
	JSON        bool
	State       string
	Successor   string
	DWS         string
	Now         time.Time
}

func runDossierCmd(ctx context.Context, url string, cli cliArgs, out, errw io.Writer) int {
	return 0
}

func runDossier(ctx context.Context, url string, o dossierOpts, out, errw io.Writer) int {
	return 0
}
