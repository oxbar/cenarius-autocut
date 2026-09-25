package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/transcribe"
)

// DirectorResponse is the strict JSON contract asked from the LLM. The model
// never writes FFmpeg; it only returns editorial intent that Go validates.
type DirectorResponse struct {
	Hook         string          `json:"hook"`
	CTA          string          `json:"cta"`
	VisualEvents []directorEvent `json:"visual_events"`
	Zooms        []ZoomEvent     `json:"zooms"`
	Emphasis     []string        `json:"emphasis"`
	// Legacy (v1.5) answers are still understood.
	Overlays []OverlayEvent `json:"overlays"`
}

type directorEvent struct {
	UnitID     string   `json:"unit_id"`
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Type       string   `json:"type"`
	Concept    string   `json:"concept"`
	Queries    []string `json:"queries"`
	Query      string   `json:"query"`
	Layout     string   `json:"layout"`
	Importance float64  `json:"importance"`
	Reason     string   `json:"reason"`
}

const directorPrompt = `Você é o DIRETOR EDITORIAL de Reels/TikTok/Shorts em português brasileiro. Você decide INTENÇÃO editorial; não escreve comandos de vídeo.

O vídeo é um creator falando para a câmera (A-roll). Você decide quando inserir B-roll REAL (vídeo/foto de banco livre), cards, cutaways fullscreen e zooms, sempre com motivo semântico.

Responda SOMENTE com JSON válido, sem markdown, sem texto extra, exatamente neste formato:
{"hook":"frase do gancho","cta":"frase de CTA ou vazio",
 "visual_events":[{"unit_id":"u02","start":3.2,"end":5.6,"type":"broll","concept":"software development","queries":["software developer programming computer","programmer coding","source code on computer screen"],"layout":"reaction","importance":0.82,"reason":"ilustra diretamente a explicação"}],
 "zooms":[{"start":0.0,"end":0.9,"kind":"punch_zoom","scale":1.08,"reason":"gancho"}],
 "emphasis":["PALAVRA"]}

REGRAS:
- Analise cada unidade com a anterior e a seguinte: gancho, explicação, exemplo, punchline, mudança de ideia, CTA, emoção.
- Vídeo de 20-30s: 3 a 5 visual_events no total. Para >30s, escale gradualmente (~1 insert a cada 7-10s), no máximo 12. Não deixe longos trechos estáticos quando houver ideia visualizável.
- visual_events NÃO podem se sobrepor. Deixe pelo menos 0.4s de A-roll entre eles.
- O primeiro segundo é do creator (sem B-roll antes de 1.2s); use punch_zoom 1.07-1.10 no gancho.
- type: "broll" (vídeo/foto contextual), "card" (logo, print, interface, entidade), "pip" (imagem pequena).
- layout: "reaction" (creator em cima, B-roll embaixo — preferido), "fullscreen" (cutaway de 1.2-2.5s, bom para punchline), "card", "pip".
- Duração: reaction 1.6-3.0s; fullscreen 1.2-2.5s; card 1.6-2.8s.
- queries: 2 a 4 buscas CONCRETAS em inglês, do mais específico ao mais genérico, que existam como foto/vídeo em banco livre (ex.: "programmer coding", "data center servers"). Nunca abstratas ("sucesso", "futuro"). Não invente fatos nem pessoas.
- concept: nome curto em inglês do que será mostrado.
- importance 0-1: conceito central da fala > menção de passagem.
- zooms: kind "punch_zoom" (ênfase, 1.08-1.12), "slow_push" (explicação longa, 1.055-1.07), "reframe" (mudança de ideia, 1.06-1.08). No máximo 4. Não coloque zoom dentro de fullscreen.
- emphasis: no máximo 3 palavras que devem ser destacadas na legenda.
- Use os tempos exatos das palavras.

UNIDADES SEMÂNTICAS (id, início, fim, papel sugerido, conceitos detectados, texto):
%s

PALAVRAS COM TIMESTAMPS (w=palavra, t=início, e=fim, segundos):
%s`

var thinkRE = regexp.MustCompile(`(?s)<think>.*?</think>`)

