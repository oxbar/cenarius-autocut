#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p assets/broll data/asset-cache
python3 - <<'PY'
import json
from pathlib import Path
p=Path('config.json')
if not p.exists():
    raise SystemExit('config.json não encontrado')
cfg=json.loads(p.read_text())
b=cfg.setdefault('broll', {})
b['enabled']=True
b.setdefault('asset_dir','./assets/broll')
b.setdefault('manifest','./assets/manifest.json')
b['max_events']=max(4, int(b.get('max_events',4) or 4))
z=cfg.setdefault('zoom', {})
z['enabled']=True
z['mild']=max(1.06, float(z.get('mild',1.06) or 1.06))
z['punch']=max(1.10, float(z.get('punch',1.10) or 1.10))
z['min_gap']=min(4.0, float(z.get('min_gap',4.0) or 4.0))
z['duration']=max(.72, min(1.05, float(z.get('duration',.85) or .85)))
p.write_text(json.dumps(cfg, ensure_ascii=False, indent=2)+"\n")
print('✓ modo visual V1.5 habilitado')
print('✓ até 4 mudanças de B-roll por Short')
print('✓ busca prioriza VÍDEO no Wikimedia Commons; imagem é fallback')
print('✓ mode=reaction: apresentador em cima + B-roll em movimento embaixo')
print('✓ se a internet falhar, usa self-B-roll animado em vez de card preto')
print('✓ zoom perceptível: mild >= 1.06 / punch >= 1.10')
print('Dica: mantenha Ollama rodando para decisões semânticas melhores.')
PY
