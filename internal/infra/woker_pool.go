package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
)

type TaskHandler func(context.Context, json.RawMessage) error

var (
	ErrInvalidTaskPayload = errors.New("invalid task payload")
	ErrTaskNotRegistered  = errors.New("task not registered")
)

type WorkerPool struct {
	tasks  map[string]TaskHandler
	rwmutx sync.RWMutex
	pool   *ants.Pool
}

func NewWorkerPool() (*WorkerPool, error) {
	p, err := ants.NewPool(1000, ants.WithNonblocking(true))
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
		return fmt.Errorf("%w: %q", ErrTaskNotRegistered, event_type)
	}

	return p.pool.Submit(func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("task panic: %v", recovered)
			}
			on_done(err)
		}()
		if err = ctx.Err(); err == nil {
			err = handle(ctx, payload)
		}
	})
}

func (p *WorkerPool) ReleaseTimeout(ctx context.Context, dt time.Duration) error {
	return p.pool.ReleaseTimeout(dt)
}
