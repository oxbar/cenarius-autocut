# Biblioteca local de trilhas (CENARIUS AutoCut)

Coloque aqui as músicas que o editor pode usar como trilha de fundo e liste
cada uma em `manifest.json` com os **moods** em que ela funciona:

```json
{
  "tracks": [
    {"file": "neon-run.mp3", "title": "Neon Run", "moods": ["energetic"],
     "license": "YouTube Audio Library", "author": "Artista", "source_url": "https://..."},
    {"file": "dark-pulse.mp3", "title": "Dark Pulse", "moods": ["tense", "dramatic"],
     "license": "CC BY 4.0", "author": "Artista", "source_url": "https://..."}
  ]
}
```

Moods: `energetic`, `inspiring`, `tense`, `dramatic`, `chill`, `funny`.

## De onde tirar trilhas com segurança

- **YouTube Audio Library** (YouTube Studio → Biblioteca de áudio): baixe
  manualmente faixas liberadas para uso em vídeos. Algumas exigem atribuição —
  registre em `license`/`author`.
- Músicas que você comprou/licenciou ou produziu.
- CC0 / CC BY / CC BY-SA (Jamendo, ccMixter, Free Music Archive).

**Não use áudio extraído de vídeos do YouTube** (clipes, músicas, trilhas de
outros canais): Instagram, TikTok e YouTube reconhecem o áudio por Content ID
e o reel pode ser silenciado, bloqueado ou gerar strike.

Sem trilha local para o mood, o CENARIUS procura música CC no Openverse
(sem chave); sem nenhuma trilha válida, o vídeo sai só com voz e efeitos.
Os créditos de toda trilha usada vão para `assets-attribution.json`.
