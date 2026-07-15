package livetv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"strconv"
	"strings"
	"time"
)

// Program is one EPG entry from an XMLTV guide.
type Program struct {
	ID           string // stable, derived from channel + start + title
	ChannelTvgID string // the XMLTV channel id (joins to Channel.TvgID)
	Title        string
	Description  string
	Category     string
	Start        time.Time
	Stop         time.Time
}

// guideChannel is an XMLTV <channel> with its display name(s), used to match
// playlist channels to the guide by name when tvg-id does not line up.
type guideChannel struct {
	ID    string
	Names []string
}

// xmltv mirrors the parts of the XMLTV schema Cubozoa reads.
type xmltvDoc struct {
	Channels   []xmltvChannel   `xml:"channel"`
	Programmes []xmltvProgramme `xml:"programme"`
}

type xmltvChannel struct {
	ID           string   `xml:"id,attr"`
	DisplayNames []string `xml:"display-name"`
}

type xmltvProgramme struct {
	Start   string `xml:"start,attr"`
	Stop    string `xml:"stop,attr"`
	Channel string `xml:"channel,attr"`
	Title   string `xml:"title"`
	Desc    string `xml:"desc"`
	Cat     string `xml:"category"`
}

// ParseXMLTV parses an XMLTV guide into programs. Entries with an unparseable
// time or empty channel are skipped rather than failing the whole guide.
func ParseXMLTV(data []byte) ([]Program, error) {
	progs, _, err := parseGuide(data)
	return progs, err
}

// parseGuide parses both the programmes and the channel display names.
func parseGuide(data []byte) ([]Program, []guideChannel, error) {
	var doc xmltvDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}

	channels := make([]guideChannel, 0, len(doc.Channels))
	for _, c := range doc.Channels {
		if c.ID == "" {
			continue
		}
		names := make([]string, 0, len(c.DisplayNames))
		for _, n := range c.DisplayNames {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		channels = append(channels, guideChannel{ID: c.ID, Names: names})
	}

	out := make([]Program, 0, len(doc.Programmes))
	for _, p := range doc.Programmes {
		if p.Channel == "" {
			continue
		}
		start, ok := parseXMLTVTime(p.Start)
		if !ok {
			continue
		}
		stop, ok := parseXMLTVTime(p.Stop)
		if !ok {
			stop = start.Add(30 * time.Minute)
		}
		title := strings.TrimSpace(p.Title)
		out = append(out, Program{
			ID:           programID(p.Channel, start, title),
			ChannelTvgID: p.Channel,
			Title:        title,
			Description:  strings.TrimSpace(p.Desc),
			Category:     strings.TrimSpace(p.Cat),
			Start:        start,
			Stop:         stop,
		})
	}
	return out, channels, nil
}

// parseXMLTVTime parses "20060102150405 -0700" or "20060102150405" (assumed UTC).
func parseXMLTVTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse("20060102150405 -0700", s); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("20060102150405", s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func programID(channel string, start time.Time, title string) string {
	sum := sha256.Sum256([]byte("program:" + channel + ":" + strconv.FormatInt(start.Unix(), 10) + ":" + title))
	return hex.EncodeToString(sum[:16])
}
