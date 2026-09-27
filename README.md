# CENARIUS Studio / AutoCut

Editor local para Shorts/Reels/TikTok feito em Go. O mesmo core agora atende **Edição IA**, **React Mode** e **Longo → Shorts**. O pipeline combina FFmpeg, whisper.cpp, planner semântico, captions ASS, zooms editoriais, B-roll contextual, SFX e render 9:16. Ollama é opcional e funciona como diretor/ranker sem substituir as validações determinísticas do Go.

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
- FFmpeg compatível (via script de correção)
- whisper.cpp
- yt-dlp (para Longo → Shorts via YouTube)
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
./bin/cenarius react -i ~/Desktop/eu.mov -reaction ~/Desktop/video.mp4 -audio duck -o ./data/react
./bin/cenarius shorts -i ~/Desktop/podcast.mp4 -count 5
./bin/cenarius shorts -url "https://www.youtube.com/watch?v=..." -count 5 -generate
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

## Editor inteligente (v1.7)

O pipeline agora trabalha como um editor/diretor, não como detector de palavras:

1. **Unidades semânticas** (`internal/planner/semantic.go`): a fala é quebrada em frases/respirações e cada uma recebe um papel — `hook`, `explanation`, `example`, `punchline`, `turn` (mudança de ideia), `question`, `cta` — e os conceitos citados (léxico em `concepts.go`, cada conceito com 3–4 queries em inglês).
2. **Diretor editorial** (Ollama/Qwen, opcional): recebe unidades + palavras com timestamps e devolve JSON estrito (`visual_events`, `zooms`, `emphasis`). O Go valida tudo; JSON inválido ou Ollama offline → fallback heurístico, sem interromper o pipeline.
3. **Scheduler visual** (`scheduler.go`): `ResolveVisualConflicts` remove sobreposições (importância, relevância, qualidade da fonte), respeita `broll.min_visual_gap` e um orçamento de ritmo proporcional: 3–5 inserts em 20–30 s e crescimento gradual em vídeos maiores (até 12 por padrão). O orçamento também prioriza conceitos diferentes quando há alternativas. Nada de B-roll antes de 1.2 s: o primeiro segundo é do creator com punch zoom.
4. **Zooms por intenção**: `punch_zoom` (hook/punchline, 1.08–1.12), `reframe` (mudança de ideia), `slow_push` (A-roll longo, 1.055–1.07).
5. **AssetResolver** (`internal/assets`), nesta ordem:
   local (`assets/manifest.json`) → cache (`data/asset-cache`) → **Pexels vídeo** → **Pixabay vídeo** → **Pexels imagem** → **Pixabay imagem** → Wikimedia Commons vídeo → Commons imagem → motion graphic procedural (FFmpeg) → self-broll (último recurso, registrado como `WARN`).
   Várias queries por evento; vídeo e orientação vertical recebem preferência; o mesmo arquivo não é reutilizado no mesmo render quando existe alternativa. Commons mantém validação de licença livre (CC0/PD/CC BY/CC BY-SA; NC/ND/desconhecida rejeitadas); Pexels/Pixabay são registrados com suas licenças de conteúdo e página de origem. Todo arquivo é validado (ffprobe + decode de 1 frame para vídeo, decoder para imagem); HTML/erro/arquivo truncado → próximo candidato. Créditos em `assets-attribution.json`.
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
- `remote`: habilita busca remota; tenta Pexels/Pixabay quando as chaves existem e Wikimedia Commons como fonte sem chave (`CENARIUS_NO_REMOTE_ASSETS=1` desliga tudo)
- `procedural` / `allow_self_broll`: fallbacks
- `reaction_split` / `reaction_focus_y`: altura da divisão e posição do rosto no A-roll
- `seam_captions`: move a legenda para a costura durante split/card
- `CENARIUS_BOLD_FONT=/caminho/fonte.ttf`: fonte pesada dos motion graphics

### Pexels / Pixabay (opcional)

Copie `.env.example` para `.env` e preencha somente localmente:

```bash
cp .env.example .env
# PEXELS_API_KEY=...
# PIXABAY_API_KEY=...
```

