#!/usr/bin/env bash
# CENARIUS AutoCut v1.9 visual + audio + montagem smoke test.
# Runs the full pipeline (probe -> audio -> whisper stub -> semantic planner ->
# asset resolver -> render) offline and checks that the output really is a
# smart edit: A-roll + captions + zoom + video B-roll + Ken Burns still +
# procedural graphic + SFX, 1080x1920 30 fps H.264/AAC with unchanged duration.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$ROOT/.smoke"
rm -rf "$TMP" && mkdir -p "$TMP/bin" "$TMP/models" "$TMP/out" "$TMP/assets"
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
fail() { echo "SMOKE FALHOU: $*" >&2; exit 1; }

# Local MOVING B-roll (with an audio track that must NOT reach the output) and
# a local still image (must get Ken Burns). No network is used.
"$FFMPEG_BIN" -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc=size=1280x720:rate=30:duration=3" \
  -f lavfi -i "sine=frequency=1500:sample_rate=48000:duration=3" \
  -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$TMP/assets/technology.mp4"
"$FFMPEG_BIN" -hide_banner -loglevel error -y \
  -f lavfi -i "mandelbrot=size=1200x800:rate=1" -frames:v 1 "$TMP/assets/java.png"
# Local licence-safe music bed (20 s chord). The manifest lists every mood so
# whatever mood the planner detects resolves to this track (no network).
mkdir -p "$TMP/music"
"$FFMPEG_BIN" -hide_banner -loglevel error -y \
  -f lavfi -i "aevalsrc='0.2*sin(2*PI*220*t)+0.15*sin(2*PI*277*t)+0.12*sin(2*PI*330*t)':s=48000:d=20" \
  -c:a pcm_s16le "$TMP/music/bed.wav"
cat > "$TMP/music/manifest.json" <<JSON
{"tracks":[{"file":"bed.wav","title":"Smoke Bed","moods":["energetic","inspiring","tense","dramatic","chill","funny"],"license":"CC0 (smoke)","author":"CENARIUS"}]}
JSON
cat > "$TMP/manifest.json" <<JSON
{"assets":[
 {"keyword":"tecnologia","aliases":["tecnologias"],"concepts":["technology"],"file":"technology.mp4","license":"CC0 (smoke)"},
 {"keyword":"java","concepts":["java programming"],"file":"java.png","license":"CC0 (smoke)"}
]}
JSON

# 13.95 s portrait source at 24 fps. It is long enough to exercise three
# distinct visual events, a punchline zoom and SFX while keeping CI smoke
# fast. Output is 30 fps and the duration must not change.
"$FFMPEG_BIN" -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc2=size=720x1280:rate=24:duration=13.95" \
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=13.95" \
  -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$TMP/input.mp4"

