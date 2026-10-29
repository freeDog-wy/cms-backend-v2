package job

import (
	"context"
	"errors"
	"testing"
)

type outboxHandlerFunc func(context.Context) error

func (f outboxHandlerFunc) HandleTasks(ctx context.Context) error { return f(ctx) }

func TestOutboxJobSharesLifecycleWithSessionCleanup(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var taskCtx context.Context
	outboxJob := NewOutboxHandleJob(parent, outboxHandlerFunc(func(ctx context.Context) error {
		taskCtx = ctx
		return nil
	}))
	outboxJob.Run()
	if taskCtx != parent || taskCtx.Err() != nil {
		t.Fatal("outbox job must keep the shared context alive after Run")
	}

	cancel()
	if !errors.Is(taskCtx.Err(), context.Canceled) {
		t.Fatal("outbox work did not observe shutdown")
	}
	var cleanupCtx context.Context
	cleanupJob := NewSessionCleanupJob(parent, sessionCleanerFunc(func(ctx context.Context) (int64, error) {
		cleanupCtx = ctx
		return 0, ctx.Err()
	}))
	cleanupJob.Run()
	if cleanupCtx == nil || !errors.Is(cleanupCtx.Err(), context.Canceled) {
		t.Fatal("session cleanup did not observe the same shutdown")
	}
}
