package job

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sessionCleanerFunc func(context.Context) (int64, error)

func (f sessionCleanerFunc) CleanupExpired(ctx context.Context) (int64, error) {
	return f(ctx)
}

func TestSessionCleanupJobDeadlineAndCancellation(t *testing.T) {
	var jobCtx context.Context
	started := time.Now()
	j := NewSessionCleanupJob(context.Background(), sessionCleanerFunc(func(ctx context.Context) (int64, error) {
		jobCtx = ctx
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(started) || time.Until(deadline) > 30*time.Second {
			t.Fatalf("invalid cleanup deadline: %v (present=%v)", deadline, ok)
		}
		return 0, nil
	}))
	j.Run()
	if jobCtx == nil || !errors.Is(jobCtx.Err(), context.Canceled) {
		t.Fatal("job did not release its context")
	}
}

func TestSessionCleanupJobPropagatesShutdown(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	j := NewSessionCleanupJob(parent, sessionCleanerFunc(func(ctx context.Context) (int64, error) {
		called = true
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("shutdown cancellation not propagated: %v", ctx.Err())
		}
		return 0, ctx.Err()
	}))
	j.Run()
	if !called {
		t.Fatal("cleanup was not invoked")
	}
}
