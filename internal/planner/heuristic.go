package planner

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/transcribe"
)

var punchRE = regexp.MustCompile(`(?i)\b(mas|só que|so que|detalhe|problema|produção|producao|bug|quebrou|erro|ninguém|ninguem|nunca|sexta-feira|sexta feira|resultado|verdade|absurdo|surreal|simplesmente)\b`)

var roleWeight = map[string]float64{
	RoleHook: 0.05, RoleExplanation: 0.12, RoleExample: 0.18, RolePunchline: 0.15,
	RoleTurn: 0.08, RoleQuestion: 0.06, RoleCTA: 0,
}

var maxLenByLayout = map[string]float64{LayoutReaction: 3.0, LayoutFullscreen: 2.5, LayoutCard: 2.8, LayoutPIP: 2.6}
var baseLenByLayout = map[string]float64{LayoutReaction: 2.4, LayoutFullscreen: 1.8, LayoutCard: 2.2, LayoutPIP: 2.0}

// Heuristic builds a complete plan without any LLM. It is the fallback used
// whenever Ollama is disabled, unavailable or returns invalid JSON.
func Heuristic(tr transcribe.Transcript, cfg config.Config) Plan {
	return HeuristicCtx(context.Background(), tr, cfg)
}

func HeuristicCtx(ctx context.Context, tr transcribe.Transcript, cfg config.Config) Plan {
	logger := logx.From(ctx)
	started := time.Now()
	logger.Info("planner.start", "tokens", len(tr.Tokens), "segments", len(tr.Segments))
	p := Plan{Version: PlanVersion}
	if len(tr.Segments) == 0 && len(tr.Tokens) == 0 {
		return p
	}
	units := BuildUnits(tr)
	for _, u := range units {
		names := make([]string, 0, len(u.Concepts))
		for _, c := range u.Concepts {
			names = append(names, c.Name)
		}
		logger.Info("planner.semantic_unit", "id", u.ID, "start", round3(u.Start), "end", round3(u.End), "role", u.Role, "concepts", strings.Join(names, ","), "text", u.Text)
	}
	p.SemanticUnits = units
	if cfg.Broll.Enabled {
		p.VisualEvents = CandidatesFromUnits(units, transcriptEnd(tr))
	}
	p = Finalize(ctx, p, transcriptEnd(tr), cfg, nil)
	logger.Info("planner.heuristic.done", "duration_ms", time.Since(started).Milliseconds(), "visual_events", len(p.VisualEvents), "zooms", len(p.Zooms), "sfx", len(p.SFX))
	return p
}

// CandidatesFromUnits proposes visual events from semantic units. It uses the
// unit role, the neighbouring units and concept repetition to score an event,
// so a concept that is the subject of the explanation beats a passing mention.
func CandidatesFromUnits(units []SemanticUnit, dur float64) []VisualEvent {
	count := map[string]int{}
	for _, u := range units {
		for _, c := range u.Concepts {
			count[c.Name]++
		}
	}
	lastUsed := map[string]float64{}
	var out []VisualEvent
	for i, u := range units {
		if u.Role == RoleCTA {
			continue
		}
		for k, hit := range u.Concepts {
			if k >= 2 {
				break
			}
			c, ok := ConceptByName(hit.Name)
			if !ok {
				continue
			}
			if last, ok := lastUsed[c.Name]; ok && hit.At-last < 8 {
				continue
			}
			layout, typ := c.Layout, c.Type
			if u.Role == RolePunchline && typ == TypeBroll {
				layout = LayoutFullscreen
			}
			start := math.Max(0, hit.At-0.10)
			if u.Role == RoleHook && start < 1.2 {
				start = 1.2 // the first second belongs to the creator + punch zoom
			}
			end := start + baseLenByLayout[layout]
			if ue := u.End + 0.25; ue > end {
				end = math.Min(ue, start+maxLenByLayout[layout])
			}
			if dur > 0 {
				end = math.Min(end, dur)
			}
			if end-start < 1.2 {
				continue
			}
			relevance := c.Salience
			importance := 0.35 + 0.4*c.Salience + roleWeight[u.Role]
			reason := reasonFor(u.Role)
			if count[c.Name] > 1 {
				importance += 0.08
				reason = "conceito central repetido na fala; " + reason
			}
			if neighbourMentions(units, i, c.Name) {
				relevance = math.Min(1, relevance+0.1)
			}
			if k > 0 {
				importance -= 0.1
			}
			out = append(out, VisualEvent{
				Start: round3(start), End: round3(end), Type: typ, Concept: c.Name, Label: c.Label,
				Queries: append([]string(nil), c.Queries...), Layout: layout,
				Importance: round3(clamp(importance, 0, 1)), Relevance: round3(relevance),
				Role: u.Role, UnitID: u.ID, Origin: "heuristic", Reason: reason,
			})
			lastUsed[c.Name] = hit.At
		}
	}
	return out
}

