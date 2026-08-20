package meeting

import (
	"context"
	"os"
	"testing"
	"time"
)

type testEncoder struct{}

func (testEncoder) Encode(_ context.Context, samples []int16, _ int, _ int, outputPath string) error {
	return os.WriteFile(outputPath, []byte{byte(len(samples)), byte(len(samples) >> 8)}, 0o644)
}

func TestRecorderWritesPerUserChunksAndManifest(t *testing.T) {
	root := t.TempDir()
	started := time.Now().Add(-100 * time.Millisecond).UTC()
	recorder, err := NewRecorder(root, "meeting-1", "guild", "channel", "123", started, 40*time.Millisecond, testEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	frame := PCMFrame{UserID: "123", PCM: []int16{1, 2, 3}}
	if err := recorder.ReceivePCMFrame(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	if err := recorder.ReceivePCMFrame(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	manifest, err := recorder.Stop(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Chunks) != 1 {
		t.Fatalf("expected one chunk, got %d", len(manifest.Chunks))
	}
	chunkPath := root + "\\meeting-1\\" + manifest.Chunks[0].Path
	if _, err := os.Stat(chunkPath); err != nil {
		t.Fatalf("chunk was not written: %v", err)
	}
	if _, err := os.Stat(root + "\\meeting-1\\meeting.json"); err != nil {
		t.Fatalf("manifest was not written: %v", err)
	}
}

func TestManifestRejectsTraversal(t *testing.T) {
	manifest := NewManifest("meeting-1", "guild", "channel", "123", time.Now())
	manifest.EndedAt = time.Now().Add(time.Second)
	manifest.AddChunk(Chunk{UserID: "123", StartMS: 0, EndMS: 20, SampleRate: 48000, Channels: 2, Path: "../escape.flac", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}
