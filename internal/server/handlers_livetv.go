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
	// Surface the currently-airing program on the tile when an EPG is loaded.
	if s.liveTV != nil {
		if prog, ok := s.liveTV.CurrentProgram(c.ID, time.Now()); ok {
			p := s.programToDto(prog)
			dto.CurrentProgram = &p
		}
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

// GET /LiveTv/GuideInfo — the window the EPG covers (a rolling day if no guide
// is loaded).
func (s *Server) handleGuideInfo(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	start, end := now, now.Add(24*time.Hour)
	if s.liveTV != nil {
		if gs, ge := s.liveTV.GuideWindow(); !gs.IsZero() && !ge.IsZero() {
			start, end = gs, ge
		}
	}
	s.writeJSON(w, http.StatusOK, jellyfin.GuideInfo{
		StartDate: start.Format(time.RFC3339),
		EndDate:   end.Format(time.RFC3339),
	})
}

// GET|POST /LiveTv/Programs — EPG entries, optionally filtered by ChannelIds and
// a MinStartDate/MaxStartDate window.
func (s *Server) handlePrograms(w http.ResponseWriter, r *http.Request) {
	if s.liveTV == nil {
		s.writeJSON(w, http.StatusOK, emptyResult())
		return
	}
	q := r.URL.Query()
	channelIDs := splitCSV(q.Get("ChannelIds"))
	from := parseTimeOr(q.Get("MinStartDate"), time.Now().Add(-6*time.Hour))
	to := parseTimeOr(q.Get("MaxStartDate"), time.Now().Add(24*time.Hour))

	entries := s.liveTV.Programs(channelIDs, from, to)
	items := make([]jellyfin.BaseItemDto, 0, len(entries))
	for _, e := range entries {
		items = append(items, s.programToDto(e))
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            items,
		TotalRecordCount: len(items),
	})
}

func (s *Server) programToDto(e livetv.GuideEntry) jellyfin.BaseItemDto {
	return jellyfin.BaseItemDto{
		Name:      e.Title,
		ServerID:  s.store.ServerID(),
		ID:        e.ID,
		Type:      "Program",
		MediaType: "Video",
		ChannelID: e.ChannelID,
		Overview:  e.Description,
		StartDate: e.Start.Format(time.RFC3339),
		EndDate:   e.Stop.Format(time.RFC3339),
	}
}

func parseTimeOr(s string, def time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return def
}

// handleLiveTvEmpty answers the DVR endpoints (recordings/timers) with an empty,
// well-formed result: no recording yet, but clients must not error.
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
