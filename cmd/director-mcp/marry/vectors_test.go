package marry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The pinned vector files are copies of marvel's contracts/readiness files, the
// shared test of the routing rule and the marrying step. These hashes are the
// copies as pinned; a changed vector is a changed contract and is re-pinned on
// purpose, in a PR that says so (testdata/README.md).
const (
	routingVectorsSHA256 = "4cb6a7295845ace0a9456d8723013675907b0def798b016ad7e265ad540a6be4"
	marryVectorsSHA256   = "5fe464863800eb75911fb2f4b5e5c8190678057480e2b4801f08bebead92376f"

	// marryVectorsGrepSkips is how many grep steps the pinned marry vectors
	// carry. Those steps read marvel's Go source, so director skips them; the
	// count is pinned so a re-pin that adds one is a visible change, not a
	// silent gap in what director checks.
	marryVectorsGrepSkips = 4
)

// strictDecode decodes b into v and refuses an unknown field, so a vector that
// names a field this package does not have fails instead of being ignored.
func strictDecode(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func readPinned(t *testing.T, name, wantSHA string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != wantSHA {
		t.Fatalf("testdata/%s sha256 = %s, pinned %s (re-pin on purpose, see testdata/README.md)", name, got, wantSHA)
	}
	return b
}

type marryStep struct {
	Op             string          `json:"op"`
	Input          *Input          `json:"input"`
	Lease          *Lease          `json:"lease"`
	Now            time.Time       `json:"now"`
	ReRead         *SendReRead     `json:"reread"`
	Address        string          `json:"address"`
	File           string          `json:"file"`
	AllowedImports []string        `json:"allowed_imports"`
	ForbidLiterals []string        `json:"forbid_literals"`
	ForbidCalls    []string        `json:"forbid_calls"`
	Error          string          `json:"error"`
	Want           json.RawMessage `json:"want"`
}

type marryVectors struct {
	Version int      `json:"version"`
	Design  string   `json:"design"`
	Note    string   `json:"note"`
	Rows    []string `json:"rows"`
	Vectors []struct {
		Name  string      `json:"name"`
		Row   string      `json:"row"`
		Steps []marryStep `json:"steps"`
	} `json:"vectors"`
}

// TestMarryVectorsPassInDirector runs every marry, take, recheck and address
// step of marvel's shared vectors against director's port. The grep steps read
// marvel's Go source and run in marvel only (the file's own note).
func TestMarryVectorsPassInDirector(t *testing.T) {
	var v marryVectors
	if err := strictDecode(readPinned(t, "marry-vectors.json", marryVectorsSHA256), &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || len(v.Rows) == 0 || len(v.Vectors) == 0 {
		t.Fatalf("version %d, %d rows, %d vectors", v.Version, len(v.Rows), len(v.Vectors))
	}
	ran := map[string]int{}
	for _, vec := range v.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			if len(vec.Steps) == 0 {
				t.Fatal("no steps")
			}
			for i, st := range vec.Steps {
				if st.Op == "grep" {
					ran["grep (marvel only)"]++
					continue
				}
				ran[st.Op]++
				runMarryStep(t, strconv.Itoa(i)+" "+st.Op, st)
			}
		})
	}
	for _, op := range []string{"marry", "take", "recheck", "address"} {
		if ran[op] == 0 {
			t.Errorf("the pinned vectors ran no %s step", op)
		}
	}
	skips := ran["grep (marvel only)"]
	t.Logf("steps run by op: marry %d, take %d, recheck %d, address %d; grep steps skipped (marvel only): %d",
		ran["marry"], ran["take"], ran["recheck"], ran["address"], skips)
	if skips != marryVectorsGrepSkips {
		t.Errorf("the pinned marry vectors carry %d grep steps, which director skips; this test pins %d. A changed count means the vectors were re-pinned: read the new steps before changing the pin", skips, marryVectorsGrepSkips)
	}
}

func runMarryStep(t *testing.T, name string, st marryStep) {
	t.Helper()
	switch st.Op {
	case "marry":
		if st.Input == nil {
			t.Fatalf("step %s: no input", name)
		}
		if st.Error != "" {
			if _, err := Run(*st.Input); err == nil || !strings.Contains(err.Error(), st.Error) {
				t.Errorf("step %s: Run error = %v, want one containing %q", name, err, st.Error)
			}
			return
		}
		var want Output
		if err := strictDecode(st.Want, &want); err != nil {
			t.Fatalf("step %s: want: %v", name, err)
		}
		got, err := Run(*st.Input)
		if err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			g, _ := json.Marshal(got)
			t.Errorf("step %s\n got %s\nwant %s", name, g, st.Want)
		}
	case "take":
		var want string
		if err := json.Unmarshal(st.Want, &want); err != nil || st.Lease == nil {
			t.Fatalf("step %s: lease %v, want %v", name, st.Lease != nil, err)
		}
		if got := AnswerTake(*st.Lease, st.Now); got != want {
			t.Errorf("step %s: AnswerTake at %s = %q, want %q", name, st.Now.Format(time.RFC3339), got, want)
		}
	case "recheck":
		var want SendCheck
		if err := strictDecode(st.Want, &want); err != nil || st.Lease == nil || st.ReRead == nil {
			t.Fatalf("step %s: lease %v, reread %v, want %v", name, st.Lease != nil, st.ReRead != nil, err)
		}
		if got := RecheckBeforeSend(*st.Lease, *st.ReRead); got != want {
			t.Errorf("step %s: RecheckBeforeSend = %+v, want %+v", name, got, want)
		}
	case "address":
		var want string
		if err := json.Unmarshal(st.Want, &want); err != nil {
			t.Fatalf("step %s: want %v", name, err)
		}
		got := "ok"
		if err := CheckAddress(st.Address); err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("step %s: CheckAddress(%q) = %q, want %q", name, st.Address, got, want)
		}
	default:
		t.Fatalf("step %s: unknown op", name)
	}
}

type routeVectors struct {
	Version int       `json:"version"`
	Design  string    `json:"design"`
	Note    string    `json:"note"`
	Now     time.Time `json:"now"`
	Vectors []struct {
		Name  string          `json:"name"`
		Rule  string          `json:"rule"`
		Input RouteInput      `json:"input"`
		Want  json.RawMessage `json:"want"`
	} `json:"vectors"`
}

// TestRouteSeatVectorsPassInDirector runs marvel's routing vectors, at the
// file's own now, against director's router.
func TestRouteSeatVectorsPassInDirector(t *testing.T) {
	var v routeVectors
	if err := strictDecode(readPinned(t, "routing-vectors.json", routingVectorsSHA256), &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || v.Now.IsZero() || len(v.Vectors) == 0 {
		t.Fatalf("version %d, now %v, %d vectors", v.Version, v.Now, len(v.Vectors))
	}
	for _, vec := range v.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			var want RouteDecision
			if err := strictDecode(vec.Want, &want); err != nil {
				t.Fatalf("want: %v", err)
			}
			if got := RouteSeat(vec.Input, v.Now); !reflect.DeepEqual(got, want) {
				g, _ := json.Marshal(got)
				t.Errorf("rule %s\n got %s\nwant %s", vec.Rule, g, vec.Want)
			}
		})
	}
}
