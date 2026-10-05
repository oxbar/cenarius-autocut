package assembly

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
	"unicode"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/transcribe"
)

const directorPrompt = `Você é um EDITOR DE VÍDEO PROFISSIONAL de Reels/TikTok/Shorts. Você recebeu vários clipes brutos (peças de um quebra-cabeça) e um pedido do criador. Monte UM vídeo vertical coeso que cumpra o pedido.

PEDIDO DO CRIADOR:
%s

DURAÇÃO ALVO: cerca de %.0f segundos (pode ser menor se o conteúdo pedir; nunca muito maior).

CLIPES DISPONÍVEIS (id, duração, fala com tempos, o que aparece na tela, energia/movimento 0-1):
%s

COMO EDITAR:
- Escolha a ORDEM que conta melhor a história/piada: gancho forte primeiro, desenvolvimento, clímax/punchline, fechamento.
- Pode descartar clipes que não servem ao pedido e usar só um trecho de cada clipe (start/end em segundos do próprio clipe).
- Não corte frases no meio: use os tempos das frases.
- Texto na tela ("text") curto, em português, estilo meme/título quando fizer sentido (máx. 6 palavras). Não coloque texto em todo trecho.
- "sfx" opcional por trecho: whoosh, pop, hit, impact, riser, click. Use com moderação (impact/hit na punchline).
- "zoom": true para dar ênfase (punchline/reação).
- "keep_audio": false quando o áudio original atrapalha (ex.: ruído, fala irrelevante); true quando a fala importa.
- "music_mood": energetic, inspiring, tense, dramatic, chill ou funny.
- "captions": true se a fala dos clipes deve virar legenda.

Responda SOMENTE com JSON válido neste formato:
{"title":"título curto","music_mood":"funny","captions":true,"notes":"ideia da montagem em uma frase",
 "segments":[{"clip_id":"c02","start":0.0,"end":3.2,"role":"hook","text":"QUANDO O CÓDIGO COMPILA","text_pos":"top","sfx":"hit","zoom":false,"keep_audio":true,"reason":"gancho"}]}`

// Direct produces the cut: Ollama director first, heuristic editor as
// fallback. The result is always validated against the real clips.
func Direct(ctx context.Context, cfg config.Config, clips []Clip, opts Options) Plan {
	logger := logx.From(ctx)
	target := opts.Duration
	if target <= 0 {
		target = cfg.Assembly.DefaultDuration
	}
	if cfg.Ollama.Enabled {
		started := time.Now()
		raw, err := askDirector(ctx, cfg, clips, opts.Prompt, target)
		if err == nil {
			p, perr := ParseDirector(raw, clips, opts, target, cfg.Assembly.MaxSegment)
			if perr == nil {
				p.Origin = "ollama"
				logger.Info("assembly.director.ok", "segments", len(p.Segments), "duration_s", round2(p.Duration()), "duration_ms", time.Since(started).Milliseconds())
				return p
			}
			logger.Warn("assembly.director.invalid", "error", perr, "raw", truncate(raw, 600))
		} else {
			logger.Warn("assembly.director.unavailable", "error", err)
		}
	}
	p := Heuristic(clips, opts, target, cfg.Assembly.MaxSegment)
	logger.Info("assembly.director.heuristic", "segments", len(p.Segments), "duration_s", round2(p.Duration()))
	return p
}

func askDirector(ctx context.Context, cfg config.Config, clips []Clip, prompt string, target float64) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": cfg.Ollama.Model, "prompt": fmt.Sprintf(directorPrompt, prompt, target, clipsForPrompt(clips)),
		"stream": false, "format": "json", "think": false, "options": map[string]any{"temperature": 0.4, "num_ctx": 8192},
	})
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("ollama HTTP %s", resp.Status)
	}
	var wrap struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(rb, &wrap); err != nil {
		return "", err
	}
	return thinkRE.ReplaceAllString(wrap.Response, ""), nil
}

var thinkRE = regexp.MustCompile(`(?s)<think>.*?</think>`)

type phrase struct {
	T0   float64 `json:"t0"`
	T1   float64 `json:"t1"`
	Text string  `json:"texto"`
}

