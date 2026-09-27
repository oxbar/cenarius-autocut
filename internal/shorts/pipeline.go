package shorts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cenarius-autocut/internal/app"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/transcribe"
)

type Progress func(stage string, percent int, detail string)

type Analysis struct {
	Source         string      `json:"source"`
	Duration       float64     `json:"duration"`
	TranscriptJSON string      `json:"transcript_json"`
	HighlightsJSON string      `json:"highlights_json"`
	DebugLog       string      `json:"debug_log"`
	Candidates     []Candidate `json:"candidates"`
}

type GeneratedClip struct {
	Candidate Candidate  `json:"candidate"`
	Source    string     `json:"source"`
	Result    app.Result `json:"result"`
}

func Analyze(ctx context.Context, cfg config.Config, input, outDir string, count int, progress Progress) (analysis Analysis, err error) {
	if !cfg.Shorts.Enabled {
		return Analysis{}, fmt.Errorf("Longo → Shorts está desativado no config")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return Analysis{}, err
	}
	ctx, debugLog, closer, err := logx.StartJob(ctx, outDir)
	if err != nil {
		return Analysis{}, err
	}
	defer closer.Close()
	logger := logx.From(ctx)
	p := func(stage string, n int, detail string) {
		logger.Info("shorts.stage", "name", stage, "percent", n, "detail", detail)
		if progress != nil {
			progress(stage, n, detail)
		}
	}
	p("probe", 5, "analisando vídeo longo")
	info, err := media.Probe(ctx, cfg.FFprobe, input)
	if err != nil {
		return Analysis{}, err
	}
	if !info.HasAudio {
		return Analysis{}, fmt.Errorf("o vídeo longo não possui áudio")
	}
	wav := filepath.Join(outDir, "source-audio.wav")
	p("audio", 12, "extraindo áudio uma única vez")
	if err := media.ExtractAudio(ctx, cfg.FFmpeg, input, wav); err != nil {
		return Analysis{}, err
	}
	p("transcribe", 25, "transcrevendo vídeo longo com Whisper")
	tr, trPath, err := transcribe.WhisperCPP(ctx, cfg.Whisper, wav, outDir)
	if err != nil {
		return Analysis{}, err
	}
	p("candidates", 58, "criando janelas semânticas de 40–50s")
	candidates := BuildCandidates(tr, cfg.Shorts, info.Duration)
	if len(candidates) == 0 {
		return Analysis{}, fmt.Errorf("não foi possível formar cortes semânticos")
	}
	p("rank", 70, "ranqueando hook, engajamento, valor e compartilhamento")
	candidates = RankWithOllama(ctx, candidates, cfg)
	if count <= 0 {
		count = cfg.Shorts.DefaultCount
	}
	if cfg.Shorts.MaxCount > 0 && count > cfg.Shorts.MaxCount {
		count = cfg.Shorts.MaxCount
	}
	selected := Select(candidates, count, cfg.Shorts.MaxOverlap)
	for i := range selected {
		selected[i].ID = fmt.Sprintf("clip-%02d", i+1)
	}
	highlights := filepath.Join(outDir, "highlights.json")
	b, _ := json.MarshalIndent(struct {
		Duration   float64     `json:"source_duration"`
		Note       string      `json:"score_note"`
		Candidates []Candidate `json:"candidates"`
	}{info.Duration, "Score editorial heurístico/IA; não é previsão de viralização.", selected}, "", "  ")
	if err := os.WriteFile(highlights, b, 0644); err != nil {
		return Analysis{}, err
	}
	logger.Info("shorts.analysis.done", "source", input, "duration_s", info.Duration, "candidates", len(selected), "highlights", highlights)
	p("done", 100, fmt.Sprintf("%d cortes encontrados", len(selected)))
	return Analysis{Source: input, Duration: info.Duration, TranscriptJSON: trPath, HighlightsJSON: highlights, DebugLog: debugLog, Candidates: selected}, nil
}

func Generate(ctx context.Context, cfg config.Config, source string, c Candidate, outDir string, progress Progress) (GeneratedClip, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return GeneratedClip{}, err
	}
	clipSource := filepath.Join(outDir, "source-clip.mp4")
	if progress != nil {
		progress("extract", 4, fmt.Sprintf("extraindo %.1fs–%.1fs", c.Start, c.End))
	}
	if err := ExtractRange(ctx, cfg, source, c.Start, c.End, clipSource); err != nil {
		return GeneratedClip{}, err
	}
	resultDir := filepath.Join(outDir, "edited")
	res, err := app.Run(ctx, cfg, clipSource, resultDir, func(stage string, percent int, detail string) {
		if progress != nil {
			// Reserve the first 8% for extraction and map the editor into 8..100.
			progress(stage, 8+int(float64(percent)*.92), detail)
		}
	})
	if err != nil {
		return GeneratedClip{}, err
	}
	return GeneratedClip{Candidate: c, Source: clipSource, Result: res}, nil
}

func ExtractRange(ctx context.Context, cfg config.Config, input string, start, end float64, out string) error {
	if start < 0 || end <= start {
		return fmt.Errorf("intervalo inválido %.3f–%.3f", start, end)
	}
	dur := end - start
	_, err := execx.Run(ctx, nil, cfg.FFmpeg, "-hide_banner", "-y", "-ss", fmt.Sprintf("%.3f", start), "-i", input, "-t", fmt.Sprintf("%.3f", dur),
		"-map_metadata", "-1", "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-movflags", "+faststart", out)
	return err
}

func CandidateByID(xs []Candidate, id string) (Candidate, bool) {
	for _, c := range xs {
		if c.ID == id {
			return c, true
		}
	}
	return Candidate{}, false
}

func ParseCount(s string, def, max int) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	if n <= 0 {
		n = def
	}
	if max > 0 && n > max {
		n = max
	}
	return n
}
