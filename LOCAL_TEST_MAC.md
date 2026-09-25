# Teste local no Mac

## 1. Bootstrap

```bash
cd cenarius-autocut
./scripts/bootstrap-macos.sh
```

O bootstrap usa Homebrew para instalar `go`, `ffmpeg` e `whisper.cpp`, baixa o modelo `medium`, compila o binário e roda o diagnóstico.

Para um primeiro teste mais rápido:

```bash
WHISPER_MODEL=small ./scripts/bootstrap-macos.sh
```

## 2. Smoke test sem IA/STT real

```bash
make smoke
```

Isso cria um vídeo sintético, passa pelo pipeline inteiro e confirma saída 1080x1920/30fps com legenda e zoom.

## 3. Teste real via CLI

```bash
./bin/cenarius edit -i "$HOME/Desktop/meu-video.mov" -o ./data/teste-real
open ./data/teste-real/final.mp4
```

Arquivos úteis para depurar:

```text
data/teste-real/transcript.json
data/teste-real/edit-plan.json
data/teste-real/captions.ass
data/teste-real/cut.mp4
data/teste-real/final.mp4
```

## 4. Teste pela interface

```bash
./bin/cenarius serve
```

Abra no navegador:

```text
http://127.0.0.1:8484
```

Arraste um MP4/MOV. A página mostra as etapas e libera o download quando terminar.

## 5. Ativar planejamento semântico local

Primeiro confirme que o Ollama está rodando e que você tem um modelo adequado. Depois altere:

```json
"ollama": {
  "enabled": true,
  "url": "http://127.0.0.1:11434",
  "model": "qwen3:8b"
}
```

Sem Ollama a aplicação continua funcional usando heurísticas determinísticas.

## 6. Imagens/B-roll

Coloque PNG/JPG em `assets/broll/` e edite `assets/manifest.json`. Exemplo:

```json
{"keyword":"instagram","aliases":["meta"],"file":"instagram.png"}
```

Se a fala/planner pedir `instagram`, a imagem entra automaticamente. O arquivo ausente não derruba o job.

## Diagnóstico

```bash
./bin/cenarius doctor
```

Ele verifica FFmpeg, ffprobe, whisper-cli, libass e o modelo Whisper.
