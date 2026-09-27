package shorts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/transcribe"
)

type Scores struct {
	Hook         int `json:"hook"`
	Engagement   int `json:"engagement"`
	Value        int `json:"value"`
	Shareability int `json:"shareability"`
	Total        int `json:"total"`
}

type Candidate struct {
	ID       string  `json:"id"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Duration float64 `json:"duration"`
	Title    string  `json:"title"`
	Text     string  `json:"text"`
	HookType string  `json:"hook_type"`
	Reason   string  `json:"reason"`
	Scores   Scores  `json:"scores"`
}

type ollamaClip struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	HookScore         int    `json:"hook_score"`
	EngagementScore   int    `json:"engagement_score"`
	ValueScore        int    `json:"value_score"`
	ShareabilityScore int    `json:"shareability_score"`
	HookType          string `json:"hook_type"`
	Reason            string `json:"reason"`
}

type ollamaResponse struct {
	Clips []ollamaClip `json:"clips"`
}

var (
	strongHookRE = regexp.MustCompile(`(?i)\b(por que|como|ningu[eé]m|nunca|verdade|problema|erro|segredo|isso|aten[cç][aã]o|pare de|voc[eê]|ia|intelig[eê]ncia artificial)\b`)
	contrastRE   = regexp.MustCompile(`(?i)\b(mas|por[eé]m|s[oó] que|ao contr[aá]rio|enquanto|em vez de|n[aã]o.*mas)\b`)
	fillerRE     = regexp.MustCompile(`(?i)^\s*(ent[aã]o|tipo|assim|basicamente|bom|cara|galera)\b`)
)

// BuildCandidates creates grounded clip windows strictly from semantic-unit
// boundaries. The LLM may score these windows later, but it never invents a
// timestamp that did not come from Whisper.
func BuildCandidates(tr transcribe.Transcript, sc config.ShortsConfig, sourceDuration float64) []Candidate {
	units := planner.BuildUnits(tr)
	if len(units) == 0 {
		return nil
	}
	normalizeShorts(&sc)
	var out []Candidate
	for i := range units {
		if units[i].Role == planner.RoleCTA {
			continue
		}
		bestAdded := 0
		for j := i; j < len(units); j++ {
			start := units[i].Start
			end := units[j].End
			d := end - start
			if d > sc.MaxDuration+0.001 {
				break
			}
			if d < sc.MinDuration {
				continue
			}
			// Prefer endings inside the desired 40-50s band. Outside the band,
			// keep at most one fallback per start so candidate volume stays sane.
			ideal := d >= sc.IdealMin && d <= sc.IdealMax
			if !ideal && bestAdded > 0 {
				continue
			}
			c := scoreWindow(units[i:j+1], start, end)
			out = append(out, c)
			bestAdded++
			if ideal && d >= (sc.IdealMin+sc.IdealMax)/2 {
				break
			}
		}
	}
	if len(out) == 0 && sourceDuration >= 15 {
		start := units[0].Start
		end := units[len(units)-1].End
		if sourceDuration > 0 && end > sourceDuration {
			end = sourceDuration
		}
		out = append(out, scoreWindow(units, start, end))
	}
	for i := range out {
		out[i].ID = fmt.Sprintf("clip-%03d", i+1)
	}
	return out
}

func normalizeShorts(sc *config.ShortsConfig) {
	if sc.MinDuration <= 0 {
		sc.MinDuration = 30
	}
	if sc.IdealMin < sc.MinDuration {
		sc.IdealMin = 40
	}
	if sc.IdealMax < sc.IdealMin {
		sc.IdealMax = 50
	}
	if sc.MaxDuration < sc.IdealMax {
		sc.MaxDuration = 55
	}
	if sc.MaxOverlap <= 0 || sc.MaxOverlap >= 1 {
		sc.MaxOverlap = .35
	}
}

func scoreWindow(units []planner.SemanticUnit, start, end float64) Candidate {
	texts := make([]string, 0, len(units))
	roles := map[string]int{}
	concepts := map[string]bool{}
	for _, u := range units {
		texts = append(texts, strings.TrimSpace(u.Text))
		roles[u.Role]++
		for _, c := range u.Concepts {
			concepts[c.Name] = true
		}
	}
	text := strings.Join(texts, " ")
	first := units[0]
	dur := end - start

	hook := 8
	switch first.Role {
	case planner.RoleHook:
		hook += 7
	case planner.RoleQuestion:
		hook += 8
	case planner.RoleTurn:
		hook += 5
	case planner.RolePunchline:
		hook += 6
	}
	if strings.Contains(first.Text, "?") {
		hook += 4
	}
	if strongHookRE.MatchString(first.Text) {
		hook += 4
	}
	if fillerRE.MatchString(first.Text) {
		hook -= 4
	}

	eng := 8 + minInt(5, len(units)/2)
	eng += minInt(4, roles[planner.RoleQuestion]*3)
	eng += minInt(5, roles[planner.RolePunchline]*3)
	eng += minInt(3, roles[planner.RoleTurn]*2)
	if dur >= 40 && dur <= 50 {
		eng += 4
	} else if dur >= 30 && dur <= 55 {
		eng += 2
	}

	value := 7
	value += minInt(7, roles[planner.RoleExplanation]*2)
	value += minInt(5, roles[planner.RoleExample]*3)
	value += minInt(5, len(concepts)*2)
	if len(strings.Fields(text)) >= 65 {
		value += 2
	}

	share := 6
	share += minInt(7, roles[planner.RolePunchline]*4)
	share += minInt(4, roles[planner.RoleQuestion]*2)
	if contrastRE.MatchString(text) {
		share += 5
	}
	if strings.Contains(text, "!") {
		share += 2
	}

	hook, eng, value, share = clamp25(hook), clamp25(eng), clamp25(value), clamp25(share)
	scores := Scores{Hook: hook, Engagement: eng, Value: value, Shareability: share}
	scores.Total = hook + eng + value + share
	hookType := "statement"
	if strings.Contains(first.Text, "?") || first.Role == planner.RoleQuestion {
		hookType = "question"
	} else if contrastRE.MatchString(first.Text) {
		hookType = "contrast"
	}
	return Candidate{
		Start: round3(start), End: round3(end), Duration: round3(dur), Text: strings.TrimSpace(text),
		Title: titleFromText(first.Text), HookType: hookType, Scores: scores,
		Reason: reasonForScores(scores, roles),
	}
}

func Select(candidates []Candidate, count int, maxOverlap float64) []Candidate {
	if count <= 0 {
		count = 5
	}
	if maxOverlap <= 0 || maxOverlap >= 1 {
		maxOverlap = .35
	}
	ranked := append([]Candidate(nil), candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Scores.Total == ranked[j].Scores.Total {
			return distanceFrom45(ranked[i].Duration) < distanceFrom45(ranked[j].Duration)
		}
		return ranked[i].Scores.Total > ranked[j].Scores.Total
	})
	out := make([]Candidate, 0, count)
	for _, c := range ranked {
		tooSimilar := false
		for _, x := range out {
			if overlapRatio(c, x) > maxOverlap {
				tooSimilar = true
				break
			}
		}
		if tooSimilar {
			continue
		}
		out = append(out, c)
		if len(out) >= count {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scores.Total > out[j].Scores.Total })
	return out
}

// RankWithOllama only scores IDs generated by BuildCandidates. Invalid IDs or
// scores are ignored, preserving the grounded heuristic candidate.
func RankWithOllama(ctx context.Context, candidates []Candidate, cfg config.Config) []Candidate {
	if !cfg.Ollama.Enabled || !cfg.Shorts.UseOllamaRank || len(candidates) == 0 {
		return candidates
	}
	logger := logx.From(ctx)
	pool := append([]Candidate(nil), candidates...)
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Scores.Total > pool[j].Scores.Total })
	if len(pool) > 16 {
		pool = pool[:16]
	}
	type promptCandidate struct {
		ID       string  `json:"id"`
		Start    float64 `json:"start"`
		End      float64 `json:"end"`
		Duration float64 `json:"duration"`
		Text     string  `json:"text"`
	}
	pcs := make([]promptCandidate, len(pool))
	for i, c := range pool {
		pcs[i] = promptCandidate{c.ID, c.Start, c.End, c.Duration, c.Text}
	}
	cb, _ := json.Marshal(pcs)
	prompt := `Você seleciona cortes de vídeos longos para Shorts/Reels. Os timestamps abaixo são imutáveis e já foram validados pelo sistema. NÃO invente nem altere start/end. Apenas avalie os IDs existentes.
Pontue cada dimensão de 0 a 25: hook, engagement, value, shareability. O total deve ser a soma (0-100). Dê um título curto em pt-BR, hook_type (question|contrast|statement) e reason curto. Score é auxílio editorial, não previsão de viralização.
Responda SOMENTE JSON: {"clips":[{"id":"clip-001","title":"...","hook_score":20,"engagement_score":20,"value_score":20,"shareability_score":20,"hook_type":"statement","reason":"..."}]}
CANDIDATOS:
` + string(cb)
	body := map[string]any{"model": cfg.Ollama.Model, "prompt": prompt, "stream": false, "format": "json", "think": false, "options": map[string]any{"temperature": .2, "num_ctx": 8192}}
	bb, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/generate", bytes.NewReader(bb))
	if err != nil {
		return candidates
	}
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		logger.Warn("shorts.ollama.fallback", "error", err)
		return candidates
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode/100 != 2 {
		logger.Warn("shorts.ollama.fallback", "status", resp.Status, "body", string(rb))
		return candidates
	}
	var wrap struct {
		Response string `json:"response"`
	}
	if json.Unmarshal(rb, &wrap) != nil {
		return candidates
	}
	var ranked ollamaResponse
	raw := strings.TrimSpace(wrap.Response)
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```")
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	if json.Unmarshal([]byte(raw), &ranked) != nil {
		logger.Warn("shorts.ollama.fallback", "reason", "invalid json")
		return candidates
	}
	byID := map[string]ollamaClip{}
	for _, x := range ranked.Clips {
		if validScore(x.HookScore) && validScore(x.EngagementScore) && validScore(x.ValueScore) && validScore(x.ShareabilityScore) {
			byID[x.ID] = x
		}
	}
	out := append([]Candidate(nil), candidates...)
	for i := range out {
		x, ok := byID[out[i].ID]
		if !ok {
			continue
		}
		out[i].Scores = Scores{Hook: x.HookScore, Engagement: x.EngagementScore, Value: x.ValueScore, Shareability: x.ShareabilityScore}
		out[i].Scores.Total = x.HookScore + x.EngagementScore + x.ValueScore + x.ShareabilityScore
		if strings.TrimSpace(x.Title) != "" {
			out[i].Title = trimRunes(strings.TrimSpace(x.Title), 72)
		}
		if strings.TrimSpace(x.Reason) != "" {
			out[i].Reason = trimRunes(strings.TrimSpace(x.Reason), 220)
		}
		switch x.HookType {
		case "question", "contrast", "statement":
			out[i].HookType = x.HookType
		}
	}
	logger.Info("shorts.ollama.ok", "candidates", len(pool), "ranked", len(byID), "duration_ms", time.Since(started).Milliseconds())
	return out
}

