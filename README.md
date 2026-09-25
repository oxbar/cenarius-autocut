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

## B-roll / imagens contextuais

Coloque imagens próprias ou licenciadas em `assets/broll/` e mapeie em `assets/manifest.json`.

Exemplo:

```json
{"keyword":"instagram","aliases":["meta"],"file":"instagram.png"}
```

Quando a palavra aparece na fala e o planner criar um overlay, FFmpeg insere a imagem por ~1–2s. Se o arquivo não existir, ele ignora o overlay e continua o render.

Isso é intencional: a V1 não baixa imagens aleatórias da internet e não assume direitos de uso.

## Ajustando o estilo

Tudo fica em `config.json`:
- `cuts.min_silence`: silêncio mínimo para cortar
- `cuts.keep_silence`: quanto de pausa manter
- `captions.max_words`: palavras por bloco
- `captions.font_size`, cores e margem vertical
- `zoom.mild` / `zoom.punch`
- `output.crf`: qualidade H.264 (menor = melhor/maior arquivo)

## Limitações honestas da V1

- B-roll automático só usa a biblioteca local mapeada.
- Reconhecimento de punchline é heurístico sem Ollama.
- Não faz face tracking/crop inteligente ainda.
- A revisão humana continua recomendada antes de publicar.

## Próximas etapas

V2: preview de timeline e aceitar/remover B-roll antes do render; busca em Wikimedia/Pexels com licença; face tracking; cards de screenshots; templates `Commentary`, `Reaction`, `Meme`.

## Segurança

O servidor escuta `127.0.0.1` por padrão. Não exponha na internet sem autenticação, limites e sandbox.
