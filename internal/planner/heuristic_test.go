package planner

import (
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
	"testing"
)

func TestIAIsNotMatchedInsidePortugueseWords(t *testing.T) {
	if matchKeyword("tecnologia em geral e relevância", "ia") {
		t.Fatal("'ia' must not match inside tecnologia/relevância")
	}
	if !matchKeyword("vamos falar de IA hoje", "ia") {
		t.Fatal("standalone IA should match")
	}
}

func TestHeuristicDoesNotSpamGenericOverlays(t *testing.T) {
	cfg := config.Default()
	tr := transcribe.Transcript{Segments: []transcribe.Segment{
		{Text: "falando sobre tecnologia em geral", Start: 0, End: 4},
		{Text: "coisas de alta relevância e gostaria de saber", Start: 4.2, End: 9},
	}}
	p := Heuristic(tr, cfg)
	if len(p.Overlays) != 0 {
		t.Fatalf("overlays=%#v", p.Overlays)
	}
}
