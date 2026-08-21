package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type httpProvider struct {
	backend          string
	endpoint         string
	apiKey           string
	model            string
	ffmpegExecutable string
	chunkSeconds     int
	silenceRMS       float64
	language         string
	http             *http.Client
}

func newParakeet(config Config) (*httpProvider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("transcriber parakeet: NVIDIA API key is required")
	}
	if strings.TrimSpace(config.Endpoint) == "" {
		config.Endpoint = "https://integrate.api.nvidia.com/v1/audio/transcriptions"
	}
	if strings.TrimSpace(config.Model) == "" {
		config.Model = "nvidia/parakeet-tdt-1.1b"
	}
	if strings.TrimSpace(config.FFmpegExecutable) == "" {
		return nil, fmt.Errorf("transcriber parakeet: FFmpeg executable is required")
	}
	if config.ChunkSeconds <= 0 {
		config.ChunkSeconds = 30
	}
	return &httpProvider{backend: "parakeet", endpoint: config.Endpoint, apiKey: config.APIKey, model: config.Model, ffmpegExecutable: config.FFmpegExecutable, chunkSeconds: config.ChunkSeconds, silenceRMS: config.SilenceRMS, language: config.Language, http: &http.Client{Timeout: timeout(config.HTTPTimeout)}}, nil
}

func newOpenAI(config Config) (*httpProvider, error) {
	if strings.TrimSpace(config.Endpoint) == "" || strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("transcriber openai: endpoint and model are required")
	}
	if strings.TrimSpace(config.FFmpegExecutable) == "" {
		return nil, fmt.Errorf("transcriber openai: FFmpeg executable is required")
	}
	if config.ChunkSeconds <= 0 {
		config.ChunkSeconds = 30
	}
	return &httpProvider{backend: "openai", endpoint: config.Endpoint, apiKey: config.APIKey, model: config.Model, ffmpegExecutable: config.FFmpegExecutable, chunkSeconds: config.ChunkSeconds, silenceRMS: config.SilenceRMS, language: config.Language, http: &http.Client{Timeout: timeout(config.HTTPTimeout)}}, nil
}

func (p *httpProvider) Description() map[string]any {
	return map[string]any{"backend": p.backend, "endpoint": p.endpoint, "model": p.model, "chunk_seconds": p.chunkSeconds, "silence_rms": p.silenceRMS}
}

func (p *httpProvider) Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error) {
	if strings.TrimSpace(language) == "" {
		language = p.language
	}
	samples, err := loadPCM16(ctx, p.ffmpegExecutable, inputPath)
	if err != nil {
		return transcript.Transcript{}, err
	}
	result := transcript.Transcript{Schema: transcript.Schema, Language: language}
	for _, chunk := range splitPCM(samples, p.chunkSeconds) {
		if isSilent(chunk.Samples, p.silenceRMS) {
			continue
		}
		data, err := p.send(ctx, wavBytes(chunk.Samples), language, filepath.Base(inputPath))
		if err != nil {
			return transcript.Transcript{}, err
		}
		parsed, err := parseProviderResponse(data, chunk.EndSeconds-chunk.StartSeconds)
		if err != nil {
			return transcript.Transcript{}, fmt.Errorf("transcriber %s: %w", p.backend, err)
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

func (p *httpProvider) send(ctx context.Context, wav []byte, language, fileName string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, fmt.Errorf("transcriber %s: create file part: %w", p.backend, err)
	}
	if _, err := part.Write(wav); err != nil {
		return nil, fmt.Errorf("transcriber %s: write audio: %w", p.backend, err)
	}
	_ = writer.WriteField("model", p.model)
	_ = writer.WriteField("response_format", "verbose_json")
	if language != "" {
		_ = writer.WriteField("language", language)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("transcriber %s: close multipart body: %w", p.backend, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("transcriber %s: create request: %w", p.backend, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transcriber %s: request: %w", p.backend, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("transcriber %s: read response: %w", p.backend, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("transcriber %s: HTTP %d: %s", p.backend, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func parseProviderResponse(data []byte, duration float64) (transcript.Transcript, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return transcript.Transcript{}, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		if parsed, err := transcript.Parse(data); err == nil {
			return parsed, nil
		}
		var raw struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &raw); err == nil && strings.TrimSpace(raw.Text) != "" {
			trimmed = strings.TrimSpace(raw.Text)
		} else {
			return transcript.Transcript{}, fmt.Errorf("provider JSON has no segments or text")
		}
	}
	return transcript.Transcript{Schema: transcript.Schema, Text: trimmed, Segments: []transcript.Segment{{StartSeconds: 0, EndSeconds: duration, Text: trimmed}}}, nil
}
