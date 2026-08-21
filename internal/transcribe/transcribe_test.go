package transcribe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

func TestSplitPCMUsesThirtySecondChunks(t *testing.T) {
	chunks := splitPCM(make([]int16, sampleRate*30+sampleRate/2), 30)
	if len(chunks) != 2 || chunks[0].StartSeconds != 0 || chunks[0].EndSeconds != 30 || chunks[1].StartSeconds != 30 || chunks[1].EndSeconds != 30.5 {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestRemoteWhisperSendsQualityFieldsAndParsesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("response_format"); got != "json" {
			t.Fatalf("response format = %q", got)
		}
		if got := r.FormValue("token_timestamps"); got != "true" {
			t.Fatalf("token timestamps = %q", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil || len(data) < 44 || string(data[:4]) != "RIFF" {
			t.Fatalf("invalid WAV payload: %v, %d bytes", err, len(data))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"language":"en","text":"hello","segments":[{"start":0,"end":1,"text":"hello","words":[{"start":0,"end":1,"word":"hello"}]}]}`)
	}))
	defer server.Close()

	provider := &remoteWhisper{Endpoint: server.URL, Model: "ggml-large-v3-q5_0.bin", ResponseFormat: "json", WordTimestamps: true, HTTP: server.Client()}
	data, err := provider.send(context.Background(), wavBytes([]int16{1, 2, 3}), "en")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseProviderResponse([]byte(data), 3)
	if err != nil || parsed.Text != "hello" || len(parsed.Segments) != 1 || len(parsed.Segments[0].Words) != 1 {
		t.Fatalf("unexpected parsed response: %#v, %v", parsed, err)
	}
}

func TestNewRejectsImplicitBackendAndUnknownBackend(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "backend is required") {
		t.Fatalf("expected required backend error, got %v", err)
	}
	if _, err := New(Config{Backend: "whisper"}); err == nil || !strings.Contains(err.Error(), "unsupported backend") {
		t.Fatalf("expected unsupported backend error, got %v", err)
	}
}

func TestJoinSegments(t *testing.T) {
	got := joinSegments([]transcript.Segment{{Text: " first "}, {Text: ""}, {Text: "second"}})
	if got != "first second" {
		t.Fatalf("joined text = %q", got)
	}
}
