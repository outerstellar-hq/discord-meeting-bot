//go:build windows

package transcribe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/outerstellar-hq/discord-meeting-bot/internal/transcript"
	"golang.org/x/sys/windows"
)

type crispASR struct {
	modelPath             string
	nativeDir             string
	backend               string
	language              string
	threads               int
	requireGPU            bool
	ffmpegExecutable      string
	chunkSeconds          int
	silenceRMS            float64
	sessionOpen           *windows.LazyProc
	sessionOpenLang       *windows.LazyProc
	sessionClose          *windows.LazyProc
	sessionTranscribe     *windows.LazyProc
	sessionTranscribeLang *windows.LazyProc
	resultFree            *windows.LazyProc
	resultSegments        *windows.LazyProc
	segmentText           *windows.LazyProc
	segmentStart          *windows.LazyProc
	segmentEnd            *windows.LazyProc
	segmentWords          *windows.LazyProc
	wordText              *windows.LazyProc
	wordStart             *windows.LazyProc
	wordEnd               *windows.LazyProc
	systemInfo            *windows.LazyProc
	sessionMu             sync.Mutex
	session               uintptr
}

func newCrispASR(config Config) (Transcriber, error) {
	if strings.TrimSpace(config.ModelPath) == "" {
		return nil, fmt.Errorf("transcriber crispasr: model path is required")
	}
	if _, err := os.Stat(config.ModelPath); err != nil {
		return nil, fmt.Errorf("transcriber crispasr: model path %q: %w", config.ModelPath, err)
	}
	if strings.TrimSpace(config.FFmpegExecutable) == "" {
		return nil, fmt.Errorf("transcriber crispasr: FFmpeg executable is required")
	}
	if config.ChunkSeconds <= 0 {
		config.ChunkSeconds = 30
	}
	if config.Threads <= 0 {
		config.Threads = 4
	}
	nativeDir := strings.TrimSpace(config.NativeDir)
	if nativeDir == "" {
		nativeDir = strings.TrimSpace(os.Getenv("AINOTEBOOK_CRISPASR_NATIVE_DIR"))
	}
	if nativeDir == "" {
		return nil, fmt.Errorf("transcriber crispasr: AINOTEBOOK_CRISPASR_NATIVE_DIR is required so the native ABI is pinned")
	}
	if config.RequireGPU {
		if _, err := os.Stat(filepath.Join(nativeDir, "ggml-cuda.dll")); err != nil {
			return nil, fmt.Errorf("transcriber crispasr: CUDA native backend is required but ggml-cuda.dll is unavailable in %q; explicitly set CRISPASR_ALLOW_CPU=true to authorize CPU", nativeDir)
		}
	}
	if err := windows.SetDllDirectory(nativeDir); err != nil {
		return nil, fmt.Errorf("transcriber crispasr: set native DLL directory %q: %w", nativeDir, err)
	}
	for _, name := range []string{"ggml-base.dll", "ggml-cpu.dll", "ggml.dll", "whisper.dll"} {
		if err := windows.NewLazyDLL(filepath.Join(nativeDir, name)).Load(); err != nil {
			return nil, fmt.Errorf("transcriber crispasr: load %s: %w", name, err)
		}
	}
	if config.RequireGPU {
		if err := windows.NewLazyDLL(filepath.Join(nativeDir, "ggml-cuda.dll")).Load(); err != nil {
			return nil, fmt.Errorf("transcriber crispasr: load CUDA backend: %w", err)
		}
	}
	lib := windows.NewLazyDLL(filepath.Join(nativeDir, "crispasr.dll"))
	if err := lib.Load(); err != nil {
		return nil, fmt.Errorf("transcriber crispasr: load crispasr.dll: %w", err)
	}
	get := func(name string) *windows.LazyProc { return lib.NewProc(name) }
	result := &crispASR{modelPath: config.ModelPath, nativeDir: nativeDir, backend: config.NativeBackend, language: config.Language, threads: config.Threads, requireGPU: config.RequireGPU, ffmpegExecutable: config.FFmpegExecutable, chunkSeconds: config.ChunkSeconds, silenceRMS: config.SilenceRMS,
		sessionOpen: get("crispasr_session_open"), sessionOpenLang: get("crispasr_session_open_explicit"), sessionClose: get("crispasr_session_close"), sessionTranscribe: get("crispasr_session_transcribe"), sessionTranscribeLang: get("crispasr_session_transcribe_lang"), resultFree: get("crispasr_session_result_free"), resultSegments: get("crispasr_session_result_n_segments"), segmentText: get("crispasr_session_result_segment_text"), segmentStart: get("crispasr_session_result_segment_t0"), segmentEnd: get("crispasr_session_result_segment_t1"), segmentWords: get("crispasr_session_result_n_words"), wordText: get("crispasr_session_result_word_text"), wordStart: get("crispasr_session_result_word_t0"), wordEnd: get("crispasr_session_result_word_t1"), systemInfo: get("whisper_print_system_info")}
	if config.RequireGPU {
		info, _, _ := result.systemInfo.Call()
		if !strings.Contains(strings.ToUpper(cString(info)), "CUDA") {
			return nil, fmt.Errorf("transcriber crispasr: native runtime did not report CUDA; CPU is not authorized")
		}
	}
	return result, nil
}

func (c *crispASR) Description() map[string]any {
	return map[string]any{"backend": "crispasr", "model_path": c.modelPath, "native_dir": c.nativeDir, "native_backend": c.backend, "language": c.language, "threads": c.threads, "require_gpu": c.requireGPU, "chunk_seconds": c.chunkSeconds, "silence_rms": c.silenceRMS, "word_timestamps": "native-when-available"}
}

