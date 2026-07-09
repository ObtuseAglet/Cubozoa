package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/security"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// maxPlaybackBody caps the PlaybackInfo / progress request bodies. They carry a
// device profile and playback state — kilobytes at most.
const maxPlaybackBody = 1 << 20 // 1 MiB

// mediaSource builds the MediaSourceInfo advertised for an item. Direct play is
// always offered; transcoding is additionally offered when ffmpeg is available,
// letting the client pick. The token is embedded in the transcoding URL so the
// HLS playlist and segment requests authenticate.
func (s *Server) mediaSource(it *store.MediaItem, token string) jellyfin.MediaSourceInfo {
	src := jellyfin.MediaSourceInfo{
		Protocol:             "File",
		ID:                   it.ID,
		Name:                 it.Name,
		IsRemote:             false,
		Container:            it.Container,
		Size:                 it.SizeBytes,
		RunTimeTicks:         it.RunTimeTicks,
		SupportsDirectPlay:   true,
		SupportsDirectStream: true,
		MediaStreams:         s.mediaStreams(it, token),
	}
	if s.transcoder != nil {
		src.SupportsTranscoding = true
		src.TranscodingSubProtocol = "hls"
		src.TranscodingContainer = "ts"
		src.TranscodingURL = "/Videos/" + it.ID + "/master.m3u8?api_key=" + url.QueryEscape(token)
	}
	return src
}

// mediaStreams maps stored stream metadata to the wire DTO, appending external
// subtitle tracks with their delivery URLs (served as WebVTT). Subtitle stream
// indexes continue after the probed tracks.
func (s *Server) mediaStreams(it *store.MediaItem, token string) []jellyfin.MediaStream {
	out := make([]jellyfin.MediaStream, 0, len(it.Streams)+len(it.Subtitles))
	for _, st := range it.Streams {
		ms := jellyfin.MediaStream{
			Type:      st.Type,
			Index:     st.Index,
			Codec:     st.Codec,
			Language:  st.Language,
			Channels:  st.Channels,
			Width:     st.Width,
			Height:    st.Height,
			Title:     st.Title,
			IsDefault: st.IsDefault,
		}
		// Embedded text subtitle tracks become deliverable (extracted to WebVTT)
		// when ffmpeg is available.
		if st.Type == "Subtitle" && s.transcoder != nil && media.IsTextSubtitleCodec(st.Codec) {
			ms.IsTextSubtitleStream = true
			ms.DeliveryMethod = "External"
			ms.DeliveryURL = "/Videos/" + it.ID + "/Subtitles/embedded/" + itoaInt(st.Index) +
				"/Stream.vtt?api_key=" + url.QueryEscape(token)
		}
		out = append(out, ms)
	}
	base := len(it.Streams)
	for i, sub := range it.Subtitles {
		out = append(out, jellyfin.MediaStream{
			Type:                 "Subtitle",
			Index:                base + i,
			Codec:                sub.Codec,
			Language:             sub.Language,
			Title:                sub.Title,
			DisplayTitle:         sub.Title,
			IsForced:             sub.Forced,
			IsExternal:           true,
			IsTextSubtitleStream: true,
			DeliveryMethod:       "External",
			DeliveryURL: "/Videos/" + it.ID + "/Subtitles/" + itoaInt(i) +
				"/Stream.vtt?api_key=" + url.QueryEscape(token),
		})
	}
	return out
}

func itoaInt(n int) string { return strconv.Itoa(n) }

// GET|POST /Items/{itemId}/PlaybackInfo — tell the client how it can play an
// item. The request body (a device profile) is accepted but not yet used for
// negotiation, since Cubozoa only offers direct play for now.
func (s *Server) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("itemId")

	// A Live TV channel is not in the media store; serve its infinite source.
	if s.liveTV != nil {
		if ch, ok := s.liveTV.Channel(id); ok {
			s.channelPlaybackInfo(w, r, ch.ID, ch.Name)
			return
		}
	}

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
		MediaSources:  []jellyfin.MediaSourceInfo{s.mediaSource(it, clientAuthFrom(r).Token)},
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
