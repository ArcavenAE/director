package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func seenFixture(t *testing.T) (context.Context, jetstream.JetStream, jetstream.JetStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	nc, js := provisionAudit(t, ctx, url)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, js, gjs
}

// After a reader restart the entry is there with its first and last seen
// times, keyed cluster/role.
func TestReadGlobalSeenShowsAnEntryAfterARestart(t *testing.T) {
	ctx, js, gjs := seenFixture(t)
	gkv, _ := gjs.KeyValue(ctx, globalPresenceBucket)
	_, _ = gkv.Put(ctx, "presence.cluster-b.supervisor.01ABC", []byte(`{"cluster":"cluster-b","role":"supervisor","agent_id":"sup-b","state":"idle"}`))
	r1 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r1.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	r2 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "b.json")})
	if err := r2.Pass(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, state, err := readGlobalSeen(ctx, js)
	if err != nil || state != seenLedger {
		t.Fatalf("state = %v, err = %v, want seenLedger and no error", state, err)
	}
	e, ok := got["cluster-b/supervisor"]
	want := r1.ledger.GlobalID[0]
	if !ok || !e.FirstSeen.Equal(want.FirstSeen) || !e.LastSeen.Equal(want.LastSeen) || e.Live != want.Live {
		t.Fatalf("entry = %+v (found %v), want %+v", e, ok, want)
	}
}

// No ask reader on this cluster: no bucket is no record, and the read must not
// create the bucket.
func TestReadGlobalSeenWithNoBucketIsNoRecord(t *testing.T) {
	ctx, js, _ := seenFixture(t)
	got, state, err := readGlobalSeen(ctx, js)
	if err != nil || state != seenNoLedger || len(got) != 0 {
		t.Fatalf("got %v, state %v, err %v, want empty, seenNoLedger, no error", got, state, err)
	}
	if _, err := js.KeyValue(ctx, askBucket); !errors.Is(err, jetstream.ErrBucketNotFound) {
		t.Fatalf("the read created or changed the bucket: %v", err)
	}
}

// A bucket that holds no globals key (a store from before the table was kept)
// is also no record.
func TestReadGlobalSeenWithNoGlobalsKeyIsNoRecord(t *testing.T) {
	ctx, js, _ := seenFixture(t)
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: askBucket})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "meta", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	_, state, err := readGlobalSeen(ctx, js)
	if err != nil || state != seenNoLedger {
		t.Fatalf("state %v, err %v, want seenNoLedger and no error", state, err)
	}
}

// A readable table with no entry for a cluster is not seen: the ledger was
// read and the cluster is not in it.
func TestReadGlobalSeenWithATableAndNoEntryIsNotSeen(t *testing.T) {
	ctx, js, gjs := seenFixture(t)
	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, state, err := readGlobalSeen(ctx, js)
	if err != nil || state != seenLedger {
		t.Fatalf("state %v, err %v, want seenLedger and no error", state, err)
	}
	if _, ok := got["cluster-z/supervisor"]; ok || len(got) != 0 {
		t.Fatalf("table = %v, want empty", got)
	}
}

// A globals value that cannot be parsed is a read failure, never an empty table.
func TestReadGlobalSeenWithACorruptValueIsAnError(t *testing.T) {
	ctx, js, _ := seenFixture(t)
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: askBucket})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "globals", []byte(`{not json`)); err != nil {
		t.Fatal(err)
	}
	_, _, err = readGlobalSeen(ctx, js)
	if err == nil {
		t.Fatal("a corrupt globals value read as no error")
	}
}

// The read writes nothing: every key keeps its revision and no key appears.
func TestReadGlobalSeenNeverWrites(t *testing.T) {
	ctx, js, gjs := seenFixture(t)
	gkv, _ := gjs.KeyValue(ctx, globalPresenceBucket)
	_, _ = gkv.Put(ctx, "presence.cluster-b.supervisor.01ABC", []byte(`{"cluster":"cluster-b","role":"supervisor","agent_id":"sup-b","state":"idle"}`))
	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, askBucket)
	if err != nil {
		t.Fatal(err)
	}
	revs := func() map[string]uint64 {
		out := map[string]uint64{}
		keys, _ := kv.Keys(ctx)
		for _, k := range keys {
			e, err := kv.Get(ctx, k)
			if err != nil {
				t.Fatal(err)
			}
			out[k] = e.Revision()
		}
		return out
	}
	before := revs()
	if _, state, err := readGlobalSeen(ctx, js); err != nil || state != seenLedger {
		t.Fatalf("state %v, err %v", state, err)
	}
	if after := revs(); !reflect.DeepEqual(before, after) {
		t.Fatalf("the read changed the bucket: before %v, after %v", before, after)
	}
}
