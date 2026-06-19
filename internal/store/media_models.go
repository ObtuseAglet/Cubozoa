package store

import "time"

// Library is a top-level media collection (e.g. "Movies"), backed by a
// directory on disk. It maps to a Jellyfin "CollectionFolder" / view.
type Library struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // CollectionType: movies, tvshows, music, homevideos, mixed
	Path      string    `json:"path"`
	ItemCount int       `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
	ScannedAt time.Time `json:"scanned_at"`
}

// MediaItem is a single browsable entry produced by scanning a library. The
// on-disk Path is kept internal: it is never exposed to non-admin clients, so a
// compromised or curious account cannot map the server's filesystem layout.
type MediaItem struct {
	ID             string    `json:"id"`
	LibraryID      string    `json:"library_id"`
	ParentID       string    `json:"parent_id"`
	Name           string    `json:"name"`
	SortName       string    `json:"sort_name"`
	Type           string    `json:"type"`       // Movie, Episode, Video, Audio, Folder
	MediaType      string    `json:"media_type"` // Video, Audio
	Path           string    `json:"path"`
	Container      string    `json:"container"` // file extension without the dot
	ProductionYear int       `json:"production_year,omitempty"`
	SizeBytes      int64     `json:"size_bytes"`
	DateCreated    time.Time `json:"date_created"`

	// Local artwork discovered next to the media file. Paths are internal and
	// never sent to clients; the tags are content fingerprints clients use for
	// cache-busting on the image endpoints.
	PrimaryImagePath  string `json:"primary_image_path,omitempty"`
	PrimaryImageTag   string `json:"primary_image_tag,omitempty"`
	BackdropImagePath string `json:"backdrop_image_path,omitempty"`
	BackdropImageTag  string `json:"backdrop_image_tag,omitempty"`
}

// UserItemData is per-user, per-item playback state: resume position, play
// count, watched and favorite flags. It is stored separately from the shared
// MediaItem catalog so a re-scan never disturbs a user's progress.
type UserItemData struct {
	UserID                string    `json:"user_id"`
	ItemID                string    `json:"item_id"`
	PlaybackPositionTicks int64     `json:"playback_position_ticks"`
	PlayCount             int       `json:"play_count"`
	Played                bool      `json:"played"`
	IsFavorite            bool      `json:"is_favorite"`
	LastPlayedAt          time.Time `json:"last_played_at"`
}
