package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/meeting"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/summary"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type fakeTranscriber struct{}

func (fakeTranscriber) Transcribe(_ context.Context, inputPath, _ string) (transcript.Transcript, error) {
	text := "owner"
	if filepath.Base(inputPath) == "other.flac" {
		text = "other"
	}
	return transcript.Transcript{Language: "en", Segments: []transcript.Segment{{StartSeconds: 0, EndSeconds: 1, Text: text}}}, nil
}

type fakeSummarizer struct{}

func (fakeSummarizer) Summarize(_ context.Context, value transcript.Transcript) (summary.Result, error) {
	return summary.Result{Title: "Test meeting", Summary: value.Text}, nil
}

func TestFinalizeMergesSpeakerIdentityAndWritesArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "owner.flac"), []byte("owner"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.flac"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := meeting.NewManifest("meeting-1", "guild", "channel", "123", time.Now().Add(-time.Minute))
	manifest.EndedAt = time.Now()
	manifest.AddChunk(meeting.Chunk{UserID: "123", StartMS: 1000, EndMS: 2000, SampleRate: 48000, Channels: 2, Path: "owner.flac", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	manifest.AddChunk(meeting.Chunk{UserID: "456", StartMS: 0, EndMS: 1000, SampleRate: 48000, Channels: 2, Path: "other.flac", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	result, err := Finalize(context.Background(), manifest, root, fakeTranscriber{}, fakeSummarizer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Transcript.Segments) != 2 || result.Transcript.Segments[0].SpeakerKey != "SPEAKER_456" || result.Transcript.Segments[1].SpeakerKey != "ALEXANDER" {
		t.Fatalf("unexpected speaker ordering: %#v", result.Transcript.Segments)
	}
	if result.Transcript.Segments[0].TurnID != "T000001" || result.Transcript.Segments[1].TurnID != "T000002" {
		t.Fatalf("turn IDs are not unique: %#v", result.Transcript.Segments)
	}
	for _, name := range []string{"transcript.json", "summary.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}
