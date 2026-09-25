# Test status

Validated in the build environment:

- `go test ./...` passes.
- Go binary builds successfully.
- End-to-end render was exercised against one of the supplied 28.6s reference MP4s using a deterministic fake Whisper transcript.
- Result was 1080x1920 at 30 fps with burned ASS captions, smart zooms and normalized audio.
- Local B-roll overlay path was also exercised with a generated PNG asset.
- A hang found during B-roll testing was fixed by stopping the output at the finite A/V stream (`-shortest`).

Not validated in this environment:

- Real Whisper inference/model download, because the container cannot resolve external hosts. The project uses the current `whisper-cli -ojf` JSON interface and the macOS bootstrap installs `whisper.cpp` from Homebrew and downloads a GGML model locally.
- Ollama planning; it is optional and falls back to the heuristic planner.

Run `make smoke` locally to validate FFmpeg + Go without waiting for Whisper inference. Then run `./bin/cenarius doctor` and a real upload.
