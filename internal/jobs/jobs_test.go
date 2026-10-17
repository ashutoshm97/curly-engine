package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashutoshm97/curly-engine/internal/store"
	"github.com/ashutoshm97/curly-engine/internal/store/storetest"
)

func TestRunnerRetriesAndPublishes(t *testing.T) {
	st := storetest.Open(t)
	r := NewRunner(st, 2)
	var calls atomic.Int32
	r.Handle("echo", func(ctx context.Context, j *store.Job, rep Reporter) (any, error) {
		rep.Stage("working")
		if calls.Add(1) == 1 {
			return nil, errors.New("transient")
		}
		return map[string]string{"payload": j.Payload}, nil
	})
	r.Handle("bad", func(ctx context.Context, j *store.Job, rep Reporter) (any, error) {
		return nil, Permanent{errors.New("nope")}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, _ := r.Submit(ctx, "echo", map[string]int{"n": 1}, "")
	events, unsub := r.Subscribe(j.ID)
	defer unsub()
	r.Start(ctx)
	// Skip the 2s backoff.
	time.Sleep(200 * time.Millisecond)
	st.Enqueue(ctx, "noop", "{}", "") // nudge
	for deadline := time.After(10 * time.Second); ; {
		select {
		case e := <-events:
			if e.Type == "done" {
				goto bad
			}
		case <-deadline:
			t.Fatal("timeout waiting for done")
		}
	}
bad:
	if calls.Load() != 2 {
		t.Errorf("calls = %d", calls.Load())
	}
	b, _ := r.Submit(ctx, "bad", nil, "")
	time.Sleep(700 * time.Millisecond)
	got, _ := st.GetJob(ctx, b.ID)
	if got.Status != store.JobFailed || got.Attempts != 1 {
		t.Errorf("permanent job = %+v", got)
	}
}
