package job

import (
	"context"
	"log"
)

type outboxHandler interface {
	HandleTasks(ctx context.Context) error
}

type OutboxHandleJob struct {
	ctx       context.Context
	outboxSrv outboxHandler
}

func NewOutboxHandleJob(ctx context.Context, outboxSrv outboxHandler) *OutboxHandleJob {
	return &OutboxHandleJob{
		ctx:       ctx,
		outboxSrv: outboxSrv,
	}
}

func (job *OutboxHandleJob) Run() {
	// 工作池异步执行任务，不能在本轮 Run 返回时取消其上下文。
	err := job.outboxSrv.HandleTasks(job.ctx)
	if err != nil {
		log.Printf("outbox job err: %s", err.Error())
	}
}
