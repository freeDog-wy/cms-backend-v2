package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
)

type TaskHandler func(context.Context, json.RawMessage) error

type WorkerPool struct {
	tasks  map[string]TaskHandler
	rwmutx sync.RWMutex
	pool   *ants.Pool
}

func NewWorkerPool() (*WorkerPool, error) {
	p, err := ants.NewPool(1000)
	if err != nil {
		return nil, err
	}
	return &WorkerPool{
		tasks:  make(map[string]TaskHandler),
		rwmutx: sync.RWMutex{},
		pool:   p,
	}, nil
}

func (p *WorkerPool) RegisterTask(event_type string, task TaskHandler) error {
	p.rwmutx.Lock()
	defer p.rwmutx.Unlock()

	_, ok := p.tasks[event_type]
	if ok {
		return fmt.Errorf("event type %q already registered", event_type)
	}
	p.tasks[event_type] = task
	return nil
}

func (p *WorkerPool) SubmitTask(
	ctx context.Context,
	event_type string,
	payload json.RawMessage,
	on_done func(error),
) error {

	p.rwmutx.RLock()
	handle, exists := p.tasks[event_type]
	p.rwmutx.RUnlock()

	if !exists {
		return fmt.Errorf("event type %q not registered", event_type)
	}

	return p.pool.Submit(func() {
		err := handle(ctx, payload)
		on_done(err)
	})
}

func (p *WorkerPool) ReleaseTimeout(ctx context.Context, dt time.Duration) error {
	return p.pool.ReleaseTimeout(dt)
}
