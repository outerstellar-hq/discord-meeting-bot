package discord

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/meeting"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/pipeline"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/summary"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcribe"
	"github.com/thomas-vilte/dave-go/session"
)

type Config struct {
	Token            string
	GuildID          snowflake.ID
	ChannelID        snowflake.ID
	OwnerUserID      string
	OutputRoot       string
	FFmpegExecutable string
	Video            meeting.VideoConfig
	Transcriber      transcribe.Transcriber
	Summarizer       summary.Client
	ChunkDuration    time.Duration
}

type Bot struct {
	config Config
	logger *slog.Logger
	mu     sync.Mutex
	client *bot.Client
	conn   voice.Conn
	rec    *meeting.Recorder
	video  *meeting.VideoRecorder
	start  time.Time
}

func NewBot(config Config, logger *slog.Logger) (*Bot, error) {
	if config.Token == "" || config.GuildID == 0 || config.ChannelID == 0 || config.OwnerUserID == "" {
		return nil, fmt.Errorf("discord bot: token, guild ID, channel ID, and owner user ID are required")
	}
	if config.OutputRoot == "" || config.FFmpegExecutable == "" || config.Transcriber == nil {
		return nil, fmt.Errorf("discord bot: output root, FFmpeg executable, and explicit transcriber backend are required")
	}
	if config.Video.Enabled && (config.Video.Executable == "" || len(config.Video.Args) == 0) {
		return nil, fmt.Errorf("discord bot: video recording requires executable and arguments")
	}
	if config.Summarizer.Endpoint == "" || config.Summarizer.Model == "" {
		return nil, fmt.Errorf("discord bot: summary endpoint and model are required")
	}
	if config.ChunkDuration <= 0 {
		return nil, fmt.Errorf("discord bot: chunk duration must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Bot{config: config, logger: logger}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	client, err := disgo.New(b.config.Token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildVoiceStates)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(session.CreateFunc())),
		bot.WithEventListenerFunc(b.onReady),
		bot.WithEventListenerFunc(b.onCommand),
	)
	if err != nil {
		return fmt.Errorf("discord bot: create client: %w", err)
	}
	b.mu.Lock()
	b.client = client
	b.mu.Unlock()
	defer client.Close(context.Background())
	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("discord bot: open gateway: %w", err)
	}
	<-ctx.Done()
	b.stopIfRunning(context.Background())
	return nil
}

func (b *Bot) onReady(event *events.Ready) {
	command := discord.SlashCommandCreate{Name: "record", Description: "Record the configured voice channel"}
	if _, err := event.Client().Rest.CreateGuildCommand(event.Client().ID(), b.config.GuildID, command); err != nil {
		b.logger.Error("register record command", slog.Any("error", err))
		return
	}
	stop := discord.SlashCommandCreate{Name: "stop-recording", Description: "Stop recording and process the meeting"}
	if _, err := event.Client().Rest.CreateGuildCommand(event.Client().ID(), b.config.GuildID, stop); err != nil {
		b.logger.Error("register stop command", slog.Any("error", err))
		return
	}
	b.logger.Info("meeting bot ready", slog.String("guild_id", b.config.GuildID.String()), slog.String("channel_id", b.config.ChannelID.String()))
}

func (b *Bot) onCommand(event *events.ApplicationCommandInteractionCreate) {
	if event.GuildID() == nil || *event.GuildID() != b.config.GuildID {
		return
	}
	switch event.Data.CommandName() {
	case "record":
		if err := event.DeferCreateMessage(false); err != nil {
			b.logger.Error("acknowledge record command", slog.Any("error", err))
			return
		}
		if err := b.startRecording(context.Background()); err != nil {
			_ = event.CreateMessage(discord.MessageCreate{Content: "Recording could not start: " + err.Error()})
			return
		}
		_ = event.CreateMessage(discord.MessageCreate{Content: "Recording started. Use /stop-recording when the meeting is over."})
	case "stop-recording":
		if err := event.DeferCreateMessage(false); err != nil {
			b.logger.Error("acknowledge stop command", slog.Any("error", err))
			return
		}
		manifest, err := b.stopRecording(context.Background())
		if err != nil {
			_ = event.CreateMessage(discord.MessageCreate{Content: "Recording could not stop cleanly: " + err.Error()})
			return
		}
		_ = event.CreateMessage(discord.MessageCreate{Content: "Recording saved. Transcription and summary are running in " + filepath.Join(b.config.OutputRoot, manifest.MeetingID)})
	}
}

