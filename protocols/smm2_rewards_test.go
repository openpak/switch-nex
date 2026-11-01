package protocols

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	emit "github.com/openpak/rewards-emit"
	"github.com/openpak/switch-nex/rmc"
)

func TestSMM2UploadAndPlayEmitRewardsEvents(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in struct{ Events []map[string]any }
		json.Unmarshal(b, &in)
		mu.Lock()
		got = append(got, in.Events...)
		mu.Unlock()
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	e, _, blobs := courseEnv()
	rw := emit.New(emit.Config{URL: srv.URL, Key: "k", Source: "t", Flush: 10 * time.Millisecond})
	e.Rewards = rw
	id := upload(t, e, blobs, 0xaaaaaaaaaaaa0001, "A course")

	dw := rmc.NewWriter()
	dw.StructOf(0, func(w *rmc.Writer) { w.U64(id) })
	call(t, e, 0xaaaaaaaaaaaa0001, 0x19, dw.Bytes()) // the owner fetching it: not a play
	call(t, e, 0xbbbbbbbbbbbb0002, 0x19, dw.Bytes()) // someone else: a play
	rw.Close(context.Background())

	mu.Lock()
	defer mu.Unlock()
	metrics := map[string]map[string]any{}
	for _, ev := range got {
		metrics[ev["metric"].(string)] = ev
	}
	if len(got) != 2 || metrics["maker_courses_uploaded"] == nil || metrics["maker_courses_played"] == nil {
		t.Fatalf("events %v", got)
	}
	if id := metrics["maker_courses_uploaded"]["identity"].(map[string]any); id["namespace"] != "switch" || id["subject"] != "aaaaaaaaaaaa0001" {
		t.Fatalf("identity %v", id)
	}
	if id := metrics["maker_courses_played"]["identity"].(map[string]any); id["subject"] != "bbbbbbbbbbbb0002" {
		t.Fatalf("identity %v", id)
	}
}
