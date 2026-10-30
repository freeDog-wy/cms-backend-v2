package pkg

import (
	"context"
	"fmt"

	"github.com/alexedwards/argon2id"
	"golang.org/x/sync/semaphore"
)

type PasswordHasher interface {
	Hash(ctx context.Context, password string) (string, error)
	Verify(ctx context.Context, password, hash string) (bool, error)
}

// Argon2Hasher shares one calculation budget across hashing and verification.
// Create one instance per process and inject it into all password consumers.
// Calls wait for capacity until their context is canceled or expires.
type Argon2Hasher struct {
	concurrency *semaphore.Weighted
	params      argon2id.Params
}

var _ PasswordHasher = (*Argon2Hasher)(nil)

func NewArgon2Hasher(maxConcurrency int) (*Argon2Hasher, error) {
	if maxConcurrency <= 0 {
		return nil, fmt.Errorf("argon2 max concurrency must be positive")
	}
	return &Argon2Hasher{
		concurrency: semaphore.NewWeighted(int64(maxConcurrency)),
		params: argon2id.Params{
			Memory:      19 * 1024, // KiB; preserve the project's existing parameters.
			Iterations:  2,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	}, nil
}

func (h *Argon2Hasher) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := h.concurrency.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	release := func() { h.concurrency.Release(1) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (h *Argon2Hasher) Hash(ctx context.Context, password string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	// Argon2 itself cannot be canceled. Keep the permit until it actually returns.
	hash, err := argon2id.CreateHash(password, &h.params)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hash, nil
}

func (h *Argon2Hasher) Verify(ctx context.Context, password, hash string) (bool, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	// The stored hash supplies its parameters, including those of older accounts.
	match, err := argon2id.ComparePasswordAndHash(password, hash)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return match, nil
}
