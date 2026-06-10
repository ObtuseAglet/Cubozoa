package media

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// videoExtensions is the set of container extensions Cubozoa treats as video.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".ts": true, ".m2ts": true, ".3gp": true, ".ogv": true, ".divx": true,
}

// audioExtensions is the set of container extensions Cubozoa treats as audio.
var audioExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".aac": true, ".ogg": true,
	".oga": true, ".opus": true, ".wav": true, ".wma": true, ".alac": true,
}

// mediaKind classifies a file by extension. The empty string means "not media".
func mediaKind(path string) (kind string, container string) {
	ext := strings.ToLower(filepath.Ext(path))
	container = strings.TrimPrefix(ext, ".")
	switch {
	case videoExtensions[ext]:
		return "Video", container
	case audioExtensions[ext]:
		return "Audio", container
	default:
		return "", ""
	}
}

// yearRe matches a 4-digit year, typically wrapped in parentheses, e.g. (1999).
var yearRe = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

// junkRe matches common release-tag noise that should be stripped from titles
// (resolution, source, codec, etc.).
var junkRe = regexp.MustCompile(`(?i)\b(1080p|2160p|720p|480p|4k|x264|x265|h264|h265|hevc|aac|ac3|dts|bluray|brrip|bdrip|webrip|web-dl|hdtv|dvdrip|remux|proper|repack)\b`)

// separatorRe collapses common filename separators into spaces.
var separatorRe = regexp.MustCompile(`[._]+`)

// multiSpaceRe collapses runs of whitespace.
var multiSpaceRe = regexp.MustCompile(`\s+`)

// parseName derives a human title and (best-effort) production year from a media
// filename. It strips the extension, release-tag junk, and a trailing year,
// turning "The.Matrix.1999.1080p.BluRay.x264.mkv" into ("The Matrix", 1999).
func parseName(filename string) (title string, year int) {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	base = separatorRe.ReplaceAllString(base, " ")

	// Prefer the LAST year-like token: a release year follows the title, so in
	// "Blade Runner 2049 2017" the real year is 2017 and "2049" stays in the
	// title. Everything from the chosen year onward (year + trailing tags) is
	// dropped.
	if idxs := yearRe.FindAllStringIndex(base, -1); len(idxs) > 0 {
		last := idxs[len(idxs)-1]
		if y, err := strconv.Atoi(base[last[0]:last[1]]); err == nil {
			year = y
		}
		base = base[:last[0]]
	}

	base = junkRe.ReplaceAllString(base, " ")
	base = strings.Trim(base, " -()[]")
	base = multiSpaceRe.ReplaceAllString(base, " ")
	title = strings.TrimSpace(base)
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	return title, year
}

// inferLibraryType guesses a Jellyfin CollectionType from a folder name, so an
// operator can simply name a folder "Movies" or "TV Shows" and get the right
// behavior without configuration.
func inferLibraryType(folderName string) string {
	n := strings.ToLower(folderName)
	switch {
	case containsAny(n, "movie", "film", "cinema"):
		return "movies"
	case containsAny(n, "tv", "show", "series", "episode"):
		return "tvshows"
	case containsAny(n, "music", "audio", "song", "album"):
		return "music"
	case containsAny(n, "home video", "homevideo", "personal"):
		return "homevideos"
	default:
		return "mixed"
	}
}

// itemTypeFor maps a library collection type and media kind to the Jellyfin
// item Type reported to clients.
func itemTypeFor(libraryType, kind string) string {
	switch libraryType {
	case "movies":
		return "Movie"
	case "tvshows":
		return "Episode"
	case "music":
		return "Audio"
	default:
		if kind == "Audio" {
			return "Audio"
		}
		return "Video"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
