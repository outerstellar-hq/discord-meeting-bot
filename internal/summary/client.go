package summary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type Result struct {
	Schema        string   `json:"schema"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Decisions     []string `json:"decisions"`
	ActionItems   []string `json:"action_items"`
	OpenQuestions []string `json:"open_questions"`
}

type Client struct {
	Endpoint string
	APIKey   string
	Model    string
	HTTP     *http.Client
}

func (c Client) Summarize(ctx context.Context, t transcript.Transcript) (Result, error) {
	if c.Endpoint == "" || c.Model == "" {
		return Result{}, fmt.Errorf("summary: endpoint and model are required")
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	prompt, err := json.Marshal(t)
	if err != nil {
		return Result{}, fmt.Errorf("summary: encode transcript: %w", err)
	}
	body := map[string]any{
		"model":           c.Model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": "Return only JSON with keys schema, title, summary, decisions, action_items, open_questions. Ground every claim in the transcript. Use empty arrays when evidence is absent."},
			{"role": "user", "content": "Summarize this meeting transcript:\n" + string(prompt)},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("summary: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return Result{}, fmt.Errorf("summary: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("summary: request: %w", err)
	}
	defer resp.Body.Close()
	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("summary: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return Result{}, fmt.Errorf("summary: endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBytes)))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBytes, &envelope); err != nil {
		return Result{}, fmt.Errorf("summary: decode response: %w", err)
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return Result{}, fmt.Errorf("summary: response contained no assistant content")
	}
	var result Result
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &result); err != nil {
		return Result{}, fmt.Errorf("summary: decode structured content: %w", err)
	}
	result.Schema = "starline.discord-meeting.summary.v1"
	return result, nil
}
