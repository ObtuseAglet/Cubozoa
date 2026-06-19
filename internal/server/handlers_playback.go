package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/security"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// maxPlaybackBody caps the PlaybackInfo / progress request bodies. They carry a
// device profile and playback state — kilobytes at most.
const maxPlaybackBody = 1 << 20 // 1 MiB

// mediaSource builds the MediaSourceInfo advertised for an item. Direct play and
// direct stream are offered; transcoding is not, until ffmpeg lands.
func (s *Server) mediaSource(it *store.MediaItem) jellyfin.MediaSourceInfo {
	return jellyfin.MediaSourceInfo{
		Protocol:             "File",
		ID:                   it.ID,
		Name:                 it.Name,
		IsRemote:             false,
		Container:            it.Container,
		Size:                 it.SizeBytes,
		SupportsDirectPlay:   true,
		SupportsDirectStream: true,
		SupportsTranscoding:  false,
		MediaStreams:         []jellyfin.MediaStream{},
	}
}

// GET|POST /Items/{itemId}/PlaybackInfo — tell the client how it can play an
// item. The request body (a device profile) is accepted but not yet used for
// negotiation, since Cubozoa only offers direct play for now.
func (s *Server) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("itemId")
	it, err := s.media.Item(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound)
			return
		}
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	if r.Body != nil {
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxPlaybackBody))
	}

	playSession, err := security.NewID()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, http.StatusOK, jellyfin.PlaybackInfoResponse{
		MediaSources:  []jellyfin.MediaSourceInfo{s.mediaSource(it)},
		PlaySessionID: playSession,
	})
}

// GET|HEAD /Videos/{id}/{file} — stream the raw media file for direct play.
//
// The {file} segment is the client's "stream" or "stream.<ext>" name; it is not
// trusted for path resolution. The file served is resolved solely from the item
// ID, and http.ServeContent provides correct HTTP Range handling (seeking) plus
// conditional requests, so players can scrub without downloading the whole file.
func (s *Server) handleVideoStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if file := r.PathValue("file"); file != "" && !strings.HasPrefix(file, "stream") {
		s.writeError(w, http.StatusNotFound)
		return
	}

	st, err := s.media.ItemStream(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound)
			return
		}
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	f, err := os.Open(st.Path)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		s.writeError(w, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", st.ContentType)
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// POST /Sessions/Playing, /Sessions/Playing/Progress, /Sessions/Playing/Stopped
// — playback lifecycle reports. The reported position is persisted as the
// user's resume point; a malformed body is tolerated (still 204) so a client's
// telemetry quirk never breaks playback.
func (s *Server) handlePlaybackReport(w http.ResponseWriter, r *http.Request) {
	var info jellyfin.PlaybackProgressInfo
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlaybackBody)).Decode(&info)
	}
	if u := userFrom(r); u != nil && info.ItemID != "" {
		if err := s.userData.ReportPosition(u.ID, info.ItemID, info.PositionTicks); err != nil {
			s.log.Warn("recording playback position", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
