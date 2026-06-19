package media

import (
	"fmt"
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

// trackNumberRe matches a leading track number, e.g. "01 - ", "1.", "03_".
var trackNumberRe = regexp.MustCompile(`^\s*(\d{1,3})\s*[-._)\s]+\s*`)

// parseTrack derives artist, album, track number and title from a music file's
// path segments. The conventional "Artist/Album/NN Title" layout is handled, as
// are flatter "Artist/NN Title" arrangements (album defaults to "Unknown
// Album"). A leading number in the filename is read as the track number.
func parseTrack(relParts []string) (artist, album string, track int, title string) {
	filename := relParts[len(relParts)-1]
	base := strings.TrimSuffix(filename, filepath.Ext(filename))

	switch {
	case len(relParts) >= 3:
		artist = cleanTitle(relParts[0])
		album = cleanTitle(relParts[1])
	case len(relParts) == 2:
		artist = cleanTitle(relParts[0])
		album = "Unknown Album"
	default:
		artist = "Unknown Artist"
		album = "Unknown Album"
	}
	if artist == "" {
		artist = "Unknown Artist"
	}
	if album == "" {
		album = "Unknown Album"
	}

	rest := base
	if m := trackNumberRe.FindStringSubmatch(base); m != nil {
		track = atoiDefault(m[1], 0)
		rest = base[len(m[0]):]
	}
	title = cleanTitle(rest)
	if title == "" {
		title = cleanTitle(base)
	}
	return artist, album, track, title
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

// episodeRe matches the common "S01E02" form (with optional separators), and
// seasonXEpisode matches the "1x02" form.
var (
	episodeRe       = regexp.MustCompile(`(?i)s(\d{1,2})[ ._-]*e(\d{1,3})`)
	seasonXEpisode  = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	seasonFolderRe  = regexp.MustCompile(`(?i)^(?:season|series|s)[ ._-]*(\d{1,2})$`)
	episodeTitleCut = regexp.MustCompile(`(?i)s\d{1,2}[ ._-]*e\d{1,3}|\d{1,2}x\d{1,3}`)
)

// parseEpisode derives series name, season number, episode number and episode
// title from a media file's path segments relative to the library root. It
// handles the typical "Series/Season NN/Series - SxxEyy - Title.ext" layout as
// well as flatter arrangements, falling back to sensible defaults so a file is
// never dropped.
func parseEpisode(relParts []string) (seriesName string, season, episode int, title string) {
	filename := relParts[len(relParts)-1]
	base := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Series name: the top-level folder under the library, when present.
	if len(relParts) >= 2 {
		seriesName = cleanTitle(relParts[0])
	} else {
		seriesName = cleanTitle(stripEpisodeTokens(base))
	}
	if seriesName == "" {
		seriesName = "Unknown"
	}

	season, episode = -1, -1
	spaced := separatorRe.ReplaceAllString(base, " ")
	if m := episodeRe.FindStringSubmatch(spaced); m != nil {
		season = atoiDefault(m[1], 1)
		episode = atoiDefault(m[2], 0)
	} else if m := seasonXEpisode.FindStringSubmatch(spaced); m != nil {
		season = atoiDefault(m[1], 1)
		episode = atoiDefault(m[2], 0)
	}

	// Season can also come from a "Season NN" folder when the filename lacks it.
	if season < 0 && len(relParts) >= 2 {
		for _, seg := range relParts[:len(relParts)-1] {
			if m := seasonFolderRe.FindStringSubmatch(seg); m != nil {
				season = atoiDefault(m[1], 1)
				break
			}
		}
	}
	if season < 0 {
		season = 1
	}
	if episode < 0 {
		episode = 0
	}

	title = episodeTitle(base)
	if title == "" {
		if episode > 0 {
			title = fmt.Sprintf("Episode %d", episode)
		} else {
			title = cleanTitle(base)
		}
	}
	return seriesName, season, episode, title
}

// episodeTitle returns the human title that follows the SxxEyy token.
func episodeTitle(base string) string {
	spaced := separatorRe.ReplaceAllString(base, " ")
	loc := episodeTitleCut.FindStringIndex(spaced)
	if loc == nil {
		return ""
	}
	rest := strings.TrimSpace(spaced[loc[1]:])
	rest = strings.Trim(rest, " -_.")
	rest = junkRe.ReplaceAllString(rest, " ")
	rest = multiSpaceRe.ReplaceAllString(rest, " ")
	return strings.TrimSpace(rest)
}

// stripEpisodeTokens removes SxxEyy / NxM markers from a string.
func stripEpisodeTokens(s string) string {
	s = episodeRe.ReplaceAllString(s, " ")
	s = seasonXEpisode.ReplaceAllString(s, " ")
	return s
}

// cleanTitle normalizes a folder/file fragment into a display title.
func cleanTitle(s string) string {
	s = separatorRe.ReplaceAllString(s, " ")
	s = junkRe.ReplaceAllString(s, " ")
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Trim(s, " -()[]"))
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