`.env` está no `.gitignore`. Variáveis já exportadas no shell têm precedência; nenhuma chave entra em `config.json`, `edit-plan.json` ou `debug.log`. Sem chaves, o resolver continua usando local/cache/Wikimedia/fallbacks.

FFmpeg: use o `ffmpeg-full` (libass + drawtext). O `ffmpeg` padrão do Homebrew não tem `drawtext`; nesse caso os motion graphics usam libass e o self-broll fica sem rótulo. Os testes escolhem `FFMPEG_BIN`/`FFPROBE_BIN`, depois `ffmpeg-full` do Homebrew, depois o PATH.

## Emoção no áudio e stock inteligente (v1.8)

### Trilha emocional

O planner detecta o **mood** do reel pela fala (`energetic`, `inspiring`,
`tense`, `dramatic`, `chill`, `funny`) — ou usa o mood escolhido pelo Ollama —
e monta a camada sonora:

- **Trilha de fundo** em loop exato na duração do vídeo, com fade in/out e
  **ducking automático** (sidechain): abaixa sozinha quando você fala.
- **Drop** nas punchlines: a música some por ~0,9 s e volta suave, para a frase
  "cair". No máximo ~1 por 30 s, com 8 s de distância.
- **Riser** subindo antes do drop, **impact** no drop e um **hit** no gancho.
  Gerados pelo FFmpeg (sem licença envolvida), sempre entre −28 e −18 dB.

Origem da trilha, nesta ordem:
1. `-music arquivo.mp3` (ou `music.file`)
2. biblioteca local `assets/music/manifest.json` filtrada pelo mood
   (veja `assets/music/README.md` — ideal para faixas da YouTube Audio Library)
3. cache
4. **Openverse** (música CC0/CC BY/CC BY-SA do Jamendo/ccMixter, sem chave)

Sem trilha válida o vídeo segue só com voz + efeitos. Créditos em
`assets-attribution.json`. O modo reação não recebe trilha (o áudio do vídeo
reagido já é a trilha).

```bash
./bin/cenarius edit -i video.mov -o ./data/out                 # mood automático
./bin/cenarius edit -i video.mov -o ./data/out -mood tense     # força o mood
./bin/cenarius edit -i video.mov -o ./data/out -music trilha.mp3
./bin/cenarius edit -i video.mov -o ./data/out -no-music       # só voz
```

Config (`music`): `enabled`, `file`, `mood`, `library`, `manifest`, `remote`,
`gain_db` (−30..−14, padrão −20), `duck`, `drops`, `emotion_sfx`.

> Não extraia música de vídeos do YouTube: Instagram/TikTok/YouTube detectam o
> áudio (Content ID) e podem silenciar, bloquear ou dar strike no reel.

### Stock menos aleatório

Pexels/Pixabay ordenam por popularidade, não só por significado. Agora todo
resultado passa por um **portão semântico**: a query e as **âncoras visuais**
do conceito (ex.: programação → code, laptop, keyboard, screen…) precisam
aparecer no alt/tags/slug do resultado. Resultado fora do tema é descartado
(`visual.asset.candidate accepted=false reason=off-topic` no `debug.log`) e o
ranking passa a ser 60% significado + 40% formato/posição.

## Ajustando o estilo

Tudo fica em `config.json`:
- `cuts.min_silence`: silêncio mínimo para cortar
- `cuts.keep_silence`: quanto de pausa manter
- `captions.max_words`: palavras por bloco
- `captions.font_size`, `safe_margin`, `max_width_ratio`, cores e margem vertical
- `captions.timing_offset`: atraso positivo pequeno (default 0.06 s) para evitar legenda visualmente adiantada
- `captions.active_scale`: escala da palavra ativa (default 1.05)
- `zoom.mild` / `zoom.emphasis` / `zoom.punch` (1.035 é praticamente invisível; use 1.055+)
- `output.crf`: qualidade H.264 (menor = melhor/maior arquivo)

## Limitações honestas da V1

