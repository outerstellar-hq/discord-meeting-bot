package transcript

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const Schema = "starline.captions.transcript.v2"

type Word struct {
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Text         string  `json:"text"`
}

type Segment struct {
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Text         string  `json:"text"`
	Words        []Word  `json:"words,omitempty"`
	SpeakerKey   string  `json:"speaker_key"`
	TurnID       string  `json:"turn_id"`
}

type Speaker struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	DiscordID  string `json:"discord_id,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type Transcript struct {
	Schema   string    `json:"schema"`
	Language string    `json:"language"`
	Text     string    `json:"text"`
	Speakers []Speaker `json:"speakers"`
	Segments []Segment `json:"segments"`
}

type whisperVerbose struct {
	Language string           `json:"language"`
	Text     string           `json:"text"`
	Segments []whisperSegment `json:"segments"`
}

type whisperSegment struct {
	Start float64       `json:"start"`
	End   float64       `json:"end"`
	Text  string        `json:"text"`
	Words []whisperWord `json:"words"`
}

type whisperWord struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Word  string  `json:"word"`
}

func Parse(data []byte) (Transcript, error) {
	var raw struct {
		Schema   string            `json:"schema"`
		Language string            `json:"language"`
		Text     string            `json:"text"`
		Speakers []Speaker         `json:"speakers"`
		Segments []json.RawMessage `json:"segments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Transcript{}, fmt.Errorf("transcript: decode JSON: %w", err)
	}
	if len(raw.Segments) == 0 {
		return Transcript{}, fmt.Errorf("transcript: segments are required")
	}
	result := Transcript{Schema: Schema, Language: raw.Language, Text: raw.Text, Speakers: raw.Speakers}
	for _, segmentData := range raw.Segments {
		var segment Segment
		if err := json.Unmarshal(segmentData, &segment); err == nil && segment.SpeakerKey != "" {
			if segment.TurnID == "" {
				segment.TurnID = fmt.Sprintf("T%06d", len(result.Segments)+1)
			}
			result.Segments = append(result.Segments, segment)
			continue
		}
		var whisper whisperSegment
		if err := json.Unmarshal(segmentData, &whisper); err != nil {
			return Transcript{}, fmt.Errorf("transcript: decode segment: %w", err)
		}
		words := make([]Word, 0, len(whisper.Words))
		for _, word := range whisper.Words {
			words = append(words, Word{StartSeconds: word.Start, EndSeconds: word.End, Text: strings.TrimSpace(word.Word)})
		}
		result.Segments = append(result.Segments, Segment{
			StartSeconds: whisper.Start,
			EndSeconds:   whisper.End,
			Text:         strings.TrimSpace(whisper.Text),
			Words:        words,
		})
	}
	if result.Text == "" {
		var parts []string
		for _, segment := range result.Segments {
			if text := strings.TrimSpace(segment.Text); text != "" {
				parts = append(parts, text)
			}
		}
		result.Text = strings.Join(parts, " ")
	}
	return result, nil
}

func Merge(chunks []Transcript, offsets []float64, speakerKey, discordID string) (Transcript, error) {
	if len(chunks) != len(offsets) {
		return Transcript{}, fmt.Errorf("transcript: chunk and offset counts differ")
	}
	result := Transcript{Schema: Schema, Speakers: []Speaker{{Key: speakerKey, Name: speakerKey, DiscordID: discordID}}}
	for i, chunk := range chunks {
		if chunk.Language != "" && result.Language == "" {
			result.Language = chunk.Language
		}
		for _, segment := range chunk.Segments {
			segment.StartSeconds += offsets[i]
			segment.EndSeconds += offsets[i]
			segment.SpeakerKey = speakerKey
			segment.TurnID = fmt.Sprintf("T%06d", len(result.Segments)+1)
			for wordIndex := range segment.Words {
				segment.Words[wordIndex].StartSeconds += offsets[i]
				segment.Words[wordIndex].EndSeconds += offsets[i]
			}
			result.Segments = append(result.Segments, segment)
		}
	}
	sort.SliceStable(result.Segments, func(i, j int) bool { return result.Segments[i].StartSeconds < result.Segments[j].StartSeconds })
	var parts []string
	for _, segment := range result.Segments {
		if text := strings.TrimSpace(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	result.Text = strings.Join(parts, " ")
	return result, nil
}
