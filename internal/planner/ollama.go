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
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/transcribe"
)

func WithOllama(ctx context.Context, base Plan, tr transcribe.Transcript, cfg config.Config) Plan {
	logger := logx.From(ctx)
	if !cfg.Ollama.Enabled {
		logger.Info("planner.ollama.skip", "reason", "disabled")
		return base
	}
	b, _ := json.Marshal(tr.Segments)
	prompt := `Você é diretor/editor profissional de vídeos verticais estilo Shorts/Reels em português brasileiro. Analise a transcrição com timestamps e devolva SOMENTE JSON válido neste formato:
{"zooms":[{"start":0.0,"end":1.0,"scale":1.04,"reason":"..."}],"overlays":[{"start":2.0,"end":4.0,"keyword":"chatgpt","query":"ChatGPT OpenAI interface","position":"bottom","mode":"bottom","reason":"..."}],"sfx":[{"time":2.0,"name":"pop","gain_db":-22,"reason":"..."}],"emphasis":["PALAVRA"]}.

Regras editoriais:
- O apresentador é o foco. Efeitos existem para sustentar a história, não para poluir.
- Em ~20-30s: 1-3 zooms, 1-3 overlays e no máximo 3 SFX.
- Zoom apenas em gancho, mudança clara de ideia, reação ou punchline.
- Overlay apenas para algo realmente visual: pessoa, empresa, app, produto, site, linguagem, ferramenta, notícia, objeto ou conceito visual claro.
- query deve ser uma busca visual curta em inglês ou português, sem frases abstratas.
- mode permitido: bottom (imagem nos 40% inferiores), fullscreen (imagem cobre a tela por pouco tempo), card (imagem menor em card central/inferior).
- Prefira bottom/card. Use fullscreen por no máximo ~1.8s quando a imagem merece foco total.
- Não repita o mesmo assunto visual.
- Não use "ia" isoladamente se não houver uma referência visual concreta; prefira "artificial intelligence computer technology".
- SFX permitido: pop para entrada de card/overlay; whoosh para fullscreen ou mudança forte. Ganho entre -30 e -16 dB.
- Não invente fatos nem entidades ausentes da fala.
- Destaque no máximo 3 palavras realmente importantes.

Transcrição: ` + string(b)
	reqBody, _ := json.Marshal(map[string]any{"model": cfg.Ollama.Model, "prompt": prompt, "stream": false, "format": "json"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		logger.Warn("planner.ollama.error", "error", err)
		return base
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Minute}
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("planner.ollama.unavailable", "error", err, "duration_ms", time.Since(started).Milliseconds())
		return base
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		logger.Warn("planner.ollama.http_error", "status", resp.Status)
		return base
	}
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var wrap struct {
		Response string `json:"response"`
	}
	if json.Unmarshal(rb, &wrap) != nil {
		logger.Warn("planner.ollama.invalid_wrapper")
		return base
	}
	var p Plan
	if err := json.Unmarshal([]byte(wrap.Response), &p); err != nil {
		logger.Warn("planner.ollama.invalid_json", "error", err, "response", wrap.Response)
		return base
	}
	sanitize(&p, tr, cfg)
	logger.Info("planner.ollama.ok", "duration_ms", time.Since(started).Milliseconds(), "zooms", len(p.Zooms), "overlays", len(p.Overlays), "sfx", len(p.SFX))
	return mergePlans(base, p, cfg)
}

func mergePlans(base, ai Plan, cfg config.Config) Plan {
	out := base
	// AI events have priority, but preserve heuristic events when the model omitted
	// a useful category. De-duplication keeps the result restrained.
	for _, z := range ai.Zooms {
		if !zoomNear(z, out.Zooms, 1.0) {
			out.Zooms = append(out.Zooms, z)
		}
	}
	for _, o := range ai.Overlays {
		if !overlayNear(o, out.Overlays, 1.5) && len(out.Overlays) < cfg.Broll.MaxEvents {
			out.Overlays = append(out.Overlays, o)
		}
	}
	for _, s := range ai.SFX {
		if len(out.SFX) >= 3 {
			break
		}
		if !sfxNear(s, out.SFX, .45) {
			out.SFX = append(out.SFX, s)
		}
	}
	for _, e := range ai.Emphasis {
		out.Emphasis = appendUnique(out.Emphasis, e)
	}
	sanitize(&out, transcribe.Transcript{}, cfg)
	return out
}

func zoomNear(z ZoomEvent, xs []ZoomEvent, gap float64) bool {
	for _, x := range xs {
		if abs(x.Start-z.Start) < gap {
			return true
		}
	}
	return false
}
func overlayNear(o OverlayEvent, xs []OverlayEvent, gap float64) bool {
	for _, x := range xs {
		if strings.EqualFold(x.Keyword, o.Keyword) || abs(x.Start-o.Start) < gap {
			return true
		}
	}
	return false
}
func sfxNear(s SFXEvent, xs []SFXEvent, gap float64) bool {
	for _, x := range xs {
		if abs(x.Time-s.Time) < gap {
			return true
		}
	}
	return false
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func sanitize(p *Plan, tr transcribe.Transcript, cfg config.Config) {
	maxT := transcriptEnd(tr)
	if maxT <= 0 {
		// Called during merging where all base events have already been bounded.
		maxT = 60 * 60
	}
	zs := p.Zooms[:0]
	for _, z := range p.Zooms {
		if len(zs) >= 4 {
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
		o.Query = strings.TrimSpace(o.Query)
		if o.Query == "" {
			o.Query = o.Keyword
		}
		if o.Start < 0 {
			o.Start = 0
		}
		if o.End > maxT {
			o.End = maxT
		}
		if o.End-o.Start > 3.0 {
			o.End = o.Start + 3.0
		}
		if o.Mode != "bottom" && o.Mode != "fullscreen" && o.Mode != "card" {
			o.Mode = "bottom"
		}
		if o.Position != "top" && o.Position != "bottom" {
			o.Position = "bottom"
		}
		key := strings.ToLower(o.Keyword)
		if o.Keyword != "" && o.End > o.Start+0.45 && !seen[key] {
			os = append(os, o)
			seen[key] = true
			if len(os) >= cfg.Broll.MaxEvents {
				break
			}
		}
	}
	p.Overlays = os

	ss := p.SFX[:0]
	for _, s := range p.SFX {
		if len(ss) >= 3 {
			break
		}
		if s.Time < 0 || s.Time > maxT {
			continue
		}
		if s.Name != "pop" && s.Name != "whoosh" {
			continue
		}
		if s.GainDB == 0 {
			s.GainDB = -22
		}
		if s.GainDB < -35 {
			s.GainDB = -35
		}
		if s.GainDB > -14 {
			s.GainDB = -14
		}
		ss = append(ss, s)
	}
	p.SFX = ss
}

func DebugJSON(p Plan) string { b, _ := json.MarshalIndent(p, "", "  "); return fmt.Sprintf("%s", b) }
