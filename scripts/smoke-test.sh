#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$ROOT/.smoke"
rm -rf "$TMP" && mkdir -p "$TMP/bin" "$TMP/models" "$TMP/out"
command -v ffmpeg >/dev/null
command -v ffprobe >/dev/null

# Prefer a FFmpeg build with ASS/libass. Homebrew's regular ffmpeg can be
# installed without it, while ffmpeg-full exposes the subtitle filter.
FFMPEG_BIN="${FFMPEG_BIN:-$(command -v ffmpeg)}"
FFPROBE_BIN="${FFPROBE_BIN:-$(command -v ffprobe)}"
if ! "$FFMPEG_BIN" -hide_banner -filters 2>/dev/null | awk '$2 == "ass" { found=1 } END { exit(found ? 0 : 1) }'; then
  if command -v brew >/dev/null 2>&1 && brew --prefix ffmpeg-full >/dev/null 2>&1; then
    FFMPEG_BIN="$(brew --prefix ffmpeg-full)/bin/ffmpeg"
    FFPROBE_BIN="$(brew --prefix ffmpeg-full)/bin/ffprobe"
  fi
fi
if ! "$FFMPEG_BIN" -hide_banner -filters 2>/dev/null | awk '$2 == "ass" { found=1 } END { exit(found ? 0 : 1) }'; then
  echo "ERRO: nenhum FFmpeg com ASS/libass foi encontrado." >&2
  echo "No macOS rode: ./scripts/fix-ffmpeg-macos.sh" >&2
  exit 1
fi

# 6s synthetic portrait source. The fake Whisper below validates the entire editor/render pipeline.
"$FFMPEG_BIN" -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc2=size=720x1280:rate=30:duration=6" \
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=6" \
  -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$TMP/input.mp4"

cat > "$TMP/bin/whisper-cli" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
out=""
while (($#)); do case "$1" in -of|--output-file) out="$2"; shift 2;; *) shift;; esac; done
cat > "${out}.json" <<'JSON'
{"result":{"language":"pt"},"transcription":[
{"offsets":{"from":300,"to":2600},"text":"Tecnologia sem hype.","tokens":[{"text":"Tecnologia","offsets":{"from":300,"to":1200}},{"text":"sem","offsets":{"from":1200,"to":1600}},{"text":"hype.","offsets":{"from":1600,"to":2600}}]},
{"offsets":{"from":3000,"to":5200},"text":"Este é o CENARIUS.","tokens":[{"text":"Este","offsets":{"from":3000,"to":3500}},{"text":"é","offsets":{"from":3500,"to":3800}},{"text":"o","offsets":{"from":3800,"to":4000}},{"text":"CENARIUS.","offsets":{"from":4000,"to":5200}}]}
]}
JSON
STUB
chmod +x "$TMP/bin/whisper-cli"
touch "$TMP/models/fake.bin"
cat > "$TMP/config.json" <<JSON
{
  "work_dir": "$TMP/work",
  "ffmpeg": "$FFMPEG_BIN",
  "ffprobe": "$FFPROBE_BIN",
  "whisper": {"binary":"$TMP/bin/whisper-cli","model":"$TMP/models/fake.bin","language":"pt","threads":0},
  "ollama": {"enabled":false,"url":"http://127.0.0.1:11434","model":"qwen3:8b"},
  "output": {"width":1080,"height":1920,"fps":30,"crf":18,"preset":"medium","audio_bitrate":"192k"},
  "cuts": {"enabled":false,"noise_db":-35,"min_silence":0.85,"keep_silence":0.18,"min_keep_segment":0.15},
  "captions": {"enabled":true,"font_name":"Arial","font_size":72,"primary_color":"&H00FFFFFF","highlight_color":"&H0000D7FF","outline_color":"&H00000000","outline":5,"shadow":1,"margin_v":420,"max_words":5,"uppercase":true},
  "zoom": {"enabled":true,"mild":1.055,"punch":1.10,"min_gap":3.0,"duration":1.2},
  "broll": {"enabled":false,"asset_dir":"$ROOT/assets/broll","manifest":"$ROOT/assets/manifest.json","max_events":5}
}
JSON
mkdir -p "$ROOT/bin"
go build -o "$ROOT/bin/cenarius" "$ROOT/cmd/cenarius"
"$ROOT/bin/cenarius" edit -config "$TMP/config.json" -i "$TMP/input.mp4" -o "$TMP/out"
"$FFPROBE_BIN" -v error -select_streams v:0 -show_entries stream=width,height,r_frame_rate -of default=nw=1 "$TMP/out/final.mp4"
echo "SMOKE OK: $TMP/out/final.mp4"
