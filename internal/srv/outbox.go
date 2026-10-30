package srv

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/infra"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"golang.org/x/sync/semaphore"
	"gorm.io/gorm"
)

const outboxMaxInFlight = 32

type OutboxSrv struct {
	db           *gorm.DB
	workerPool   *infra.WorkerPool
	scanMu       sync.Mutex
	capacity     *semaphore.Weighted
	leaseSeconds int
}

func NewOutboxSrv(db *gorm.DB, workerPool *infra.WorkerPool, taskTimeout time.Duration) *OutboxSrv {
	return &OutboxSrv{
		db:           db,
		workerPool:   workerPool,
		capacity:     semaphore.NewWeighted(outboxMaxInFlight),
		leaseSeconds: int(math.Ceil(taskTimeout.Seconds())),
	}
}

func (o *OutboxSrv) HandleTasks(ctx context.Context) error {
	if !o.scanMu.TryLock() {
		return nil
	}
	defer o.scanMu.Unlock()
	r := generated.OutboxRepo[model.OutboxMessage](o.db)
	if err := r.RecoverExpired(ctx, 100, 30); err != nil {
		return err
	}
	// 领取前预留执行额度，避免每轮扫描继续堆积已领取任务。
	batchSize := 0
	for batchSize < outboxMaxInFlight && o.capacity.TryAcquire(1) {
		batchSize++
	}
	if batchSize == 0 {
		return nil
	}
	msgs, err := r.ClaimBatch(ctx, batchSize, o.leaseSeconds)
	if err != nil {
		o.capacity.Release(int64(batchSize))
		return err
	}
	o.capacity.Release(int64(batchSize - len(msgs)))

	var submitErrors []error
	for _, msg := range msgs {
		// 截止时间从领取时开始计算，覆盖工作池排队及邮件并发额度等待。
		taskCtx, cancel := context.WithDeadline(ctx, msg.LockedUntil.Add(-5*time.Second))
		complete := func(taskErr error) {
			defer o.capacity.Release(1)
			defer cancel()
			if writeErr := completeOutboxTask(taskCtx, o.db, msg, taskErr); writeErr != nil {
				log.Printf("outbox message %d result update failed: %v", msg.ID, writeErr)
			}
		}
		err := o.workerPool.SubmitTask(
			taskCtx,
			msg.EventType,
			json.RawMessage(msg.Payload),
			complete,
		)
		if err != nil {
			complete(err)
			submitErrors = append(submitErrors, err)
		}
	}
	return errors.Join(submitErrors...)
}

func completeOutboxTask(ctx context.Context, db *gorm.DB, msg model.OutboxMessage, taskErr error) error {
	// 任务可能因服务退出而取消，仍需给状态回写一个有界的执行机会。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if taskErr == nil {
		return generated.OutboxRepo[model.OutboxMessage](db).MarkSucceeded(writeCtx, msg.ID, *msg.LeaseToken)
	}
	return generated.OutboxRepo[model.OutboxMessage](db).RetryOrFail(writeCtx, msg.ID, *msg.LeaseToken,
		taskErr.Error(), outboxRetryDelay(msg.Attempts), outboxTaskPermanent(taskErr))
}

func outboxTaskPermanent(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, infra.ErrInvalidTaskPayload) || errors.Is(err, infra.ErrTaskNotRegistered) {
		return true
	}
	var emailErr *pkg.EmailError
	if errors.As(err, &emailErr) {
		return !emailErr.Retryable
	}
	// 数据库等未分类执行错误允许有界重试，仍受 max_attempts 限制。
	return false
}

func outboxRetryDelay(attempts int32) int {
	base := min(900, 10<<min(max(int(attempts)-1, 0), 7))
	return min(900, base+rand.IntN(base/5+1))
}
