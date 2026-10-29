package pkg

import (
	"context"
	"errors"
	"sync"
	"testing"

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

func TestArgon2HasherSharedBudgetAndErrorRelease(t *testing.T) {
	h, err := NewArgon2Hasher(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	release, acquired := h.concurrency.TryAcquire("argon2")
	if !acquired {
		t.Fatal("could not occupy calculation slot")
	}
	defer release()
	if _, err := h.Hash(ctx, "password"); !errors.Is(err, ErrPasswordHasherBusy) {
		t.Fatalf("Hash while saturated: %v", err)
	}
	if _, err := h.Verify(ctx, "password", "invalid hash"); !errors.Is(err, ErrPasswordHasherBusy) {
		t.Fatalf("Verify must use the same budget: %v", err)
	}
	release()

	if _, err := h.Verify(ctx, "password", "invalid hash"); !errors.Is(err, argon2id.ErrInvalidHash) {
		t.Fatalf("expected hash parsing error: %v", err)
	}
	if _, err := h.Hash(ctx, "password"); err != nil {
		t.Fatalf("Verify error leaked the calculation slot: %v", err)
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
	release, acquired := h.concurrency.TryAcquire("argon2")
	if !acquired {
		t.Fatal("canceled operation leaked the calculation slot")
	}
	release()
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
		if err != nil && !errors.Is(err, ErrPasswordHasherBusy) {
			t.Errorf("unexpected concurrent result: %v", err)
		}
	}
	// Both slots must remain usable after the concurrent work completes.
	releaseFirst, first := h.concurrency.TryAcquire("argon2")
	if !first {
		t.Fatal("first slot leaked")
	}
	defer releaseFirst()
	releaseSecond, second := h.concurrency.TryAcquire("argon2")
	if !second {
		t.Fatal("second slot leaked")
	}
	defer releaseSecond()
	if releaseExtra, extra := h.concurrency.TryAcquire("argon2"); extra {
		releaseExtra()
		t.Fatal("budget exceeded two calculations")
	}
}
