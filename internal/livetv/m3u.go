// Package livetv adds IPTV "Live TV" support: it ingests an M3U playlist (and,
// optionally, an XMLTV guide), exposes the channels through the Jellyfin Live TV
// API so existing clients can browse and play them, and proxies channel streams
// through the transcode pipeline.
//
// The playlist and guide are operator configuration, so the upstream URLs are
// trusted; clients never supply a URL — they reference a channel by ID and the
// server resolves the stream server-side.
package livetv

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// Channel is one IPTV channel parsed from an M3U playlist.
type Channel struct {
	ID     string // stable ID derived from tvg-id or the stream URL
	TvgID  string // XMLTV channel id, used to join with the EPG
	Name   string
	Number string
	Logo   string // remote logo URL (may be empty)
	Group  string // group-title, e.g. "News", "Sports"
	URL    string // upstream stream URL
}

// attrRe extracts key="value" attributes from an #EXTINF line.
var attrRe = regexp.MustCompile(`([a-zA-Z0-9\-]+)="([^"]*)"`)

// ParseM3U parses an M3U/M3U8 playlist into channels. It is lenient: malformed
// or non-media lines are skipped rather than aborting the parse, and an entry is
// only emitted once a URL line follows its #EXTINF header.
func ParseM3U(data []byte) []Channel {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var channels []Channel
	var pending *Channel
	autoNum := 0

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || line == "#EXTM3U" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			pending = parseExtInf(line[len("#EXTINF:"):])
		case strings.HasPrefix(line, "#EXTGRP:"):
			if pending != nil && pending.Group == "" {
				pending.Group = strings.TrimSpace(line[len("#EXTGRP:"):])
			}
		case strings.HasPrefix(line, "#"):
			// Other directives (#EXTVLCOPT, #KODIPROP, comments): ignore.
		default:
			// A URL line completes the pending entry.
			if pending == nil {
				continue
			}
			if !isStreamURL(line) {
				pending = nil
				continue
			}
			pending.URL = line
			autoNum++
			finalizeChannel(pending, autoNum)
			channels = append(channels, *pending)
			pending = nil
		}
	}
	return channels
}

// parseExtInf parses the portion of an #EXTINF line after "#EXTINF:", i.e.
// "<duration> key=\"v\" ...,Display Name".
func parseExtInf(s string) *Channel {
	ch := &Channel{}

	// Display name is everything after the last comma that is not inside quotes.
	if name, rest, ok := splitDisplayName(s); ok {
		ch.Name = strings.TrimSpace(name)
		s = rest
	}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		key, val := strings.ToLower(m[1]), m[2]
		switch key {
		case "tvg-id":
			ch.TvgID = val
		case "tvg-name":
			if ch.Name == "" {
				ch.Name = val
			}
		case "tvg-logo":
			ch.Logo = val
		case "tvg-chno", "channel-number":
			ch.Number = val
		case "group-title":
			ch.Group = val
		}
	}
	return ch
}

// splitDisplayName separates the ",Display Name" suffix from the duration/
// attribute section. It splits at the FIRST unquoted comma, so a display name
// that itself contains commas is preserved intact while commas inside quoted
// attribute values are ignored.
func splitDisplayName(s string) (name, rest string, ok bool) {
	inQuotes := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				return s[i+1:], s[:i], true
			}
		}
	}
	return "", s, false
}

func finalizeChannel(ch *Channel, autoNum int) {
	if ch.Name == "" {
		ch.Name = "Channel " + strconv.Itoa(autoNum)
	}
	if ch.Number == "" {
		ch.Number = strconv.Itoa(autoNum)
	}
	ch.ID = channelID(ch)
}

// channelID derives a stable ID from the tvg-id when present (so it survives
// reordering) or the stream URL otherwise.
func channelID(ch *Channel) string {
	seed := ch.TvgID
	if seed == "" {
		seed = ch.URL
	}
	sum := sha256.Sum256([]byte("livetv:" + seed))
	return hex.EncodeToString(sum[:16])
}

func isStreamURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "rtmp://") || strings.HasPrefix(s, "rtsp://")
}
