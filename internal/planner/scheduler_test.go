package planner

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

// The exact v1.5 bug: 0.85-3.0 and 2.0-4.5 overlapping.
func TestResolveVisualConflictsRealCase(t *testing.T) {
	events := []VisualEvent{
		{ID: "gravacao", Start: 0.85, End: 3.0, Layout: LayoutReaction, Importance: 0.55},
		{ID: "programacao", Start: 2.0, End: 4.5, Layout: LayoutReaction, Importance: 0.85},
		{ID: "tecnologia", Start: 4.77, End: 6.92, Layout: LayoutReaction, Importance: 0.6},
	}
	out, conflicts := ResolveVisualConflicts(events, 0.35)
	if len(conflicts) == 0 {
		t.Fatal("conflict not detected")
	}
	if ov := FindOverlaps(out, 0.35); len(ov) != 0 {
		t.Fatalf("overlaps remain: %v in %+v", ov, out)
	}
	var kept bool
	for _, e := range out {
		if e.ID == "programacao" {
			kept = true
			if e.Start != 2.0 || e.End != 4.5 {
				t.Fatalf("winner must keep its interval: %+v", e)
			}
		}
		if e.End-e.Start < MinEventDuration {
			t.Fatalf("event shorter than minimum: %+v", e)
		}
	}
	if !kept {
		t.Fatalf("most important event was dropped: %+v", out)
	}
}

func TestResolveVisualConflictsTrimsIntoFreeWindow(t *testing.T) {
	events := []VisualEvent{
		{ID: "a", Start: 3, End: 5, Importance: 0.9},
		{ID: "b", Start: 4.5, End: 8, Importance: 0.5},
	}
	out, conflicts := ResolveVisualConflicts(events, 0.35)
	if len(out) != 2 || conflicts[0].Action != "trimmed" {
		t.Fatalf("expected trim, got %+v %+v", out, conflicts)
	}
	if out[1].Start < 5.35-1e-9 {
		t.Fatalf("trimmed event must start after winner + gap: %+v", out[1])
	}
}

func TestExplicitlyCompatibleEventsMayOverlap(t *testing.T) {
	events := []VisualEvent{
		{ID: "a", Start: 3, End: 5, Importance: 0.9, AllowOverlap: true, Layout: LayoutPIP},
		{ID: "b", Start: 3.5, End: 5.5, Importance: 0.5, AllowOverlap: true, Layout: LayoutPIP},
	}
	out, _ := ResolveVisualConflicts(events, 0.35)
	if len(out) != 2 {
		t.Fatalf("compatible events must both survive: %+v", out)
	}
}

func TestNoOverlapProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		var evs []VisualEvent
		for i := 0; i < 12; i++ {
			s := rng.Float64() * 25
			evs = append(evs, VisualEvent{ID: string(rune('a' + i)), Start: s, End: s + 0.6 + rng.Float64()*3, Importance: rng.Float64()})
		}
		out, _ := Schedule(evs, 28, 0.35, 6)
		if ov := FindOverlaps(out, 0.35); len(ov) != 0 {
			t.Fatalf("iteration %d: overlaps %v in %+v", iter, ov, out)
		}
		if len(out) > TargetVisualEvents(28, 6) {
			t.Fatalf("rhythm budget exceeded: %d", len(out))
		}
	}
}

func words(text string, start, step float64) []transcribe.Token {
	var ts []transcribe.Token
	t := start
	for _, w := range strings.Fields(text) {
		ts = append(ts, transcribe.Token{Text: w, Start: t, End: t + step*0.9})
		t += step
	}
	return ts
}

// ~25 s talking-head transcript similar to the IMG_6823 test clip.
func richTranscript() transcribe.Transcript {
	var toks []transcribe.Token
	toks = append(toks, words("Hoje a inteligência artificial está mudando a programação.", 0.2, 0.36)...)
	toks = append(toks, words("Todo programador precisa entender como a tecnologia funciona no dia a dia.", 3.6, 0.34)...)
	toks = append(toks, words("Por exemplo, empresas estão usando o ChatGPT para escrever código em produção.", 8.3, 0.34)...)
	toks = append(toks, words("Mas ninguém te conta o problema: segurança e dados vazando no mundo digital.", 13.2, 0.34)...)
	toks = append(toks, words("Então estuda a base, entende o servidor e o banco de dados.", 18.4, 0.34)...)
	toks = append(toks, words("Segue o perfil para mais conteúdo.", 22.6, 0.36)...)
	return transcribe.Transcript{Language: "pt", Tokens: toks, Segments: []transcribe.Segment{{Text: "x", Start: 0.2, End: toks[len(toks)-1].End, Tokens: toks}}}
}

