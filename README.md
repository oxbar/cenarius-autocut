# CENARIUS AutoCut

Editor local para Shorts/Reels/TikTok feito em Go. A V1 automatiza **cortes de silêncio, transcrição offline, legendas ASS grandes, zooms editoriais, normalização de áudio, B-roll local por palavra-chave e render 9:16**. Opcionalmente usa Ollama para melhorar o plano de edição sem API paga.

## Por que Go

Go coordena jobs e arquivos; FFmpeg faz codec/render; whisper.cpp faz STT; Ollama toma decisões editoriais. Reescrever codecs em Go/Rust só aumentaria complexidade sem melhorar a qualidade do render.

## macOS (Apple Silicon/Intel)

```bash
unzip cenarius-autocut.zip
cd cenarius-autocut
./scripts/bootstrap-macos.sh
./bin/cenarius serve
```

Abra `http://127.0.0.1:8484`, arraste um MP4/MOV e acompanhe o job.

O bootstrap instala via Homebrew:
- Go
- FFmpeg
- whisper.cpp
- modelo Whisper `medium` por padrão

O modelo é baixado de `ggerganov/whisper.cpp` no Hugging Face. Para testar mais rápido, antes do bootstrap:

```bash
WHISPER_MODEL=small ./scripts/bootstrap-macos.sh
```

`medium` tende a transcrever PT-BR melhor; `small` baixa/processa mais rápido.

## CLI

```bash
./bin/cenarius doctor
./bin/cenarius edit -i ~/Desktop/video.mov -o ./data/meu-video
```

Saídas:
- `final.mp4`
- `captions.ass`
- `transcript.json`
- `edit-plan.json`
- `cut.mp4`

## Ollama opcional

O sistema funciona sem LLM (planner heurístico). Para planejamento semântico local:

```bash
brew install ollama
ollama serve
ollama pull qwen3:8b
```

Em `config.json`:

```json
"ollama": {"enabled": true, "url": "http://127.0.0.1:11434", "model": "qwen3:8b"}
```

Se Ollama estiver indisponível ou responder JSON inválido, o sistema volta automaticamente ao planner heurístico.

## Editor inteligente (v1.6)

O pipeline agora trabalha como um editor/diretor, não como detector de palavras:

1. **Unidades semânticas** (`internal/planner/semantic.go`): a fala é quebrada em frases/respirações e cada uma recebe um papel — `hook`, `explanation`, `example`, `punchline`, `turn` (mudança de ideia), `question`, `cta` — e os conceitos citados (léxico em `concepts.go`, cada conceito com 3–4 queries em inglês).
2. **Diretor editorial** (Ollama/Qwen, opcional): recebe unidades + palavras com timestamps e devolve JSON estrito (`visual_events`, `zooms`, `emphasis`). O Go valida tudo; JSON inválido ou Ollama offline → fallback heurístico, sem interromper o pipeline.
3. **Scheduler visual** (`scheduler.go`): `ResolveVisualConflicts` remove sobreposições (importância, relevância, qualidade da fonte), respeita `broll.min_visual_gap` e um orçamento de ritmo (3–5 inserts em 20–30 s). Nada de B-roll antes de 1.2 s: o primeiro segundo é do creator com punch zoom.
4. **Zooms por intenção**: `punch_zoom` (hook/punchline, 1.08–1.12), `reframe` (mudança de ideia), `slow_push` (A-roll longo, 1.055–1.07).
5. **AssetResolver** (`internal/assets`), nesta ordem:
   local (`assets/manifest.json`) → cache (`data/asset-cache`) → **vídeo** do Wikimedia Commons → imagem do Commons → motion graphic procedural (FFmpeg) → self-broll (último recurso, registrado como `WARN`).
   Várias queries por evento; licença verificada (CC0/PD/CC BY/CC BY-SA; NC/ND/desconhecida rejeitadas); todo arquivo é validado (ffprobe + decode de 1 frame para vídeo, decoder para imagem); HTML/erro/arquivo truncado → próximo candidato. Créditos em `assets-attribution.json`.
6. **Layouts**: `reaction` (creator em cima, B-roll embaixo, legenda na costura), `fullscreen` (cutaway curto), `card` (print/logo com cantos arredondados, sombra e entrada suave sobre painel escuro), `pip`. Vídeo é preferido; imagem recebe Ken Burns discreto. B-roll nunca leva áudio e a legenda é queimada por último (nunca fica coberta).
7. **SFX discretos** (whoosh/pop/click/beep, −28 a −18 dB), nunca em todos os inserts.

### Biblioteca local de B-roll

Arquivos próprios/licenciados em `assets/broll/` têm prioridade máxima:

```json
{"keyword":"java","aliases":["spring"],"concepts":["java programming"],"file":"java.mp4","license":"CC0","author":"Você"}
```

Vídeos (`.mp4`, `.webm`, `.mov`) e imagens (`.png`, `.jpg`, `.webp`) são aceitos; arquivos inválidos são ignorados.

### Configuração nova (`broll`)

- `min_visual_gap`: respiro mínimo de A-roll entre inserts (0.35 s)
- `remote`: busca gratuita no Wikimedia Commons (`CENARIUS_NO_REMOTE_ASSETS=1` também desliga)
- `procedural` / `allow_self_broll`: fallbacks
- `reaction_split` / `reaction_focus_y`: altura da divisão e posição do rosto no A-roll
- `seam_captions`: move a legenda para a costura durante split/card
- `CENARIUS_BOLD_FONT=/caminho/fonte.ttf`: fonte pesada dos motion graphics

FFmpeg: use o `ffmpeg-full` (libass + drawtext). O `ffmpeg` padrão do Homebrew não tem `drawtext`; nesse caso os motion graphics usam libass e o self-broll fica sem rótulo. Os testes escolhem `FFMPEG_BIN`/`FFPROBE_BIN`, depois `ffmpeg-full` do Homebrew, depois o PATH.

## Ajustando o estilo

Tudo fica em `config.json`:
- `cuts.min_silence`: silêncio mínimo para cortar
- `cuts.keep_silence`: quanto de pausa manter
- `captions.max_words`: palavras por bloco
- `captions.font_size`, cores e margem vertical
- `zoom.mild` / `zoom.emphasis` / `zoom.punch` (1.035 é praticamente invisível; use 1.055+)
- `output.crf`: qualidade H.264 (menor = melhor/maior arquivo)

## Limitações honestas da V1

- A cobertura de vídeo livre no Wikimedia Commons é limitada; para temas de nicho, a biblioteca local faz muita diferença.
- Sem Ollama, conceitos vêm de um léxico (roles e contexto são heurísticos).
- O reenquadramento usa `reaction_focus_y` fixo (sem face tracking).
- A revisão humana continua recomendada antes de publicar.

## Próximas etapas

V2: preview de timeline e aceitar/remover B-roll antes do render; busca em Wikimedia/Pexels com licença; face tracking; cards de screenshots; templates `Commentary`, `Reaction`, `Meme`.

## Segurança

O servidor escuta `127.0.0.1` por padrão. Não exponha na internet sem autenticação, limites e sandbox.