func (c *crispASR) Transcribe(ctx context.Context, inputPath, language string) (transcript.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return transcript.Transcript{}, err
	}
	if strings.TrimSpace(language) == "" {
		language = c.language
	}
	samples, err := loadPCM16(ctx, c.ffmpegExecutable, inputPath)
	if err != nil {
		return transcript.Transcript{}, err
	}
	modelPtr, err := cStringPtr(c.modelPath)
	if err != nil {
		return transcript.Transcript{}, err
	}
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.session == 0 {
		if c.backend == "" {
			c.session, _, _ = c.sessionOpen.Call(modelPtr, uintptr(c.threads))
		} else {
			backendPtr, backendErr := cStringPtr(c.backend)
			if backendErr != nil {
				return transcript.Transcript{}, backendErr
			}
			c.session, _, _ = c.sessionOpenLang.Call(modelPtr, backendPtr, uintptr(c.threads))
		}
	}
	if c.session == 0 {
		return transcript.Transcript{}, fmt.Errorf("transcriber crispasr: failed to open model session")
	}
	session := c.session

	result := transcript.Transcript{Schema: transcript.Schema, Language: language}
	for _, chunk := range splitPCM(samples, c.chunkSeconds) {
		if err := ctx.Err(); err != nil {
			return transcript.Transcript{}, err
		}
		if isSilent(chunk.Samples, c.silenceRMS) {
			continue
		}
		if len(chunk.Samples) == 0 {
			continue
		}
		floatSamples := make([]float32, len(chunk.Samples))
		for i, sample := range chunk.Samples {
			floatSamples[i] = float32(sample) / 32768
		}
		var nativeResult uintptr
		if language == "" {
			nativeResult, _, _ = c.sessionTranscribe.Call(session, uintptr(unsafe.Pointer(&floatSamples[0])), uintptr(len(floatSamples)))
		} else {
			languagePtr, languageErr := cStringPtr(language)
			if languageErr != nil {
				return transcript.Transcript{}, languageErr
			}
			nativeResult, _, _ = c.sessionTranscribeLang.Call(session, uintptr(unsafe.Pointer(&floatSamples[0])), uintptr(len(floatSamples)), languagePtr)
		}
		runtime.KeepAlive(floatSamples)
		if nativeResult == 0 {
			return transcript.Transcript{}, fmt.Errorf("transcriber crispasr: session transcription failed at %.2fs", chunk.StartSeconds)
		}
		parsed, parseErr := c.readResult(nativeResult, chunk.StartSeconds, chunk.EndSeconds)
		c.resultFree.Call(nativeResult)
		if parseErr != nil {
			return transcript.Transcript{}, parseErr
		}
		result.Segments = append(result.Segments, parsed...)
	}
	result.Text = joinSegments(result.Segments)
	return result, nil
}

func (c *crispASR) readResult(result uintptr, offset, chunkEnd float64) ([]transcript.Segment, error) {
	// The native API reports timestamps in centiseconds, matching ainotebook's JNA adapter.
	ptr := uintptr(result)
	count, _, _ := c.resultSegments.Call(ptr)
	segments := make([]transcript.Segment, 0, count)
	for i := uintptr(0); i < count; i++ {
		text := strings.TrimSpace(cString(callPtr(c.segmentText, ptr, i)))
		if text == "" {
			continue
		}
		start := float64(int64(callUint(c.segmentStart, ptr, i)))/100 + offset
		end := float64(int64(callUint(c.segmentEnd, ptr, i)))/100 + offset
		if end <= start {
			end = chunkEnd
		}
		segment := transcript.Segment{StartSeconds: start, EndSeconds: end, Text: text}
		wordCount := callUint(c.segmentWords, ptr, i)
		for wordIndex := uintptr(0); wordIndex < wordCount; wordIndex++ {
			word := strings.TrimSpace(cString(callPtr(c.wordText, ptr, i, wordIndex)))
			if word == "" {
				continue
			}
			wordStart := float64(int64(callUint(c.wordStart, ptr, i, wordIndex)))/100 + offset
			wordEnd := float64(int64(callUint(c.wordEnd, ptr, i, wordIndex)))/100 + offset
			if wordEnd < wordStart {
				wordEnd = wordStart
			}
			segment.Words = append(segment.Words, transcript.Word{StartSeconds: wordStart, EndSeconds: wordEnd, Text: word})
		}
		// CrispASR versions/models may return segment timestamps without word
		// entries. Keep the segment rather than discarding valid transcription;
		// native word entries are preserved whenever the ABI provides them.
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("transcriber crispasr: native result returned no timestamped speech segments")
	}
	return segments, nil
}

func cStringPtr(value string) (uintptr, error) {
	ptr, err := windows.BytePtrFromString(value)
	if err != nil {
		return 0, fmt.Errorf("transcriber crispasr: invalid native string %q: %w", value, err)
	}
	return uintptr(unsafe.Pointer(ptr)), nil
}

func cString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	length, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("lstrlenA").Call(ptr)
	if length == 0 {
		return ""
	}
	bytes := make([]byte, length)
	windows.NewLazySystemDLL("kernel32.dll").NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&bytes[0])), ptr, length)
	return string(bytes)
}

func callUint(proc *windows.LazyProc, args ...uintptr) uintptr {
	value, _, _ := proc.Call(args...)
	return value
}

func callPtr(proc *windows.LazyProc, args ...uintptr) uintptr { return callUint(proc, args...) }