func overlapRatio(a, b Candidate) float64 {
	lo := math.Max(a.Start, b.Start)
	hi := math.Min(a.End, b.End)
	if hi <= lo {
		return 0
	}
	denom := math.Min(a.Duration, b.Duration)
	if denom <= 0 {
		return 0
	}
	return (hi - lo) / denom
}

func titleFromText(s string) string {
	words := strings.Fields(strings.TrimSpace(s))
	if len(words) > 10 {
		words = words[:10]
	}
	t := strings.Trim(strings.Join(words, " "), " .,!?:;\"'")
	if t == "" {
		return "Corte selecionado"
	}
	return trimRunes(t, 72)
}

func reasonForScores(s Scores, roles map[string]int) string {
	parts := []string{}
	if s.Hook >= 18 {
		parts = append(parts, "abertura forte")
	}
	if roles[planner.RoleExample] > 0 {
		parts = append(parts, "exemplo concreto")
	}
	if roles[planner.RolePunchline] > 0 {
		parts = append(parts, "payoff/punchline")
	}
	if s.Value >= 18 {
		parts = append(parts, "boa densidade de valor")
	}
	if len(parts) == 0 {
		parts = append(parts, "trecho semanticamente completo")
	}
	return strings.Join(parts, ", ")
}

func validScore(v int) bool { return v >= 0 && v <= 25 }
func clamp25(v int) int {
	if v < 0 {
		return 0
	}
	if v > 25 {
		return 25
	}
	return v
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func distanceFrom45(x float64) float64 { return math.Abs(x - 45) }
func round3(x float64) float64         { return math.Round(x*1000) / 1000 }
func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
