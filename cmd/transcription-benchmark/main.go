package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcribe"
)

type receipt struct {
	Backend         string    `json:"backend"`
	ModelPath       string    `json:"model_path"`
	InputRoot       string    `json:"input_root"`
	Files           int       `json:"files"`
	AudioSeconds    float64   `json:"audio_seconds"`
	ElapsedSeconds  float64   `json:"elapsed_seconds"`
	RealTimeFactor  float64   `json:"real_time_factor"`
	TranscribedText []string  `json:"transcribed_text"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
}

func main() {
	if len(os.Args) != 2 {
		fatal("usage: transcription-benchmark <input-root>")
	}
	inputRoot := os.Args[1]
	files, err := audioFiles(inputRoot)
	if err != nil {
		fatal(err.Error())
	}
	if len(files) == 0 {
		fatal("no .wav or .flac files found in " + inputRoot)
	}

	modelPath := os.Getenv("TRANSCRIBER_MODEL_PATH")
	if modelPath == "" {
		modelPath = os.Getenv("AINOTEBOOK_CRISPASR_MODEL_PATH")
	}
	config := transcribe.Config{
		Backend:          "crispasr",
		ModelPath:        modelPath,
		FFmpegExecutable: firstNonEmpty(os.Getenv("FFMPEG_EXECUTABLE"), "ffmpeg"),
		NativeDir:        os.Getenv("AINOTEBOOK_CRISPASR_NATIVE_DIR"),
		NativeBackend:    os.Getenv("AINOTEBOOK_CRISPASR_BACKEND"),
		Language:         os.Getenv("TRANSCRIBER_LANGUAGE"),
		Threads:          positiveInt(os.Getenv("AINOTEBOOK_CRISPASR_THREADS"), 4),
		ChunkSeconds:     positiveInt(os.Getenv("TRANSCRIBER_CHUNK_SECONDS"), 30),
		SilenceRMS:       floatEnv(os.Getenv("TRANSCRIBER_SILENCE_RMS"), 0.005),
		RequireGPU:       !truthy(os.Getenv("AINOTEBOOK_CRISPASR_ALLOW_CPU")),
	}
	model, err := transcribe.New(config)
	if err != nil {
		fatal(err.Error())
	}

	started := time.Now().UTC()
	var totalAudio float64
	texts := make([]string, 0, len(files))
	for index, file := range files {
		startedFile := time.Now()
		result, err := model.Transcribe(context.Background(), file, config.Language)
		if err != nil {
			fatal(fmt.Sprintf("file %d/%d %s: %v", index+1, len(files), file, err))
		}
		elapsed := time.Since(startedFile)
		seconds := audioDurationSeconds(file)
		totalAudio += seconds
		texts = append(texts, result.Text)
		fmt.Printf("%d/%d %s audio=%.1fs elapsed=%.1fs segments=%d\n", index+1, len(files), filepath.Base(file), seconds, elapsed.Seconds(), len(result.Segments))
	}
	finished := time.Now().UTC()
	elapsed := finished.Sub(started).Seconds()
	result := receipt{Backend: "crispasr", ModelPath: modelPath, InputRoot: inputRoot, Files: len(files), AudioSeconds: totalAudio, ElapsedSeconds: elapsed, TranscribedText: texts, StartedAt: started, FinishedAt: finished}
	if elapsed > 0 {
		result.RealTimeFactor = elapsed / totalAudio
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	output := filepath.Join(inputRoot, "transcription-benchmark.json")
	if err := os.WriteFile(output, append(data, '\n'), 0o644); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("total audio=%.1fs elapsed=%.1fs RTF=%.3f receipt=%s\n", totalAudio, elapsed, result.RealTimeFactor, output)
}

func audioFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".wav" || ext == ".flac" {
			files = append(files, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func audioDurationSeconds(path string) float64 {
	if strings.EqualFold(filepath.Ext(path), ".wav") {
		data, err := os.ReadFile(path)
		if err == nil && len(data) >= 44 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
			for offset := 12; offset+8 <= len(data); {
				size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
				end := offset + 8 + size
				if end > len(data) {
					break
				}
				if string(data[offset:offset+4]) == "fmt " && size >= 16 {
					sampleRate := binary.LittleEndian.Uint32(data[offset+12 : offset+16])
					channels := binary.LittleEndian.Uint16(data[offset+10 : offset+12])
					bits := binary.LittleEndian.Uint16(data[offset+22 : offset+24])
					for next := end; next+8 <= len(data); {
						dataSize := int(binary.LittleEndian.Uint32(data[next+4 : next+8]))
						if string(data[next:next+4]) == "data" && sampleRate > 0 && channels > 0 && bits > 0 {
							return float64(dataSize) / float64(sampleRate*uint32(channels)*uint32(bits/8))
						}
						next += 8 + dataSize
					}
				}
				offset = end
			}
		}
	}
	return 30
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func positiveInt(value string, fallback int) int {
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func floatEnv(value string, fallback float64) float64 {
	var parsed float64
	if _, err := fmt.Sscanf(value, "%f", &parsed); err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
