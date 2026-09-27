#!/usr/bin/env bash
set -euo pipefail
if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew não encontrado. Instale em https://brew.sh e rode novamente." >&2
  exit 1
fi

# We intentionally do not require Homebrew core ffmpeg here: on current macOS
# bottles it may be built without libass. The fix script chooses a compatible
# ffmpeg and writes its absolute path into config.json.
brew install go whisper.cpp yt-dlp

mkdir -p models
MODEL=${WHISPER_MODEL:-large-v3-turbo}
URL="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${MODEL}.bin"
TARGET="models/ggml-${MODEL}.bin"
if [[ ! -f "$TARGET" ]]; then
  echo "Baixando Whisper ${MODEL}..."
  curl -L --fail --progress-bar "$URL" -o "$TARGET"
fi
cp -n config.example.json config.json 2>/dev/null || true
python3 - "$MODEL" <<'PYCFG'
import json, sys
model=sys.argv[1]
p="config.json"
with open(p, encoding="utf-8") as f: d=json.load(f)
d.setdefault("whisper", {})["model"] = f"./models/ggml-{model}.bin"
with open(p,"w",encoding="utf-8") as f: json.dump(d,f,ensure_ascii=False,indent=2); f.write("\n")
PYCFG

./scripts/fix-ffmpeg-macos.sh

cat <<'TXT'

Pronto.
Execute:
  ./bin/cenarius serve
Abra:
  http://127.0.0.1:8484

Opcional IA local (melhor plano editorial):
  brew install ollama
  ollama serve
  ollama pull qwen3:8b
  e mude ollama.enabled para true em config.json
TXT
