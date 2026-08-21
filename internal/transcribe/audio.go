package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

const sampleRate = 16000

type pcmChunk struct {
	StartSeconds float64
	EndSeconds   float64
	Samples      []int16
}

func loadPCM16(ctx context.Context, executable, inputPath string) ([]int16, error) {
	if strings.TrimSpace(executable) == "" {
		return nil, fmt.Errorf("transcriber: FFmpeg executable is required for 16 kHz audio conversion")
	}
	cmd := exec.CommandContext(ctx, executable,
		"-hide_banner", "-loglevel", "error", "-i", inputPath,
		"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "1", "pipe:1",
	)
	cmd.Env = append(os.Environ(), "AGENT_OWNER=discord-meeting-bot", "AGENT_TASK=transcription-audio")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("transcriber: convert %q to 16 kHz mono PCM: %w: %s", inputPath, err, strings.TrimSpace(stderr.String()))
	}
	if len(stdout.Bytes())%2 != 0 {
		return nil, fmt.Errorf("transcriber: FFmpeg returned an odd PCM byte count for %q", inputPath)
	}
	samples := make([]int16, len(stdout.Bytes())/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(stdout.Bytes()[i*2:]))
	}
	return samples, nil
}

func splitPCM(samples []int16, chunkSeconds int) []pcmChunk {
	if chunkSeconds <= 0 {
		chunkSeconds = 30
	}
	chunkSamples := chunkSeconds * sampleRate
	if len(samples) == 0 {
		return []pcmChunk{{}}
	}
	chunks := make([]pcmChunk, 0, (len(samples)+chunkSamples-1)/chunkSamples)
	for start := 0; start < len(samples); start += chunkSamples {
		end := start + chunkSamples
		if end > len(samples) {
			end = len(samples)
		}
		chunks = append(chunks, pcmChunk{
			StartSeconds: float64(start) / sampleRate,
			EndSeconds:   float64(end) / sampleRate,
			Samples:      samples[start:end],
		})
	}
	return chunks
}

func isSilent(samples []int16, threshold float64) bool {
	if len(samples) == 0 {
		return true
	}
	if threshold <= 0 {
		return false
	}
	var sum float64
	for _, sample := range samples {
		value := float64(sample) / 32768
		sum += value * value
	}
	return math.Sqrt(sum/float64(len(samples))) < threshold
}

func wavBytes(samples []int16) []byte {
	dataSize := len(samples) * 2
	result := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	result.WriteString("RIFF")
	_ = binary.Write(result, binary.LittleEndian, uint32(36+dataSize))
	result.WriteString("WAVEfmt ")
	_ = binary.Write(result, binary.LittleEndian, uint32(16))
	_ = binary.Write(result, binary.LittleEndian, uint16(1))
	_ = binary.Write(result, binary.LittleEndian, uint16(1))
	_ = binary.Write(result, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(result, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(result, binary.LittleEndian, uint16(2))
	_ = binary.Write(result, binary.LittleEndian, uint16(16))
	result.WriteString("data")
	_ = binary.Write(result, binary.LittleEndian, uint32(dataSize))
	for _, sample := range samples {
		_ = binary.Write(result, binary.LittleEndian, sample)
	}
	return result.Bytes()
}

func joinSegments(segments []transcript.Segment) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if text := strings.TrimSpace(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func timeout(seconds int) time.Duration {
	if seconds <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(seconds) * time.Second
}
