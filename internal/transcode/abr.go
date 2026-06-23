package transcode

import "strconv"

// Rendition is one quality level in an adaptive-bitrate ladder. A zero-value
// Rendition (Height and VideoKbps both 0) means "transcode at source size using
// a quality target" — the single-stream default.
type Rendition struct {
	Name      string // stable label used in URLs ("1080", "720", ... or "auto")
	Height    int    // target height; 0 = no scaling
	Width     int    // approximate width for the master playlist's RESOLUTION
	VideoKbps int    // target video bitrate; 0 = use CRF
	AudioKbps int    // target audio bitrate; 0 = default
	Bandwidth int    // advertised bits/s for the master playlist
}

// AutoRendition is the default single stream: source resolution, quality-based.
var AutoRendition = Rendition{Name: "auto", AudioKbps: 128, Bandwidth: 3_000_000}

// ladderHeights is the standard quality ladder, highest first.
var ladderHeights = []int{1080, 720, 480, 360}

// SelectRenditions builds an adaptive ladder for a source of the given
// dimensions and (optional) bitrate. It never upscales: the top rung is the
// source resolution, and lower rungs are the standard heights below it. When
// the height is unknown, a single auto rendition is returned.
func SelectRenditions(width, height, bitrate int) []Rendition {
	if height <= 0 {
		auto := AutoRendition
		if bitrate > auto.Bandwidth {
			auto.Bandwidth = bitrate
		}
		return []Rendition{auto}
	}

	// Distinct target heights: the standard rungs strictly below the source,
	// plus the source height itself as the top rung (no upscaling).
	heights := []int{height}
	for _, h := range ladderHeights {
		if h < height {
			heights = append(heights, h)
		}
	}
	if len(heights) > 4 {
		heights = heights[:4]
	}

	out := make([]Rendition, 0, len(heights))
	for _, h := range heights {
		vk := videoKbpsFor(h)
		if bitrate > 0 && h == height {
			// Don't spend more than the source on the top rung.
			if srcKbps := bitrate / 1000; srcKbps > 0 && srcKbps < vk {
				vk = srcKbps
			}
		}
		ak := audioKbpsFor(h)
		out = append(out, Rendition{
			Name:      strconv.Itoa(h),
			Height:    h,
			Width:     evenWidth(width, height, h),
			VideoKbps: vk,
			AudioKbps: ak,
			Bandwidth: (vk + ak) * 1100, // bits/s with ~10% container overhead
		})
	}
	return out
}

func videoKbpsFor(height int) int {
	switch {
	case height >= 1080:
		return 5000
	case height >= 720:
		return 2800
	case height >= 480:
		return 1400
	case height >= 360:
		return 800
	default:
		return 500
	}
}

func audioKbpsFor(height int) int {
	switch {
	case height >= 1080:
		return 160
	case height >= 480:
		return 128
	default:
		return 96
	}
}

// evenWidth scales a source width to a target height, rounded up to an even
// number — matching ffmpeg's "scale=-2:H" — so the advertised RESOLUTION agrees
// with the bytes actually produced. H.264 requires even dimensions.
func evenWidth(srcW, srcH, targetH int) int {
	if srcW <= 0 || srcH <= 0 {
		return 0
	}
	w := srcW * targetH / srcH
	return (w + 1) &^ 1
}
