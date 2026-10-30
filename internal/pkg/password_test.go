package pkg

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
)

func TestArgon2HasherRoundTrip(t *testing.T) {
	h, err := NewArgon2Hasher(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash, err := h.Hash(ctx, "correct password")
	if err != nil {
		t.Fatal(err)
	}
	params, _, _, err := argon2id.DecodeHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	if params.Memory != 19*1024 || params.Iterations != 2 || params.Parallelism != 1 {
		t.Fatalf("unexpected parameters: %+v", params)
	}
	for _, tc := range []struct {
		password string
		want     bool
	}{{"correct password", true}, {"wrong password", false}} {
		match, err := h.Verify(ctx, tc.password, hash)
		if err != nil || match != tc.want {
			t.Fatalf("Verify: match=%v err=%v, want %v", match, err, tc.want)
		}
	}
}

func TestArgon2HasherErrorRelease(t *testing.T) {
	h, err := NewArgon2Hasher(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := h.Verify(ctx, "password", "invalid hash"); !errors.Is(err, argon2id.ErrInvalidHash) {
		t.Fatalf("expected hash parsing error: %v", err)
	}
	if _, err := h.Hash(ctx, "password"); err != nil {
		t.Fatalf("Verify error leaked the calculation slot: %v", err)
	}
}

func TestArgon2HasherWaitsForCapacity(t *testing.T) {
	for _, operation := range []string{"Hash", "Verify"} {
		for _, outcome := range []string{"release", "cancel", "deadline"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				h, err := NewArgon2Hasher(1)
				if err != nil {
					t.Fatal(err)
				}
				hash, err := h.Hash(context.Background(), "password")
				if err != nil {
					t.Fatal(err)
				}
				if !h.concurrency.TryAcquire(1) {
					t.Fatal("could not occupy calculation slot")
				}
				var release sync.Once
				freeSlot := func() { release.Do(func() { h.concurrency.Release(1) }) }
				t.Cleanup(freeSlot)

				timeout := 5 * time.Second
				if outcome == "deadline" {
					timeout = 200 * time.Millisecond
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				result := make(chan error, 1)
				go func() {
					if operation == "Hash" {
						_, err := h.Hash(ctx, "password")
						result <- err
						return
					}
					match, err := h.Verify(ctx, "password", hash)
					if err == nil && !match {
						err = errors.New("valid password did not match")
					}
					result <- err
				}()
				select {
				case err := <-result:
					t.Fatalf("operation returned while capacity was occupied: %v", err)
				case <-time.After(20 * time.Millisecond):
				}

				var wantErr error
				switch outcome {
				case "release":
					freeSlot()
				case "cancel":
					wantErr = context.Canceled
					cancel()
				case "deadline":
					wantErr = context.DeadlineExceeded
				}
				select {
				case err := <-result:
					if !errors.Is(err, wantErr) {
						t.Fatalf("got %v, want %v", err, wantErr)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("operation did not finish")
				}
				freeSlot()
				if !h.concurrency.TryAcquire(1) {
					t.Fatal("operation leaked the calculation slot")
				}
				h.concurrency.Release(1)
			})
		}
	}
}

func TestArgon2HasherCanceledContext(t *testing.T) {
	h, err := NewArgon2Hasher(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Hash(ctx, "password"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash: %v", err)
	}
	if _, err := h.Verify(ctx, "password", "invalid hash"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify: %v", err)
	}
	if !h.concurrency.TryAcquire(1) {
		t.Fatal("canceled operation leaked the calculation slot")
	}
	h.concurrency.Release(1)
}

func TestArgon2HasherVerifiesExistingParameters(t *testing.T) {
	h, err := NewArgon2Hasher(1)
	if err != nil {
		t.Fatal(err)
	}
	// Older hashes retain the settings encoded when they were created.
	legacy := argon2id.Params{
		Memory: 64 * 1024, Iterations: 1, Parallelism: 1,
		SaltLength: 16, KeyLength: 32,
	}
	hash, err := argon2id.CreateHash("legacy password", &legacy)
	if err != nil {
		t.Fatal(err)
	}
	match, err := h.Verify(context.Background(), "legacy password", hash)
	if err != nil || !match {
		t.Fatalf("legacy verification: match=%v err=%v", match, err)
	}
}

func TestArgon2HasherRejectsInvalidConcurrency(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := NewArgon2Hasher(limit); err == nil {
			t.Fatalf("accepted invalid limit %d", limit)
		}
	}
}

func TestArgon2HasherConcurrentCalls(t *testing.T) {
	h, err := NewArgon2Hasher(2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash, err := h.Hash(ctx, "password")
	if err != nil {
		t.Fatal(err)
	}

	const requests = 16
	start := make(chan struct{})
	results := make(chan error, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				_, err := h.Hash(ctx, "password")
				results <- err
				return
			}
			match, err := h.Verify(ctx, "password", hash)
			if err == nil && !match {
				results <- errors.New("valid password did not match")
				return
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("unexpected concurrent result: %v", err)
		}
	}
	// Both slots must remain usable after the concurrent work completes.
	if !h.concurrency.TryAcquire(1) {
		t.Fatal("first slot leaked")
	}
	defer h.concurrency.Release(1)
	if !h.concurrency.TryAcquire(1) {
		t.Fatal("second slot leaked")
	}
	defer h.concurrency.Release(1)
	if h.concurrency.TryAcquire(1) {
		h.concurrency.Release(1)
		t.Fatal("budget exceeded two calculations")
	}
}
