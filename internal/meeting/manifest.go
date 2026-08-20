package meeting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ManifestSchema = "starline.discord-meeting.manifest.v1"

type Chunk struct {
	UserID     string `json:"user_id"`
	StartMS    int64  `json:"start_ms"`
	EndMS      int64  `json:"end_ms"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
}

type Manifest struct {
	Schema      string    `json:"schema"`
	MeetingID   string    `json:"meeting_id"`
	GuildID     string    `json:"guild_id"`
	ChannelID   string    `json:"channel_id"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	OwnerUserID string    `json:"owner_user_id"`
	Chunks      []Chunk   `json:"chunks"`
}

func NewManifest(meetingID, guildID, channelID, ownerUserID string, startedAt time.Time) Manifest {
	return Manifest{
		Schema:      ManifestSchema,
		MeetingID:   meetingID,
		GuildID:     guildID,
		ChannelID:   channelID,
		OwnerUserID: ownerUserID,
		StartedAt:   startedAt.UTC(),
		Chunks:      make([]Chunk, 0),
	}
}

func (m *Manifest) AddChunk(chunk Chunk) {
	m.Chunks = append(m.Chunks, chunk)
	sort.Slice(m.Chunks, func(i, j int) bool {
		if m.Chunks[i].StartMS != m.Chunks[j].StartMS {
			return m.Chunks[i].StartMS < m.Chunks[j].StartMS
		}
		return m.Chunks[i].UserID < m.Chunks[j].UserID
	})
}

func (m Manifest) Validate() error {
	if m.Schema != ManifestSchema {
		return fmt.Errorf("meeting manifest: unsupported schema %q", m.Schema)
	}
	if strings.TrimSpace(m.MeetingID) == "" {
		return fmt.Errorf("meeting manifest: meeting_id is required")
	}
	if m.StartedAt.IsZero() || m.EndedAt.IsZero() || m.EndedAt.Before(m.StartedAt) {
		return fmt.Errorf("meeting manifest: invalid meeting timestamps")
	}
	for _, chunk := range m.Chunks {
		if !validUserID(chunk.UserID) {
			return fmt.Errorf("meeting manifest: invalid user_id %q", chunk.UserID)
		}
		if chunk.StartMS < 0 || chunk.EndMS <= chunk.StartMS {
			return fmt.Errorf("meeting manifest: invalid chunk interval for %q", chunk.UserID)
		}
		if chunk.SampleRate <= 0 || chunk.Channels <= 0 {
			return fmt.Errorf("meeting manifest: invalid audio format for %q", chunk.UserID)
		}
		if filepath.IsAbs(chunk.Path) || strings.Contains(filepath.ToSlash(chunk.Path), "../") {
			return fmt.Errorf("meeting manifest: chunk path must be relative: %q", chunk.Path)
		}
		if len(chunk.SHA256) != sha256.Size*2 {
			return fmt.Errorf("meeting manifest: invalid sha256 for %q", chunk.Path)
		}
	}
	return nil
}

func Save(path string, manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("meeting manifest: encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("meeting manifest: create directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("meeting manifest: write temporary file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("meeting manifest: promote: %w", err)
	}
	return nil
}

func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func validUserID(userID string) bool {
	if userID == "" {
		return false
	}
	for _, r := range userID {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
