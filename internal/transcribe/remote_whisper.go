package transcribe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type remoteWhisper struct {
	Endpoint         string
	Model            string
	FFmpegExecutable string
	ChunkSeconds     int
	SilenceRMS       float64
	ResponseFormat   string
	WordTimestamps   bool
	Language         string
	HTTP             *http.Client
}

func newRemoteWhisper(config Config) (*remoteWhisper, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("transcriber remote-whisper: endpoint is required")
	}
	if strings.TrimSpace(config.FFmpegExecutable) == "" {
		return nil, fmt.Errorf("transcriber remote-whisper: FFmpeg executable is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("transcriber remote-whisper: model identity is required; record the model pinned by the server")
	}
	if config.ChunkSeconds <= 0 {
		config.ChunkSeconds = 30
	}
	if strings.TrimSpace(config.ResponseFormat) == "" {
		config.ResponseFormat = "json"
	}
	return &remoteWhisper{
		Endpoint: strings.TrimRight(config.Endpoint, "/"), Model: config.Model,
		FFmpegExecutable: config.FFmpegExecutable, ChunkSeconds: config.ChunkSeconds,
		SilenceRMS: config.SilenceRMS, ResponseFormat: config.ResponseFormat, WordTimestamps: config.WordTimestamps,
		Language: config.Language,
		HTTP:     &http.Client{Timeout: timeout(config.HTTPTimeout)},
	}, nil
}

func (t *remoteWhisper) Description() map[string]any {
	return map[string]any{"backend": "remote-whisper", "endpoint": t.Endpoint + "/inference", "model": t.Model, "chunk_seconds": t.ChunkSeconds, "silence_rms": t.SilenceRMS, "response_format": t.ResponseFormat, "word_timestamps": t.WordTimestamps}
}

func (t *remoteWhisper) Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error) {
	if strings.TrimSpace(language) == "" {
		language = t.Language
	}
	samples, err := loadPCM16(ctx, t.FFmpegExecutable, inputPath)
	if err != nil {
		return transcript.Transcript{}, err
	}
	chunks := splitPCM(samples, t.ChunkSeconds)
	result := transcript.Transcript{Schema: transcript.Schema, Language: language}
	for _, chunk := range chunks {
		if isSilent(chunk.Samples, t.SilenceRMS) {
			continue
		}
		text, err := t.send(ctx, wavBytes(chunk.Samples), language)
		if err != nil {
			return transcript.Transcript{}, err
		}
		parsed, err := parseProviderResponse([]byte(text), chunk.EndSeconds-chunk.StartSeconds)
		if err != nil {
			return transcript.Transcript{}, fmt.Errorf("transcriber remote-whisper: %w", err)
		}
		for _, segment := range parsed.Segments {
			segment.StartSeconds += chunk.StartSeconds
			segment.EndSeconds += chunk.StartSeconds
			for i := range segment.Words {
				segment.Words[i].StartSeconds += chunk.StartSeconds
				segment.Words[i].EndSeconds += chunk.StartSeconds
			}
			result.Segments = append(result.Segments, segment)
		}
		if result.Language == "" {
			result.Language = parsed.Language
		}
	}
	result.Text = joinSegments(result.Segments)
	return result, nil
}

func (t *remoteWhisper) send(ctx context.Context, wav []byte, language string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("transcriber remote-whisper: create file part: %w", err)
	}
	if _, err := part.Write(wav); err != nil {
		return "", fmt.Errorf("transcriber remote-whisper: write audio: %w", err)
	}
	_ = writer.WriteField("temperature", "0.0")
	_ = writer.WriteField("response_format", t.ResponseFormat)
	if t.WordTimestamps {
		_ = writer.WriteField("token_timestamps", "true")
		_ = writer.WriteField("split_on_word", "true")
		_ = writer.WriteField("max_len", "60")
	}
	if language != "" {
		_ = writer.WriteField("language", language)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("transcriber remote-whisper: close multipart body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint+"/inference", &body)
	if err != nil {
		return "", fmt.Errorf("transcriber remote-whisper: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcriber remote-whisper: request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("transcriber remote-whisper: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("transcriber remote-whisper: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}