// WithOllama asks the local LLM to act as editorial director. Any failure
// (disabled, offline, HTTP error, invalid JSON, empty/invalid events) falls
// back to the heuristic plan; the pipeline is never interrupted.
func WithOllama(ctx context.Context, base Plan, tr transcribe.Transcript, cfg config.Config) Plan {
	logger := logx.From(ctx)
	if !cfg.Ollama.Enabled {
		logger.Info("planner.fallback", "reason", "ollama disabled", "using", "heuristic")
		return base
	}
	units := base.SemanticUnits
	if len(units) == 0 {
		units = BuildUnits(tr)
	}
	prompt := fmt.Sprintf(directorPrompt, unitsForPrompt(units), compactTranscriptJSON(tr))
	body := map[string]any{
		"model": cfg.Ollama.Model, "prompt": prompt, "stream": false, "format": "json", "think": false,
		"options": map[string]any{"temperature": 0.3, "num_ctx": 8192},
	}
	reqBody, _ := json.Marshal(body)
	logger.Info("planner.ollama.request", "model", cfg.Ollama.Model, "url", cfg.Ollama.URL, "units", len(units), "prompt_chars", len(prompt))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		logger.Warn("planner.fallback", "reason", err, "using", "heuristic")
		return base
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Minute}
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("planner.fallback", "reason", "ollama unavailable", "error", err, "duration_ms", time.Since(started).Milliseconds(), "using", "heuristic")
		return base
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		logger.Warn("planner.fallback", "reason", "ollama http "+resp.Status, "body", string(rb), "using", "heuristic")
		return base
	}
	var wrap struct {
		Response string `json:"response"`
	}
	if json.Unmarshal(rb, &wrap) != nil {
		logger.Warn("planner.ollama.invalid", "reason", "invalid wrapper")
		logger.Warn("planner.fallback", "reason", "ollama invalid wrapper", "using", "heuristic")
		return base
	}
	logger.Info("planner.ollama.response", "duration_ms", time.Since(started).Milliseconds(), "chars", len(wrap.Response))
	logger.Debug("planner.ollama.raw", "response", wrap.Response)

	ai, err := ParseDirector(wrap.Response, tr, units, cfg)
	if err != nil {
		logger.Warn("planner.ollama.invalid", "error", err)
		logger.Warn("planner.fallback", "reason", "ollama invalid plan", "using", "heuristic")
		return base
	}
	merged := base
	merged.VisualEvents = append(append([]VisualEvent(nil), ai.VisualEvents...), base.VisualEvents...)
	merged.Emphasis = append(append([]string(nil), ai.Emphasis...), base.Emphasis...)
	if ai.Hook != "" {
		merged.Hook = ai.Hook
	}
	if ai.CTA != "" {
		merged.CTA = ai.CTA
	}
	out := Finalize(ctx, merged, transcriptEnd(tr), cfg, ai.Zooms)
	logger.Info("planner.ollama.ok", "ai_events", len(ai.VisualEvents), "ai_zooms", len(ai.Zooms), "final_events", len(out.VisualEvents), "final_zooms", len(out.Zooms))
	return out
}

// ParseDirector parses and validates the LLM answer. It returns an error when
// nothing usable survives validation.
func ParseDirector(raw string, tr transcribe.Transcript, units []SemanticUnit, cfg config.Config) (Plan, error) {
	s := strings.TrimSpace(thinkRE.ReplaceAllString(raw, ""))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
	s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	var d DirectorResponse
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&d); err != nil {
		return Plan{}, fmt.Errorf("json inválido: %w", err)
	}
	dur := transcriptEnd(tr)
	if dur <= 0 {
		return Plan{}, fmt.Errorf("transcrição vazia")
	}
	p := Plan{Hook: strings.TrimSpace(d.Hook), CTA: strings.TrimSpace(d.CTA)}
	for _, o := range d.Overlays {
		d.VisualEvents = append(d.VisualEvents, directorEvent{Start: o.Start, End: o.End, Concept: o.Keyword, Query: o.Query, Layout: o.Mode, Reason: o.Reason, Importance: 0.65})
	}
	wordStarts := make([]float64, 0, len(tr.Tokens))
	for _, t := range tr.Tokens {
		wordStarts = append(wordStarts, t.Start)
	}
	for _, e := range d.VisualEvents {
		ve, ok := validateDirectorEvent(e, dur, wordStarts, units)
		if ok {
			p.VisualEvents = append(p.VisualEvents, ve)
		}
	}
	for _, z := range d.Zooms {
		if math.IsNaN(z.Start) || math.IsNaN(z.End) || z.End <= z.Start || z.Start < 0 || z.Start > dur {
			continue
		}
		switch z.Kind {
		case ZoomPunch, ZoomSlowPush, ZoomReframe:
		case "", "punch", "zoom":
			z.Kind = ZoomPunch
		case "push", "slow push", "slow_push_in":
			z.Kind = ZoomSlowPush
		default:
			continue
		}
		if z.End-z.Start > 3.5 {
			z.End = z.Start + 3.5
		}
		z.Scale = ClampZoomScale(z.Kind, z.Scale)
		if z.Reason == "" {
			z.Reason = "diretor"
		}
		p.Zooms = append(p.Zooms, z)
		if len(p.Zooms) >= 4 {
			break
		}
	}
	for _, w := range d.Emphasis {
		w = strings.TrimSpace(w)
		if w != "" && len([]rune(w)) <= 30 && len(p.Emphasis) < 3 {
			p.Emphasis = appendUnique(p.Emphasis, w)
		}
	}
	if len(p.VisualEvents) == 0 && len(p.Zooms) == 0 {
		return Plan{}, fmt.Errorf("nenhum visual_event/zoom válido (recebidos %d eventos)", len(d.VisualEvents))
	}
	return p, nil
}

