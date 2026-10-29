package srv

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/infra"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"gorm.io/gorm"
)

type OutboxSrv struct {
	db         *gorm.DB
	workerPool *infra.WorkerPool
}

func NewOutboxSrv(db *gorm.DB, workerPool *infra.WorkerPool) *OutboxSrv {
	return &OutboxSrv{
		db:         db,
		workerPool: workerPool,
	}
}

func (o *OutboxSrv) HandleTasks(ctx context.Context) error {
	msgs, err := generated.OutboxRepo[model.OutboxMessage](o.db).ClaimBatch(ctx, 100, 30)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		err := o.workerPool.SubmitTask(
			ctx,
			msg.EventType,
			json.RawMessage(msg.Payload),
			func(err error) {
				if writeErr := completeOutboxTask(ctx, o.db, msg, err); writeErr != nil {
					log.Printf("outbox message %d result update failed: %v", msg.ID, writeErr)
				}
			},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func completeOutboxTask(ctx context.Context, db *gorm.DB, msg model.OutboxMessage, taskErr error) error {
	// 任务可能因服务退出而取消，仍需给状态回写一个有界的执行机会。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if taskErr == nil {
		return generated.OutboxRepo[model.OutboxMessage](db).MarkSucceeded(writeCtx, msg.ID, *msg.LeaseToken)
	}
	// 取消和超时允许重试；仍受消息表 max_attempts 限制。
	permanent := !errors.Is(taskErr, context.Canceled) && !errors.Is(taskErr, context.DeadlineExceeded)
	return generated.OutboxRepo[model.OutboxMessage](db).RetryOrFail(writeCtx, msg.ID, *msg.LeaseToken, taskErr.Error(), 0, permanent)
}
