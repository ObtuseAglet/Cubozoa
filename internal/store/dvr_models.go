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

	// SeriesTimerID links a recording auto-created by a series timer back to it.
	SeriesTimerID string `json:"series_timer_id,omitempty"`
}

// SeriesTimer records every upcoming airing whose title matches, optionally
// limited to one channel.
type SeriesTimer struct {
	ID               string    `json:"id"`
	ChannelID        string    `json:"channel_id,omitempty"` // empty when RecordAnyChannel
	ChannelName      string    `json:"channel_name,omitempty"`
	Name             string    `json:"name"` // program/series title to match
	RecordAnyChannel bool      `json:"record_any_channel,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// Recording status values.
const (
	RecScheduled = "scheduled"
	RecRecording = "recording"
	RecCompleted = "completed"
	RecFailed    = "failed"
	RecCancelled = "cancelled"
)
