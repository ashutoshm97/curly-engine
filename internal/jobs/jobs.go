// Package jobs runs SQLite-queued jobs on a goroutine pool and fans progress
// events out to subscribers (used by the SSE endpoint).
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ashutoshm97/curly-engine/internal/store"
)

type Event struct {
	JobID string          `json:"jobId"`
	Type  string          `json:"type"` // stage | data | done | error
	Stage string          `json:"stage,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Reporter lets handlers publish progress.
type Reporter interface {
	Stage(name string)
	Data(v any)
}

type Handler func(ctx context.Context, j *store.Job, r Reporter) (result any, err error)

// Permanent marks an error that must not be retried.
type Permanent struct{ Err error }

func (p Permanent) Error() string { return p.Err.Error() }
func (p Permanent) Unwrap() error { return p.Err }

type Runner struct {
	st       *store.Store
	handlers map[string]Handler
	workers  int
	wake     chan struct{}
	timeout  time.Duration

	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

func NewRunner(st *store.Store, workers int) *Runner {
	if workers <= 0 {
		workers = 4
	}
	return &Runner{st: st, handlers: map[string]Handler{}, workers: workers, wake: make(chan struct{}, 1),
		timeout: 5 * time.Minute, subs: map[string]map[chan Event]struct{}{}}
}

func (r *Runner) Handle(typ string, h Handler) { r.handlers[typ] = h }

// Submit enqueues a job and wakes a worker.
func (r *Runner) Submit(ctx context.Context, typ string, payload any, idemKey string) (*store.Job, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	j, _, err := r.st.Enqueue(ctx, typ, string(b), idemKey)
	if err != nil {
		return nil, err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return j, nil
}

// Start launches workers until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) *sync.WaitGroup {
	if n, _ := r.st.RecoverRunning(ctx); n > 0 {
		slog.Info("requeued interrupted jobs", "count", n)
	}
	var wg sync.WaitGroup
	for range r.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.loop(ctx)
		}()
	}
	return &wg
}

func (r *Runner) loop(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		for {
			j, err := r.st.Claim(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("claim", "err", err)
				break
			}
			if j == nil {
				break
			}
			r.run(ctx, j)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-tick.C:
		}
	}
}

type reporter struct {
	r   *Runner
	ctx context.Context
	id  string
}

func (p reporter) Stage(name string) {
	p.r.st.SetStage(p.ctx, p.id, name)
	p.r.publish(Event{JobID: p.id, Type: "stage", Stage: name})
}

func (p reporter) Data(v any) {
	b, err := json.Marshal(v)
	if err == nil {
		p.r.publish(Event{JobID: p.id, Type: "data", Data: b})
	}
}

func (r *Runner) run(ctx context.Context, j *store.Job) {
	h, ok := r.handlers[j.Type]
	if !ok {
		r.st.Fail(ctx, j, "no handler for "+j.Type, false)
		r.publish(Event{JobID: j.ID, Type: "error", Error: "no handler"})
		return
	}
	jctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	res, err := safe(jctx, h, j, reporter{r, ctx, j.ID})
	if err != nil {
		var perm Permanent
		retry, _ := r.st.Fail(ctx, j, err.Error(), !errors.As(err, &perm))
		slog.Warn("job failed", "id", j.ID, "type", j.Type, "attempt", j.Attempts, "retry", retry, "err", err)
		if !retry {
			r.publish(Event{JobID: j.ID, Type: "error", Error: err.Error()})
		}
		return
	}
	b, _ := json.Marshal(res)
	r.st.Complete(ctx, j.ID, string(b))
	slog.Info("job done", "id", j.ID, "type", j.Type, "ms", time.Since(start).Milliseconds())
	r.publish(Event{JobID: j.ID, Type: "done", Data: b})
}

func safe(ctx context.Context, h Handler, j *store.Job, rep Reporter) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = Permanent{Err: errors.New("panic in job handler")}
			slog.Error("job panic", "id", j.ID, "panic", p)
		}
	}()
	return h(ctx, j, rep)
}

// Subscribe returns a channel of events for a job; call the returned func to unsubscribe.
func (r *Runner) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	r.mu.Lock()
	if r.subs[jobID] == nil {
		r.subs[jobID] = map[chan Event]struct{}{}
	}
	r.subs[jobID][ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs[jobID], ch)
		if len(r.subs[jobID]) == 0 {
			delete(r.subs, jobID)
		}
		r.mu.Unlock()
	}
}

func (r *Runner) publish(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.subs[e.JobID] {
		select {
		case ch <- e:
		default: // slow subscriber: drop rather than block workers
		}
	}
}