// Phrases splits a clip's words at punctuation and pauses, so the director
// can trim at sentence boundaries.
func Phrases(words []transcribe.Token) []phrase {
	var out []phrase
	var cur []transcribe.Token
	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, phrase{T0: round2(cur[0].Start), T1: round2(cur[len(cur)-1].End), Text: wordsText(cur)})
		cur = nil
	}
	for i, w := range words {
		cur = append(cur, w)
		t := strings.TrimSpace(w.Text)
		pause := i+1 < len(words) && words[i+1].Start-w.End > 0.45
		if strings.ContainsAny(t, ".!?") || pause || len(cur) >= 14 {
			flush()
		}
	}
	flush()
	return out
}

func clipsForPrompt(clips []Clip) string {
	type row struct {
		ID       string   `json:"id"`
		Name     string   `json:"arquivo"`
		Duration float64  `json:"duracao"`
		Phrases  []phrase `json:"frases,omitempty"`
		Visual   string   `json:"visual"`
		Tags     []string `json:"tags,omitempty"`
		Emotion  string   `json:"emocao,omitempty"`
		Energy   float64  `json:"energia"`
		Motion   float64  `json:"movimento"`
		Audio    bool     `json:"tem_audio"`
	}
	rows := make([]row, 0, len(clips))
	for _, c := range clips {
		ph := Phrases(c.Words)
		if len(ph) > 10 {
			ph = ph[:10]
		}
		rows = append(rows, row{c.ID, c.Name, round2(c.Duration), ph, c.Visual, c.Tags, c.Emotion, round2(c.Energy), round2(c.Motion), c.HasAudio})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

type directorJSON struct {
	Title     string    `json:"title"`
	MusicMood string    `json:"music_mood"`
	Captions  *bool     `json:"captions"`
	Notes     string    `json:"notes"`
	Segments  []Segment `json:"segments"`
}

// ParseDirector validates the LLM cut against the real clips.
func ParseDirector(raw string, clips []Clip, opts Options, target, maxSeg float64) (Plan, error) {
	var d directorJSON
	if err := json.Unmarshal([]byte(stripFences(raw)), &d); err != nil {
		return Plan{}, fmt.Errorf("JSON inválido: %w", err)
	}
	p := Plan{Title: cleanText(d.Title, 60), Prompt: opts.Prompt, MusicMood: strings.ToLower(strings.TrimSpace(d.MusicMood)), Notes: cleanText(d.Notes, 200), Segments: d.Segments, Clips: clips}
	if d.Captions != nil {
		p.Captions = *d.Captions
	} else {
		p.Captions = anySpeech(clips)
	}
	if opts.Captions != nil {
		p.Captions = *opts.Captions
	}
	if err := Validate(&p, clips, target, maxSeg); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// Validate repairs and bounds a plan: unknown clips and impossible ranges
// are dropped, cuts snap to word boundaries, texts/roles/sfx are sanitized
// and the total is kept near the target.
func Validate(p *Plan, clips []Clip, target, maxSeg float64) error {
	byID := map[string]Clip{}
	for _, c := range clips {
		byID[c.ID] = c
	}
	if maxSeg <= 0 {
		maxSeg = 8
	}
	var out []Segment
	for _, s := range p.Segments {
		c, ok := byID[strings.TrimSpace(s.ClipID)]
		if !ok || math.IsNaN(s.Start) || math.IsNaN(s.End) {
			continue
		}
		s.ClipID = c.ID
		s.Start = math.Max(0, math.Min(s.Start, c.Duration-0.5))
		if s.End <= s.Start || s.End > c.Duration {
			s.End = math.Min(c.Duration, s.Start+maxSeg)
		}
		s.Start, s.End = snapToWords(c.Words, s.Start, s.End, c.Duration)
		if s.End-s.Start > maxSeg*1.5 {
			s.End = s.Start + maxSeg*1.5
			_, s.End = snapToWords(c.Words, s.Start, s.End, c.Duration)
		}
		if s.End-s.Start < 0.6 || overlapsUsed(out, s) {
			continue
		}
		s.Role = strings.ToLower(strings.TrimSpace(s.Role))
		if !validRoles[s.Role] {
			s.Role = ""
		}
		s.Text = cleanText(s.Text, 60)
		s.TextPos = strings.ToLower(strings.TrimSpace(s.TextPos))
		if !validPos[s.TextPos] {
			s.TextPos = "top"
		}
		s.SFX = strings.ToLower(strings.TrimSpace(s.SFX))
		if !validSFX[s.SFX] {
			s.SFX = ""
		}
		if !c.HasAudio {
			s.KeepAudio = false
		}
		s.Start, s.End = round3(s.Start), round3(s.End)
		s.Reason = cleanText(s.Reason, 160)
		out = append(out, s)
		if len(out) >= 40 {
			break
		}
	}
	if len(out) == 0 {
		return fmt.Errorf("nenhum trecho válido")
	}
	p.Segments = fitDuration(out, target)
	if p.Segments[0].Role == "" {
		p.Segments[0].Role = "hook"
	}
	if !planner.IsMood(p.MusicMood) {
		p.MusicMood = moodForPrompt(p.Prompt)
	}
	if p.Title == "" {
		p.Title = headline(p.Prompt)
	}
	return nil
}

// fitDuration keeps the timeline within ~25% above the target by trimming
// the tails of the longest segments, then dropping trailing ones.
func fitDuration(segs []Segment, target float64) []Segment {
	if target <= 0 {
		return segs
	}
	limit := target * 1.25
	total := func() float64 {
		d := 0.0
		for _, s := range segs {
			d += s.Duration()
		}
		return d
	}
	for iter := 0; total() > limit && iter < 200; iter++ {
		longest := 0
		for i := range segs {
			if segs[i].Duration() > segs[longest].Duration() {
				longest = i
			}
		}
		if segs[longest].Duration() <= 1.5 {
			break
		}
		segs[longest].End = round3(segs[longest].End - math.Min(0.5, segs[longest].Duration()-1.5))
	}
	for total() > limit && len(segs) > 1 {
		segs = segs[:len(segs)-1]
	}
	return segs
}

func overlapsUsed(used []Segment, s Segment) bool {
	for _, u := range used {
		if u.ClipID != s.ClipID {
			continue
		}
		ov := math.Min(u.End, s.End) - math.Max(u.Start, s.Start)
		if ov > 0.5*math.Min(u.Duration(), s.End-s.Start) {
			return true
		}
	}
	return false
}

// snapToWords moves cut points out of the middle of words (±0.45 s).
func snapToWords(words []transcribe.Token, start, end, dur float64) (float64, float64) {
	for _, w := range words {
		if start > w.Start && start < w.End && start-w.Start < 0.45 {
			start = math.Max(0, w.Start-0.05)
		}
		if end > w.Start && end < w.End && w.End-end < 0.45 {
			end = math.Min(dur, w.End+0.08)
		}
	}
	return start, end
}

// Heuristic is the fallback editor (no LLM): keeps the upload order as the
// narrative, drops clips unrelated to the prompt when there is too much
// material, takes the best window of each clip, opens with a headline and
// lands the last piece with an impact + zoom.
func Heuristic(clips []Clip, opts Options, target, maxSeg float64) Plan {
	p := Plan{Prompt: opts.Prompt, Origin: "heuristic", Clips: clips, Title: headline(opts.Prompt), MusicMood: moodForPrompt(opts.Prompt)}
	if target <= 0 {
		target = 30
	}
	if maxSeg <= 0 {
		maxSeg = 8
	}
	use := append([]Clip(nil), clips...)
	total := 0.0
	for _, c := range use {
		total += c.Duration
	}
	if total > target*1.25 && len(use) > 1 {
		type sc struct {
			c Clip
			r float64
		}
		scored := make([]sc, 0, len(use))
		anyRel := false
		for _, c := range use {
			r := promptRelevance(opts.Prompt, c)
			anyRel = anyRel || r > 0
			scored = append(scored, sc{c, r})
		}
		if anyRel {
			use = use[:0]
			for _, x := range scored {
				if x.r > 0 {
					use = append(use, x.c)
				}
			}
		}
	}
	share := math.Max(1.5, math.Min(maxSeg, target/float64(len(use))))
	for i, c := range use {
		start, end := bestWindow(c, share)
		s := Segment{ClipID: c.ID, Start: round3(start), End: round3(end), KeepAudio: c.HasAudio, Role: "build", TextPos: "top", Reason: "ordem de upload (editor heurístico)"}
		switch {
		case i == 0:
			s.Role, s.Text, s.SFX, s.Reason = "hook", p.Title, "hit", "gancho: abre com o título do pedido"
		case i == len(use)-1 && len(use) > 2:
			s.Role, s.SFX, s.Zoom, s.Reason = "punchline", "impact", true, "fechamento com impacto"
		default:
			s.SFX = "whoosh"
		}
		p.Segments = append(p.Segments, s)
	}
	p.Captions = anySpeech(use)
	if opts.Captions != nil {
		p.Captions = *opts.Captions
	}
	_ = Validate(&p, clips, target, maxSeg)
	return p
}

// bestWindow picks the most useful piece of a clip: the spoken part when
// there is speech (whole phrases), the middle otherwise.
func bestWindow(c Clip, length float64) (float64, float64) {
	if c.Duration <= length {
		return 0, c.Duration
	}
	if len(c.Words) > 0 {
		start := math.Max(0, c.Words[0].Start-0.12)
		end := start + length
		for _, ph := range Phrases(c.Words) {
			if ph.T1 <= start+length+0.4 {
				end = math.Max(end, math.Min(ph.T1+0.1, start+length+0.4))
			}
		}
		return start, math.Min(c.Duration, end)
	}
	start := (c.Duration - length) / 2
	return start, start + length
}

var stop = map[string]bool{"de": true, "da": true, "do": true, "das": true, "dos": true, "a": true, "o": true, "as": true, "os": true, "e": true, "um": true, "uma": true, "com": true, "para": true, "pra": true, "que": true, "em": true, "no": true, "na": true, "video": true, "vídeo": true, "faça": true, "crie": true, "quero": true, "meme": true, "sobre": true}

func tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := f[:0]
	for _, w := range f {
		if len([]rune(w)) >= 3 && !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

func promptRelevance(prompt string, c Clip) float64 {
	q := tokens(prompt)
	if len(q) == 0 {
		return 0
	}
	hay := map[string]bool{}
	for _, w := range tokens(c.Transcript + " " + c.Visual + " " + strings.Join(c.Tags, " ") + " " + c.Name) {
		hay[w] = true
	}
	hit := 0
	for _, w := range q {
		if hay[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(q))
}

func moodForPrompt(prompt string) string {
	low := strings.ToLower(prompt)
	for _, k := range []string{"meme", "engraçad", "zoeira", "piada", "humor", "comédia", "kkk", "rir"} {
		if strings.Contains(low, k) {
			return planner.MoodFunny
		}
	}
	units := []planner.SemanticUnit{{Text: prompt, Role: planner.RolePunchline}}
	m, _ := planner.DetectMood(units)
	return m
}

var quotedRE = regexp.MustCompile(`["“]([^"”]{3,60})["”]`)

// headline derives an on-screen title from the prompt: quoted text wins,
// otherwise the first words of the request (instructions removed).
func headline(prompt string) string {
	if m := quotedRE.FindStringSubmatch(prompt); m != nil {
		return cleanText(m[1], 42)
	}
	p := strings.TrimSpace(prompt)
	for _, pre := range []string{"faça um meme", "faça um vídeo", "crie um vídeo", "crie um meme", "monte um vídeo", "quero um vídeo", "faça", "crie", "monte", "quero"} {
		if strings.HasPrefix(strings.ToLower(p), pre) {
			p = strings.TrimSpace(p[len(pre):])
			break
		}
	}
	p = strings.TrimLeft(p, ":,.- ")
	for _, pre := range []string{"sobre ", "de ", "com "} {
		if strings.HasPrefix(strings.ToLower(p), pre) {
			p = p[len(pre):]
			break
		}
	}
	words := strings.Fields(p)
	if len(words) > 6 {
		words = words[:6]
	}
	return cleanText(strings.Join(words, " "), 42)
}

func cleanText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.NewReplacer("{", "", "}", "", "\\", "").Replace(s)), " ")
	r := []rune(s)
	if len(r) > max {
		s = strings.TrimSpace(string(r[:max]))
	}
	return s
}

func anySpeech(clips []Clip) bool {
	for _, c := range clips {
		if len(c.Words) >= 3 {
			return true
		}
	}
	return false
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
