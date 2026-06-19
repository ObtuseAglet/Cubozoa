package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// GET /Videos/{id}/Subtitles/{index}/{file} — deliver an external subtitle.
//
// The requested format is taken from the {file} extension (e.g. Stream.vtt).
// SubRip sources are converted to WebVTT on the fly so browser players work;
// other formats are served as-is. The on-disk path is resolved server-side from
// the item ID and subtitle ordinal — never supplied by the client.
func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}

	track, err := s.media.ItemSubtitle(id, index)
	if err != nil {
		if errors.Is(err, media.ErrNoImage) || errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound)
			return
		}
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	data, err := os.ReadFile(track.Path)
	if err != nil {
		s.writeError(w, http.StatusNotFound)
		return
	}

	wantVTT := strings.EqualFold(filepath.Ext(r.PathValue("file")), ".vtt")
	srcSRT := track.Format == "srt" || track.Format == "sub" || track.Codec == "subrip"

	switch {
	case wantVTT && srcSRT:
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		data = media.SRTToVTT(data)
	case wantVTT:
		// Already VTT (or close enough to deliver as text/vtt).
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	default:
		w.Header().Set("Content-Type", subtitleContentType(track.Format))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func subtitleContentType(format string) string {
	switch strings.ToLower(format) {
	case "vtt":
		return "text/vtt; charset=utf-8"
	case "srt", "sub":
		return "application/x-subrip; charset=utf-8"
	case "ass", "ssa":
		return "text/x-ssa; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}
