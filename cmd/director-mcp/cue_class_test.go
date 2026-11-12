package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// offScript drives one fixed arrival script through a cue and returns every
// mail cue as JSON, in order. It is the baseline for the class-aware cue's
// switch: with the switch off, the output must equal what main sent.
func offScript(t *testing.T, mutate func(*cueConfig)) string {
	t.Helper()
	r := newCueRig(t)
	if mutate != nil {
		mutate(&r.c.cfg)
	}
	r.started()
	r.cues = nil
	steps := []struct {
		n uint64
		d time.Duration
	}{
		{1, 2 * time.Second}, {2, time.Second}, {3, time.Second}, {3, 3 * time.Second},
		{4, 10 * time.Second}, {4, 290 * time.Second}, {4, 700 * time.Second},
	}
	for _, s := range steps {
		r.src.n = s.n
		r.step(s.d)
	}
	r.c.answered()
	r.src.n = 0
	r.step(2 * time.Second)
	r.src.n = 2
	r.step(2 * time.Second)
	out, err := json.Marshal(r.mailCues())
	if err != nil {
		t.Fatal(err)
	}
	_ = context.Background()
	return string(out)
}

func TestClassAwareOffEqualsMain(t *testing.T) {
	const want = `[{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"1","message_id":"m1","nonce":"nonce2","performative":"REQUEST","sender":"sup@ws"}},{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"2","message_id":"m1","nonce":"nonce3","performative":"REQUEST","sender":"sup@ws"}},{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"1","message_id":"m1","nonce":"nonce4","performative":"REQUEST","sender":"sup@ws"}},{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"1","message_id":"m1","nonce":"nonce5","performative":"REQUEST","sender":"sup@ws"}}]`
	if got := offScript(t, nil); got != want {
		t.Fatalf("default cue changed:\n got %s\nwant %s", got, want)
	}
}

// ---- class-aware cue (aae-orc-bykbv) ---------------------------------------

func (f *fakeSource) waiting(context.Context) ([]summaryItem, error) {
	if f.waitErr != nil {
		return nil, f.waitErr
	}
	return append([]summaryItem(nil), f.items...), nil
}

func (f *fakeSource) floors(context.Context) (map[string]uint64, error) {
	if f.floorFn != nil {
		return f.floorFn(), nil
	}
	return f.floorMap, nil
}

func classEnvItem(tier string, seq uint64, perf, id string) summaryItem {
	return summaryItem{Tier: tier, Seq: seq, Env: &Envelope{
		SchemaVersion: 1, MessageID: id, Performative: perf,
		Sender:  Sender{AgentID: "sup", Workspace: "ws"},
		Content: Content{Type: "text", Data: "x"},
	}}
}

// classRig is a cue rig whose source models a real inbox: arrive adds a waiting
// message, drain empties it as a seat that read its mail would.
type classRig struct {
	*cueRig
	t0   time.Time
	seq  uint64
	seen int
}

func newClassRig(t *testing.T, classAware bool) *classRig {
	t.Helper()
	r := newCueRig(t)
	r.c.cfg.ClassAware = classAware
	if classAware {
		r.c.cls = newClassifier()
	}
	r.started()
	r.cues = nil
	return &classRig{cueRig: r, t0: r.now}
}

func (r *classRig) arrive(perf, id string) {
	r.seq++
	r.src.items = append(r.src.items, classEnvItem("local", r.seq, perf, id))
	r.src.n = uint64(len(r.src.items))
	r.src.oldest = metaOf(r.src.items[0].Env)
}

func (r *classRig) drain() {
	r.src.items, r.src.n = nil, 0
	r.c.answered()
}

// tickTo ticks once a second until offset (seconds from t0); a mail cue that
// goes out is answered at once, as a seat that wakes and drains does. It
// returns the offsets at which cues went out.
func (r *classRig) tickTo(offset int, cuesAt *[]int) {
	for int(r.now.Sub(r.t0)/time.Second) < offset {
		r.step(time.Second)
		if len(r.mailCues()) > r.seen {
			*cuesAt = append(*cuesAt, int(r.now.Sub(r.t0)/time.Second))
			r.seen = len(r.mailCues())
			r.drain()
		}
	}
}

// The 15 arrivals of 2026-11-12, as offsets in seconds from the first. The
// record did not keep their performatives; all are taken as INFORM.
var fifteenArrivals = []int{0, 151, 253, 2015, 2065, 2091, 2329, 2612, 3706, 3725, 3736, 3780, 3797, 3894, 3922}