func neighbourMentions(units []SemanticUnit, i int, name string) bool {
	for _, j := range []int{i - 1, i + 1} {
		if j < 0 || j >= len(units) {
			continue
		}
		for _, c := range units[j].Concepts {
			if c.Name == name {
				return true
			}
		}
	}
	return false
}

func reasonFor(role string) string {
	switch role {
	case RoleHook:
		return "reforça o gancho com o assunto citado"
	case RoleExample:
		return "ilustra o exemplo dado"
	case RolePunchline:
		return "cutaway de impacto na punchline"
	case RoleTurn:
		return "marca a mudança de ideia"
	case RoleQuestion:
		return "contextualiza a pergunta"
	}
	return "ilustra diretamente a explicação"
}

// Finalize schedules visual events (no overlaps, rhythm budget), plans zooms
// around them, assigns discrete SFX and emphasis words. seedZooms are zooms
// proposed by the LLM director; they win over heuristic zooms.
func Finalize(ctx context.Context, p Plan, dur float64, cfg config.Config, seedZooms []ZoomEvent) Plan {
	logger := logx.From(ctx)
	MigrateLegacy(&p)
	if dur <= 0 {
		for _, e := range p.VisualEvents {
			dur = math.Max(dur, e.End)
		}
	}
	gap := cfg.Broll.MinVisualGap
	logger.Info("visual.schedule.before", "events", len(p.VisualEvents), "json", compactEvents(p.VisualEvents))
	for i := range p.VisualEvents {
		if p.VisualEvents[i].ID == "" {
			p.VisualEvents[i].ID = "cand-" + itoa3(i+1)
		}
	}
	for _, pair := range FindOverlaps(p.VisualEvents, gap) {
		logger.Info("visual.conflict.detected", "a", pair[0], "b", pair[1], "min_gap_s", gap)
	}
	scheduled, conflicts := Schedule(p.VisualEvents, dur, gap, cfg.Broll.MaxEvents)
	for _, c := range conflicts {
		logger.Info("visual.conflict.resolved", "winner", c.Winner, "loser", c.Loser, "action", c.Action, "new_start", c.NewStart, "new_end", c.NewEnd)
	}
	p.VisualEvents = scheduled
	logger.Info("visual.schedule.after", "events", len(p.VisualEvents), "json", compactEvents(p.VisualEvents))

	if cfg.Zoom.Enabled {
		p.Zooms = PlanZooms(p.SemanticUnits, p.VisualEvents, dur, cfg, append(seedZooms, p.Zooms...))
	} else {
		p.Zooms = nil
	}
	p.SFX = PlanSFX(p.VisualEvents)
	p.Emphasis = planEmphasis(p)
	for _, u := range p.SemanticUnits {
		if u.Role == RoleHook && p.Hook == "" {
			p.Hook = u.Text
		}
		if u.Role == RoleCTA && p.CTA == "" {
			p.CTA = u.Text
		}
	}
	if p.Version == "" {
		p.Version = PlanVersion
	}
	return p
}

// ClampZoomScale keeps zooms visible but tasteful: 1.035 is invisible, 1.15
// looks amateur.
func ClampZoomScale(kind string, s float64) float64 {
	switch kind {
	case ZoomSlowPush, ZoomReframe:
		return clamp(s, 1.055, 1.10)
	default:
		return clamp(s, 1.055, 1.12)
	}
}

