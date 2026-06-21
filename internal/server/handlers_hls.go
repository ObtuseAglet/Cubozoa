package server

import (
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// hlsStartTicks reads the seek offset (Jellyfin's StartTimeTicks, in 100ns
// ticks) from the request, accepting either casing. Zero means "from the start".
func hlsStartTicks(r *http.Request) int64 {
	q := r.URL.Query()
	v := q.Get("StartTimeTicks")
	if v == "" {
		v = q.Get("startTimeTicks")
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	if n < 0 {
		n = 0
	}
	return n
}

// hlsSessionKey identifies a transcode by item plus seek offset, so seeking to a
// new position starts its own transcode rather than colliding with the original.
func hlsSessionKey(itemID string, startTicks int64) string {
	return itemID + ":" + strconv.FormatInt(startTicks, 10)
}

// GET /Videos/{id}/main.m3u8 (and master.m3u8) — start (or join) an HLS
// transcode of an item and return its media playlist. An optional
// StartTimeTicks seeks the transcode so playback can begin mid-file.
//
// ffmpeg's playlist lists bare segment names; we rewrite each to an absolute
// path under this item's segment endpoint, carrying the api_key (so segment
// requests authenticate) and the start offset (so they route to this session).
func (s *Server) handleHlsPlaylist(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	startTicks := hlsStartTicks(r)
	key := hlsSessionKey(id, startTicks)

	st, err := s.media.ItemStream(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}

	if _, err := s.transcoder.EnsureSession(key, st.Path, ticksToSeconds(startTicks)); err != nil {
		s.log.Error("starting transcode", "item", id, "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	playlist, err := s.transcoder.Playlist(key)
	if err != nil {
		s.log.Warn("transcode playlist not ready", "item", id, "err", err)
		s.writeError(w, http.StatusServiceUnavailable)
		return
	}

	rewritten := rewritePlaylist(playlist, clientAuthFrom(r).Token, startTicks)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rewritten)
}

// GET /Videos/{id}/hls/{seg} — serve a single transcoded segment.
func (s *Server) handleHlsSegment(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	seg := r.PathValue("seg")
	key := hlsSessionKey(id, hlsStartTicks(r))

	path, err := s.transcoder.Segment(key, seg)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}

	f, err := os.Open(path)
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

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// ticksToSeconds converts Jellyfin 100ns ticks to seconds.
func ticksToSeconds(ticks int64) float64 { return float64(ticks) / 1e7 }

// rewritePlaylist rewrites bare segment URIs in an HLS media playlist to point
// at the segment endpoint, appending the api_key and the start offset. Comment/
// tag lines (starting with '#') and blank lines are passed through unchanged.
func rewritePlaylist(playlist []byte, token string, startTicks int64) []byte {
	q := "?api_key=" + url.QueryEscape(token)
	if startTicks > 0 {
		q += "&StartTimeTicks=" + strconv.FormatInt(startTicks, 10)
	}
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines[i] = "hls/" + url.PathEscape(trimmed) + q
	}
	return []byte(strings.Join(lines, "\n"))
}