cat > "$TMP/bin/whisper-cli" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
out=""
while (($#)); do case "$1" in -of|--output-file) out="$2"; shift 2;; *) shift;; esac; done
cat > "${out}.json" <<'JSON'
{"result": {"language": "pt"}, "transcription": [{"offsets": {"from": 300, "to": 2357}, "text": " Hoje a tecnologia mudou tudo.", "tokens": [{"text": " Hoje", "offsets": {"from": 300, "to": 677}}, {"text": " a", "offsets": {"from": 720, "to": 1097}}, {"text": " tecnologia", "offsets": {"from": 1140, "to": 1517}}, {"text": " mudou", "offsets": {"from": 1559, "to": 1937}}, {"text": " tudo.", "offsets": {"from": 1979, "to": 2357}}]}, {"offsets": {"from": 3200, "to": 5960}, "text": " Quem aprende Java precisa entender o b\u00e1sico.", "tokens": [{"text": " Quem", "offsets": {"from": 3200, "to": 3560}}, {"text": " aprende", "offsets": {"from": 3600, "to": 3960}}, {"text": " Java", "offsets": {"from": 4000, "to": 4360}}, {"text": " precisa", "offsets": {"from": 4400, "to": 4760}}, {"text": " entender", "offsets": {"from": 4800, "to": 5160}}, {"text": " o", "offsets": {"from": 5200, "to": 5560}}, {"text": " b\u00e1sico.", "offsets": {"from": 5600, "to": 5960}}]}, {"offsets": {"from": 7400, "to": 10160}, "text": " Por exemplo, o ChatGPT escreve muito r\u00e1pido.", "tokens": [{"text": " Por", "offsets": {"from": 7400, "to": 7760}}, {"text": " exemplo,", "offsets": {"from": 7800, "to": 8160}}, {"text": " o", "offsets": {"from": 8200, "to": 8560}}, {"text": " ChatGPT", "offsets": {"from": 8600, "to": 8960}}, {"text": " escreve", "offsets": {"from": 9000, "to": 9360}}, {"text": " muito", "offsets": {"from": 9400, "to": 9760}}, {"text": " r\u00e1pido.", "offsets": {"from": 9800, "to": 10160}}]}, {"offsets": {"from": 11300, "to": 13922}, "text": " Mas ningu\u00e9m te conta o problema real!", "tokens": [{"text": " Mas", "offsets": {"from": 11300, "to": 11642}}, {"text": " ningu\u00e9m", "offsets": {"from": 11680, "to": 12022}}, {"text": " te", "offsets": {"from": 12060, "to": 12402}}, {"text": " conta", "offsets": {"from": 12440, "to": 12782}}, {"text": " o", "offsets": {"from": 12820, "to": 13162}}, {"text": " problema", "offsets": {"from": 13200, "to": 13542}}, {"text": " real!", "offsets": {"from": 13580, "to": 13922}}]}, {"offsets": {"from": 14900, "to": 17659}, "text": " Seguran\u00e7a \u00e9 o detalhe que ningu\u00e9m v\u00ea.", "tokens": [{"text": " Seguran\u00e7a", "offsets": {"from": 14900, "to": 15260}}, {"text": " \u00e9", "offsets": {"from": 15300, "to": 15660}}, {"text": " o", "offsets": {"from": 15700, "to": 16060}}, {"text": " detalhe", "offsets": {"from": 16100, "to": 16460}}, {"text": " que", "offsets": {"from": 16500, "to": 16860}}, {"text": " ningu\u00e9m", "offsets": {"from": 16900, "to": 17259}}, {"text": " v\u00ea.", "offsets": {"from": 17299, "to": 17659}}]}, {"offsets": {"from": 19600, "to": 21238}, "text": " Segue para mais conte\u00fado.", "tokens": [{"text": " Segue", "offsets": {"from": 19600, "to": 19978}}, {"text": " para", "offsets": {"from": 20020, "to": 20398}}, {"text": " mais", "offsets": {"from": 20440, "to": 20818}}, {"text": " conte\u00fado.", "offsets": {"from": 20860, "to": 21238}}]}]}
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
  "output": {"width":1080,"height":1920,"fps":30,"crf":20,"preset":"ultrafast","audio_bitrate":"192k"},
  "cuts": {"enabled":false,"noise_db":-35,"min_silence":0.85,"keep_silence":0.18,"min_keep_segment":0.15},
  "captions": {"enabled":true,"font_name":"Arial","font_size":74,"primary_color":"&H00FFFFFF","highlight_color":"&H0000D7FF","outline_color":"&H00000000","outline":5,"shadow":0,"margin_v":560,"safe_margin":112,"max_words":6,"max_chars_per_line":22,"uppercase":true,"active_word":true,"timing_offset":0.06,"active_scale":1.05,"max_width_ratio":0.80},
  "zoom": {"enabled":true,"mild":1.06,"emphasis":1.085,"punch":1.10,"min_gap":3.0,"duration":0.85},
  "broll": {"enabled":true,"asset_dir":"$TMP/assets","manifest":"$TMP/manifest.json","max_events":5,
            "min_visual_gap":0.35,"remote":false,"procedural":true,"allow_self_broll":true},
  "music": {"enabled":true,"mood":"auto","library":"$TMP/music","manifest":"$TMP/music/manifest.json",
            "remote":false,"gain_db":-20,"duck":true,"drops":true,"emotion_sfx":true}
}
JSON
mkdir -p "$ROOT/bin"
go build -o "$ROOT/bin/cenarius" "$ROOT/cmd/cenarius"
CENARIUS_NO_REMOTE_ASSETS=1 "$ROOT/bin/cenarius" edit -config "$TMP/config.json" -i "$TMP/input.mp4" -o "$TMP/out"

OUT="$TMP/out/final.mp4"
PLAN="$TMP/out/edit-plan.json"
LOG="$TMP/out/debug.log"
[ -s "$OUT" ] || fail "final.mp4 não gerado"
[ -s "$LOG" ] || fail "debug.log não gerado"
[ -s "$TMP/out/assets-attribution.json" ] || fail "assets-attribution.json não gerado"

# --- format ---------------------------------------------------------------
V=$("$FFPROBE_BIN" -v error -select_streams v:0 -show_entries stream=codec_name,width,height,r_frame_rate,pix_fmt -of csv=p=0 "$OUT")
A=$("$FFPROBE_BIN" -v error -select_streams a -show_entries stream=codec_name -of csv=p=0 "$OUT")
echo "video: $V | audio: $A"
[ "$V" = "h264,1080,1920,yuv420p,30/1" ] || fail "stream de vídeo inesperado: $V (esperado h264 1080x1920 yuv420p 30/1)"
[ "$A" = "aac" ] || fail "áudio deveria ser um único stream AAC, veio: $A"
"$FFPROBE_BIN" -v error -show_entries format_tags=major_brand -of csv=p=0 "$OUT" >/dev/null
head -c 4096 "$OUT" | grep -q moov || fail "faststart: átomo moov não está no início"

# --- duration -------------------------------------------------------------
IN_DUR=$("$FFPROBE_BIN" -v error -show_entries format=duration -of default=nw=1:nk=1 "$TMP/input.mp4")
OUT_DUR=$("$FFPROBE_BIN" -v error -show_entries format=duration -of default=nw=1:nk=1 "$OUT")
awk -v a="$IN_DUR" -v b="$OUT_DUR" 'BEGIN { d=a-b; if (d<0) d=-d; if (d>0.20) { printf("ERRO duração: input=%ss output=%ss\n", a, b) > "/dev/stderr"; exit 1 } }'
grep -q 'msg=duration.check' "$LOG" || fail "duration.check ausente no debug.log"

# --- editorial content ----------------------------------------------------
grep -q '"visual_events"' "$PLAN" || fail "edit-plan sem visual_events"
grep -q '"type": "video"' "$PLAN" || fail "nenhum B-roll em vídeo no plano"
grep -q '"type": "image"' "$PLAN" || fail "nenhuma imagem estática (Ken Burns) no plano"
grep -q '"kind": "punch_zoom"' "$PLAN" || fail "punch zoom ausente"
grep -q '"name": "' "$PLAN" || fail "nenhum SFX no plano"
grep -q '"source": "self-broll"' "$PLAN" && fail "self-broll usado com assets locais/procedural disponíveis"
grep -q 'msg=visual.render.event.*media_type=video' "$LOG" || fail "render de B-roll em vídeo não registrado"
grep -q 'msg=visual.render.event.*media_type=image' "$LOG" || fail "render de imagem não registrado"
grep -q 'msg=visual.render.complete' "$LOG" || fail "visual.render.complete ausente"
grep -q 'msg=audio.sfx.event' "$LOG" || fail "SFX não renderizado"
grep -q 'msg=planner.semantic_unit' "$LOG" || fail "planner semântico não registrado"
grep -q 'msg=audio.music.plan' "$LOG" || fail "mood/trilha não planejados"
grep -q 'msg=audio.music.resolved.*tier=local' "$LOG" || fail "trilha local não resolvida"
grep -q 'msg=audio.music.render' "$LOG" || fail "trilha não mixada no render"
grep -q '\[voicen\]' "$OUT.filter" || fail "voz não normalizada antes da mixagem"
grep -q "pow(10," "$OUT.filter" || fail "trilha sem ducking pelos trechos de fala"
grep -q 'alimiter' "$OUT.filter" || fail "mix sem limitador final"
grep -q '\[0:a\]loudnorm' "$OUT.filter" && fail "loudnorm na voz antes do amix encurta o vídeo"
grep -q 'msg=audio.music.balance' "$LOG" || fail "balanço voz/música não registrado"
grep -q '"mood": "' "$PLAN" || fail "edit-plan sem mood da trilha"
grep -q '"name": "hit"' "$PLAN" || grep -q '"name": "impact"' "$PLAN" || fail "nenhum SFX emocional no plano"
grep -q 'msg=visual.schedule.after' "$LOG" || fail "scheduler visual não registrado"
grep -Eiq 'latitude|longitude|ISO6709|quicktime\.location' "$LOG" && fail "debug.log contém metadados de localização"
python3 - "$PLAN" <<'PY' || fail "eventos visuais sobrepostos ou fora do ritmo"
import json, sys
p = json.load(open(sys.argv[1]))
ev = sorted(p["visual_events"], key=lambda e: e["start"])
assert 3 <= len(ev) <= 6, f"{len(ev)} eventos"
for a, b in zip(ev, ev[1:]):
    assert b["start"] >= a["end"] + 0.35 - 1e-6, (a["id"], b["id"])
for e in ev:
    assert e["start"] >= 1.2 and e["end"] - e["start"] >= 1.0, e["id"]
print("eventos:", ", ".join(f'{e["id"]}:{e["layout"]}:{e["asset"]["source"]}/{e["asset"]["type"]}' for e in ev))
PY

# --- visual difference: the B-roll really appears in the bottom half -------
FIRST_VIDEO_START=$(python3 -c "import json;e=[x for x in json.load(open('$PLAN'))['visual_events'] if x['asset']['type']=='video' and x['layout']=='reaction'];print(e[0]['start']+0.8 if e else '')")
if [ -n "$FIRST_VIDEO_START" ]; then
  "$FFMPEG_BIN" -v error -y -ss "$FIRST_VIDEO_START" -i "$OUT" -frames:v 1 -vf "crop=1080:700:0:1100,scale=64:40" -f rawvideo -pix_fmt gray "$TMP/during.raw"
  "$FFMPEG_BIN" -v error -y -ss 0.4 -i "$OUT" -frames:v 1 -vf "crop=1080:700:0:1100,scale=64:40" -f rawvideo -pix_fmt gray "$TMP/before.raw"
  cmp -s "$TMP/during.raw" "$TMP/before.raw" && fail "metade inferior idêntica ao A-roll durante o B-roll"
fi

# --- mixer: re-render only the audio with the music quieter ---------------
"$ROOT/bin/cenarius" remix -config "$TMP/config.json" -o "$TMP/out" -music-db -30 -duck-db 14 >/dev/null
grep -q 'msg=audio.remix.done' "$LOG" || fail "remix não concluído"
grep -q '"gain_db": -30' "$TMP/out/audio-mix.json" || fail "mix do remix não registrado"
REMIX_DUR=$("$FFPROBE_BIN" -v error -show_entries format=duration -of default=nw=1:nk=1 "$OUT")
awk -v a="$IN_DUR" -v b="$REMIX_DUR" 'BEGIN { d=a-b; if (d<0) d=-d; if (d>0.20) { printf("ERRO duração após remix: input=%ss output=%ss\n", a, b) > "/dev/stderr"; exit 1 } }'
echo "remix OK: música -30 dB abaixo da voz, duração ${REMIX_DUR}s"

# --- Montagem IA: 3 clipes (horizontal, vertical e "iPhone" rotacionado) + prompt
"$FFMPEG_BIN" -hide_banner -loglevel error -y -f lavfi -i "mandelbrot=size=640x360:rate=30" \
  -f lavfi -i "sine=frequency=500:duration=4" -t 4 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$TMP/raw_rot.mp4"
"$FFMPEG_BIN" -hide_banner -loglevel error -y -display_rotation 90 -i "$TMP/raw_rot.mp4" -c copy "$TMP/iphone_rot.mov"
"$ROOT/bin/cenarius" assemble -config "$TMP/config.json" -o "$TMP/montagem" -prompt 'faça um meme "SMOKE MONTAGEM" com os clipes' \
  -duration 12 "$TMP/assets/technology.mp4" "$TMP/input.mp4" "$TMP/iphone_rot.mov" >/dev/null
M_OUT="$TMP/montagem/final.mp4"
[ -s "$M_OUT" ] || fail "montagem: final.mp4 não gerado"
MV=$("$FFPROBE_BIN" -v error -select_streams v:0 -show_entries stream=codec_name,width,height,r_frame_rate,pix_fmt -of csv=p=0 "$M_OUT")
MA=$("$FFPROBE_BIN" -v error -select_streams a -show_entries stream=codec_name -of csv=p=0 "$M_OUT")
[ "$MV" = "h264,1080,1920,yuv420p,30/1" ] || fail "montagem: vídeo inesperado: $MV"
[ "$MA" = "aac" ] || fail "montagem: áudio inesperado: $MA"
grep -q '"segments"' "$TMP/montagem/assembly-plan.json" || fail "montagem: roteiro ausente"
grep -q 'SMOKE MONTAGEM' "$TMP/montagem/captions.ass" || fail "montagem: texto na tela ausente"
grep -q 'msg=assembly.done' "$TMP/montagem/debug.log" || fail "montagem: não concluída"
M_DUR=$("$FFPROBE_BIN" -v error -show_entries format=duration -of default=nw=1:nk=1 "$M_OUT")
awk -v d="$M_DUR" 'BEGIN { if (d < 3 || d > 12*1.25+0.3) { printf("ERRO montagem com duração fora do alvo: %ss\n", d) > "/dev/stderr"; exit 1 } }'
echo "montagem OK: ${M_DUR}s, $(grep -c '"clip_id"' "$TMP/montagem/assembly-plan.json") trechos"

echo "duration input=${IN_DUR}s output=${OUT_DUR}s"
echo "debug log: $LOG"
echo "SMOKE OK: $OUT"
