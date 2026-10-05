package assembly

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/transcribe"
)

func words(start float64, text string) []transcribe.Token {
	var ts []transcribe.Token
	t := start
	for _, w := range strings.Fields(text) {
		ts = append(ts, transcribe.Token{Text: " " + w, Start: t, End: t + 0.3})
		t += 0.35
	}
	return ts
}

func sampleClips() []Clip {
	return []Clip{
		{ID: "c01", Name: "dev_chorando.mp4", Duration: 6, HasAudio: true, Words: words(0.5, "quando o deploy falha na sexta feira."), Transcript: "quando o deploy falha na sexta feira.", Energy: 0.6},
		{ID: "c02", Name: "gato_teclado.mp4", Duration: 4, Visual: "gato pisando no teclado do computador", Energy: 0.2, Motion: 0.5},
		{ID: "c03", Name: "festa.mp4", Duration: 9, HasAudio: true, Visual: "pessoas comemorando no escritório", Motion: 0.8},
	}
}

func TestValidateRepairsTheCut(t *testing.T) {
	p := Plan{Prompt: "meme de deploy", Segments: []Segment{
		{ClipID: "c99", Start: 0, End: 2}, // unknown clip
		{ClipID: "c01", Start: 0.6, End: 2.7, SFX: "explosão", TextPos: "left", KeepAudio: true}, // cuts inside words
		{ClipID: "c01", Start: 0.7, End: 2.5},                                                    // duplicate portion
		{ClipID: "c02", Start: 3.9, End: 3.95},                                                   // too short
		{ClipID: "c03", Start: -2, End: 50, Role: "PUNCHLINE", Text: "  COMEMORA {\\b1} ", SFX: "impact", Zoom: true},
		{ClipID: "c02", Start: 1, End: 3, KeepAudio: true}, // clip without audio
	}}
	if err := Validate(&p, sampleClips(), 30, 8); err != nil {
		t.Fatal(err)
	}
	if len(p.Segments) != 3 {
		t.Fatalf("want 3 valid segments, got %+v", p.Segments)
	}
	a := p.Segments[0]
	if a.Start > 0.5 || a.SFX != "" || a.TextPos != "top" || a.Role != "hook" {
		t.Fatalf("word snap / sanitize failed: %+v", a)
	}
	b := p.Segments[1]
	if b.Start != 0 || b.End > 9 || b.Role != "punchline" || strings.ContainsAny(b.Text, "{}\\") || b.SFX != "impact" {
		t.Fatalf("clamp / text clean failed: %+v", b)
	}
	if p.Segments[2].KeepAudio {
		t.Fatal("clip without audio cannot keep audio")
	}
	if !planner.IsMood(p.MusicMood) || p.Title == "" {
		t.Fatalf("mood/title defaults missing: %+v", p)
	}
	empty := Plan{Segments: []Segment{{ClipID: "nope", Start: 0, End: 3}}}
	if err := Validate(&empty, sampleClips(), 30, 8); err == nil {
		t.Fatal("plan without valid segments must fail")
	}
}

func TestFitDurationKeepsNearTarget(t *testing.T) {
	segs := []Segment{{ClipID: "a", Start: 0, End: 10}, {ClipID: "b", Start: 0, End: 10}, {ClipID: "c", Start: 0, End: 10}}
	out := fitDuration(segs, 12)
	total := 0.0
	for _, s := range out {
		total += s.Duration()
		if s.Duration() < 1.5-1e-9 {
			t.Fatalf("segment trimmed below 1.5 s: %+v", s)
		}
	}
	if total > 12*1.25+1e-6 {
		t.Fatalf("total %.2f over target", total)
	}
}

