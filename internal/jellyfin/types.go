package jellyfin

// PublicSystemInfo is returned, unauthenticated, from GET /System/Info/Public.
// It is the first thing a client fetches to discover and identify a server.
type PublicSystemInfo struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	ID                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

// SystemInfo is the authenticated GET /System/Info response. Clients read many
// of these fields to decide which features to enable.
type SystemInfo struct {
	LocalAddress               string `json:"LocalAddress"`
	ServerName                 string `json:"ServerName"`
	Version                    string `json:"Version"`
	ProductName                string `json:"ProductName"`
	OperatingSystem            string `json:"OperatingSystem"`
	OperatingSystemDisplayName string `json:"OperatingSystemDisplayName"`
	ID                         string `json:"Id"`
	StartupWizardCompleted     bool   `json:"StartupWizardCompleted"`
	PackageName                string `json:"PackageName"`
	HasPendingRestart          bool   `json:"HasPendingRestart"`
	IsShuttingDown             bool   `json:"IsShuttingDown"`
	SupportsLibraryMonitor     bool   `json:"SupportsLibraryMonitor"`
	HasUpdateAvailable         bool   `json:"HasUpdateAvailable"`
	CanSelfRestart             bool   `json:"CanSelfRestart"`
	CanLaunchWebBrowser        bool   `json:"CanLaunchWebBrowser"`
	WebSocketPortNumber        int    `json:"WebSocketPortNumber"`
}

// AuthenticateRequest is the POST /Users/AuthenticateByName body.
type AuthenticateRequest struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
}

// AuthenticationResult is returned on successful authentication.
type AuthenticationResult struct {
	User        UserDto        `json:"User"`
	SessionInfo SessionInfoDto `json:"SessionInfo"`
	AccessToken string         `json:"AccessToken"`
	ServerID    string         `json:"ServerId"`
}

// UserDto describes a user to a client.
type UserDto struct {
	Name                      string            `json:"Name"`
	ServerID                  string            `json:"ServerId"`
	ID                        string            `json:"Id"`
	HasPassword               bool              `json:"HasPassword"`
	HasConfiguredPassword     bool              `json:"HasConfiguredPassword"`
	HasConfiguredEasyPassword bool              `json:"HasConfiguredEasyPassword"`
	EnableAutoLogin           bool              `json:"EnableAutoLogin"`
	LastLoginDate             string            `json:"LastLoginDate,omitempty"`
	LastActivityDate          string            `json:"LastActivityDate,omitempty"`
	Configuration             UserConfiguration `json:"Configuration"`
	Policy                    UserPolicy        `json:"Policy"`
}

// UserConfiguration holds per-user display preferences. Defaults are fine for
// milestone 1; clients tolerate the zero values.
type UserConfiguration struct {
	PlayDefaultAudioTrack     bool     `json:"PlayDefaultAudioTrack"`
	DisplayMissingEpisodes    bool     `json:"DisplayMissingEpisodes"`
	SubtitleMode              string   `json:"SubtitleMode"`
	EnableNextEpisodeAutoPlay bool     `json:"EnableNextEpisodeAutoPlay"`
	OrderedViews              []string `json:"OrderedViews"`
	LatestItemsExcludes       []string `json:"LatestItemsExcludes"`
	MyMediaExcludes           []string `json:"MyMediaExcludes"`
	GroupedFolders            []string `json:"GroupedFolders"`
}

// UserPolicy is the permission set for a user. Clients gate admin UI on
// IsAdministrator, so this must be populated correctly.
type UserPolicy struct {
	IsAdministrator                bool     `json:"IsAdministrator"`
	IsHidden                       bool     `json:"IsHidden"`
	IsDisabled                     bool     `json:"IsDisabled"`
	EnableUserPreferenceAccess     bool     `json:"EnableUserPreferenceAccess"`
	EnableRemoteAccess             bool     `json:"EnableRemoteAccess"`
	EnableMediaPlayback            bool     `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding bool     `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding bool     `json:"EnableVideoPlaybackTranscoding"`
	EnableContentDownloading       bool     `json:"EnableContentDownloading"`
	EnableAllFolders               bool     `json:"EnableAllFolders"`
	EnabledFolders                 []string `json:"EnabledFolders"`
	EnableAllDevices               bool     `json:"EnableAllDevices"`
	AuthenticationProviderID       string   `json:"AuthenticationProviderId"`
	PasswordResetProviderID        string   `json:"PasswordResetProviderId"`
	SyncPlayAccess                 string   `json:"SyncPlayAccess"`
}

// SessionInfoDto describes the established session.
type SessionInfoDto struct {
	ID                    string   `json:"Id"`
	UserID                string   `json:"UserId"`
	UserName              string   `json:"UserName"`
	Client                string   `json:"Client"`
	DeviceName            string   `json:"DeviceName"`
	DeviceID              string   `json:"DeviceId"`
	ApplicationVersion    string   `json:"ApplicationVersion"`
	ServerID              string   `json:"ServerId"`
	SupportsRemoteControl bool     `json:"SupportsRemoteControl"`
	PlayableMediaTypes    []string `json:"PlayableMediaTypes"`
	LastActivityDate      string   `json:"LastActivityDate,omitempty"`
}

// BrandingOptions is the GET /Branding/Configuration response.
type BrandingOptions struct {
	LoginDisclaimer     string `json:"LoginDisclaimer"`
	CustomCss           string `json:"CustomCss"`
	SplashscreenEnabled bool   `json:"SplashscreenEnabled"`
}

// QueryResult is the generic paged-list envelope used across the API.
type QueryResult[T any] struct {
	Items            []T `json:"Items"`
	TotalRecordCount int `json:"TotalRecordCount"`
	StartIndex       int `json:"StartIndex"`
}
