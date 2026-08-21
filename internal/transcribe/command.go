package transcribe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type Command struct {
	Backend    string
	Executable string
	Args       []string
	ModelPath  string
	Language   string
}

func newCommand(config Config, backend string) (*Command, error) {
	if strings.TrimSpace(config.Executable) == "" || len(config.Args) == 0 {
		return nil, fmt.Errorf("transcriber %s: executable and arguments are required", backend)
	}
	if backend != "command" && strings.TrimSpace(config.ModelPath) == "" {
		return nil, fmt.Errorf("transcriber %s: model path is required", backend)
	}
	if config.ModelPath != "" {
		if _, err := os.Stat(config.ModelPath); err != nil {
			return nil, fmt.Errorf("transcriber %s: model path %q: %w", backend, config.ModelPath, err)
		}
	}
	return &Command{Backend: backend, Executable: config.Executable, Args: append([]string(nil), config.Args...), ModelPath: config.ModelPath, Language: config.Language}, nil
}

func (c Command) Description() map[string]any {
	return map[string]any{"backend": c.Backend, "executable": c.Executable, "model_path": filepath.Clean(c.ModelPath), "arguments": c.Args}
}

func (c Command) Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error) {
	if c.Executable == "" {
		return transcript.Transcript{}, fmt.Errorf("transcriber: executable is required")
	}
	if strings.TrimSpace(language) == "" {
		language = c.Language
	}
	args := make([]string, len(c.Args))
	for i, arg := range c.Args {
		args[i] = strings.NewReplacer("{input}", inputPath, "{language}", language, "{model}", c.ModelPath).Replace(arg)
	}
	cmd := exec.CommandContext(ctx, c.Executable, args...)
	cmd.Env = append(os.Environ(), "AGENT_OWNER=discord-meeting-bot", "AGENT_TASK=meeting-transcription")
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return transcript.Transcript{}, fmt.Errorf("transcriber: %q failed: %w: %s", c.Executable, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return transcript.Transcript{}, fmt.Errorf("transcriber: start %q: %w", c.Executable, err)
	}
	result, err := transcript.Parse(output)
	if err != nil {
		return transcript.Transcript{}, fmt.Errorf("transcriber: parse %q output: %w", c.Executable, err)
	}
	return result, nil
}
