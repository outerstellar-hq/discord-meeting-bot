# Discord meeting recorder

This is a standalone Go Discord bot package intended to become its own repository later.

It records each Discord participant separately, preserving Discord's user identity as the speaker label. At stop time it writes per-user FLAC chunks and a manifest. A configured non-Python transcription executable is then run over each chunk, timestamps are merged into the canonical Starline transcript v2 shape, and a configured OpenAI-compatible endpoint produces `summary.json` with decisions, action items, and open questions.

The owner is labelled `ALEXANDER` from `DISCORD_OWNER_USER_ID`. Other participants remain `SPEAKER_<discord-id>`; this avoids pretending that an audio embedding guessed a person's name. Discord user identity is the primary speaker identification signal. A voiceprint check can be added later as a separate closed-gate verification step.

## Runtime requirements

- Go 1.26+
- FFmpeg available at the configured `FFMPEG_EXECUTABLE` path, with FLAC encoding enabled
- A transcription executable that accepts the configured argument list and writes Whisper-verbose or Starline transcript JSON to stdout
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

Transcription and summary happen only after recording stops. The bot does not upload audio anywhere by itself.

The repository module path is `github.com/outerstellar-hq/discord-meeting-bot`.
