package server

import (
	"errors"
	"net/http"
	"os"

	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// GET|HEAD /Items/{id}/Images/{type}[/{index}] — serve an item's local artwork.
//
// The client never supplies a filesystem path: it provides only an item ID and
// an image type, which the media service resolves to a vetted file within the
// owning library. The original bytes are streamed via http.ServeContent, which
// gives correct conditional-request (ETag/304) and range handling for free.
//
// Resize parameters (fillWidth/maxHeight/quality) are accepted but ignored for
// now; clients downscale client-side. Server-side transcoding of images is a
// later enhancement.
func (s *Server) handleItemImage(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")
	imageType := r.PathValue("type")

	img, err := s.media.ItemImage(itemID, imageType)
	if err != nil {
		// A Live TV channel logo is not a library item — serve it (cached from
		// the upstream tvg-logo URL) before giving up.
		if (errors.Is(err, media.ErrNoImage) || errors.Is(err, store.ErrNotFound)) && s.serveChannelLogo(w, r, itemID) {
			return
		}
		if errors.Is(err, media.ErrNoImage) || errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound)
			return
		}
		s.log.Error("resolving image", "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	f, err := os.Open(img.Path)
	if err != nil {
		// The datastore referenced a file that has since vanished.
		s.writeError(w, http.StatusNotFound)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		s.writeError(w, http.StatusNotFound)
		return
	}

	h := w.Header()
	h.Set("Content-Type", img.ContentType)
	// Artwork is immutable for a given tag, so allow long-lived private caching.
	// "private" keeps shared proxies from holding media for other users.
	h.Set("Cache-Control", "private, max-age=86400")
	if img.Tag != "" {
		h.Set("ETag", `"`+img.Tag+`"`)
	}

	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// serveChannelLogo serves a Live TV channel's logo if the id is a channel with a
// cached/available logo, returning whether it handled the request.
func (s *Server) serveChannelLogo(w http.ResponseWriter, r *http.Request, channelID string) bool {
	if s.liveTV == nil {
		return false
	}
	path, contentType, ok := s.liveTV.LogoPath(r.Context(), channelID)
	if !ok {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	return true
}
