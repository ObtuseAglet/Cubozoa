package jellyfin

// PlaybackInfoResponse answers a client's "how can I play this?" request. For
// each playable media source it declares which playback methods are supported.
type PlaybackInfoResponse struct {
	MediaSources  []MediaSourceInfo `json:"MediaSources"`
	PlaySessionID string            `json:"PlaySessionId"`
}

// MediaSourceInfo describes one playable source for an item. Cubozoa currently
// advertises direct play / direct stream only; transcoding arrives with ffmpeg
// in a later milestone. The real on-disk path is never included.
type MediaSourceInfo struct {
	Protocol             string        `json:"Protocol"` // "File"
	ID                   string        `json:"Id"`
	Name                 string        `json:"Name"`
	IsRemote             bool          `json:"IsRemote"`
	Container            string        `json:"Container"`
	Size                 int64         `json:"Size,omitempty"`
	RunTimeTicks         int64         `json:"RunTimeTicks,omitempty"`
	SupportsDirectPlay   bool          `json:"SupportsDirectPlay"`
	SupportsDirectStream bool          `json:"SupportsDirectStream"`
	SupportsTranscoding  bool          `json:"SupportsTranscoding"`
	MediaStreams         []MediaStream `json:"MediaStreams"`
	RequiresOpening      bool          `json:"RequiresOpening"`
	RequiresClosing      bool          `json:"RequiresClosing"`
}

// MediaStream describes a single track (video, audio, subtitle) within a media
// source. Cubozoa cannot populate codec details until it probes files with
// ffprobe (a later milestone), so this is declared for forward-compatibility
// and currently sent as an empty list.
type MediaStream struct {
	Type      string `json:"Type"`
	Index     int    `json:"Index"`
	Codec     string `json:"Codec,omitempty"`
	Language  string `json:"Language,omitempty"`
	IsDefault bool   `json:"IsDefault"`
}

// PlaybackProgressInfo is the body clients POST to the playback-reporting
// endpoints. Only the fields Cubozoa acts on are declared; the rest are ignored.
type PlaybackProgressInfo struct {
	ItemID        string `json:"ItemId"`
	MediaSourceID string `json:"MediaSourceId"`
	PlaySessionID string `json:"PlaySessionId"`
	PositionTicks int64  `json:"PositionTicks"`
	IsPaused      bool   `json:"IsPaused"`
}
