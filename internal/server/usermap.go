package server

import (
	"time"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// toUserDto maps an internal user to the Jellyfin client DTO. The policy is
// derived from the user's role: administrators get full access, and all users
// get media-playback permissions enabled by default.
func (s *Server) toUserDto(u *store.User) jellyfin.UserDto {
	dto := jellyfin.UserDto{
		Name:                  u.Name,
		ServerID:              s.store.ServerID(),
		ID:                    u.ID,
		HasPassword:           true,
		HasConfiguredPassword: true,
		Configuration: jellyfin.UserConfiguration{
			PlayDefaultAudioTrack: true,
			SubtitleMode:          "Default",
			OrderedViews:          []string{},
			LatestItemsExcludes:   []string{},
			MyMediaExcludes:       []string{},
			GroupedFolders:        []string{},
		},
		Policy: jellyfin.UserPolicy{
			IsAdministrator:                u.IsAdmin,
			EnableUserPreferenceAccess:     true,
			EnableRemoteAccess:             true,
			EnableMediaPlayback:            true,
			EnableAudioPlaybackTranscoding: true,
			EnableVideoPlaybackTranscoding: true,
			EnableContentDownloading:       true,
			EnableAllFolders:               true,
			EnabledFolders:                 []string{},
			EnableAllDevices:               true,
			SyncPlayAccess:                 "CreateAndJoinGroups",
		},
	}
	if !u.LastLoginAt.IsZero() {
		dto.LastLoginDate = u.LastLoginAt.Format(time.RFC3339Nano)
		dto.LastActivityDate = u.LastLoginAt.Format(time.RFC3339Nano)
	}
	return dto
}
