package repo

import (
	"context"

	"gorm.io/datatypes"
)

//go:generate go run gorm.io/cli/gorm gen -i $GOFILE -o ./generated

type OutboxRepo[T any] interface {
	////////////////////////////
	// 向本地消息表插入事件消息，默认为pending状态
	// attempts = 0、max_attempts = 10
	// available_at、created_at、updated_at 为当前事务开始时间
	// 租约、错误和完成时间为 NULL
	//
	// INSERT INTO @@table (event_type, payload)
	// VALUES (@event_type, @payload::jsonb);
	InsertMessage(ctx context.Context, event_type string, payload datatypes.JSON) error

	////////////////////////////
	// 原子性领取批量事件
	//
	// WITH candidates AS (
	//     SELECT id
	//     FROM @@table
	//     WHERE status = 'pending'
	//       AND available_at <= statement_timestamp()
	//       AND attempts < max_attempts
	//     ORDER BY available_at, id
	//     LIMIT @batch_size
	//     FOR UPDATE SKIP LOCKED
	// )
	// UPDATE @@table AS m
	// SET status       = 'processing',
	//     attempts     = m.attempts + 1,
	//     lease_token  = gen_random_uuid(),
	//     locked_until = statement_timestamp()
	//                    + @locked_seconds::integer * INTERVAL '1 second',
	//     updated_at   = statement_timestamp()
	// FROM candidates AS c
	// WHERE m.id = c.id
	// RETURNING m.*;
	ClaimBatch(ctx context.Context, batch_size int, locked_seconds int) ([]T, error)

	////////////////////////////
	// 成功回写必须校验领取令牌
	//
	// UPDATE outbox_messages
	// SET status       = 'succeeded',
	//     lease_token  = NULL,
	//     locked_until = NULL,
	//     last_error   = NULL,
	//     finished_at  = statement_timestamp(),
	//     updated_at   = statement_timestamp()
	// WHERE id = @id
	//   AND status = 'processing'
	//   AND lease_token = @lease_token::uuid;
	MarkSucceeded(ctx context.Context, id int64, lease_token string) error

	////////////////////////////
	// 当前 worker 执行失败后调用
	// 可重试且次数未耗尽：回到 pending，延迟执行。
	// 永久错误或次数耗尽：进入 failed。
	// 保留令牌校验，防止旧 worker 修改已重新领取的消息。
	//
	// UPDATE @@table
	// SET status = CASE
	//         WHEN @permanent OR attempts >= max_attempts
	//             THEN 'failed'
	//         ELSE 'pending'
	//     END,
	//     available_at = CASE
	//         WHEN @permanent OR attempts >= max_attempts
	//             THEN available_at
	//         ELSE statement_timestamp()
	//              + @retry_seconds::integer * INTERVAL '1 second'
	//     END,
	//     last_error = left(@error_message::text, 2000),
	//     finished_at = CASE
	//         WHEN @permanent OR attempts >= max_attempts
	//             THEN statement_timestamp()
	//         ELSE NULL
	//     END,
	//     lease_token = NULL,
	//     locked_until = NULL,
	//     updated_at = statement_timestamp()
	// WHERE id = @id
	//   AND status = 'processing'
	//   AND lease_token = @lease_token::uuid;
	RetryOrFail(
		ctx context.Context, id int64, lease_token string,
		error_message string, retry_seconds int, permanent bool,
	) error

	////////////////////////////
	// 定时批量回收过期任务
	// 处理进程崩溃、任务卡住、结果回写失败后遗留的 processing 消息。
	//
	// WITH expired AS (
	//     SELECT id
	//     FROM @@table
	//     WHERE status = 'processing'
	//       AND locked_until <= statement_timestamp()
	//     ORDER BY locked_until, id
	//     LIMIT @batch_size
	//     FOR UPDATE SKIP LOCKED
	// )
	// UPDATE @@table AS m
	// SET status = CASE
	//         WHEN m.attempts >= m.max_attempts
	//             THEN 'failed'
	//         ELSE 'pending'
	//     END,
	//     available_at = CASE
	//         WHEN m.attempts >= m.max_attempts
	//             THEN m.available_at
	//         ELSE statement_timestamp()
	//              + @retry_seconds::integer * INTERVAL '1 second'
	//     END,
	//     last_error = 'processing lease expired',
	//     finished_at = CASE
	//         WHEN m.attempts >= m.max_attempts
	//             THEN statement_timestamp()
	//         ELSE NULL
	//     END,
	//     lease_token = NULL,
	//     locked_until = NULL,
	//     updated_at = statement_timestamp()
	// FROM expired AS e
	// WHERE m.id = e.id;
	RecoverExpired(ctx context.Context, batch_size int, retry_seconds int) error
}
