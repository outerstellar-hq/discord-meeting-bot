package transcribe

import (
	"context"
	"fmt"
	"strings"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

// Transcriber is the only model boundary used by meeting finalization.
// Implementations are deliberately explicit: selecting a backend also selects
// the model/runtime contract used to produce the transcript.
type Transcriber interface {
	Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error)
}

type Config struct {
	Backend          string
	Endpoint         string
	APIKey           string
	Model            string
	ModelPath        string
	Executable       string
	Args             []string
	FFmpegExecutable string
	ChunkSeconds     int
	SilenceRMS       float64
	HTTPTimeout      int
	ResponseFormat   string
	WordTimestamps   bool
	Language         string
	NativeDir        string
	NativeBackend    string
	Threads          int
	RequireGPU       bool
}

type Describer interface {
	Description() map[string]any
}

func New(config Config) (Transcriber, error) {
	backend := strings.ToLower(strings.TrimSpace(config.Backend))
	if backend == "" {
		return nil, fmt.Errorf("transcriber: backend is required; choose an ainotebook backend explicitly")
	}
	switch backend {
	case "remote-whisper":
		return newRemoteWhisper(config)
	case "parakeet":
		return newParakeet(config)
	case "openai":
		return newOpenAI(config)
	case "crispasr":
		return newCrispASR(config)
	case "command", "onnx-whisper", "onnx-moonshine", "onnx-gigaam", "onnx-sensevoice", "onnx-parakeet-local":
		return newCommand(config, backend)
	default:
		return nil, fmt.Errorf("transcriber: unsupported backend %q", config.Backend)
	}
}
