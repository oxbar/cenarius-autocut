package shorts

import (
	"testing"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

func longTranscript() transcribe.Transcript {
	texts := []string{
		"Por que a inteligência artificial está mudando a programação?",
		"Hoje muita gente olha para IA como uma simples ferramenta.",
		"Mas o ponto importante é como ela muda a forma de resolver problemas.",
		"Por exemplo você pode testar uma hipótese muito mais rápido.",
		"Isso não elimina engenharia de software nem conhecimento técnico.",
		"O programador continua precisando entender arquitetura e produto.",
		"Só que agora existe uma camada nova de automação no fluxo.",
		"E isso muda completamente a velocidade de experimentação!",
		"Agora pensa em um time pequeno construindo um produto grande.",
		"Ele consegue validar ideias antes de investir meses de trabalho.",
		"Esse é o valor real quando a tecnologia é usada com contexto.",
		"Mas existe outro problema que pouca gente comenta.",
		"Ferramenta sem critério também produz código ruim muito mais rápido.",
		"Por isso revisão testes e observabilidade ficam ainda mais importantes.",
		"A diferença está em usar IA como multiplicador e não como piloto automático.",
		"Se você entende o problema a ferramenta acelera a solução.",
		"Se não entende ela apenas acelera o erro.",
		"Essa é a parte que muda a conversa sobre produtividade!",
		"Comenta depois qual ferramenta você mais usa no seu dia a dia.",
		"E segue para ver mais conteúdo sobre tecnologia e programação.",
	}
	var tr transcribe.Transcript
	for i, text := range texts {
		start := float64(i) * 6.0
		end := start + 5.7
		tr.Segments = append(tr.Segments, transcribe.Segment{Text: text, Start: start, End: end})
	}
	return tr
}

func TestBuildCandidatesGroundedDurations(t *testing.T) {
	cfg := config.Default().Shorts
	cs := BuildCandidates(longTranscript(), cfg, 120)
	if len(cs) < 3 {
		t.Fatalf("expected several candidates, got %d", len(cs))
	}
	for _, c := range cs {
		if c.End <= c.Start || c.Duration < cfg.MinDuration-.01 || c.Duration > cfg.MaxDuration+.01 {
			t.Fatalf("invalid grounded window: %+v", c)
		}
		if c.Scores.Total < 0 || c.Scores.Total > 100 {
			t.Fatalf("invalid score: %+v", c.Scores)
		}
	}
}

func TestSelectRemovesHeavyOverlap(t *testing.T) {
	cs := []Candidate{
		{ID: "a", Start: 0, End: 45, Duration: 45, Scores: Scores{Total: 95}},
		{ID: "b", Start: 5, End: 50, Duration: 45, Scores: Scores{Total: 94}},
		{ID: "c", Start: 60, End: 105, Duration: 45, Scores: Scores{Total: 88}},
	}
	out := Select(cs, 3, .35)
	if len(out) != 2 || out[0].ID != "a" || out[1].ID != "c" {
		t.Fatalf("overlap resolver failed: %+v", out)
	}
}

func TestValidateYouTubeURL(t *testing.T) {
	for _, u := range []string{"https://youtube.com/watch?v=abc", "https://youtu.be/abc", "https://m.youtube.com/watch?v=abc"} {
		if err := ValidateYouTubeURL(u); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"file:///tmp/a", "https://example.com/video", "javascript:alert(1)"} {
		if err := ValidateYouTubeURL(u); err == nil {
			t.Fatalf("must reject %s", u)
		}
	}
}
