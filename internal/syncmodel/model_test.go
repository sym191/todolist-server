package syncmodel

import (
	"encoding/json"
	"testing"
)

func TestMutationValidate(t *testing.T) {
	mutation := Mutation{
		ID:            "m-1",
		EntityType:    EntityTask,
		EntityID:      "task-1",
		OperationType: "update_task",
		Payload:       json.RawMessage(`{"id":"task-1"}`),
	}
	if err := mutation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	mutation.EntityType = "unknown"
	if err := mutation.Validate(); err == nil {
		t.Fatal("Validate() expected unsupported entity error")
	}
}
