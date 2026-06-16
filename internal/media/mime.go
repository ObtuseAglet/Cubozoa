package media

import "strings"

// StreamContentType maps a media container (file extension without the dot) to
// the MIME type used when streaming the file for direct play.
func StreamContentType(container string) string {
	switch strings.ToLower(container) {
	// Video
	case "mp4", "m4v":
		return "video/mp4"
	case "mkv":
		return "video/x-matroska"
	case "webm":
		return "video/webm"
	case "avi":
		return "video/x-msvideo"
	case "mov":
		return "video/quicktime"
	case "wmv":
		return "video/x-ms-wmv"
	case "flv":
		return "video/x-flv"
	case "ts", "m2ts":
		return "video/mp2t"
	case "mpg", "mpeg":
		return "video/mpeg"
	case "3gp":
		return "video/3gpp"
	case "ogv":
		return "video/ogg"
	// Audio
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "m4a", "aac", "alac":
		return "audio/mp4"
	case "ogg", "oga", "opus":
		return "audio/ogg"
	case "wav":
		return "audio/wav"
	case "wma":
		return "audio/x-ms-wma"
	default:
		return "application/octet-stream"
	}
}
