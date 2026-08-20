package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/meeting"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/summary"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type Transcriber interface {
	Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error)
}

type Summarizer interface {
	Summarize(ctx context.Context, t transcript.Transcript) (summary.Result, error)
}

type Result struct {
	Transcript transcript.Transcript
	Summary    summary.Result
}

func Finalize(ctx context.Context, manifest meeting.Manifest, meetingRoot string, transcriber Transcriber, summarizer Summarizer) (Result, error) {
	if err := manifest.Validate(); err != nil {
		return Result{}, err
	}
	if transcriber == nil || summarizer == nil {
		return Result{}, fmt.Errorf("pipeline: transcriber and summarizer are required")
	}
	perSpeaker := make(map[string][]transcript.Transcript)
	perSpeakerOffsets := make(map[string][]float64)
	for _, chunk := range manifest.Chunks {
		path := filepath.Join(meetingRoot, chunk.Path)
		if _, err := os.Stat(path); err != nil {
			return Result{}, fmt.Errorf("pipeline: audio chunk %q: %w", path, err)
		}
		chunkTranscript, err := transcriber.Transcribe(ctx, path, "")
		if err != nil {
			return Result{}, fmt.Errorf("pipeline: transcribe %q: %w", path, err)
		}
		key := "SPEAKER_" + chunk.UserID
		if chunk.UserID == manifest.OwnerUserID {
			key = "ALEXANDER"
		}
		perSpeaker[key] = append(perSpeaker[key], chunkTranscript)
		perSpeakerOffsets[key] = append(perSpeakerOffsets[key], float64(chunk.StartMS)/1000)
	}
	merged := transcript.Transcript{Schema: transcript.Schema, Speakers: make([]transcript.Speaker, 0)}
	speakerKeys := make([]string, 0, len(perSpeaker))
	for speakerKey := range perSpeaker {
		speakerKeys = append(speakerKeys, speakerKey)
	}
	sort.Strings(speakerKeys)
	for _, speakerKey := range speakerKeys {
		chunks := perSpeaker[speakerKey]
		discordID := ""
		if speakerKey != "ALEXANDER" {
			discordID = speakerKey[len("SPEAKER_"):]
		} else {
			discordID = manifest.OwnerUserID
		}
		part, err := transcript.Merge(chunks, perSpeakerOffsets[speakerKey], speakerKey, discordID)
		if err != nil {
			return Result{}, err
		}
		merged.Speakers = append(merged.Speakers, part.Speakers...)
		merged.Segments = append(merged.Segments, part.Segments...)
		if merged.Language == "" {
			merged.Language = part.Language
		}
	}
	sort.SliceStable(merged.Segments, func(i, j int) bool {
		return merged.Segments[i].StartSeconds < merged.Segments[j].StartSeconds
	})
	mergedText := ""
	for index := range merged.Segments {
		merged.Segments[index].TurnID = fmt.Sprintf("T%06d", index+1)
		segment := merged.Segments[index]
		if mergedText != "" {
			mergedText += " "
		}
		mergedText += segment.Text
	}
	merged.Text = mergedText
	resultSummary, err := summarizer.Summarize(ctx, merged)
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: summarize: %w", err)
	}
	if err := writeJSON(filepath.Join(meetingRoot, "transcript.json"), merged); err != nil {
		return Result{}, err
	}
	if err := writeJSON(filepath.Join(meetingRoot, "summary.json"), resultSummary); err != nil {
		return Result{}, err
	}
	return Result{Transcript: merged, Summary: resultSummary}, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("pipeline: encode %q: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("pipeline: write %q: %w", path, err)
	}
	return nil
}
