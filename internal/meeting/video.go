package meeting

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type VideoConfig struct {
	Enabled    bool
	Executable string
	Args       []string
}

type VideoRecorder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	log    *os.File
	done   chan error
	output string
}

func StartVideo(config VideoConfig, meetingRoot string) (*VideoRecorder, error) {
	if !config.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(config.Executable) == "" || len(config.Args) == 0 {
		return nil, fmt.Errorf("video recorder: executable and arguments are required")
	}
	if meetingRoot == "" {
		return nil, fmt.Errorf("video recorder: meeting root is required")
	}
	if err := os.MkdirAll(meetingRoot, 0o755); err != nil {
		return nil, fmt.Errorf("video recorder: create meeting root: %w", err)
	}
	output := filepath.Join(meetingRoot, "video.mp4")
	args := expandVideoArgs(config.Args, output, meetingRoot)
	logFile, err := os.OpenFile(filepath.Join(meetingRoot, "video.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("video recorder: create log: %w", err)
	}
	cmd := exec.Command(config.Executable, args...)
	cmd.Env = append(os.Environ(), "AGENT_OWNER=discord-meeting-bot", "AGENT_TASK=meeting-video")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("video recorder: create stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("video recorder: start %q: %w", config.Executable, err)
	}
	recorder := &VideoRecorder{cmd: cmd, stdin: stdin, log: logFile, done: make(chan error, 1), output: output}
	go func() {
		recorder.done <- cmd.Wait()
	}()
	return recorder, nil
}

func (r *VideoRecorder) OutputPath() string {
	if r == nil {
		return ""
	}
	return r.output
}

func (r *VideoRecorder) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.stdin != nil {
		_, _ = io.WriteString(r.stdin, "q\n")
		_ = r.stdin.Close()
	}
	select {
	case err := <-r.done:
		_ = r.log.Close()
		if err != nil {
			return fmt.Errorf("video recorder: FFmpeg exited: %w; see %s", err, filepath.Join(filepath.Dir(r.output), "video.log"))
		}
		return nil
	case <-ctx.Done():
		_ = r.cmd.Process.Kill()
		err := <-r.done
		_ = r.log.Close()
		if err == nil {
			err = ctx.Err()
		}
		return fmt.Errorf("video recorder: stop: %w; see %s", err, filepath.Join(filepath.Dir(r.output), "video.log"))
	}
}

func expandVideoArgs(args []string, output, meetingRoot string) []string {
	replacer := strings.NewReplacer("{output}", output, "{meeting_root}", meetingRoot)
	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = replacer.Replace(arg)
	}
	return result
}
