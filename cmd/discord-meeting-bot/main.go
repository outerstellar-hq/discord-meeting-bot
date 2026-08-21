package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/discord"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/meeting"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/summary"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcribe"
)

func main() {
	config, err := loadConfig()
	if err != nil {
		slog.Error("invalid configuration", slog.Any("error", err))
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	bot, err := discord.NewBot(config, logger)
	if err != nil {
		slog.Error("create bot", slog.Any("error", err))
		os.Exit(2)
	}
	if err := discord.RunUntilSignal(context.Background(), bot); err != nil {
		slog.Error("bot stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func loadConfig() (discord.Config, error) {
	guildID, err := discord.ParseSnowflake(os.Getenv("DISCORD_GUILD_ID"), "DISCORD_GUILD_ID")
	if err != nil {
		return discord.Config{}, err
	}
	channelID, err := discord.ParseSnowflake(os.Getenv("DISCORD_VOICE_CHANNEL_ID"), "DISCORD_VOICE_CHANNEL_ID")
	if err != nil {
		return discord.Config{}, err
	}
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("TRANSCRIBER_BACKEND")))
	if backend == "" {
		return discord.Config{}, fmt.Errorf("TRANSCRIBER_BACKEND is required; choose an explicit ainotebook backend")
	}
	args, err := transcriberArgs(backend)
	if err != nil {
		return discord.Config{}, err
	}
	modelPath := strings.TrimSpace(os.Getenv("TRANSCRIBER_MODEL_PATH"))
	if modelPath == "" {
		modelPath = modelPathForBackend(backend)
	}
	endpoint := strings.TrimSpace(os.Getenv("TRANSCRIBER_ENDPOINT"))
	if endpoint == "" && backend == "remote-whisper" {
		endpoint = strings.TrimSpace(os.Getenv("AINOTEBOOK_REMOTE_WHISPER_URL"))
	}
	chunkSeconds, err := positiveIntEnv("TRANSCRIBER_CHUNK_SECONDS", 30)
	if err != nil {
		return discord.Config{}, err
	}
	silenceRMS, err := floatEnv("TRANSCRIBER_SILENCE_RMS", 0.005)
	if err != nil || silenceRMS < 0 {
		return discord.Config{}, fmt.Errorf("TRANSCRIBER_SILENCE_RMS must be a non-negative number")
	}
	transcriber, err := transcribe.New(transcribe.Config{
		Backend: backend, Endpoint: endpoint, APIKey: firstNonEmpty(os.Getenv("TRANSCRIBER_API_KEY"), os.Getenv("NVIDIA_API_KEY")),
		Model: strings.TrimSpace(os.Getenv("TRANSCRIBER_MODEL")), ModelPath: modelPath,
		Executable: os.Getenv("TRANSCRIBER_EXECUTABLE"), Args: args, FFmpegExecutable: os.Getenv("FFMPEG_EXECUTABLE"),
		ChunkSeconds: chunkSeconds, SilenceRMS: silenceRMS,
		ResponseFormat: firstNonEmpty(os.Getenv("TRANSCRIBER_RESPONSE_FORMAT"), "json"),
		WordTimestamps: strings.EqualFold(strings.TrimSpace(os.Getenv("TRANSCRIBER_WORD_TIMESTAMPS")), "true"),
		Language:       strings.TrimSpace(os.Getenv("TRANSCRIBER_LANGUAGE")),
		NativeDir:      os.Getenv("AINOTEBOOK_CRISPASR_NATIVE_DIR"), NativeBackend: os.Getenv("AINOTEBOOK_CRISPASR_BACKEND"),
		Threads:    positiveIntDefault(os.Getenv("AINOTEBOOK_CRISPASR_THREADS"), 4),
		RequireGPU: backend == "crispasr" && !truthy(os.Getenv("AINOTEBOOK_CRISPASR_ALLOW_CPU")),
	})
	if err != nil {
		return discord.Config{}, err
	}
	video, err := loadVideoConfig()
	if err != nil {
		return discord.Config{}, err
	}
	chunkMinutes := 1
	if raw := strings.TrimSpace(os.Getenv("AUDIO_CHUNK_MINUTES")); raw != "" {
		chunkMinutes, err = strconv.Atoi(raw)
		if err != nil || chunkMinutes < 1 {
			return discord.Config{}, fmt.Errorf("AUDIO_CHUNK_MINUTES must be a positive integer")
		}
	}
	return discord.Config{
		Token:            os.Getenv("DISCORD_BOT_TOKEN"),
		GuildID:          guildID,
		ChannelID:        channelID,
		OwnerUserID:      strings.TrimSpace(os.Getenv("DISCORD_OWNER_USER_ID")),
		OutputRoot:       os.Getenv("MEETING_OUTPUT_ROOT"),
		FFmpegExecutable: os.Getenv("FFMPEG_EXECUTABLE"),
		Video:            video,
		Transcriber:      transcriber,
		Summarizer: summary.Client{
			Endpoint: os.Getenv("SUMMARY_ENDPOINT"),
			APIKey:   os.Getenv("SUMMARY_API_KEY"),
			Model:    os.Getenv("SUMMARY_MODEL"),
		},
		ChunkDuration: time.Duration(chunkMinutes) * time.Minute,
	}, nil
}

func loadVideoConfig() (meeting.VideoConfig, error) {
	if !truthy(os.Getenv("VIDEO_RECORDING_ENABLED")) {
		return meeting.VideoConfig{}, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("VIDEO_ARGS_JSON")), &args); err != nil || len(args) == 0 {
		return meeting.VideoConfig{}, fmt.Errorf("VIDEO_ARGS_JSON must be a JSON array when video recording is enabled")
	}
	return meeting.VideoConfig{Enabled: true, Executable: firstNonEmpty(os.Getenv("VIDEO_EXECUTABLE"), os.Getenv("FFMPEG_EXECUTABLE")), Args: args}, nil
}

func transcriberArgs(backend string) ([]string, error) {
	switch backend {
	case "command", "onnx-whisper", "onnx-moonshine", "onnx-gigaam", "onnx-sensevoice", "onnx-parakeet-local":
		var args []string
		if err := json.Unmarshal([]byte(os.Getenv("TRANSCRIBER_ARGS_JSON")), &args); err != nil || len(args) == 0 {
			return nil, fmt.Errorf("TRANSCRIBER_ARGS_JSON must be a JSON array for backend %q", backend)
		}
		return args, nil
	default:
		return nil, nil
	}
}

func positiveIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
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

func modelPathForBackend(backend string) string {
	vars := map[string]string{
		"crispasr": "AINOTEBOOK_CRISPASR_MODEL_PATH", "onnx-whisper": "AINOTEBOOK_ONNX_MODEL_DIR",
		"onnx-moonshine": "AINOTEBOOK_MOONSHINE_MODEL_DIR", "onnx-gigaam": "AINOTEBOOK_GIGAAM_MODEL_DIR",
		"onnx-sensevoice": "AINOTEBOOK_SENSEVOICE_MODEL_DIR", "onnx-parakeet-local": "AINOTEBOOK_PARAKEET_LOCAL_MODEL_DIR",
	}
	if name := vars[backend]; name != "" {
		return strings.TrimSpace(os.Getenv(name))
	}
	return ""
}

func positiveIntEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func floatEnv(name string, fallback float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseFloat(value, 64)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