// PlanZooms chooses A-roll camera moves by intent: punch zoom on the hook and
// punchlines, reframe on a change of idea, slow push on long A-roll stretches.
func PlanZooms(units []SemanticUnit, events []VisualEvent, dur float64, cfg config.Config, seed []ZoomEvent) []ZoomEvent {
	var zs []ZoomEvent
	add := func(z ZoomEvent) bool {
		if z.Kind == "" {
			z.Kind = ZoomPunch
		}
		z.Start = math.Max(0, z.Start)
		if dur > 0 {
			z.End = math.Min(z.End, dur)
		}
		if z.End-z.Start < 0.3 {
			return false
		}
		z.Scale = ClampZoomScale(z.Kind, z.Scale)
		for _, e := range events {
			// A zoom under a fullscreen cutaway is invisible; skip it.
			if e.Layout == LayoutFullscreen && z.Start < e.End && e.Start < z.End {
				return false
			}
		}
		for _, x := range zs {
			if z.Start < x.End+0.4 && x.Start < z.End+0.4 {
				return false
			}
		}
		z.Start, z.End, z.Scale = round3(z.Start), round3(z.End), round3(z.Scale)
		zs = append(zs, z)
		return true
	}
	for _, z := range seed {
		add(z)
	}
	emph := cfg.Zoom.Emphasis
	if emph == 0 {
		emph = 1.085
	}
	hasHook := false
	for _, z := range zs {
		if z.Start < 0.5 {
			hasHook = true
		}
	}
	if !hasHook && dur > 0.6 {
		add(ZoomEvent{Start: 0, End: math.Min(0.95, dur), Scale: emph, Kind: ZoomPunch, Reason: "gancho: punch zoom no primeiro segundo"})
	}
	for i, u := range units {
		if i == 0 {
			continue
		}
		switch u.Role {
		case RolePunchline:
			add(ZoomEvent{Start: u.Start, End: u.Start + 0.95, Scale: cfg.Zoom.Punch, Kind: ZoomPunch, Reason: "ênfase/punchline"})
		case RoleTurn:
			add(ZoomEvent{Start: u.Start, End: math.Min(u.End, u.Start+1.8), Scale: math.Max(cfg.Zoom.Mild, 1.065), Kind: ZoomReframe, Reason: "mudança de ideia: reenquadramento"})
		case RoleQuestion:
			add(ZoomEvent{Start: u.Start, End: u.Start + 0.85, Scale: emph, Kind: ZoomPunch, Reason: "pergunta ao público"})
		}
	}
	// Fill long static A-roll stretches with a slow push so the shot breathes.
	for {
		a, b, ok := longestStatic(zs, events, dur)
		if !ok || b-a < 5.0 {
			break
		}
		if !add(ZoomEvent{Start: a + 0.4, End: a + math.Min(3.2, b-a-0.8), Scale: math.Max(cfg.Zoom.Mild, 1.06), Kind: ZoomSlowPush, Reason: "A-roll longo: slow push-in"}) {
			break
		}
	}
	limit := int(math.Max(3, math.Ceil(dur/10)+2))
	if dur <= 30 && limit > 6 {
		limit = 6
	}
	if dur > 30 && limit > 10 {
		limit = 10
	}
	sort.Slice(zs, func(i, j int) bool { return zs[i].Start < zs[j].Start })
	if len(zs) > limit {
		zs = zs[:limit]
	}
	return zs
}

// longestStatic returns the longest interval without any visual change.
func longestStatic(zs []ZoomEvent, events []VisualEvent, dur float64) (float64, float64, bool) {
	marks := []float64{0, dur}
	for _, z := range zs {
		marks = append(marks, z.Start, z.End)
	}
	for _, e := range events {
		marks = append(marks, e.Start, e.End)
	}
	sort.Float64s(marks)
	best, ba, bb := 0.0, 0.0, 0.0
	for i := 1; i < len(marks); i++ {
		a, b := marks[i-1], marks[i]
		if busy(a, b, zs, events) {
			continue
		}
		if b-a > best {
			best, ba, bb = b-a, a, b
		}
	}
	return ba, bb, best > 0
}

func busy(a, b float64, zs []ZoomEvent, events []VisualEvent) bool {
	mid := (a + b) / 2
	for _, z := range zs {
		if mid > z.Start && mid < z.End {
			return true
		}
	}
	for _, e := range events {
		if mid > e.Start && mid < e.End {
			return true
		}
	}
	return false
}

