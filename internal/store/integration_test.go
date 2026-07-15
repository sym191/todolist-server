package store_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sym191/todolist-server/internal/auth"
	"github.com/sym191/todolist-server/internal/database"
	"github.com/sym191/todolist-server/internal/store"
	"github.com/sym191/todolist-server/internal/syncmodel"
)

func TestSyncRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	backend := store.New(pool)
	passwordHash, err := auth.HashPassword("integration-test-password")
	if err != nil {
		t.Fatal(err)
	}
	refreshHash := []byte(uuid.NewString())
	user, _, err := backend.Register(
		ctx,
		"integration-"+uuid.NewString()+"@example.com",
		passwordHash,
		uuid.NewString(),
		"test device",
		refreshHash,
		time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	projectID := uuid.NewString()
	taskID := uuid.NewString()
	projectPayload := mustJSON(t, syncmodel.Project{
		ID: projectID, Name: "Test project", Type: "normal", CreatedAt: time.Now(),
	})
	taskPayload := mustJSON(t, syncmodel.Task{
		ID: taskID, Handle: "TASK-1", ProjectID: projectID, Title: "Test task",
		Status: "todo", BoardStatusID: "todo", Urgency: "normal",
		ScheduledDate: time.Now(), BoardOrder: "a0", CreatedAt: time.Now(),
	})
	mutations := []syncmodel.Mutation{
		{ID: uuid.NewString(), EntityType: syncmodel.EntityProject, EntityID: projectID, OperationType: "create_project", Payload: projectPayload},
		{ID: uuid.NewString(), EntityType: syncmodel.EntityTask, EntityID: taskID, OperationType: "create_task", Payload: taskPayload},
	}
	results, err := backend.ApplyMutations(ctx, user.ID, mutations)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Revision >= results[1].Revision {
		t.Fatalf("unexpected results: %+v", results)
	}

	repeated, err := backend.ApplyMutations(ctx, user.ID, mutations[:1])
	if err != nil {
		t.Fatal(err)
	}
	if repeated[0].Status != "already_applied" || repeated[0].Revision != results[0].Revision {
		t.Fatalf("mutation was not idempotent: %+v", repeated[0])
	}

	changes, cursor, hasMore, err := backend.PullChanges(ctx, user.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || cursor != results[1].Revision || hasMore {
		t.Fatalf("unexpected pull: changes=%d cursor=%d hasMore=%v", len(changes), cursor, hasMore)
	}

	snapshot, err := backend.Snapshot(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projects) != 1 || len(snapshot.Tasks) != 1 || snapshot.Cursor != cursor {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
