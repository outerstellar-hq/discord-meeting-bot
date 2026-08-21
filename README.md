# Discord meeting recorder

This is a standalone Go Discord bot package intended to become its own repository later.

It records each Discord participant separately, preserving Discord's user identity as the speaker label. At stop time it writes per-user FLAC chunks and a manifest. An explicitly selected transcription backend then processes each chunk, timestamps are merged into the canonical Starline transcript v2 shape, and a configured OpenAI-compatible endpoint produces `summary.json` with decisions, action items, and open questions.

Set `VIDEO_RECORDING_ENABLED=true` to capture the Windows desktop with FFmpeg while the bot records audio. The meeting directory then also contains `video.mp4` and `video.log`. The screen video intentionally has no duplicate system audio; the Discord per-speaker FLAC tracks remain the authoritative audio and transcription source.

The owner is labelled `ALEXANDER` from `DISCORD_OWNER_USER_ID`. Other participants remain `SPEAKER_<discord-id>`; this avoids pretending that an audio embedding guessed a person's name. Discord user identity is the primary speaker identification signal. A voiceprint check can be added later as a separate closed-gate verification step.

## Runtime requirements

- Go 1.26+
- FFmpeg available at the configured `FFMPEG_EXECUTABLE` path, with FLAC encoding enabled
- One explicitly selected transcription backend. The preferred quality profile is `crispasr` with a pinned Whisper large-v3 model, CUDA-enabled native runtime, and word timestamps. The bot also supports ainotebook-informed `onnx-whisper`, `onnx-sensevoice`, `onnx-gigaam`, `onnx-moonshine`, `onnx-parakeet-local`, `remote-whisper`, NVIDIA `parakeet`, OpenAI-compatible transcription, and an explicit command adapter.
- A private OpenAI-compatible chat-completions endpoint for the final summary
- A Discord bot with Guilds and Guild Voice States intents, plus permission to connect and speak in the configured voice channel

DisGo is configured with pure-Go DAVE support for Discord's current voice encryption requirement. The pure-Go Opus decoder keeps capture independent of Python and CGO.

## Run

From this directory, copy `.env.example` to a private environment and provide real values. Do not commit tokens or meeting audio. Then run:

```text
go run ./cmd/discord-meeting-bot
```

Use `/record`, then `/stop-recording`. The output layout is:

```text
<meeting-output-root>/<meeting-id>/
  meeting.json
  audio/<discord-user-id>/*.flac
  transcript.json
  summary.json
```

Transcription and summary happen only after recording stops. The bot does not upload audio anywhere unless an HTTP backend is explicitly selected. Every finalized meeting includes `transcriber.json`, recording the chosen backend, endpoint, model identity, chunking, silence gate, and timestamp mode.

## Quality-oriented transcription

The backend names and model-directory variables mirror the transcription factory in `ainotebook`. There is no implicit Whisper fallback. For the highest-quality local path, configure `crispasr` with the same `ggml-large-v3` model and native CUDA runtime used by `ainotebook`. The Go bot calls the native `crispasr_session_*` ABI directly, requests native word timings, and fails startup if CUDA is unavailable unless `AINOTEBOOK_CRISPASR_ALLOW_CPU=true` explicitly authorizes CPU mode.

`remote-whisper` is only a selectable compatibility backend. It follows `ainotebook`'s 16 kHz mono, 30-second, RMS-silence-gated request shape, but it is not the default quality assumption. Set `TRANSCRIBER_RESPONSE_FORMAT=json` and `TRANSCRIBER_WORD_TIMESTAMPS=true` when the selected server supports Whisper/whisper.cpp timestamp fields. If the server cannot provide the configured response contract, finalization fails rather than silently degrading.

The repository module path is `github.com/outerstellar-hq/discord-meeting-bot`.

## Single-worker benchmark

To measure the real local model/runtime on a directory of WAV or FLAC speaker tracks, configure the CrispASR variables above and run:

```text
go run ./cmd/transcription-benchmark <speaker-track-directory>
```

The command processes files sequentially with one reusable CrispASR session and writes `transcription-benchmark.json` beside the input tracks. It reports total audio duration, wall time, real-time factor, and recognized text. Use a private representative meeting sample for quality evaluation; synthetic speech is suitable only for runtime smoke tests.
