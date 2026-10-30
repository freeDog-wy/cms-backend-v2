package srv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/event"
	"github.com/freeDog-wy/cms-backend-v2/internal/infra"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"github.com/freeDog-wy/cms-backend-v2/internal/task"
	"github.com/freeDog-wy/cms-backend-v2/internal/testutil"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestGuestCommentEventTransaction(t *testing.T) {
	db := testutil.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := uuid.NewString()
	s := NewCommentSrv(db, "https://example.com")
	s.manifestArticles, s.manifestLastSuccess = map[string]bool{key: true}, time.Now()
	cmd := CreateGuestCommentCmd{ArticleKey: key, RequestID: uuid.NewString(), GuestName: "访客", Content: "请审核这条评论"}
	const concurrent = 8
	var wg sync.WaitGroup
	ids, errs := make(chan int64, concurrent), make(chan error, concurrent)
	for range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.CreateGuest(ctx, cmd)
			if err != nil {
				errs <- err
				return
			}
			ids <- result.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id int64
	for got := range ids {
		if id != 0 && got != id {
			t.Fatal("duplicate submission created different comments")
		}
		id = got
	}
	if id == 0 {
		t.Fatal("no comment created")
	}
	var messages []model.OutboxMessage
	if err := db.Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].EventType != event.CommentSubmitted || messages[0].Status != model.OutboxStatusPending {
		t.Fatalf("events=%+v", messages)
	}
	var payload event.CommentSubmittedPayload
	if err := json.Unmarshal(messages[0].Payload, &payload); err != nil || payload.CommentID != id {
		t.Fatalf("payload=%s err=%v", messages[0].Payload, err)
	}
	cmd.Content = "different content"
	if _, err := s.CreateGuest(ctx, cmd); !errors.Is(err, errorx.ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	// 回复同样通知；管理员直接发布不产生待审核通知。
	if err := db.Exec("UPDATE comments SET status='approved' WHERE id=?", id).Error; err != nil {
		t.Fatal(err)
	}
	cmd.ParentID, cmd.RequestID = &id, uuid.NewString()
	if _, err := s.CreateGuest(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	var actor int64
	if err := db.Raw("INSERT INTO users(username,password_hash) VALUES ('admin','test') RETURNING id").Scan(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateStaff(ctx, actor, CreateStaffCommentCmd{ArticleKey: key, Content: "管理员评论"}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.OutboxMessage{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
	// 强制 Outbox 插入失败，证明新评论也随事务回滚。
	if err := db.Exec("ALTER TABLE outbox_messages ADD CONSTRAINT reject_test_notification CHECK(event_type <> 'comment.submitted') NOT VALID").Error; err != nil {
		t.Fatal(err)
	}
	cmd.RequestID = uuid.NewString()
	if _, err := s.CreateGuest(ctx, cmd); err == nil {
		t.Fatal("expected event insertion failure")
	}
	if err := db.Model(&model.Comment{}).Count(&count).Error; err != nil || count != 3 {
		t.Fatalf("comment rollback failed: count=%d err=%v", count, err)
	}
	if err := db.Model(&model.OutboxMessage{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
}

type notificationRecorder struct {
	mu       sync.Mutex
	messages []pkg.EmailMessage
}

func (r *notificationRecorder) Send(_ context.Context, msg pkg.EmailMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
	return nil
}

func TestCommentNotificationThroughOutbox(t *testing.T) {
	db := testutil.Postgres(t)
	pool, err := infra.NewWorkerPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.ReleaseTimeout(context.Background(), 3*time.Second); err != nil {
			t.Error(err)
		}
	})
	recorder := &notificationRecorder{}
	notifier, err := task.NewCommentReviewTask(db, recorder, "372525247@qq.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.RegisterTask(event.CommentSubmitted, notifier.Handle); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	s := NewCommentSrv(db, "https://example.com")
	s.manifestArticles, s.manifestLastSuccess = map[string]bool{key: true}, time.Now()
	for i := range 2 {
		comment, err := s.CreateGuest(context.Background(), CreateGuestCommentCmd{ArticleKey: key, RequestID: uuid.NewString(), GuestName: "访客", Content: fmt.Sprintf("评论%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := db.Exec("UPDATE comments SET status='approved' WHERE id=?", comment.ID).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	outbox := NewOutboxSrv(db, pool, 20*time.Second)
	var pendingComments []model.Comment
	if err := db.Order("id").Find(&pendingComments).Error; err != nil {
		t.Fatal(err)
	}
	if len(pendingComments) != 2 || pendingComments[0].Status != model.CommentStatusPending || pendingComments[1].Status != model.CommentStatusApproved {
		t.Fatal("unexpected comments before notification dispatch")
	}
	if err := outbox.HandleTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitOutbox(t, db, func(rows []model.OutboxMessage) bool {
		return len(rows) == 2 && rows[0].Status == model.OutboxStatusSucceeded && rows[1].Status == model.OutboxStatusSucceeded
	})
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.messages) != 1 || recorder.messages[0].To != "372525247@qq.com" {
		t.Fatalf("notifications=%+v", recorder.messages)
	}
}

func waitOutbox(t *testing.T, db *gorm.DB, ready func([]model.OutboxMessage) bool) []model.OutboxMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var rows []model.OutboxMessage
	for time.Now().Before(deadline) {
		rows = nil
		if err := db.Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if ready(rows) {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("outbox did not reach expected state: %+v", rows)
	return nil
}

func TestOutboxRetryRecoveryAndLeaseFencing(t *testing.T) {
	db := testutil.Postgres(t)
	pool, err := infra.NewWorkerPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.ReleaseTimeout(context.Background(), 3*time.Second) })
	for _, kind := range []string{"temporary", "permanent", "panic", "deadline"} {
		if err := pool.RegisterTask(kind, func(ctx context.Context, _ json.RawMessage) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 26*time.Second {
				return fmt.Errorf("missing task lease deadline")
			}
			switch kind {
			case "temporary":
				return &pkg.EmailError{Retryable: true, Err: errors.New("temporary SMTP error")}
			case "permanent":
				return &pkg.EmailError{Err: errors.New("permanent SMTP error")}
			case "panic":
				panic("test")
			default:
				return context.DeadlineExceeded
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	r := generated.OutboxRepo[model.OutboxMessage](db)
	for _, kind := range []string{"temporary", "permanent", "panic", "deadline", "unregistered"} {
		if err := r.InsertMessage(context.Background(), kind, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	outbox := NewOutboxSrv(db, pool, 20*time.Second)
	if err := outbox.HandleTasks(context.Background()); !errors.Is(err, infra.ErrTaskNotRegistered) {
		t.Fatalf("expected submission error: %v", err)
	}
	rows := waitOutbox(t, db, func(rows []model.OutboxMessage) bool {
		if len(rows) != 5 {
			return false
		}
		for _, row := range rows {
			if row.Status == model.OutboxStatusProcessing {
				return false
			}
		}
		return true
	})
	for _, row := range rows {
		want := model.OutboxStatusPending
		if row.EventType == "permanent" || row.EventType == "unregistered" {
			want = model.OutboxStatusFailed
		}
		if row.Status != want || row.Attempts != 1 {
			t.Fatalf("event=%s status=%s attempts=%d", row.EventType, row.Status, row.Attempts)
		}
		if want == model.OutboxStatusPending && time.Until(row.AvailableAt) < 8*time.Second {
			t.Fatal("retry backoff not applied")
		}
	}
	// 达到最大次数后，即使是临时错误也进入 failed。
	if err := db.Exec("UPDATE outbox_messages SET attempts=max_attempts-1, available_at=now()-interval '1 second' WHERE event_type='temporary'").Error; err != nil {
		t.Fatal(err)
	}
	if err := outbox.HandleTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitOutbox(t, db, func(rows []model.OutboxMessage) bool { return rows[0].Status == model.OutboxStatusFailed })
	// 构造进程退出遗留的租约，扫描时回收并延迟重试。
	if err := db.Exec("UPDATE outbox_messages SET status='processing',lease_token=gen_random_uuid(),locked_until=now()-interval '1 second' WHERE event_type='panic'").Error; err != nil {
		t.Fatal(err)
	}
	if err := outbox.HandleTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recovered model.OutboxMessage
	if err := db.Where("event_type='panic'").First(&recovered).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.Status != model.OutboxStatusPending || recovered.LeaseToken != nil || time.Until(recovered.AvailableAt) < 28*time.Second {
		t.Fatalf("lease not recovered: %+v", recovered)
	}
	if err := db.Exec("UPDATE outbox_messages SET available_at=now()-interval '1 second' WHERE id=?", recovered.ID).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := r.ClaimBatch(context.Background(), 1, 30)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := r.MarkSucceeded(context.Background(), claimed[0].ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var fenced model.OutboxMessage
	if err := db.First(&fenced, claimed[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if fenced.Status != model.OutboxStatusProcessing {
		t.Fatal("stale worker overwrote current lease")
	}
}

func TestOutboxBoundsInFlightTasksAcrossScans(t *testing.T) {
	db := testutil.Postgres(t)
	pool, err := infra.NewWorkerPool()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		if err := pool.ReleaseTimeout(context.Background(), 3*time.Second); err != nil {
			t.Error(err)
		}
	})
	if err := pool.RegisterTask("blocked", func(ctx context.Context, _ json.RawMessage) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	r := generated.OutboxRepo[model.OutboxMessage](db)
	for range outboxMaxInFlight + 1 {
		if err := r.InsertMessage(ctx, "blocked", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	outbox := NewOutboxSrv(db, pool, 20*time.Second)
	if err := outbox.HandleTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := outbox.HandleTasks(ctx); err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err := db.Model(&model.OutboxMessage{}).Where("status='pending'").Count(&pending).Error; err != nil || pending != 1 {
		t.Fatalf("additional scan exceeded capacity: pending=%d err=%v", pending, err)
	}
	close(release)
	waitOutbox(t, db, func(rows []model.OutboxMessage) bool {
		completed := 0
		for _, row := range rows {
			if row.Status == model.OutboxStatusSucceeded {
				completed++
			}
		}
		return completed == outboxMaxInFlight
	})
	// 状态提交先于回调归还额度，等待回调结束后再检查全部额度。
	deadline := time.Now().Add(3 * time.Second)
	for !outbox.capacity.TryAcquire(outboxMaxInFlight) {
		if time.Now().After(deadline) {
			t.Fatal("completed tasks leaked capacity")
		}
		time.Sleep(time.Millisecond)
	}
	outbox.capacity.Release(outboxMaxInFlight)
	if err := outbox.HandleTasks(ctx); err != nil {
		t.Fatal(err)
	}
	waitOutbox(t, db, func(rows []model.OutboxMessage) bool {
		for _, row := range rows {
			if row.Status != model.OutboxStatusSucceeded {
				return false
			}
		}
		return len(rows) == outboxMaxInFlight+1
	})
}