func TestSchedulingRhythmForTypicalShort(t *testing.T) {
	cfg := config.Default()
	tr := richTranscript()
	p := Heuristic(tr, cfg)
	n := len(p.VisualEvents)
	if n < 3 || n > 5 {
		t.Fatalf("want 3-5 B-roll events for ~25s, got %d: %s", n, DebugJSON(p))
	}
	total := n + len(p.Zooms)
	if total < 4 || total > 10 {
		t.Fatalf("visual changes out of rhythm: %d", total)
	}
	if ov := FindOverlaps(p.VisualEvents, cfg.Broll.MinVisualGap); len(ov) != 0 {
		t.Fatalf("overlaps: %v", ov)
	}
	for _, e := range p.VisualEvents {
		if e.Start < 1.2 {
			t.Fatalf("B-roll before the hook second: %+v", e)
		}
		if len(e.Queries) < 2 {
			t.Fatalf("event must carry alternative queries: %+v", e)
		}
		if e.Asset != nil {
			t.Fatalf("planner must not invent assets: %+v", e)
		}
	}
	if a, b, ok := longestStatic(p.Zooms, p.VisualEvents, transcriptEnd(tr)); ok && b-a > 7.5 {
		t.Fatalf("static stretch too long: %.2f-%.2f", a, b)
	}
	if len(p.Zooms) == 0 || p.Zooms[0].Start > 0.01 || p.Zooms[0].Scale < 1.07 {
		t.Fatalf("hook punch zoom missing/invisible: %+v", p.Zooms)
	}
	for _, z := range p.Zooms {
		if z.Scale < 1.055 || z.Scale > 1.12 {
			t.Fatalf("zoom scale out of range: %+v", z)
		}
	}
	if len(p.SFX) >= n && n > 1 {
		t.Fatalf("SFX on every insert (%d sfx for %d events)", len(p.SFX), n)
	}
	if p.CTA == "" {
		t.Fatalf("CTA unit not detected")
	}
}

func TestSemanticUnitsAndRoles(t *testing.T) {
	units := BuildUnits(richTranscript())
	if len(units) < 5 {
		t.Fatalf("units=%d", len(units))
	}
	if units[0].Role != RoleHook {
		t.Fatalf("first unit must be hook: %+v", units[0])
	}
	roles := map[string]bool{}
	for _, u := range units {
		roles[u.Role] = true
	}
	for _, r := range []string{RoleExample, RoleCTA} {
		if !roles[r] {
			t.Fatalf("role %s not found: %+v", r, units)
		}
	}
}

func TestParseDirectorValidatesAndEnriches(t *testing.T) {
	tr := richTranscript()
	units := BuildUnits(tr)
	raw := `<think>ok</think>{"hook":"x","visual_events":[
	 {"start":3.9,"end":9.5,"type":"broll","concept":"software development","queries":["programmer coding IDE"],"layout":"reaction","importance":0.9,"reason":"explicação"},
	 {"start":-1,"end":2,"concept":"bad"},
	 {"start":5,"end":6,"layout":"hologram","queries":["x y z"]}],
	 "zooms":[{"start":0,"end":0.9,"kind":"punch_zoom","scale":1.03}]}`
	p, err := ParseDirector(raw, tr, units, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.VisualEvents) != 1 {
		t.Fatalf("invalid events must be dropped: %+v", p.VisualEvents)
	}
	e := p.VisualEvents[0]
	if e.End-e.Start > 3.0+1e-9 || len(e.Queries) < 3 || e.Origin != "ollama" {
		t.Fatalf("event not clamped/enriched: %+v", e)
	}
	if p.Zooms[0].Scale < 1.055 {
		t.Fatalf("invisible zoom accepted: %+v", p.Zooms[0])
	}
	if _, err := ParseDirector("not json at all", tr, units, config.Default()); err == nil {
		t.Fatal("invalid JSON must error")
	}
	if _, err := ParseDirector(`{"visual_events":[]}`, tr, units, config.Default()); err == nil {
		t.Fatal("empty director plan must error")
	}
}

func TestWithOllamaFallsBackOnInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":"desculpe, não sei responder em JSON"}`))
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Ollama.URL = srv.URL
	tr := richTranscript()
	base := Heuristic(tr, cfg)
	got := WithOllama(context.Background(), base, tr, cfg)
	if DebugJSON(got) != DebugJSON(base) {
		t.Fatal("invalid LLM output must return the heuristic plan unchanged")
	}
	cfg.Ollama.URL = "http://127.0.0.1:1" // offline
	if got := WithOllama(context.Background(), base, tr, cfg); len(got.VisualEvents) != len(base.VisualEvents) {
		t.Fatal("offline Ollama must not break the pipeline")
	}
}

func TestWithOllamaMergesDirectorEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":"{\"visual_events\":[{\"start\":13.2,\"end\":15.4,\"type\":\"broll\",\"concept\":\"cybersecurity\",\"queries\":[\"hacker computer\",\"padlock keyboard\"],\"layout\":\"fullscreen\",\"importance\":0.95,\"reason\":\"punchline\"}]}"}`))
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Ollama.URL = srv.URL
	tr := richTranscript()
	got := WithOllama(context.Background(), Heuristic(tr, cfg), tr, cfg)
	found := false
	for _, e := range got.VisualEvents {
		if e.Origin == "ollama" && e.Layout == LayoutFullscreen {
			found = true
		}
	}
	if !found {
		t.Fatalf("director event not merged: %s", DebugJSON(got))
	}
	if ov := FindOverlaps(got.VisualEvents, cfg.Broll.MinVisualGap); len(ov) != 0 {
		t.Fatalf("merge produced overlaps: %v", ov)
	}
}

func TestLegacyOverlaysMigrate(t *testing.T) {
	p := Plan{Overlays: []OverlayEvent{{Start: 2, End: 4, Keyword: "java", Query: "Java programming", Mode: "bottom"}}}
	MigrateLegacy(&p)
	if len(p.VisualEvents) != 1 || p.VisualEvents[0].Layout != LayoutReaction || len(p.Overlays) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestLongestAliasWins(t *testing.T) {
	tr := transcribe.Transcript{Tokens: words("entende o banco de dados agora", 0, 0.3)}
	units := BuildUnits(tr)
	for _, u := range units {
		for _, c := range u.Concepts {
			if c.Name == "bank finance" {
				t.Fatalf("'banco de dados' matched bank: %+v", u.Concepts)
			}
		}
	}
	if len(units[0].Concepts) != 1 || units[0].Concepts[0].Name != "data" {
		t.Fatalf("%+v", units[0].Concepts)
	}
}

func TestShortFormVisualBudgetKeepsThreeChangesWhenThereIsRoom(t *testing.T) {
	if got := TargetVisualEvents(13.9, 12); got != 3 {
		t.Fatalf("13.9s short should budget 3 visual changes, got %d", got)
	}
}

func TestLongFormVisualBudgetScalesWithDuration(t *testing.T) {
	short := TargetVisualEvents(25, 12)
	long := TargetVisualEvents(88, 12)
	if short < 3 || short > 5 {
		t.Fatalf("25s short budget should stay 3-5, got %d", short)
	}
	if long < 10 || long > 12 {
		t.Fatalf("88s budget should scale to 10-12, got %d", long)
	}
}

func TestRhythmBudgetPrefersDistinctConcepts(t *testing.T) {
	events := []VisualEvent{
		{ID: "a1", Start: 2, End: 3.2, Concept: "software", Importance: .99, Relevance: .99},
		{ID: "a2", Start: 5, End: 6.2, Concept: "software", Importance: .98, Relevance: .98},
		{ID: "b", Start: 8, End: 9.2, Concept: "github", Importance: .80, Relevance: .80},
		{ID: "c", Start: 11, End: 12.2, Concept: "docker", Importance: .79, Relevance: .79},
		{ID: "d", Start: 14, End: 15.2, Concept: "kubernetes", Importance: .78, Relevance: .78},
	}
	out, _ := Schedule(events, 20, .35, 3)
	if len(out) != 3 {
		t.Fatalf("expected 3 events, got %+v", out)
	}
	seen := map[string]bool{}
	for _, e := range out {
		if seen[e.Concept] {
			t.Fatalf("budget reused concept despite alternatives: %+v", out)
		}
		seen[e.Concept] = true
	}
}
