package media

import "github.com/obtuseaglet/cubozoa/internal/store"

// textSubtitleCodecs are embedded subtitle codecs that can be converted to
// WebVTT for delivery. Image-based subtitles (PGS, VobSub, DVB) are excluded —
// they cannot become text.
var textSubtitleCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true,
	"mov_text": true, "webvtt": true, "text": true, "subviewer": true,
	"eia_608": true,
}

// IsTextSubtitleCodec reports whether an embedded subtitle codec is text-based
// and therefore extractable to WebVTT.
func IsTextSubtitleCodec(codec string) bool {
	return textSubtitleCodecs[codec]
}

// EmbeddedSubtitleInput validates that streamIndex names an extractable text
// subtitle stream on an item and returns the item's (library-root-vetted)
// source path for extraction. The client supplies only an item ID and stream
// index — never a path.
func (s *Service) EmbeddedSubtitleInput(itemID string, streamIndex int) (string, error) {
	it, err := s.store.GetItem(itemID)
	if err != nil {
		return "", err
	}
	if it.Path == "" {
		return "", store.ErrNotFound
	}
	ok := false
	for _, st := range it.Streams {
		if st.Index == streamIndex && st.Type == "Subtitle" && IsTextSubtitleCodec(st.Codec) {
			ok = true
			break
		}
	}
	if !ok {
		return "", store.ErrNotFound
	}
	if lib, err := s.store.GetLibrary(it.LibraryID); err == nil && !withinRoot(lib.Path, it.Path) {
		s.log.Warn("rejecting embedded subtitle outside library root", "item", itemID, "path", it.Path)
		return "", store.ErrNotFound
	}
	return it.Path, nil
}
