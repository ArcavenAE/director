package marry

import (
	"strings"
	"testing"
	"time"
)

func seatKeyInput() Input {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	free := func(cluster string) Seat {
		return Seat{
			Address: "agent://t1/rev-a", Team: "t1", Cluster: cluster, Availability: SeatFree,
			Classes: []string{"pr-review"}, Scopes: []string{"marvel"},
			Cells: map[string]Cell{"duty.first-turn": {
				Value: "proven", ObservedAt: now.Add(-10 * time.Minute),
				ValidUntil: now.Add(30 * time.Minute), Source: "pane:turn",
			}},
		}
	}
	item := func(id string) Item {
		return Item{
			ID: id, Priority: 1, ReadyAt: now.Add(-time.Hour), Status: "open",
			Classes: map[string]string{"label": "pr-review"}, Component: "marvel",
			Duties: []string{"first-turn"},
		}
	}
	return Input{
		RunID: "r1", Now: now,
		Items:        []Item{item("item-1"), item("item-2")},
		Seats:        []Seat{free("c1"), free("c2")},
		Placement:    Placement{SenderCluster: "c1", RuledClusters: []string{"c2"}},
		Rulings:      Rulings{WorkClassSource: "label"},
		LeaseCap:     3,
		LeaseMinutes: 30,
	}
}

// One seat name on two clusters is two seats: Run no longer refuses the run, and
// each cluster's seat takes its own item.
func TestMarryKeysSeatByClusterAndAddress(t *testing.T) {
	out, err := Run(seatKeyInput())
	if err != nil {
		t.Fatalf("one name on two clusters is not a duplicate: %v", err)
	}
	if len(out.Pairs) != 2 {
		t.Fatalf("pairs = %d, want one per cluster (2): %+v", len(out.Pairs), out.Pairs)
	}
}

// A stop entry on the address stops that name on every cluster.
func TestMarryStopOnAddressStopsEveryCluster(t *testing.T) {
	in := seatKeyInput()
	in.Stop.NotMatchable = []string{"agent://t1/rev-a"}
	out, err := Run(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pairs) != 0 {
		t.Fatalf("pairs = %+v, want none: the stop names the address on both clusters", out.Pairs)
	}
}

// The same cluster and address twice is still one seat listed twice.
func TestMarryRefusesSameClusterAndAddressTwice(t *testing.T) {
	in := seatKeyInput()
	in.Seats[1].Cluster = "c1"
	if _, err := Run(in); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("err = %v, want a listed-twice refusal", err)
	}
}
