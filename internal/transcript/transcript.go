package transcript

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
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
	Start      json.RawMessage `json:"start"`
	End        json.RawMessage `json:"end"`
	StartMS    json.RawMessage `json:"start_ms"`
	EndMS      json.RawMessage `json:"end_ms"`
	Text       string          `json:"text"`
	Words      []whisperWord   `json:"words"`
	Timestamps *whisperTimes   `json:"timestamps"`
	Offsets    *whisperOffsets `json:"offsets"`
}

type whisperWord struct {
	Start json.RawMessage `json:"start"`
	End   json.RawMessage `json:"end"`
	Word  string          `json:"word"`
}

type whisperTimes struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type whisperOffsets struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
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
			start, err := parseTimestamp(word.Start)
			if err != nil {
				return Transcript{}, fmt.Errorf("transcript: decode word start: %w", err)
			}
			end, err := parseTimestamp(word.End)
			if err != nil {
				return Transcript{}, fmt.Errorf("transcript: decode word end: %w", err)
			}
			words = append(words, Word{StartSeconds: start, EndSeconds: end, Text: strings.TrimSpace(word.Word)})
		}
		start, end, err := segmentTimes(whisper)
		if err != nil {
			return Transcript{}, err
		}
		result.Segments = append(result.Segments, Segment{
			StartSeconds: start,
			EndSeconds:   end,
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

func segmentTimes(segment whisperSegment) (float64, float64, error) {
	if segment.Timestamps != nil {
		start, err := parseClock(segment.Timestamps.From)
		if err != nil {
			return 0, 0, fmt.Errorf("transcript: decode segment start timestamp: %w", err)
		}
		end, err := parseClock(segment.Timestamps.To)
		if err != nil {
			return 0, 0, fmt.Errorf("transcript: decode segment end timestamp: %w", err)
		}
		return start, end, nil
	}
	if segment.Offsets != nil {
		return segment.Offsets.From / 1000, segment.Offsets.To / 1000, nil
	}
	if len(segment.StartMS) > 0 || len(segment.EndMS) > 0 {
		start, err := parseTimestamp(segment.StartMS)
		if err != nil {
			return 0, 0, fmt.Errorf("transcript: decode segment start_ms: %w", err)
		}
		end, err := parseTimestamp(segment.EndMS)
		if err != nil {
			return 0, 0, fmt.Errorf("transcript: decode segment end_ms: %w", err)
		}
		return start / 1000, end / 1000, nil
	}
	start, err := parseTimestamp(segment.Start)
	if err != nil {
		return 0, 0, fmt.Errorf("transcript: decode segment start: %w", err)
	}
	end, err := parseTimestamp(segment.End)
	if err != nil {
		return 0, 0, fmt.Errorf("transcript: decode segment end: %w", err)
	}
	return start, end, nil
}

func parseTimestamp(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return parseClock(text)
}

func parseClock(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		return number, nil
	}
	parsed, err := time.Parse("15:04:05.999", value)
	if err != nil {
		parsed, err = time.Parse("15:04:05.000", value)
	}
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q", value)
	}
	d := parsed.Sub(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC))
	return d.Seconds(), nil
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