var letterRE = regexp.MustCompile(`\pL{3,}`)

func validateDirectorEvent(e directorEvent, dur float64, wordStarts []float64, units []SemanticUnit) (VisualEvent, bool) {
	if math.IsNaN(e.Start) || math.IsNaN(e.End) || e.End <= e.Start || e.Start < 0 || e.Start >= dur {
		return VisualEvent{}, false
	}
	layout := strings.ToLower(strings.TrimSpace(e.Layout))
	switch layout {
	case LayoutReaction, LayoutFullscreen, LayoutCard, LayoutPIP:
	case "bottom", "split", "split_screen", "split-screen", "":
		layout = LayoutReaction
	case "cutaway", "full":
		layout = LayoutFullscreen
	default:
		return VisualEvent{}, false
	}
	typ := strings.ToLower(strings.TrimSpace(e.Type))
	switch {
	case layout == LayoutCard:
		typ = TypeCard
	case layout == LayoutPIP:
		typ = TypePIP
	case typ != TypeBroll && typ != TypeCard && typ != TypeMotion:
		typ = TypeBroll
	}
	var qs []string
	seen := map[string]bool{}
	addQ := func(q string) {
		q = strings.Join(strings.Fields(q), " ")
		k := strings.ToLower(q)
		if q == "" || len(q) > 90 || seen[k] || !letterRE.MatchString(q) || len(qs) >= 5 {
			return
		}
		seen[k] = true
		qs = append(qs, q)
	}
	for _, q := range e.Queries {
		addQ(q)
	}
	addQ(e.Query)
	concept := strings.TrimSpace(e.Concept)
	label := ""
	if c, ok := ConceptByName(concept); ok {
		label = c.Label
		for _, q := range c.Queries {
			addQ(q)
		}
	}
	if len(qs) == 0 {
		addQ(concept)
	}
	if len(qs) == 0 {
		return VisualEvent{}, false
	}
	if concept == "" {
		concept = qs[0]
	}
	start := snap(e.Start, wordStarts, 0.3)
	if start < 1.2 && dur > 4 {
		start = 1.2
	}
	end := math.Min(e.End, dur)
	maxLen := maxLenByLayout[layout]
	if end-start > maxLen {
		end = start + maxLen
	}
	if end-start < MinEventDuration {
		return VisualEvent{}, false
	}
	imp := e.Importance
	if imp <= 0 || math.IsNaN(imp) {
		imp = 0.7
	}
	imp = clamp(imp+0.05, 0, 1) // director bonus over lexicon heuristics
	unit := e.UnitID
	role := ""
	for _, u := range units {
		if (unit != "" && u.ID == unit) || (unit == "" && start >= u.Start-0.3 && start <= u.End) {
			unit, role = u.ID, u.Role
			if label == "" && len(u.Concepts) > 0 {
				if c, ok := ConceptByName(u.Concepts[0].Name); ok {
					label = c.Label
				}
			}
			break
		}
	}
	if label == "" {
		label = strings.ToUpper(concept)
	}
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "decisão do diretor"
	}
	return VisualEvent{
		Start: round3(start), End: round3(end), Type: typ, Concept: concept, Label: label, Queries: qs,
		Layout: layout, Importance: round3(imp), Relevance: round3(imp), Role: role, UnitID: unit,
		Origin: "ollama", Reason: reason,
	}, true
}

func snap(t float64, starts []float64, tol float64) float64 {
	best, bd := t, tol
	for _, s := range starts {
		if d := math.Abs(s - t); d < bd {
			best, bd = s, d
		}
	}
	return best
}

func unitsForPrompt(units []SemanticUnit) string {
	var b strings.Builder
	for _, u := range units {
		names := make([]string, 0, len(u.Concepts))
		for _, c := range u.Concepts {
			names = append(names, c.Name)
		}
		fmt.Fprintf(&b, "%s [%.2f-%.2f] %s {%s}: %s\n", u.ID, u.Start, u.End, u.Role, strings.Join(names, ", "), u.Text)
	}
	return b.String()
}

func compactTranscriptJSON(tr transcribe.Transcript) string {
	type word struct {
		W string  `json:"w"`
		T float64 `json:"t"`
		E float64 `json:"e"`
	}
	words := make([]word, 0, len(tr.Tokens))
	for _, t := range tr.Tokens {
		w := strings.TrimSpace(t.Text)
		if w == "" {
			continue
		}
		words = append(words, word{W: w, T: round3(t.Start), E: round3(t.End)})
	}
	b, _ := json.Marshal(words)
	return string(b)
}

func DebugJSON(p Plan) string { b, _ := json.MarshalIndent(p, "", "  "); return string(b) }
