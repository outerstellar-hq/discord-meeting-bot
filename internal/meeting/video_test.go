package meeting

import (
	"path/filepath"
	"testing"
)

func TestExpandVideoArgs(t *testing.T) {
	args := expandVideoArgs([]string{"-i", "desktop", "{output}", "{meeting_root}"}, `C:\meetings\one\video.mp4`, `C:\meetings\one`)
	if args[2] != filepath.Join(`C:\meetings\one`, "video.mp4") || args[3] != `C:\meetings\one` {
		t.Fatalf("unexpected expanded args: %#v", args)
	}
}
