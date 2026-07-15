package store

import "time"

// Recording is a scheduled or completed DVR capture of a Live TV channel. It
// serves double duty as a Jellyfin "timer" (while scheduled/recording) and a
// "recording" (once completed).
type Recording struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	ProgramID   string    `json:"program_id,omitempty"`
	Name        string    `json:"name"`
	Path        string    `json:"path,omitempty"` // output file, set once recording begins
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	Status      string    `json:"status"` // scheduled|recording|completed|failed|cancelled
	CreatedAt   time.Time `json:"created_at"`
}

// Recording status values.
const (
	RecScheduled = "scheduled"
	RecRecording = "recording"
	RecCompleted = "completed"
	RecFailed    = "failed"
	RecCancelled = "cancelled"
)