func runFifteen(t *testing.T, classAware bool) []int {
	r := newClassRig(t, classAware)
	var cuesAt []int
	for i, off := range fifteenArrivals {
		if off > 0 {
			r.tickTo(off, &cuesAt)
		}
		r.arrive("INFORM", fmt.Sprintf("m%d", i))
		r.step(0)
		if len(r.mailCues()) > r.seen {
			cuesAt = append(cuesAt, off)
			r.seen = len(r.mailCues())
			r.drain()
		}
	}
	r.tickTo(5000, &cuesAt)
	return cuesAt
}

func TestClassAwareFifteenInformsGiveFourCues(t *testing.T) {
	got := runFifteen(t, true)
	want := []int{300, 2315, 2629, 4006}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("cues at %v, want %v", got, want)
	}
}

func TestClassAwareOffGivesFifteenCues(t *testing.T) {
	if got := runFifteen(t, false); len(got) != 15 {
		t.Fatalf("switch off: %d cues at %v, want 15 (main)", len(got), got)
	}
}

func TestClassAwareRequestBehindHeldInformCuesWithinFloorAndNamesIt(t *testing.T) {
	r := newClassRig(t, true)
	var at []int
	r.arrive("INFORM", "inf1")
	r.step(2 * time.Second)
	r.tickTo(10, &at)
	if len(at) != 0 {
		t.Fatalf("a held INFORM cued: %v", at)
	}
	r.arrive("REQUEST", "req1")
	r.step(0)
	cues := r.mailCues()
	r.tickTo(10+int(r.c.cfg.Floor/time.Second), &at)
	cues = r.mailCues()
	if len(cues) != 1 {
		t.Fatalf("want one cue within Floor of the REQUEST, got %d: %v", len(cues), cues)
	}
	m := meta(t, cues[0])
	if m["message_id"] != "req1" || m["performative"] != "REQUEST" {
		t.Fatalf("the cue names %v, want the REQUEST req1", m)
	}
}

func TestClassAwareHeldMailDrainedBeforeTheTimerGivesNoCue(t *testing.T) {
	r := newClassRig(t, true)
	var at []int
	r.arrive("INFORM", "inf1")
	r.tickTo(100, &at)
	r.drain()
	r.tickTo(400, &at)
	if len(at) != 0 {
		t.Fatalf("cue for mail the seat already read: %v", at)
	}
}

func TestClassAwareFailsOpenWhenTheWaitingReadFails(t *testing.T) {
	r := newClassRig(t, true)
	r.src.waitErr = errors.New("peek failed")
	var at []int
	r.arrive("INFORM", "inf1")
	r.tickTo(6, &at)
	if len(at) != 1 {
		t.Fatalf("an unreadable waiting set must cue as main does, got %v", at)
	}
}

func TestClassAwareActOnTheGlobalTierCuesAtOnce(t *testing.T) {
	r := newClassRig(t, true)
	var at []int
	r.seq++
	r.src.items = append(r.src.items, classEnvItem("global", r.seq, "CANCEL", "c1"))
	r.src.n = 1
	r.src.oldest = metaOf(r.src.items[0].Env)
	r.tickTo(6, &at)
	if len(at) != 1 {
		t.Fatalf("a CANCEL on the global tier must cue at once, got %v", at)
	}
	if m := meta(t, r.mailCues()[0]); m["message_id"] != "c1" {
		t.Fatalf("cue meta: %v", m)
	}
}

func TestClassAwareBacklogIsOneCueNotHeld(t *testing.T) {
	r := newClassRig(t, true)
	r.src.floorMap = map[string]uint64{"local": 3}
	for i := 0; i < 3; i++ {
		r.arrive("INFORM", fmt.Sprintf("b%d", i))
	}
	var at []int
	r.tickTo(10, &at)
	if len(at) != 1 {
		t.Fatalf("backlog must give one cue, got %v", at)
	}
	if c := meta(t, r.mailCues()[0])["count"]; c != "3" {
		t.Fatalf("backlog cue count %v, want 3", c)
	}
}

func TestClassAwareSwitchReadsDirectorCueClass(t *testing.T) {
	for v, want := range map[string]bool{"": false, "1": true, "0": false, "true": false} {
		t.Setenv("DIRECTOR_CUE_CLASS", v)
		if v == "" {
			os.Unsetenv("DIRECTOR_CUE_CLASS")
		}
		if got := loadCueConfig().ClassAware; got != want {
			t.Errorf("DIRECTOR_CUE_CLASS=%q: ClassAware = %v, want %v", v, got, want)
		}
	}
}

