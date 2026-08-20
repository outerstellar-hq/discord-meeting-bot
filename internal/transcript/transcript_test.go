package transcript

import "testing"

func TestParseWhisperVerboseJSON(t *testing.T) {
	value, err := Parse([]byte(`{"language":"en","segments":[{"start":1.5,"end":3.0,"text":" hello ","words":[{"start":1.5,"end":2.0,"word":" hello"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if value.Schema != Schema || value.Text != "hello" {
		t.Fatalf("unexpected transcript: %#v", value)
	}
	if len(value.Segments) != 1 || value.Segments[0].StartSeconds != 1.5 {
		t.Fatalf("unexpected segments: %#v", value.Segments)
	}
}

func TestMergeAddsSpeakerAndOffset(t *testing.T) {
	value, err := Merge([]Transcript{{Language: "en", Segments: []Segment{{StartSeconds: 0, EndSeconds: 1, Text: "hello"}}}}, []float64{10}, "ALEXANDER", "123")
	if err != nil {
		t.Fatal(err)
	}
	if value.Segments[0].StartSeconds != 10 || value.Segments[0].SpeakerKey != "ALEXANDER" || value.Segments[0].TurnID != "T000001" {
		t.Fatalf("unexpected merged transcript: %#v", value)
	}
}
