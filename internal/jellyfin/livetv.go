package jellyfin

// LiveTvInfo is the GET /LiveTv/Info response. Clients read IsEnabled to decide
// whether to show the Live TV section.
type LiveTvInfo struct {
	IsEnabled    bool                `json:"IsEnabled"`
	EnabledUsers []string            `json:"EnabledUsers"`
	Services     []LiveTvServiceInfo `json:"Services"`
}

// LiveTvServiceInfo names a tuner/service backing Live TV.
type LiveTvServiceInfo struct {
	Name               string `json:"Name"`
	IsVisible          bool   `json:"IsVisible"`
	HasUpdateAvailable bool   `json:"HasUpdateAvailable"`
	Status             string `json:"Status"`
}

// GuideInfo is the GET /LiveTv/GuideInfo response: the window the EPG covers.
type GuideInfo struct {
	StartDate string `json:"StartDate"`
	EndDate   string `json:"EndDate"`
}

// TimerInfoDto describes a DVR timer (scheduled or in-progress recording).
type TimerInfoDto struct {
	ID        string `json:"Id"`
	Type      string `json:"Type"`
	ServerID  string `json:"ServerId"`
	ChannelID string `json:"ChannelId"`
	ProgramID string `json:"ProgramId,omitempty"`
	Name      string `json:"Name"`
	StartDate string `json:"StartDate"`
	EndDate   string `json:"EndDate"`
	Status    string `json:"Status"` // New | InProgress | Completed | Cancelled | Error
}

// CreateTimerRequest is the POST /LiveTv/Timers body.
type CreateTimerRequest struct {
	ChannelID string `json:"ChannelId"`
	ProgramID string `json:"ProgramId"`
	Name      string `json:"Name"`
	StartDate string `json:"StartDate"`
	EndDate   string `json:"EndDate"`
}
