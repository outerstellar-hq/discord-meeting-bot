//go:build !windows

package transcribe

import "fmt"

func newCrispASR(Config) (Transcriber, error) {
	return nil, fmt.Errorf("transcriber crispasr: the ainotebook native CrispASR ABI adapter is currently implemented for Windows only")
}