func TestClassAwareOffRunsMainScriptWithTheSwitchSetOff(t *testing.T) {
	const want = `[{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"1","message_id":"m1","nonce":"nonce2","performative":"REQUEST","sender":"sup@ws"}},{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"2","message_id":"m1","nonce":"nonce3","performative":"REQUEST","sender":"sup@ws"}},{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"1","message_id":"m1","nonce":"nonce4","performative":"REQUEST","sender":"sup@ws"}},{"content":"new message in your director inbox (call inbox_summary with cue_ack set to the nonce in this notice)","meta":{"count":"1","message_id":"m1","nonce":"nonce5","performative":"REQUEST","sender":"sup@ws"}}]`
	if got := offScript(t, func(c *cueConfig) { c.ClassAware = false }); got != want {
		t.Fatalf("switch off changed the cue:\n got %s\nwant %s", got, want)
	}
}

// Every accepted send is recorded with the classifier, so the first reply to
// the seat's own ask is act.
func TestClassAwareSendRecordsEveryAcceptedEnvelope(t *testing.T) {
	r := newDeliveryRig(t)
	cr := newCueRig(t)
	cr.c.cfg.ClassAware = true
	cr.c.cls = newClassifier()
	r.bus.attachCue(cr.c)
	r.send(t, "ask1", "REQUEST")
	r.send(t, "inf1", "INFORM")
	cr.c.cls.mu.Lock()
	defer cr.c.cls.mu.Unlock()
	if a := cr.c.cls.asks["ask1"]; a == nil || !a.ask {
		t.Fatalf("a sent REQUEST is not recorded as an ask: %+v", a)
	}
	if a := cr.c.cls.asks["inf1"]; a == nil || a.ask {
		t.Fatalf("a sent INFORM is not recorded as a non-ask: %+v", a)
	}
}

func TestClassAwareOffSendRecordsNothing(t *testing.T) {
	r := newDeliveryRig(t)
	cr := newCueRig(t)
	r.bus.attachCue(cr.c)
	if r.bus.sentCls != nil {
		t.Fatal("switch off: the bus holds a classifier")
	}
	r.send(t, "ask1", "REQUEST")
}

func TestClassAwareReadMailAndNewRequestInOneTickCuesAndNamesIt(t *testing.T) {
	for _, held := range []int{1, 3} {
		t.Run(fmt.Sprintf("held%d", held), func(t *testing.T) {
			r := newClassRig(t, true)
			var at []int
			for i := 0; i < held; i++ {
				r.arrive("INFORM", fmt.Sprintf("inf%d", i))
			}
			r.tickTo(4, &at)
			if len(at) != 0 {
				t.Fatalf("held=%d: a held INFORM cued: %v", held, at)
			}
			// The seat reads what was held; a REQUEST arrives before the next tick.
			// The waiting count is 1 again: equal to the last count for one held
			// message, below it for three.
			r.src.items, r.src.n = nil, 0
			r.arrive("REQUEST", "req1")
			r.tickTo(4+int(r.c.cfg.Floor/time.Second), &at)
			cues := r.mailCues()
			if len(cues) != 1 {
				t.Fatalf("held=%d: want one cue for the REQUEST within Floor, got %d at %v", held, len(cues), at)
			}
			if m := meta(t, cues[0]); m["message_id"] != "req1" || m["performative"] != "REQUEST" {
				t.Fatalf("held=%d: the cue names %v, want the REQUEST req1", held, m)
			}
		})
	}
}

func TestClassAwareFloorsAreReadOnAnEmptyInbox(t *testing.T) {
	r := newClassRig(t, true)
	r.src.floorFn = func() map[string]uint64 { return map[string]uint64{"local": r.seq} }
	var at []int
	r.tickTo(4, &at) // empty inbox: the floor is read now, at sequence 0
	r.arrive("INFORM", "inf1")
	r.tickTo(10, &at)
	if len(at) != 0 {
		t.Fatalf("the first fresh INFORM after an empty start was counted as backlog and cued: %v", at)
	}
	r.tickTo(310, &at)
	if fmt.Sprint(at) != "[304]" {
		t.Fatalf("held INFORM cues at %v, want [304] (D after it arrived)", at)
	}
}
