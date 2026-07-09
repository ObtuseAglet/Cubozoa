package server

import (
	"net/http"
	"net/url"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/security"
)

// channelPlaybackInfo builds the PlaybackInfo response for a Live TV channel:
// an infinite HLS source whose transcoding URL points at the live remux
// endpoint. Requires ffmpeg; without it, the channel has no playable source.
func (s *Server) channelPlaybackInfo(w http.ResponseWriter, r *http.Request, channelID, name string) {
	playSession, _ := security.NewID()
	src := jellyfin.MediaSourceInfo{
		Protocol:         "File",
		ID:               channelID,
		Name:             name,
		IsRemote:         true,
		Container:        "ts",
		IsInfiniteStream: true,
		MediaStreams:     []jellyfin.MediaStream{},
	}
	if s.transcoder != nil {
		src.SupportsTranscoding = true
		src.TranscodingSubProtocol = "hls"
		src.TranscodingContainer = "ts"
		src.TranscodingURL = "/Videos/" + channelID + "/live.m3u8?api_key=" +
			url.QueryEscape(clientAuthFrom(r).Token)
	}
	s.writeJSON(w, http.StatusOK, jellyfin.PlaybackInfoResponse{
		MediaSources:  []jellyfin.MediaSourceInfo{src},
		PlaySessionID: playSession,
	})
}

// liveSessionKey identifies a channel's live remux session.
func liveSessionKey(channelID string) string { return "live:" + channelID }

// GET /Videos/{id}/live.m3u8 — start (or join) a channel's live HLS remux and
// return its (sliding-window) media playlist.
func (s *Server) handleLiveStream(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil || s.liveTV == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	upstream, ok := s.liveTV.StreamURL(id)
	if !ok {
		s.writeError(w, http.StatusNotFound)
		return
	}

	key := liveSessionKey(id)
	if _, err := s.transcoder.EnsureLiveSession(key, upstream); err != nil {
		s.log.Error("starting live stream", "channel", id, "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	playlist, err := s.transcoder.Playlist(key)
	if err != nil {
		s.log.Warn("live playlist not ready", "channel", id, "err", err)
		s.writeError(w, http.StatusServiceUnavailable)
		return
	}
	s.writePlaylist(w, rewriteSegments(playlist, "live/", clientAuthFrom(r).Token, 0))
}

// GET /Videos/{id}/live/{seg} — a segment of a channel's live remux.
func (s *Server) handleLiveSegment(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	s.serveSegment(w, r, liveSessionKey(r.PathValue("id")), r.PathValue("seg"))
}
