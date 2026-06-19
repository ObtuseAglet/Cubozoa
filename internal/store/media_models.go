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

	// TV hierarchy. Series and Season are synthetic folder items (no media
	// file); Episodes link back to their season and series and carry their
	// numbering. ChildCount is populated for folder items.
	ChildCount        int    `json:"child_count,omitempty"`
	SeriesID          string `json:"series_id,omitempty"`
	SeriesName        string `json:"series_name,omitempty"`
	SeasonID          string `json:"season_id,omitempty"`
	IndexNumber       int    `json:"index_number,omitempty"`        // episode/track number, or season number on a Season
	ParentIndexNumber int    `json:"parent_index_number,omitempty"` // season number on an Episode

	// Music hierarchy. MusicArtist and MusicAlbum are synthetic folder items;
	// Audio tracks link back to their album and artist.
	ArtistID    string   `json:"artist_id,omitempty"`
	AlbumID     string   `json:"album_id,omitempty"`
	Album       string   `json:"album,omitempty"`
	AlbumArtist string   `json:"album_artist,omitempty"`
	Artists     []string `json:"artists,omitempty"`

	// Probed metadata (populated by ffprobe when available). RunTimeTicks is in
	// Jellyfin's 100-nanosecond ticks. Streams describes the contained tracks.
	RunTimeTicks int64             `json:"run_time_ticks,omitempty"`
	Width        int               `json:"width,omitempty"`
	Height       int               `json:"height,omitempty"`
	VideoCodec   string            `json:"video_codec,omitempty"`
	AudioCodec   string            `json:"audio_codec,omitempty"`
	Bitrate      int               `json:"bitrate,omitempty"`
	Streams      []MediaStreamInfo `json:"streams,omitempty"`

	// External sidecar subtitles discovered next to the media file. Paths are
	// internal; clients fetch them through the subtitle delivery endpoint.
	Subtitles []SubtitleTrack `json:"subtitles,omitempty"`

	// External metadata (populated by an optional provider such as TMDb).
	Overview        string   `json:"overview,omitempty"`
	CommunityRating float64  `json:"community_rating,omitempty"`
	Genres          []string `json:"genres,omitempty"`

	// Local artwork discovered next to the media file. Paths are internal and
	// never sent to clients; the tags are content fingerprints clients use for
	// cache-busting on the image endpoints.
	PrimaryImagePath  string `json:"primary_image_path,omitempty"`
	PrimaryImageTag   string `json:"primary_image_tag,omitempty"`
	BackdropImagePath string `json:"backdrop_image_path,omitempty"`
	BackdropImageTag  string `json:"backdrop_image_tag,omitempty"`
}

// MediaStreamInfo describes a single track within a media file, as discovered
// by ffprobe. Codec and language details let clients decide playability and
// pick tracks.
type MediaStreamInfo struct {
	Index     int    `json:"index"`
	Type      string `json:"type"` // Video, Audio, Subtitle
	Codec     string `json:"codec"`
	Language  string `json:"language,omitempty"`
	Channels  int    `json:"channels,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	IsDefault bool   `json:"is_default"`
	Title     string `json:"title,omitempty"`
}

// SubtitleTrack is an external sidecar subtitle file found next to a media file.
// The Path is internal; clients receive only a delivery URL.
type SubtitleTrack struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	Codec    string `json:"codec"`  // subrip, ass, ssa, webvtt
	Format   string `json:"format"` // file extension: srt, ass, ssa, vtt, sub
	Forced   bool   `json:"forced,omitempty"`
	Title    string `json:"title,omitempty"`
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
