// Package transcode wraps ffprobe (media inspection) and ffmpeg (on-demand HLS
// transcoding). Both are optional external dependencies: when the binaries are
// not present, the features that need them are simply disabled and direct play
// continues to work. Nothing here ever passes untrusted input through a shell —
// binaries are executed with explicit argument vectors.
package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ProbeResult is the subset of ffprobe output Cubozoa uses.
type ProbeResult struct {
	DurationTicks int64
	Bitrate       int
	Width         int
	Height        int
	VideoCodec    string
	AudioCodec    string
	Streams       []StreamInfo
}

// StreamInfo describes one track discovered by ffprobe.
type StreamInfo struct {
	Index     int
	Type      string // Video, Audio, Subtitle
	Codec     string
	Language  string
	Channels  int
	Width     int
	Height    int
	IsDefault bool
	Title     string
}

// Prober runs ffprobe against media files.
type Prober struct {
	bin string
}

// NewProber returns a Prober, or ok=false if ffprobe is unavailable. An empty
// path triggers a PATH lookup for "ffprobe".
func NewProber(path string) (*Prober, bool) {
	bin := resolveBinary(path, "ffprobe")
	if bin == "" {
		return nil, false
	}
	return &Prober{bin: bin}, true
}

// ffprobe JSON shapes (only the fields we read).
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	Index       int               `json:"index"`
	CodecName   string            `json:"codec_name"`
	CodecType   string            `json:"codec_type"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	Channels    int               `json:"channels"`
	Tags        map[string]string `json:"tags"`
	Disposition map[string]int    `json:"disposition"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

// Probe inspects a media file. It is bounded by a timeout so a malformed file
// cannot hang a scan.
func (p *Prober) Probe(ctx context.Context, path string) (*ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.bin,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("transcode: ffprobe failed: %w", err)
	}

	var raw ffprobeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("transcode: parsing ffprobe output: %w", err)
	}

	res := &ProbeResult{}
	if secs, err := strconv.ParseFloat(strings.TrimSpace(raw.Format.Duration), 64); err == nil {
		res.DurationTicks = int64(secs * 10_000_000) // seconds -> 100ns ticks
	}
	if br, err := strconv.Atoi(strings.TrimSpace(raw.Format.BitRate)); err == nil {
		res.Bitrate = br
	}

	for _, s := range raw.Streams {
		si := StreamInfo{
			Index:     s.Index,
			Codec:     s.CodecName,
			Channels:  s.Channels,
			Width:     s.Width,
			Height:    s.Height,
			Language:  s.Tags["language"],
			Title:     s.Tags["title"],
			IsDefault: s.Disposition["default"] == 1,
		}
		switch s.CodecType {
		case "video":
			si.Type = "Video"
			if res.VideoCodec == "" {
				res.VideoCodec, res.Width, res.Height = s.CodecName, s.Width, s.Height
			}
		case "audio":
			si.Type = "Audio"
			if res.AudioCodec == "" {
				res.AudioCodec = s.CodecName
			}
		case "subtitle":
			si.Type = "Subtitle"
		default:
			continue
		}
		res.Streams = append(res.Streams, si)
	}
	return res, nil
}

// resolveBinary returns an absolute path to a usable binary, or "" if none.
func resolveBinary(explicit, name string) string {
	if explicit != "" {
		if _, err := exec.LookPath(explicit); err == nil {
			return explicit
		}
		return ""
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}
