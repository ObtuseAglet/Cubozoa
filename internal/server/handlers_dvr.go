package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// jellyfinTimerStatus maps an internal recording status to Jellyfin's timer
// status vocabulary.
func jellyfinTimerStatus(status string) string {
	switch status {
	case store.RecScheduled:
		return "New"
	case store.RecRecording:
		return "InProgress"
	case store.RecCompleted:
		return "Completed"
	case store.RecCancelled:
		return "Cancelled"
	default:
		return "Error"
	}
}

func (s *Server) timerToDto(rec *store.Recording) jellyfin.TimerInfoDto {
	return jellyfin.TimerInfoDto{
		ID:        rec.ID,
		Type:      "Timer",
		ServerID:  s.store.ServerID(),
		ChannelID: rec.ChannelID,
		ProgramID: rec.ProgramID,
		Name:      rec.Name,
		StartDate: rec.StartAt.Format(time.RFC3339),
		EndDate:   rec.EndAt.Format(time.RFC3339),
		Status:    jellyfinTimerStatus(rec.Status),
	}
}

func (s *Server) recordingToDto(rec *store.Recording) jellyfin.BaseItemDto {
	dto := jellyfin.BaseItemDto{
		Name:         rec.Name,
		ServerID:     s.store.ServerID(),
		ID:           rec.ID,
		Type:         "Recording",
		MediaType:    "Video",
		ChannelID:    rec.ChannelID,
		StartDate:    rec.StartAt.Format(time.RFC3339),
		EndDate:      rec.EndAt.Format(time.RFC3339),
		LocationType: "FileSystem",
	}
	if d := rec.EndAt.Sub(rec.StartAt); d > 0 {
		dto.RunTimeTicks = int64(d.Seconds()) * 10_000_000
	}
	return dto
}

// recordingPlaybackInfo advertises a completed recording as a direct-play file.
func (s *Server) recordingPlaybackInfo(w http.ResponseWriter, recID, name string) {
	s.writeJSON(w, http.StatusOK, jellyfin.PlaybackInfoResponse{
		PlaySessionID: recID,
		MediaSources: []jellyfin.MediaSourceInfo{{
			Protocol:             "File",
			ID:                   recID,
			Name:                 name,
			Container:            "ts",
			SupportsDirectPlay:   true,
			SupportsDirectStream: true,
			MediaStreams:         []jellyfin.MediaStream{},
		}},
	})
}

// POST /LiveTv/Timers — schedule a recording of a channel for a time window.
func (s *Server) handleCreateTimer(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	var req jellyfin.CreateTimerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest)
		return
	}

	start, err1 := time.Parse(time.RFC3339, req.StartDate)
	end, err2 := time.Parse(time.RFC3339, req.EndDate)
	if err1 != nil || err2 != nil || req.ChannelID == "" {
		s.writeError(w, http.StatusBadRequest)
		return
	}

	name, chName := req.Name, ""
	if s.liveTV != nil {
		if ch, ok := s.liveTV.Channel(req.ChannelID); ok {
			chName = ch.Name
			if name == "" {
				name = ch.Name
			}
		} else {
			s.writeError(w, http.StatusNotFound)
			return
		}
	}
	if name == "" {
		name = "Recording"
	}

	rec, err := s.recorder.Schedule(req.ChannelID, chName, req.ProgramID, name, start, end)
	if err != nil {
		s.writeError(w, http.StatusBadRequest)
		return
	}
	s.writeJSON(w, http.StatusOK, s.timerToDto(rec))
}

// GET /LiveTv/Timers — scheduled and in-progress recordings.
func (s *Server) handleTimers(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.TimerInfoDto]{Items: []jellyfin.TimerInfoDto{}})
		return
	}
	timers := s.recorder.Timers()
	items := make([]jellyfin.TimerInfoDto, 0, len(timers))
	for _, rec := range timers {
		items = append(items, s.timerToDto(rec))
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.TimerInfoDto]{
		Items:            items,
		TotalRecordCount: len(items),
	})
}

// DELETE /LiveTv/Timers/{id} — cancel a scheduled or in-progress recording.
func (s *Server) handleCancelTimer(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	if err := s.recorder.Cancel(r.PathValue("id")); err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) seriesTimerToDto(st *store.SeriesTimer) jellyfin.SeriesTimerInfoDto {
	return jellyfin.SeriesTimerInfoDto{
		ID:               st.ID,
		Type:             "SeriesTimer",
		ServerID:         s.store.ServerID(),
		ChannelID:        st.ChannelID,
		ChannelName:      st.ChannelName,
		Name:             st.Name,
		RecordAnyChannel: st.RecordAnyChannel,
		RecordAnyTime:    true,
	}
}

// POST /LiveTv/SeriesTimers — record every matching airing of a program.
func (s *Server) handleCreateSeriesTimer(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	var req jellyfin.CreateSeriesTimerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest)
		return
	}

	name, chName := req.Name, ""
	channelID := req.ChannelID
	if req.RecordAnyChannel {
		channelID = ""
	} else if channelID != "" && s.liveTV != nil {
		if ch, ok := s.liveTV.Channel(channelID); ok {
			chName = ch.Name
			if name == "" {
				name = ch.Name
			}
		} else {
			s.writeError(w, http.StatusNotFound)
			return
		}
	}
	if name == "" {
		s.writeError(w, http.StatusBadRequest)
		return
	}

	st, err := s.recorder.ScheduleSeries(channelID, chName, name, req.RecordAnyChannel)
	if err != nil {
		s.writeError(w, http.StatusBadRequest)
		return
	}
	s.writeJSON(w, http.StatusOK, s.seriesTimerToDto(st))
}

// GET /LiveTv/SeriesTimers — configured series timers.
func (s *Server) handleSeriesTimers(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.SeriesTimerInfoDto]{Items: []jellyfin.SeriesTimerInfoDto{}})
		return
	}
	timers := s.recorder.SeriesTimers()
	items := make([]jellyfin.SeriesTimerInfoDto, 0, len(timers))
	for _, st := range timers {
		items = append(items, s.seriesTimerToDto(st))
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.SeriesTimerInfoDto]{
		Items:            items,
		TotalRecordCount: len(items),
	})
}

// DELETE /LiveTv/SeriesTimers/{id} — remove a series timer.
func (s *Server) handleCancelSeriesTimer(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	if err := s.recorder.CancelSeries(r.PathValue("id")); err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /LiveTv/Recordings — completed recordings, playable like items.
func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request) {
	if s.recorder == nil {
		s.writeJSON(w, http.StatusOK, emptyResult())
		return
	}
	recs := s.recorder.Recordings()
	items := make([]jellyfin.BaseItemDto, 0, len(recs))
	udMap := s.userData.Map(userFrom(r).ID)
	for _, rec := range recs {
		dto := s.recordingToDto(rec)
		dto.UserData = userDataDto(rec.ID, udMap[rec.ID])
		items = append(items, dto)
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            items,
		TotalRecordCount: len(items),
	})
}
