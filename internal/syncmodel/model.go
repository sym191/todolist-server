package syncmodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	EntityProject     = "project"
	EntityTask        = "task"
	EntityBoardColumn = "board_column"
)

type Mutation struct {
	ID            string          `json:"id"`
	EntityType    string          `json:"entityType"`
	EntityID      string          `json:"entityId"`
	OperationType string          `json:"operationType"`
	BaseVersion   *int64          `json:"baseVersion,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

func (m Mutation) Validate() error {
	if strings.TrimSpace(m.ID) == "" || len(m.ID) > 200 {
		return errors.New("mutation id is required and must not exceed 200 characters")
	}
	if strings.TrimSpace(m.EntityID) == "" || len(m.EntityID) > 200 {
		return errors.New("entity id is required and must not exceed 200 characters")
	}
	switch m.EntityType {
	case EntityProject, EntityTask, EntityBoardColumn:
	default:
		return fmt.Errorf("unsupported entity type %q", m.EntityType)
	}
	if strings.TrimSpace(m.OperationType) == "" || len(m.OperationType) > 80 {
		return errors.New("operation type is required and must not exceed 80 characters")
	}
	if len(m.Payload) == 0 || string(m.Payload) == "null" {
		return errors.New("payload is required")
	}
	return nil
}

func (m Mutation) IsDelete() bool {
	return strings.Contains(strings.ToLower(m.OperationType), "delete")
}

type Project struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ColorValue  int64      `json:"colorValue"`
	Type        string     `json:"type"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}

type Task struct {
	ID               string     `json:"id"`
	Handle           string     `json:"handle"`
	ProjectID        string     `json:"projectId"`
	Title            string     `json:"title"`
	Summary          string     `json:"summary"`
	Description      string     `json:"description"`
	Status           string     `json:"status"`
	BoardStatusID    string     `json:"boardStatusId"`
	Urgency          string     `json:"urgency"`
	ScheduledDate    time.Time  `json:"scheduledDate"`
	StartTime        *time.Time `json:"startTime,omitempty"`
	EndTime          *time.Time `json:"endTime,omitempty"`
	HasTimeRange     bool       `json:"hasTimeRange"`
	IsFavorite       bool       `json:"isFavorite"`
	Progress         int        `json:"progress"`
	EstimatedMinutes *int       `json:"estimatedMinutes,omitempty"`
	ActualMinutes    *int       `json:"actualMinutes,omitempty"`
	BoardOrder       string     `json:"boardOrder"`
	Version          int64      `json:"version"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
	DeletedAt        *time.Time `json:"deletedAt,omitempty"`
}

type BoardColumn struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"projectId"`
	Label        string     `json:"label"`
	LinkedStatus *string    `json:"linkedStatus,omitempty"`
	SortOrder    int        `json:"sortOrder"`
	Version      int64      `json:"version"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	DeletedAt    *time.Time `json:"deletedAt,omitempty"`
}

type MutationResult struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Revision  int64           `json:"revision"`
	Canonical json.RawMessage `json:"canonical"`
}

type Change struct {
	Revision      int64           `json:"revision"`
	EntityType    string          `json:"entityType"`
	EntityID      string          `json:"entityId"`
	OperationType string          `json:"operationType"`
	Payload       json.RawMessage `json:"payload"`
	ChangedAt     time.Time       `json:"changedAt"`
}

type Snapshot struct {
	Cursor       int64         `json:"cursor"`
	Projects     []Project     `json:"projects"`
	Tasks        []Task        `json:"tasks"`
	BoardColumns []BoardColumn `json:"boardColumns"`
}
