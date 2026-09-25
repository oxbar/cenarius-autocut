package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

func WithOllama(ctx context.Context, base Plan, tr transcribe.Transcript, cfg config.Config) Plan {
	if !cfg.Ollama.Enabled {
		return base
	}
	b, _ := json.Marshal(tr.Segments)
	prompt := `Você é um editor profissional de vídeos verticais estilo Shorts/Reels em português brasileiro. Analise a transcrição com timestamps e devolva SOMENTE JSON válido na forma {"zooms":[{"start":0.0,"end":1.0,"scale":1.04,"reason":"..."}],"overlays":[{"start":2.0,"end":4.0,"keyword":"termo visual concreto","position":"bottom","reason":"..."}],"emphasis":["PALAVRA"]}.

Regras editoriais:
- Seja conservador: 1 ou 2 zooms em um vídeo de ~20s; nunca zoom a cada frase.
- Zoom apenas em gancho, mudança clara de ideia, reação ou punchline.
- No máximo 3 overlays, sem repetir o mesmo assunto.
- Overlay apenas quando houver algo visual concreto: pessoa, empresa, app, produto, site, linguagem, ferramenta ou evento.
- NÃO use abreviações genéricas como "ia" como overlay se não houver um objeto visual específico.
- Não crie overlay só porque uma palavra contém letras de outra palavra.
- Preserve pausas que dão ritmo ou humor.
- Destaque no máximo 3 palavras realmente importantes.
- Não invente fatos ou entidades que não aparecem na transcrição.
- O apresentador continua sendo o foco; B-roll serve para ilustrar, não para poluir.

Transcrição: ` + string(b)
	reqBody, _ := json.Marshal(map[string]any{"model": cfg.Ollama.Model, "prompt": prompt, "stream": false, "format": "json"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return base
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return base
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return base
	}
	rb, _ := io.ReadAll(resp.Body)
	var wrap struct {
		Response string `json:"response"`
	}
	if json.Unmarshal(rb, &wrap) != nil {
		return base
	}
	var p Plan
	if json.Unmarshal([]byte(wrap.Response), &p) != nil {
		return base
	}
	sanitize(&p, tr, cfg)
	if len(p.Zooms) == 0 && len(p.Overlays) == 0 {
		return base
	}
	return p
}

func sanitize(p *Plan, tr transcribe.Transcript, cfg config.Config) {
	maxT := 0.0
	if len(tr.Segments) > 0 {
		maxT = tr.Segments[len(tr.Segments)-1].End
	}
	zs := p.Zooms[:0]
	for _, z := range p.Zooms {
		if len(zs) >= 3 {
			break
		}
		if z.Scale < 1.0 {
			z.Scale = 1.0
		}
		if z.Scale > 1.12 {
			z.Scale = 1.12
		}
		if z.Start < 0 {
			z.Start = 0
		}
		if z.End > maxT {
			z.End = maxT
		}
		if z.End > z.Start+0.15 {
			zs = append(zs, z)
		}
	}
	p.Zooms = zs
	os := p.Overlays[:0]
	seen := map[string]bool{}
	for _, o := range p.Overlays {
		o.Keyword = strings.TrimSpace(o.Keyword)
		if o.Start < 0 {
			o.Start = 0
		}
		if o.End > maxT {
			o.End = maxT
		}
		key := strings.ToLower(o.Keyword)
		if o.Keyword != "" && o.End > o.Start+0.2 && !seen[key] {
			os = append(os, o)
			seen[key] = true
			if len(os) >= cfg.Broll.MaxEvents {
				break
			}
		}
	}
	p.Overlays = os
}

func DebugJSON(p Plan) string { b, _ := json.MarshalIndent(p, "", "  "); return fmt.Sprintf("%s", b) }
