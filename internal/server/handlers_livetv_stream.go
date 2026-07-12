package server

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/security"
	"github.com/obtuseaglet/cubozoa/internal/transcode"
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

// liveSessionKey identifies a channel's live session. A channel that failed to
// remux (codecs not copyable) is served from a separate re-encode session; the
// server remembers that choice so playlist and segment requests agree.
func liveSessionKey(channelID string, reencode bool) string {
	if reencode {
		return "livetc:" + channelID
	}
	return "live:" + channelID
}

// GET /Videos/{id}/live.m3u8 — start (or join) a channel's live HLS stream and
// return its (sliding-window) media playlist. If a plain remux fails because the
// upstream codecs cannot be copied, it transparently falls back to re-encoding.
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

	reencode := s.liveReencode(id)
	playlist, err := s.startLive(id, upstream, reencode)
	if errors.Is(err, transcode.ErrSessionFailed) && !reencode {
		// The cheap remux failed; retry once by re-encoding and remember it.
		s.transcoder.Stop(liveSessionKey(id, false))
		s.setLiveReencode(id)
		reencode = true
		s.log.Info("live remux failed; falling back to transcoding", "channel", id)
		playlist, err = s.startLive(id, upstream, true)
	}
	if err != nil {
		s.log.Warn("live playlist not ready", "channel", id, "err", err)
		s.writeError(w, http.StatusServiceUnavailable)
		return
	}
	s.writePlaylist(w, rewriteSegments(playlist, "live/", clientAuthFrom(r).Token, 0))
}

// startLive ensures the session (remux or re-encode) and returns its playlist.
func (s *Server) startLive(id, upstream string, reencode bool) ([]byte, error) {
	key := liveSessionKey(id, reencode)
	var err error
	if reencode {
		_, err = s.transcoder.EnsureLiveTranscodeSession(key, upstream)
	} else {
		_, err = s.transcoder.EnsureLiveSession(key, upstream)
	}
	if err != nil {
		return nil, err
	}
	return s.transcoder.Playlist(key)
}

// GET /Videos/{id}/live/{seg} — a segment of a channel's live stream.
func (s *Server) handleLiveSegment(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	s.serveSegment(w, r, liveSessionKey(id, s.liveReencode(id)), r.PathValue("seg"))
}

func (s *Server) liveReencode(channelID string) bool {
	_, ok := s.liveReencodeChannels.Load(channelID)
	return ok
}

func (s *Server) setLiveReencode(channelID string) {
	s.liveReencodeChannels.Store(channelID, struct{}{})
}
