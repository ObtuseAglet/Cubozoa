package server

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

// GET /Videos/{id}/main.m3u8 (and master.m3u8) — start (or join) an HLS
// transcode of an item and return its media playlist.
//
// ffmpeg's playlist lists bare segment names; we rewrite each to an absolute
// path under this item's segment endpoint, carrying the api_key so the segment
// requests authenticate the same way the playlist request did.
func (s *Server) handleHlsPlaylist(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")

	st, err := s.media.ItemStream(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}

	if _, err := s.transcoder.EnsureSession(id, st.Path); err != nil {
		s.log.Error("starting transcode", "item", id, "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	playlist, err := s.transcoder.Playlist(id)
	if err != nil {
		s.log.Warn("transcode playlist not ready", "item", id, "err", err)
		s.writeError(w, http.StatusServiceUnavailable)
		return
	}

	token := clientAuthFrom(r).Token
	rewritten := rewritePlaylist(playlist, id, token)

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

	path, err := s.transcoder.Segment(id, seg)
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

// rewritePlaylist rewrites bare segment URIs in an HLS media playlist to point
// at this item's segment endpoint, appending the api_key. Comment/tag lines
// (starting with '#') and blank lines are passed through unchanged.
func rewritePlaylist(playlist []byte, itemID, token string) []byte {
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		seg := url.PathEscape(trimmed)
		lines[i] = "hls/" + seg + "?api_key=" + url.QueryEscape(token)
	}
	return []byte(strings.Join(lines, "\n"))
}
