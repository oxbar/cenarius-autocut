#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
MODEL=${1:-large-v3-turbo}
mkdir -p models
TARGET="models/ggml-${MODEL}.bin"
URL="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${MODEL}.bin"
if [[ ! -f "$TARGET" ]]; then
  echo "Baixando Whisper ${MODEL} (modo qualidade)..."
  curl -L --fail --progress-bar "$URL" -o "$TARGET"
else
  echo "✓ modelo já existe: $TARGET"
fi
python3 - "$MODEL" <<'PY'
import json, sys
model=sys.argv[1]
p='config.json'
with open(p, encoding='utf-8') as f: d=json.load(f)
d.setdefault('whisper', {})['model']=f'./models/ggml-{model}.bin'
d['whisper']['language']='pt'
d['whisper']['prompt']='Português brasileiro. Tecnologia, inteligência artificial, IA, programação, software, engenharia de software, Java, Spring Boot, ChatGPT, OpenAI, Claude, Gemini, Docker, Kubernetes, GitHub, LinkedIn, Instagram, TikTok, YouTube.'
d.setdefault('ollama', {})['enabled']=True
d.setdefault('ollama', {})['url']='http://127.0.0.1:11434'
d['ollama']['model']='qwen3:8b'
d['cuts']={'enabled':True,'noise_db':-42,'min_silence':1.10,'keep_silence':0.25,'min_keep_segment':0.20}
d['captions']={'enabled':True,'font_name':'Arial','font_size':82,'primary_color':'&H00FFFFFF','highlight_color':'&H0000D7FF','outline_color':'&H00000000','outline':6,'shadow':0,'margin_v':560,'safe_margin':96,'max_words':6,'max_chars_per_line':24,'max_lines':2,'uppercase':True,'active_word':True}
d['zoom']={'enabled':True,'mild':1.035,'punch':1.08,'min_gap':5.0,'duration':1.0}
d['broll']={'enabled':True,'asset_dir':'./assets/broll','manifest':'./assets/manifest.json','max_events':3}
with open(p,'w',encoding='utf-8') as f: json.dump(d,f,ensure_ascii=False,indent=2); f.write('\n')
print('✓ config.json migrado para V1.2 (mantendo ffmpeg/ffprobe atuais)')
print('✓ Whisper:', d['whisper']['model'])
PY

echo
echo "Modo qualidade ativado. Recompile com: make build"
