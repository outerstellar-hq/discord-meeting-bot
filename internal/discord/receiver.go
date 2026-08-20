package discord

import (
	"context"
	"fmt"
	"sync"

	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/outerstellar-hq/discord-meeting-bot/internal/meeting"
	"github.com/pion/opus"
)

type PCMReceiver struct {
	mu       sync.Mutex
	recorder *meeting.Recorder
	decoders map[snowflake.ID]*opus.Decoder
}

func NewPCMReceiver(recorder *meeting.Recorder) *PCMReceiver {
	return &PCMReceiver{recorder: recorder, decoders: make(map[snowflake.ID]*opus.Decoder)}
}

func (r *PCMReceiver) ReceiveOpusFrame(userID snowflake.ID, packet *voice.Packet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recorder == nil || packet == nil || len(packet.Opus) == 0 {
		return fmt.Errorf("discord receiver: recorder and opus packet are required")
	}
	decoder := r.decoders[userID]
	if decoder == nil {
		created, err := opus.NewDecoderWithOutput(meeting.DefaultSampleRate, meeting.DefaultChannels)
		if err != nil {
			return fmt.Errorf("discord receiver: create decoder for %s: %w", userID, err)
		}
		decoder = &created
		r.decoders[userID] = decoder
	}
	// Discord voice packets are normally 20 ms, but Opus permits up to 120 ms.
	pcm := make([]int16, meeting.DefaultSampleRate/1000*120*meeting.DefaultChannels)
	samplesPerChannel, err := decoder.DecodeToInt16(packet.Opus, pcm)
	if err != nil {
		return fmt.Errorf("discord receiver: decode user %s: %w", userID, err)
	}
	sampleCount := samplesPerChannel * meeting.DefaultChannels
	if sampleCount <= 0 || sampleCount > len(pcm) {
		return fmt.Errorf("discord receiver: decoder returned invalid sample count %d", sampleCount)
	}
	copyOfPCM := append([]int16(nil), pcm[:sampleCount]...)
	if err := r.recorder.ReceivePCMFrame(context.Background(), meeting.PCMFrame{UserID: userID.String(), PCM: copyOfPCM}); err != nil {
		return fmt.Errorf("discord receiver: record user %s: %w", userID, err)
	}
	return nil
}

func (r *PCMReceiver) CleanupUser(userID snowflake.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.decoders, userID)
}

func (r *PCMReceiver) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decoders = make(map[snowflake.ID]*opus.Decoder)
}