func TestParseDirector(t *testing.T) {
	raw := "```json\n" + `{"title":"Deploy na sexta","music_mood":"funny","captions":false,"notes":"n",
	 "segments":[{"clip_id":"c02","start":0,"end":3,"role":"hook","text":"QUANDO O CÓDIGO SOBE","sfx":"hit"},
	             {"clip_id":"c01","start":0.4,"end":3.1,"role":"punchline","sfx":"impact","zoom":true,"keep_audio":true}]}` + "\n```"
	p, err := ParseDirector(raw, sampleClips(), Options{Prompt: "meme"}, 30, 8)
	if err != nil {
		t.Fatal(err)
	}
	if p.Segments[0].ClipID != "c02" || p.MusicMood != "funny" || p.Captions || p.Segments[1].SFX != "impact" {
		t.Fatalf("%+v", p)
	}
	on := true
	p, _ = ParseDirector(raw, sampleClips(), Options{Prompt: "meme", Captions: &on}, 30, 8)
	if !p.Captions {
		t.Fatal("user choice must override the director")
	}
	if _, err := ParseDirector("desculpe", sampleClips(), Options{}, 30, 8); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestHeuristicEditor(t *testing.T) {
	p := Heuristic(sampleClips(), Options{Prompt: `faça um meme "DEPLOY NA SEXTA" com o gato no teclado`}, 30, 8)
	if p.Origin != "heuristic" || len(p.Segments) != 3 {
		t.Fatalf("%+v", p.Segments)
	}
	if p.Segments[0].ClipID != "c01" || p.Segments[0].Text != "DEPLOY NA SEXTA" || p.Segments[0].Role != "hook" {
		t.Fatalf("hook: %+v", p.Segments[0])
	}
	last := p.Segments[len(p.Segments)-1]
	if last.SFX != "impact" || !last.Zoom || last.Role != "punchline" {
		t.Fatalf("punchline: %+v", last)
	}
	if p.MusicMood != planner.MoodFunny {
		t.Fatalf("meme prompt must be funny: %s", p.MusicMood)
	}
	// Too much material: unrelated clips are dropped when others match.
	short := Heuristic(sampleClips(), Options{Prompt: "gato teclado computador"}, 5, 8)
	for _, s := range short.Segments {
		if s.ClipID == "c03" {
			t.Fatalf("unrelated clip kept: %+v", short.Segments)
		}
	}
	if short.Duration() > 5*1.25+1e-6 {
		t.Fatalf("target ignored: %.2f", short.Duration())
	}
}

func TestHeadlineAndPhrases(t *testing.T) {
	cases := map[string]string{
		`crie um vídeo "ELE NÃO SABIA"`:                   "ELE NÃO SABIA",
		"faça um meme sobre programador na segunda-feira": "programador na segunda-feira",
	}
	for in, want := range cases {
		if got := headline(in); got != want {
			t.Fatalf("headline(%q)=%q want %q", in, got, want)
		}
	}
	ph := Phrases(words(0, "primeira frase aqui. segunda frase"))
	if len(ph) != 2 || ph[0].Text != "primeira frase aqui." {
		t.Fatalf("%+v", ph)
	}
}

func TestOverlayWrapAndMerge(t *testing.T) {
	text, size := wrapOverlay("quando o estagiário faz deploy em produção {x}")
	if strings.Count(text, `\N`) > 2 || strings.ContainsAny(text, "{}") || size < 52 || size > 92 || text != strings.ToUpper(text) {
		t.Fatalf("wrap: %q size %d", text, size)
	}
	lay := Layout(Plan{Segments: []Segment{{ClipID: "c01", Start: 0, End: 2, Text: "oi", TextPos: "bottom"}, {ClipID: "c02", Start: 0, End: 1}}}, 30)
	if lay[1].At != 2 {
		t.Fatalf("layout: %+v", lay)
	}
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.ass")
	if err := WriteOverlayASS(fresh, lay); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(fresh)
	if !strings.Contains(string(b), "Style: Meme,") || !strings.Contains(string(b), `\pos(540,1560)`) || !strings.Contains(string(b), "OI") {
		t.Fatalf("fresh ASS:\n%s", b)
	}
	// Merge into an existing captions file: style goes before [Events].
	withCaps := filepath.Join(dir, "caps.ass")
	cfg := config.Default()
	if err := captionsASS(withCaps, cfg); err != nil {
		t.Fatal(err)
	}
	if err := WriteOverlayASS(withCaps, lay); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(withCaps)
	s := string(b)
	if strings.Index(s, "Style: Meme,") > strings.Index(s, "[Events]") || strings.Count(s, "Style: Meme,") != 1 || !strings.Contains(s, "Dialogue: 5,") {
		t.Fatalf("merge broke the script:\n%s", s)
	}
	if none := filepath.Join(dir, "none.ass"); WriteOverlayASS(none, Layout(Plan{Segments: []Segment{{ClipID: "c", Start: 0, End: 1}}}, 30)) != nil {
		t.Fatal("no overlays and no file must be a no-op")
	} else if _, err := os.Stat(none); !os.IsNotExist(err) {
		t.Fatal("no overlays must not create a file")
	}
}

func TestTimelineWordsAndEditPlan(t *testing.T) {
	clips := sampleClips()
	p := Plan{MusicMood: "funny", Segments: []Segment{
		{ClipID: "c02", Start: 0, End: 2, SFX: "hit"},
		{ClipID: "c01", Start: 0.4, End: 2.2, KeepAudio: true, Role: "punchline", SFX: "impact", Zoom: true},
	}}
	lay := Layout(p, 30)
	w := TimelineWords(lay, clips)
	if len(w) == 0 || w[0].Start < 2 || w[len(w)-1].End > 4.2+1e-6 {
		t.Fatalf("words not mapped into the second segment: %+v", w)
	}
	cfg := config.Default()
	ep := ToEditPlan(p, lay, w, cfg)
	if len(ep.Zooms) != 1 || ep.Zooms[0].Start != 2 || len(ep.SFX) != 2 {
		t.Fatalf("zoom/sfx: %+v %+v", ep.Zooms, ep.SFX)
	}
	if ep.Music == nil || ep.Music.Mood != "funny" || len(ep.Music.Speech) == 0 {
		t.Fatalf("music: %+v", ep.Music)
	}
	if len(ep.Music.Drops) != 0 {
		t.Fatal("punchline at 2 s (< 3 s) must not create a drop")
	}
	cfg.Music.Mood = "tense"
	if ep := ToEditPlan(p, lay, w, cfg); ep.Music.Mood != "tense" {
		t.Fatal("mixer mood must override the director")
	}
}
