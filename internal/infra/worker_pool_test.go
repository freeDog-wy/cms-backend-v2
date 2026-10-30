package infra

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestWorkerPoolAlwaysCompletes(t *testing.T) {
	pool, err := NewWorkerPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.ReleaseTimeout(context.Background(), time.Second) })
	if err := pool.RegisterTask("panic", func(context.Context, json.RawMessage) error { panic("test") }); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 1)
	if err := pool.SubmitTask(context.Background(), "panic", nil, func(err error) { results <- err }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-results:
		if err == nil {
			t.Fatal("panic lost")
		}
	case <-time.After(time.Second):
		t.Fatal("completion skipped after panic")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pool.SubmitTask(ctx, "panic", nil, func(err error) { results <- err }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-results:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled task did not complete")
	}
}
