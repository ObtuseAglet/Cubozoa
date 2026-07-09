package server

import (
	"net/http"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/livetv"
)

// liveTVEnabled reports whether Live TV is configured and has channels.
func (s *Server) liveTVEnabled() bool {
	return s.liveTV != nil && s.liveTV.Count() > 0
}

// channelToDto maps an IPTV channel to the Jellyfin TvChannel item shape.
func (s *Server) channelToDto(c livetv.Channel) jellyfin.BaseItemDto {
	dto := jellyfin.BaseItemDto{
		Name:        c.Name,
		ServerID:    s.store.ServerID(),
		ID:          c.ID,
		Type:        "TvChannel",
		MediaType:   "Video",
		ChannelType: "TV",
		Number:      c.Number,
		IsFolder:    false,
	}
	if c.Logo != "" {
		// Advertise a primary image; it is proxied from the upstream logo URL.
		dto.ImageTags = map[string]string{"Primary": c.ID}
	}
	return dto
}

// GET /LiveTv/Info — whether Live TV is available and which services back it.
func (s *Server) handleLiveTvInfo(w http.ResponseWriter, r *http.Request) {
	info := jellyfin.LiveTvInfo{
		IsEnabled:    s.liveTVEnabled(),
		EnabledUsers: []string{},
		Services: []jellyfin.LiveTvServiceInfo{
			{Name: "Cubozoa IPTV", IsVisible: true, Status: "Ok"},
		},
	}
	s.writeJSON(w, http.StatusOK, info)
}

// GET /LiveTv/Channels — the channel lineup (paged).
func (s *Server) handleLiveTvChannels(w http.ResponseWriter, r *http.Request) {
	if s.liveTV == nil {
		s.writeJSON(w, http.StatusOK, emptyResult())
		return
	}
	channels := s.liveTV.Channels()
	total := len(channels)

	q := r.URL.Query()
	start := clamp(parseInt(q.Get("StartIndex"), 0), 0, total)
	channels = channels[start:]
	if limit := parseInt(q.Get("Limit"), 0); limit > 0 && limit < len(channels) {
		channels = channels[:limit]
	}

	items := make([]jellyfin.BaseItemDto, 0, len(channels))
	for _, c := range channels {
		items = append(items, s.channelToDto(c))
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            items,
		TotalRecordCount: total,
		StartIndex:       start,
	})
}

// GET /LiveTv/Channels/{id} — a single channel.
func (s *Server) handleLiveTvChannel(w http.ResponseWriter, r *http.Request) {
	if s.liveTV == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	c, ok := s.liveTV.Channel(r.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusNotFound)
		return
	}
	s.writeJSON(w, http.StatusOK, s.channelToDto(c))
}

// GET /LiveTv/GuideInfo — the EPG window (a rolling day until the guide lands).
func (s *Server) handleGuideInfo(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	s.writeJSON(w, http.StatusOK, jellyfin.GuideInfo{
		StartDate: now.Format(time.RFC3339),
		EndDate:   now.Add(24 * time.Hour).Format(time.RFC3339),
	})
}

// GET|POST /LiveTv/Programs and the recording/timer endpoints return empty,
// well-formed results: no EPG or DVR yet, but clients must not error.
func (s *Server) handleLiveTvEmpty(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, emptyResult())
}

func emptyResult() jellyfin.QueryResult[jellyfin.BaseItemDto] {
	return jellyfin.QueryResult[jellyfin.BaseItemDto]{Items: []jellyfin.BaseItemDto{}}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
