package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/transcode"
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

// hlsSessionKey identifies a transcode by item, seek offset and rendition, so
// each seek position and each quality level gets its own transcode.
func hlsSessionKey(itemID string, startTicks int64, quality string) string {
	return itemID + ":" + strconv.FormatInt(startTicks, 10) + ":" + quality
}

// ticksToSeconds converts Jellyfin 100ns ticks to seconds.
func ticksToSeconds(ticks int64) float64 { return float64(ticks) / 1e7 }

// GET /Videos/{id}/master.m3u8 — the adaptive (ABR) master playlist. It lists a
// variant stream per quality rung derived from the source resolution; an
// ABR-capable client picks one based on bandwidth.
func (s *Server) handleHlsMaster(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	it, err := s.media.Item(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}

	startTicks := hlsStartTicks(r)
	token := clientAuthFrom(r).Token
	renditions := transcode.SelectRenditions(it.Width, it.Height, it.Bitrate)

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, rd := range renditions {
		if rd.Width > 0 && rd.Height > 0 {
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n", rd.Bandwidth, rd.Width, rd.Height)
		} else {
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d\n", rd.Bandwidth)
		}
		q := "?api_key=" + url.QueryEscape(token)
		if startTicks > 0 {
			q += "&StartTimeTicks=" + strconv.FormatInt(startTicks, 10)
		}
		fmt.Fprintf(&b, "hls/%s/main.m3u8%s\n", url.PathEscape(rd.Name), q)
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// GET /Videos/{id}/hls/{quality}/main.m3u8 — the media playlist for one ABR
// rendition. Segment URIs are relative, so they resolve under this rendition's
// own segment path.
func (s *Server) handleHlsVariant(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	quality := r.PathValue("quality")
	startTicks := hlsStartTicks(r)

	it, err := s.media.Item(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	rd, ok := findRendition(transcode.SelectRenditions(it.Width, it.Height, it.Bitrate), quality)
	if !ok {
		s.writeError(w, http.StatusNotFound)
		return
	}

	st, err := s.media.ItemStream(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	key := hlsSessionKey(id, startTicks, quality)
	if _, err := s.transcoder.EnsureSession(key, st.Path, ticksToSeconds(startTicks), rd); err != nil {
		s.log.Error("starting transcode", "item", id, "quality", quality, "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	playlist, err := s.transcoder.Playlist(key)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable)
		return
	}
	// Relative segment URIs resolve under /Videos/{id}/hls/{quality}/.
	s.writePlaylist(w, rewriteSegments(playlist, "", clientAuthFrom(r).Token, startTicks))
}

// GET /Videos/{id}/hls/{quality}/{seg} — a segment of an ABR rendition.
func (s *Server) handleHlsVariantSegment(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	key := hlsSessionKey(r.PathValue("id"), hlsStartTicks(r), r.PathValue("quality"))
	s.serveSegment(w, r, key, r.PathValue("seg"))
}

// GET /Videos/{id}/main.m3u8 — single-stream media playlist (auto rendition),
// kept for non-ABR clients. Segment URIs point at /Videos/{id}/hls/{seg}.
func (s *Server) handleHlsPlaylist(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	startTicks := hlsStartTicks(r)
	key := hlsSessionKey(id, startTicks, transcode.AutoRendition.Name)

	st, err := s.media.ItemStream(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	if _, err := s.transcoder.EnsureSession(key, st.Path, ticksToSeconds(startTicks), transcode.AutoRendition); err != nil {
		s.log.Error("starting transcode", "item", id, "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	playlist, err := s.transcoder.Playlist(key)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable)
		return
	}
	s.writePlaylist(w, rewriteSegments(playlist, "hls/", clientAuthFrom(r).Token, startTicks))
}

// GET /Videos/{id}/hls/{seg} — a segment of the single (auto) stream.
func (s *Server) handleHlsSegment(w http.ResponseWriter, r *http.Request) {
	if s.transcoder == nil {
		s.writeError(w, http.StatusNotFound)
		return
	}
	key := hlsSessionKey(r.PathValue("id"), hlsStartTicks(r), transcode.AutoRendition.Name)
	s.serveSegment(w, r, key, r.PathValue("seg"))
}

func findRendition(rs []transcode.Rendition, name string) (transcode.Rendition, bool) {
	for _, rd := range rs {
		if rd.Name == name {
			return rd, true
		}
	}
	return transcode.Rendition{}, false
}

func (s *Server) writePlaylist(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) serveSegment(w http.ResponseWriter, r *http.Request, sessionKey, seg string) {
	path, err := s.transcoder.Segment(sessionKey, seg)
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

// rewriteSegments rewrites the bare segment URIs in an HLS media playlist,
// prefixing them (e.g. "hls/" for the single stream, "" for a variant whose
// segments are siblings) and appending the api_key and any seek offset. Tag and
// blank lines pass through unchanged.
func rewriteSegments(playlist []byte, prefix, token string, startTicks int64) []byte {
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
		lines[i] = prefix + url.PathEscape(trimmed) + q
	}
	return []byte(strings.Join(lines, "\n"))
}
