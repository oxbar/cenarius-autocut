#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Este script é somente para macOS." >&2
  exit 1
fi
if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew não encontrado." >&2
  exit 1
fi

supports_ass() {
  local bin="$1"
  local filters
  [[ -x "$bin" ]] || command -v "$bin" >/dev/null 2>&1 || return 1

  # Do not use `ffmpeg ... | grep -q` here while `pipefail` is enabled.
  # grep -q exits as soon as it finds `ass`, which can make ffmpeg receive
  # SIGPIPE and turn a successful detection into a false negative.
  filters="$("$bin" -hide_banner -filters 2>/dev/null)" || return 1
  grep -Eq '(^|[[:space:]])ass([[:space:]]|$)' <<<"$filters"
}

CURRENT_FFMPEG="$(command -v ffmpeg || true)"
if [[ -n "$CURRENT_FFMPEG" ]] && supports_ass "$CURRENT_FFMPEG"; then
  FFMPEG_BIN="$CURRENT_FFMPEG"
  FFPROBE_BIN="$(command -v ffprobe)"
  echo "✓ FFmpeg atual já possui ASS/libass: $FFMPEG_BIN"
else
  echo "FFmpeg atual não possui o filtro ASS/libass. Instalando ffmpeg-full..."
  brew install ffmpeg-full
  PREFIX="$(brew --prefix ffmpeg-full)"
  FFMPEG_BIN="$PREFIX/bin/ffmpeg"
  FFPROBE_BIN="$PREFIX/bin/ffprobe"
  if ! supports_ass "$FFMPEG_BIN"; then
    echo "Erro: ffmpeg-full foi instalado, mas o filtro ASS não foi encontrado em $FFMPEG_BIN" >&2
    exit 1
  fi
  echo "✓ ffmpeg-full com ASS/libass: $FFMPEG_BIN"
fi

[[ -f config.json ]] || cp config.example.json config.json
# macOS sed. Use # delimiter so absolute paths with / are safe.
sed -i '' "s#\"ffmpeg\": \"[^\"]*\"#\"ffmpeg\": \"${FFMPEG_BIN}\"#" config.json
sed -i '' "s#\"ffprobe\": \"[^\"]*\"#\"ffprobe\": \"${FFPROBE_BIN}\"#" config.json

echo "✓ config.json atualizado"
go build -o bin/cenarius ./cmd/cenarius
./bin/cenarius doctor
