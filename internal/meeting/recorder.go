package meeting

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultSampleRate = 48000
	DefaultChannels   = 2
)

type FLACEncoder interface {
	Encode(ctx context.Context, samples []int16, sampleRate, channels int, outputPath string) error
}

type PCMFrame struct {
	UserID string
	PCM    []int16
}

type Recorder struct {
	mu          sync.Mutex
	root        string
	chunkFrames int64
	startedAt   time.Time
	manifest    Manifest
	encoder     FLACEncoder
	users       map[string]*userBuffer
}

type userBuffer struct {
	samples  []int16
	startMS  int64
	frames   int64
	chunkSeq int
}

func NewRecorder(root string, meetingID, guildID, channelID, ownerUserID string, startedAt time.Time, chunkDuration time.Duration, encoder FLACEncoder) (*Recorder, error) {
	if root == "" || meetingID == "" {
		return nil, fmt.Errorf("recorder: root and meeting ID are required")
	}
	if chunkDuration <= 0 {
		return nil, fmt.Errorf("recorder: chunk duration must be positive")
	}
	if encoder == nil {
		return nil, fmt.Errorf("recorder: FLAC encoder is required")
	}
	frames := int64(chunkDuration / (20 * time.Millisecond))
	if frames < 1 {
		return nil, fmt.Errorf("recorder: chunk duration is shorter than one audio frame")
	}
	return &Recorder{
		root:        root,
		chunkFrames: frames,
		startedAt:   startedAt.UTC(),
		manifest:    NewManifest(meetingID, guildID, channelID, ownerUserID, startedAt),
		encoder:     encoder,
		users:       make(map[string]*userBuffer),
	}, nil
}

func (r *Recorder) ReceivePCMFrame(ctx context.Context, frame PCMFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if frame.UserID == "" || len(frame.PCM) == 0 {
		return fmt.Errorf("recorder: user ID and PCM samples are required")
	}
	if !validUserID(frame.UserID) {
		return fmt.Errorf("recorder: invalid Discord user ID %q", frame.UserID)
	}
	user := r.users[frame.UserID]
	if user == nil {
		user = &userBuffer{startMS: r.elapsedMS()}
		r.users[frame.UserID] = user
	}
	user.samples = append(user.samples, frame.PCM...)
	user.frames++
	if user.frames < r.chunkFrames {
		return nil
	}
	return r.flushUser(ctx, frame.UserID, user)
}

func (r *Recorder) Stop(ctx context.Context, endedAt time.Time) (Manifest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for userID, user := range r.users {
		if len(user.samples) == 0 {
			continue
		}
		if err := r.flushUser(ctx, userID, user); err != nil {
			return Manifest{}, err
		}
	}
	r.manifest.EndedAt = endedAt.UTC()
	path := filepath.Join(r.root, r.manifest.MeetingID, "meeting.json")
	if err := Save(path, r.manifest); err != nil {
		return Manifest{}, err
	}
	return r.manifest, nil
}

func (r *Recorder) Manifest() Manifest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifest
}

func (r *Recorder) elapsedMS() int64 {
	return time.Since(r.startedAt).Milliseconds()
}

func (r *Recorder) flushUser(ctx context.Context, userID string, user *userBuffer) error {
	if len(user.samples) == 0 {
		return nil
	}
	startMS := user.startMS
	frameCount := user.frames
	endMS := startMS + frameCount*20
	userDir := filepath.Join(r.root, r.manifest.MeetingID, "audio", userID)
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return fmt.Errorf("recorder: create chunk directory: %w", err)
	}
	filename := fmt.Sprintf("%06d-%012d-%012d.flac", user.chunkSeq, startMS, endMS)
	absolutePath := filepath.Join(userDir, filename)
	if err := r.encoder.Encode(ctx, user.samples, DefaultSampleRate, DefaultChannels, absolutePath); err != nil {
		return fmt.Errorf("recorder: encode user %s chunk %d: %w", userID, user.chunkSeq, err)
	}
	digest, err := FileSHA256(absolutePath)
	if err != nil {
		return fmt.Errorf("recorder: hash user %s chunk %d: %w", userID, user.chunkSeq, err)
	}
	relativePath, err := filepath.Rel(filepath.Join(r.root, r.manifest.MeetingID), absolutePath)
	if err != nil {
		return fmt.Errorf("recorder: make relative chunk path: %w", err)
	}
	r.manifest.AddChunk(Chunk{
		UserID:     userID,
		StartMS:    startMS,
		EndMS:      endMS,
		SampleRate: DefaultSampleRate,
		Channels:   DefaultChannels,
		Path:       filepath.ToSlash(relativePath),
		SHA256:     digest,
	})
	user.samples = user.samples[:0]
	user.frames = 0
	user.startMS = endMS
	user.chunkSeq++
	return nil
}