// PlanSFX adds discrete sound design. Never on every insert: with n events at
// most n-1 get a sound (and never more than 4).
func PlanSFX(events []VisualEvent) []SFXEvent {
	if len(events) == 0 {
		return nil
	}
	limit := len(events)
	if limit >= 2 {
		limit--
	}
	if limit > 4 {
		limit = 4
	}
	order := append([]VisualEvent(nil), events...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].Importance > order[j].Importance })
	var out []SFXEvent
	for _, e := range order {
		if len(out) >= limit {
			break
		}
		switch e.Layout {
		case LayoutFullscreen:
			out = append(out, SFXEvent{Time: round3(math.Max(0, e.Start-0.05)), Name: "whoosh", GainDB: -22, Reason: "entrada de cutaway " + e.ID})
		case LayoutCard, LayoutPIP:
			out = append(out, SFXEvent{Time: round3(e.Start), Name: "pop", GainDB: -24, Reason: "entrada de card " + e.ID})
		default:
			if e.Importance >= 0.8 {
				out = append(out, SFXEvent{Time: round3(e.Start), Name: "click", GainDB: -27, Reason: "entrada de B-roll importante " + e.ID})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

func planEmphasis(p Plan) []string {
	var out []string
	for _, e := range p.Emphasis {
		if len(out) < 3 {
			out = appendUnique(out, e)
		}
	}
	for _, u := range p.SemanticUnits {
		if len(out) >= 3 {
			break
		}
		if u.Role == RolePunchline {
			if w := emphasisWord(u.Text); w != "" {
				out = appendUnique(out, w)
			}
		}
	}
	// Highlight the word that triggered the most important visual.
	for _, e := range p.VisualEvents {
		if len(out) >= 3 {
			break
		}
		for _, u := range p.SemanticUnits {
			if u.ID != e.UnitID {
				continue
			}
			for _, c := range u.Concepts {
				if c.Name == e.Concept && !strings.Contains(c.Alias, " ") {
					out = appendUnique(out, c.Alias)
				}
			}
		}
	}
	return out
}

func compactEvents(es []VisualEvent) string {
	var b strings.Builder
	b.WriteString("[")
	for i, e := range es {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(e.ID + ":" + e.Layout + ":" + e.Concept + "@" + ftoa(e.Start) + "-" + ftoa(e.End) + "/" + ftoa(e.Importance))
	}
	b.WriteString("]")
	return b.String()
}

func normalizeWord(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) })
	return strings.Join(strings.Fields(s), " ")
}

func transcriptEnd(tr transcribe.Transcript) float64 {
	maxT := 0.0
	for _, s := range tr.Segments {
		if s.End > maxT {
			maxT = s.End
		}
	}
	for _, t := range tr.Tokens {
		if t.End > maxT {
			maxT = t.End
		}
	}
	return maxT
}

func emphasisWord(s string) string {
	for _, w := range strings.Fields(s) {
		clean := strings.Trim(w, ".,!?;:\"'()[]{}")
		if len([]rune(clean)) >= 5 && !punchRE.MatchString(clean) {
			return clean
		}
	}
	return ""
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if strings.EqualFold(x, v) {
			return xs
		}
	}
	return append(xs, v)
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func round3(v float64) float64        { return math.Round(v*1000) / 1000 }
func min(a, b float64) float64        { return math.Min(a, b) }
func max(a, b float64) float64        { return math.Max(a, b) }

// matchKeyword is kept for backwards compatibility with tests and helpers.
// Matching is word/phrase based; it never treats "ia" as a substring of
// "tecnologia" or "gostaria".
func matchKeyword(text, kw string) bool {
	textWords := strings.Fields(normalizeWord(text))
	kwWords := strings.Fields(normalizeWord(kw))
	for i := range textWords {
		textWords[i] = normalizeWord(textWords[i])
	}
	if len(kwWords) == 0 || len(textWords) < len(kwWords) {
		return false
	}
	for i := 0; i+len(kwWords) <= len(textWords); i++ {
		ok := true
		for j := range kwWords {
			if textWords[i+j] != kwWords[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