- Sem chaves de Pexels/Pixabay, a cobertura remota depende do Wikimedia Commons; para temas de nicho, a biblioteca local continua fazendo muita diferença.
- Sem Ollama, conceitos vêm de um léxico (roles e contexto são heurísticos).
- O reenquadramento usa `reaction_focus_y` fixo (sem face tracking).
- A revisão humana continua recomendada antes de publicar.


## Studio Web v2

```bash
make build
./bin/cenarius serve
```

Abra `http://127.0.0.1:8484`. A UI embutida no próprio binário Go tem três modos e não depende de Node/Next.js:

- **Edição IA**: fluxo original de upload → planner → assets → render.
- **Reagir a vídeo**: dois uploads; o creator fica no painel superior, o vídeo reagido no inferior, captions na seam e fade curto. O layout é uma restrição explícita do produto — o planner não substitui o vídeo escolhido por stock media.
- **Longo → Shorts**: upload local ou URL do YouTube, análise semântica, score editorial, preview de candidatos e geração em lote.

A interface usa um pequeno subconjunto de ícones Lucide embutidos como SVG para continuar funcionando offline.

## React Mode

O vídeo fornecido pelo usuário vira `SourceManual` (`manual-reaction`) e tem prioridade absoluta. O resolver apenas valida a mídia; não troca por Pexels/Pixabay/Wikimedia. O evento ocupa a timeline do creator com `LayoutReaction`, portanto:

```text
┌──────────────────────────────┐
│        CREATOR / A-ROLL      │
├──────── captions ────────────┤
│        VÍDEO REAGIDO         │
└──────────────────────────────┘
```

`PrepareClip` mantém o reaction em `t=0` (não aplica o offset usado para stock footage), preservando sync. O corte automático de silêncio fica desativado nesse modo pelo mesmo motivo.

Áudio do vídeo reagido:

- `duck` (default): sidechain compressor reduz o vídeo reagido quando sua voz está presente.
- `muted`: sem áudio do vídeo reagido.
- `original`: mix audível em volume seguro sob a voz principal.

CLI:

```bash
./bin/cenarius react \
  -i ~/Downloads/IMG_6825.MOV \
  -reaction ~/Downloads/reaction.mp4 \
  -audio duck \
  -o ./data/react-demo
```

## Longo → Shorts

O CENARIUS não corta em blocos cegos de 45 segundos. O fluxo é:

```text
vídeo/YouTube
  ↓
ffprobe + áudio 16 kHz
  ↓
Whisper (uma transcrição de análise)
  ↓
unidades semânticas reais
  ↓
janelas candidatas em boundaries reais
  ↓
score heurístico + Ollama opcional
  ↓
resolver de overlap
  ↓
Top N
  ↓
CENARIUS AutoCut em cada corte selecionado
```

Por padrão:

- mínimo: **30 s**
- faixa preferida: **40–50 s**
- máximo: **55 s**
- `max_overlap`: **0.35** — evita entregar vários cortes quase iguais
- ranking: `hook`, `engagement`, `value`, `shareability` (0–25 cada; total 0–100)

O score é **auxílio editorial**, não previsão de viralização. O Ollama só recebe IDs/timestamps que já foram gerados a partir do Whisper e pode alterar título/score/razão; timestamps inexistentes são descartados pelo design.

Para YouTube, `yt-dlp` roda localmente com `--no-playlist`. O servidor aceita apenas hosts YouTube/youtu.be.

Configuração:

```json
"yt_dlp": "yt-dlp",
"shorts": {
  "enabled": true,
  "min_duration": 30,
  "ideal_min": 40,
  "ideal_max": 50,
  "max_duration": 55,
  "default_count": 5,
  "max_count": 10,
  "max_overlap": 0.35,
  "use_ollama_rank": true
}
```

### Nota sobre a referência SupoClip

O ZIP do SupoClip foi usado como **referência de produto/arquitetura** para conceitos como seleção semântica, faixa de duração, score editorial, upload/YouTube e cards de resultado. Nenhum código-fonte do SupoClip foi incorporado; a implementação acima foi escrita em Go dentro da arquitetura existente do CENARIUS.

## Segurança

O servidor escuta `127.0.0.1` por padrão. Não exponha na internet sem autenticação, limites e sandbox.
