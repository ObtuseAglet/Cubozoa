package livetv

import (
	"regexp"
	"strings"
)

// qualityTokenRe matches resolution/format/feed noise commonly appended to
// channel names ("BBC One HD", "CNN FHD", "ESPN 1080p Backup") that should not
// affect name matching.
var qualityTokenRe = regexp.MustCompile(`(?i)\b(fhd|uhd|hd|sd|4k|8k|hq|1080p?|720p?|576p?|540p?|480p?|hevc|h ?\.?265|h ?\.?264|raw|feed|backup|alt|vip|multi|geo)\b`)

// nonAlnumRe collapses everything that is not a letter or digit.
var nonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeChannelName reduces a channel/display name to a comparison key by
// lowercasing, stripping quality/feed tokens and unicode HD marks, and removing
// all non-alphanumeric characters. "BBC One HD" and "bbc.one" both become
// "bbcone".
func normalizeChannelName(s string) string {
	s = strings.ToLower(s)
	s = unicodeMarks.Replace(s)
	s = qualityTokenRe.ReplaceAllString(s, " ")
	s = nonAlnumRe.ReplaceAllString(s, "")
	return s
}

// unicodeMarks strips superscript "HD/UHD/NEW" glyphs some playlists use.
var unicodeMarks = strings.NewReplacer(
	"ᵁᴴᴰ", "", "ᴴᴰ", "", "ᴱᴰ", "", "ⁿᵉʷ", "", "ᴺᴱᵂ", "",
)

// buildNameIndex maps a normalized display name to its guide channel id, so a
// playlist channel without a matching tvg-id can still be joined by name. When
// two guide channels share a normalized name the first wins (deterministic given
// the parse order).
func buildNameIndex(channels []guideChannel) map[string]string {
	idx := make(map[string]string, len(channels)*2)
	for _, c := range channels {
		for _, name := range c.Names {
			if k := normalizeChannelName(name); k != "" {
				if _, exists := idx[k]; !exists {
					idx[k] = c.ID
				}
			}
		}
	}
	return idx
}
