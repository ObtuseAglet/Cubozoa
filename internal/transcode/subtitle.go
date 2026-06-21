package transcode

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// maxSubtitleBytes caps an extracted subtitle so a pathological stream cannot
// exhaust memory.
const maxSubtitleBytes = 8 << 20 // 8 MiB

// ExtractSubtitle pulls an embedded text subtitle stream (identified by its
// absolute ffprobe stream index) out of a media file and converts it to WebVTT.
// The run is time-bounded and the output size-capped. ffmpeg is invoked with an
// explicit argument vector — the input path is resolved by the caller, never
// supplied by a client.
func (m *Manager) ExtractSubtitle(ctx context.Context, input string, streamIndex int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.bin,
		"-v", "error",
		"-i", input,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-c:s", "webvtt",
		"-f", "webvtt",
		"pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("transcode: starting subtitle extraction: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, maxSubtitleBytes))
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("transcode: extracting subtitle: %w", waitErr)
	}
	if readErr != nil {
		return nil, readErr
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("transcode: empty subtitle output")
	}
	return data, nil
}
