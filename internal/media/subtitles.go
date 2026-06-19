package media

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// subtitleExtensions are the sidecar subtitle formats Cubozoa recognizes.
var subtitleExtensions = map[string]string{
	".srt": "subrip",
	".vtt": "webvtt",
	".ass": "ass",
	".ssa": "ssa",
	".sub": "subrip",
}

// languageNames maps common ISO-639 codes to display names for subtitle titles.
var languageNames = map[string]string{
	"en": "English", "eng": "English",
	"es": "Spanish", "spa": "Spanish",
	"fr": "French", "fre": "French", "fra": "French",
	"de": "German", "ger": "German", "deu": "German",
	"it": "Italian", "ita": "Italian",
	"pt": "Portuguese", "por": "Portuguese",
	"ru": "Russian", "rus": "Russian",
	"ja": "Japanese", "jpn": "Japanese",
	"ko": "Korean", "kor": "Korean",
	"zh": "Chinese", "chi": "Chinese", "zho": "Chinese",
	"nl": "Dutch", "dut": "Dutch", "nld": "Dutch",
	"sv": "Swedish", "swe": "Swedish",
	"pl": "Polish", "pol": "Polish",
	"ar": "Arabic", "ara": "Arabic",
}

var langTokenRe = regexp.MustCompile(`^[a-z]{2,3}$`)

// findSubtitles locates sidecar subtitle files for a media file. Recognized
// names are "<base>.ext", "<base>.<lang>.ext" and "<base>.<lang>.forced.ext"
// (tokens may appear in any order between the base name and extension).
func findSubtitles(d *dirLister, videoPath string) []store.SubtitleTrack {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	baseLower := strings.ToLower(base)

	var tracks []store.SubtitleTrack
	for _, name := range d.info(dir).files {
		ext := strings.ToLower(filepath.Ext(name))
		codec, ok := subtitleExtensions[ext]
		if !ok {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		stemLower := strings.ToLower(stem)
		if stemLower != baseLower && !strings.HasPrefix(stemLower, baseLower+".") {
			continue
		}

		lang, forced := parseSubtitleTokens(stemLower[min(len(baseLower), len(stemLower)):])
		tracks = append(tracks, store.SubtitleTrack{
			Path:     filepath.Join(dir, name),
			Language: lang,
			Codec:    codec,
			Format:   strings.TrimPrefix(ext, "."),
			Forced:   forced,
			Title:    subtitleTitle(lang, forced),
		})
	}
	return tracks
}

// parseSubtitleTokens extracts a language code and a forced flag from the
// dotted token string that follows the base name (e.g. ".en.forced").
func parseSubtitleTokens(rest string) (lang string, forced bool) {
	for _, tok := range strings.Split(rest, ".") {
		switch {
		case tok == "":
		case tok == "forced":
			forced = true
		case tok == "sdh" || tok == "cc" || tok == "hi":
			// hearing-impaired markers; recorded only via the title
		case lang == "" && langTokenRe.MatchString(tok):
			lang = tok
		}
	}
	return lang, forced
}

func subtitleTitle(lang string, forced bool) string {
	name := languageNames[lang]
	if name == "" {
		if lang == "" {
			name = "Subtitle"
		} else {
			name = strings.ToUpper(lang)
		}
	}
	if forced {
		name += " (Forced)"
	}
	return name
}

// SRTToVTT converts SubRip subtitle bytes to WebVTT, which browser-based players
// require. The transformation is purely textual (header + comma-to-period in cue
// timestamps), so it needs no external tools.
func SRTToVTT(srt []byte) []byte {
	text := strings.ReplaceAll(string(srt), "\r\n", "\n")
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "-->") {
			line = vttTimestamp(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// vttTimestamp rewrites "00:00:01,000 --> 00:00:04,000" to use '.' decimals.
func vttTimestamp(line string) string {
	return commaDecimalRe.ReplaceAllString(line, "$1.$2")
}

var commaDecimalRe = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)
