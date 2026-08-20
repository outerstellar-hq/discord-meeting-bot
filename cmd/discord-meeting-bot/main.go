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
	argsJSON := os.Getenv("TRANSCRIBER_ARGS_JSON")
	var args []string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || len(args) == 0 {
		return discord.Config{}, fmt.Errorf("TRANSCRIBER_ARGS_JSON must be a JSON array of arguments")
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
		Transcriber:      transcribe.Command{Executable: os.Getenv("TRANSCRIBER_EXECUTABLE"), Args: args},
		Summarizer: summary.Client{
			Endpoint: os.Getenv("SUMMARY_ENDPOINT"),
			APIKey:   os.Getenv("SUMMARY_API_KEY"),
			Model:    os.Getenv("SUMMARY_MODEL"),
		},
		ChunkDuration: time.Duration(chunkMinutes) * time.Minute,
	}, nil
}
