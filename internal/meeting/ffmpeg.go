package meeting

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type FFmpegEncoder struct {
	Executable string
}

func (e FFmpegEncoder) Encode(ctx context.Context, samples []int16, sampleRate, channels int, outputPath string) error {
	if e.Executable == "" {
		return fmt.Errorf("ffmpeg encoder: executable is required")
	}
	if len(samples) == 0 {
		return fmt.Errorf("ffmpeg encoder: no samples")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("ffmpeg encoder: create output directory: %w", err)
	}
	cmd := exec.CommandContext(ctx, e.Executable,
		"-hide_banner", "-loglevel", "error",
		"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "-i", "pipe:0",
		"-c:a", "flac", "-compression_level", "5", "-y", outputPath,
	)
	cmd.Env = append(os.Environ(), "AGENT_OWNER=discord-meeting-bot", "AGENT_TASK=flac-chunk")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg encoder: stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg encoder: start %q: %w", e.Executable, err)
	}
	for _, sample := range samples {
		if err := writeLE16(stdin, sample); err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("ffmpeg encoder: write PCM: %w", err)
		}
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("ffmpeg encoder: close PCM: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg encoder: encode %q: %w", outputPath, err)
	}
	return nil
}

func writeLE16(w interface{ Write([]byte) (int, error) }, sample int16) error {
	buf := []byte{byte(sample), byte(uint16(sample) >> 8)}
	_, err := w.Write(buf)
	return err
}
