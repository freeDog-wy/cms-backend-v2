package srv

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/freeDog-wy/cms-backend-v2/internal/infra"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
)

func TestOutboxFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err       error
		permanent bool
	}{
		{context.Canceled, false}, {context.DeadlineExceeded, false},
		{fmt.Errorf("wrapped: %w", infra.ErrInvalidTaskPayload), true}, {infra.ErrTaskNotRegistered, true},
		{&pkg.EmailError{Retryable: true, Err: errors.New("451")}, false},
		{&pkg.EmailError{Retryable: false, Err: errors.New("550")}, true},
		{errors.New("database unavailable"), false},
	} {
		if got := outboxTaskPermanent(tc.err); got != tc.permanent {
			t.Errorf("%v: permanent=%v", tc.err, got)
		}
	}
	for _, attempt := range []int32{0, 1, 2, 5, 10, 100} {
		base := min(900, 10<<min(max(int(attempt)-1, 0), 7))
		for range 10 {
			if delay := outboxRetryDelay(attempt); delay < base || delay > min(900, base+base/5) {
				t.Fatalf("attempt %d: delay %d", attempt, delay)
			}
		}
	}
}
