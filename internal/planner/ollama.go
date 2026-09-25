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
	transcriptJSON := compactTranscriptJSON(tr)
	prompt := `Você é um diretor e editor profissional de Reels/Shorts. Seu trabalho NÃO é só colocar legendas: você cria ritmo visual como um editor humano, usando o apresentador como A-roll e inserindo B-roll real quando ele aumenta compreensão/retenção.

Devolva SOMENTE JSON válido:
{"zooms":[{"start":0.0,"end":0.8,"scale":1.07,"reason":"gancho"}],"overlays":[{"start":2.0,"end":4.5,"keyword":"programação","query":"software developer coding computer screen","position":"bottom","mode":"reaction","reason":"B-roll contextual"}],"sfx":[{"time":2.0,"name":"whoosh","gain_db":-24,"reason":"entrada B-roll"}],"emphasis":["PALAVRA"]}.

REGRAS DE DIREÇÃO:
- Vídeo de 20-30s deve ter normalmente 2-4 mudanças visuais relevantes, espaçadas ~3-7s, se a fala permitir.
- Não invente fatos. Você PODE escolher B-roll atmosférico que represente um conceito falado (ex.: tecnologia -> software developer coding; mundo + tecnologia -> global digital network).
- O primeiro segundo precisa de um punch zoom perceptível (1.06-1.10), sem exagero.
- Use mode=reaction para colocar B-roll em movimento na metade inferior enquanto o apresentador continua visível em cima, estilo react/podcast. Esse é o modo preferido.
- Use mode=fullscreen por 1.0-1.8s para uma cutaway forte.
- Use mode=card apenas para logo, print/interface ou entidade que funcione melhor como card.
- query deve descrever IMAGENS/VÍDEOS concretos pesquisáveis, de preferência em inglês. Nunca use query abstrata.
- Não repita a mesma query/keyword.
- Em fala genérica, prefira cenas de computador, programação, smartphone, data center, rede digital, creator gravando, conforme o trecho realmente disser.
- SFX: whoosh em reaction/fullscreen; pop em card. No máximo 4, ganho -30 a -18dB.
- Zoom: 2-4 no máximo. Em punchline/mudança, 1.08-1.12. Em transição suave, 1.04-1.07.
- overlay deve durar 1.4-3.0s.
- Não deixe 8+ segundos sem alguma mudança visual se houver uma ideia visualizável.
- Destaque no máximo 3 palavras importantes.

A transcrição abaixo traz palavras com timestamps em segundos. Use esses tempos com precisão:
` + transcriptJSON
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
	logger.Debug("planner.ollama.raw", "response", wrap.Response)
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
	// AI is the director. When it produced visual events, place them first and
	// use heuristics only to fill remaining gaps. This avoids a generic heuristic
	// overlay blocking a much better semantic B-roll decision at the same time.
	out := Plan{}
	for _, z := range ai.Zooms {
		if !zoomNear(z, out.Zooms, .75) {
			out.Zooms = append(out.Zooms, z)
		}
	}
	for _, z := range base.Zooms {
		if len(out.Zooms) >= 4 {
			break
		}
		if !zoomNear(z, out.Zooms, .75) {
			out.Zooms = append(out.Zooms, z)
		}
	}
	for _, o := range ai.Overlays {
		if len(out.Overlays) >= cfg.Broll.MaxEvents {
			break
		}
		if !overlayNear(o, out.Overlays, 1.1) {
			out.Overlays = append(out.Overlays, o)
		}
	}
	for _, o := range base.Overlays {
		if len(out.Overlays) >= cfg.Broll.MaxEvents {
			break
		}
		if !overlayNear(o, out.Overlays, 1.1) {
			out.Overlays = append(out.Overlays, o)
		}
	}
	for _, s := range ai.SFX {
		if len(out.SFX) >= 4 {
			break
		}
		if !sfxNear(s, out.SFX, .35) {
			out.SFX = append(out.SFX, s)
		}
	}
	for _, s := range base.SFX {
		if len(out.SFX) >= 4 {
			break
		}
		if !sfxNear(s, out.SFX, .35) {
			out.SFX = append(out.SFX, s)
		}
	}
	out.Emphasis = append(out.Emphasis, ai.Emphasis...)
	for _, e := range base.Emphasis {
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
		if o.Mode != "bottom" && o.Mode != "reaction" && o.Mode != "fullscreen" && o.Mode != "card" {
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
		if len(ss) >= 4 {
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

func compactTranscriptJSON(tr transcribe.Transcript) string {
	type word struct {
		T float64 `json:"t"`
		E float64 `json:"e"`
		W string  `json:"w"`
	}
	words := make([]word, 0, len(tr.Tokens))
	for _, t := range tr.Tokens {
		w := strings.TrimSpace(t.Text)
		if w == "" {
			continue
		}
		words = append(words, word{T: t.Start, E: t.End, W: w})
	}
	b, _ := json.Marshal(words)
	return string(b)
}

func DebugJSON(p Plan) string { b, _ := json.MarshalIndent(p, "", "  "); return fmt.Sprintf("%s", b) }
