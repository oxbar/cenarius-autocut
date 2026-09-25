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
b['max_events']=max(3, int(b.get('max_events',3) or 3))
z=cfg.setdefault('zoom', {})
z['enabled']=True
z.setdefault('mild',1.035); z.setdefault('punch',1.08); z.setdefault('min_gap',5.0); z.setdefault('duration',1.0)
p.write_text(json.dumps(cfg, ensure_ascii=False, indent=2)+"\n")
print('✓ modo visual habilitado em config.json')
print('✓ assets locais: ./assets/broll')
print('✓ cache gratuito Wikimedia: ./data/asset-cache')
print('Dica: CENARIUS_NO_REMOTE_ASSETS=1 desativa buscas externas e usa somente local/cache/cards.')
PY