func (b *Bot) startRecording(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rec != nil {
		return fmt.Errorf("a meeting is already recording")
	}
	startedAt := time.Now().UTC()
	meetingID := startedAt.Format("20060102-150405")
	recorder, err := meeting.NewRecorder(b.config.OutputRoot, meetingID, b.config.GuildID.String(), b.config.ChannelID.String(), b.config.OwnerUserID, startedAt, b.config.ChunkDuration, meeting.FFmpegEncoder{Executable: b.config.FFmpegExecutable})
	if err != nil {
		return err
	}
	meetingRoot := filepath.Join(b.config.OutputRoot, meetingID)
	video, err := meeting.StartVideo(b.config.Video, meetingRoot)
	if err != nil {
		_, _ = recorder.Stop(context.Background(), time.Now().UTC())
		return err
	}
	conn := b.client.VoiceManager.CreateConn(b.config.GuildID)
	conn.SetOpusFrameReceiver(NewPCMReceiver(recorder))
	if err := conn.Open(ctx, b.config.ChannelID, false, false); err != nil {
		_ = video.Stop(context.Background())
		_, _ = recorder.Stop(context.Background(), time.Now().UTC())
		return fmt.Errorf("open voice channel: %w", err)
	}
	b.rec = recorder
	b.conn = conn
	b.video = video
	b.start = startedAt
	return nil
}

func (b *Bot) stopRecording(ctx context.Context) (meeting.Manifest, error) {
	b.mu.Lock()
	if b.rec == nil || b.conn == nil {
		b.mu.Unlock()
		return meeting.Manifest{}, fmt.Errorf("no meeting is recording")
	}
	recorder, conn, video := b.rec, b.conn, b.video
	b.rec, b.conn, b.video = nil, nil, nil
	b.mu.Unlock()
	conn.Close(ctx)
	manifest, err := recorder.Stop(ctx, time.Now().UTC())
	if err != nil {
		if video != nil {
			_ = video.Stop(context.Background())
		}
		return meeting.Manifest{}, err
	}
	if err := video.Stop(ctx); err != nil {
		return manifest, err
	}
	go func() {
		meetingRoot := filepath.Join(b.config.OutputRoot, manifest.MeetingID)
		result, err := pipeline.Finalize(context.Background(), manifest, meetingRoot, b.config.Transcriber, b.config.Summarizer)
		if err != nil {
			b.logger.Error("finalize meeting", slog.String("meeting_id", manifest.MeetingID), slog.Any("error", err))
			return
		}
		b.logger.Info("meeting finalized", slog.String("meeting_id", manifest.MeetingID), slog.Int("segments", len(result.Transcript.Segments)))
	}()
	return manifest, nil
}

func (b *Bot) stopIfRunning(ctx context.Context) {
	b.mu.Lock()
	running := b.rec != nil
	b.mu.Unlock()
	if running {
		if _, err := b.stopRecording(ctx); err != nil {
			b.logger.Error("stop meeting during shutdown", slog.Any("error", err))
		}
	}
}

func RunUntilSignal(ctx context.Context, b *Bot) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return b.Run(ctx)
}

func ParseSnowflake(value, name string) (snowflake.ID, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a Discord snowflake: %q", name, value)
	}
	return snowflake.ID(parsed), nil
}
