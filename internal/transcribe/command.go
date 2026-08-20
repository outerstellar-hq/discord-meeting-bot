package transcribe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
)

type Command struct {
	Executable string
	Args       []string
}

func (c Command) Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error) {
	if c.Executable == "" {
		return transcript.Transcript{}, fmt.Errorf("transcriber: executable is required")
	}
	args := make([]string, len(c.Args))
	for i, arg := range c.Args {
		args[i] = strings.NewReplacer("{input}", inputPath, "{language}", language).Replace(arg)
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
