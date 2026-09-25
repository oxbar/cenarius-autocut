package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/captions"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/render"
	"cenarius-autocut/internal/silence"
	"cenarius-autocut/internal/transcribe"
)

type Progress func(stage string, percent int, detail string)

type Result struct {
	Output, ASS, TranscriptJSON, PlanJSON, DebugLog, AttributionJSON string
	OriginalDuration, FinalDuration                                  float64
}

func Run(ctx context.Context, cfg config.Config, input, outDir string, progress Progress) (result Result, err error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return Result{}, err
	}

	ctx, debugLog, closer, err := logx.StartJob(ctx, outDir)
	if err != nil {
		return Result{}, fmt.Errorf("criar debug.log: %w", err)
	}
	defer closer.Close()
	logger := logx.From(ctx)

	defer func() {
		if err != nil {
			logger.Error("pipeline.failed", "error", err)
		} else {
			logger.Info("pipeline.done", "output", result.Output, "original_duration_s", result.OriginalDuration, "final_duration_s", result.FinalDuration)
		}
	}()

	p := func(s string, n int, d string) {
		logger.Info("stage", "name", s, "percent", n, "detail", d)
		if progress != nil {
			progress(s, n, d)
		}
	}

	cfgJSON, _ := json.Marshal(cfg)
	logger.Info("pipeline.start", "input", input, "out_dir", outDir, "debug_log", debugLog)
	logger.Debug("config", "json", string(cfgJSON))

	p("probe", 3, "analisando mídia")
	info, err := media.Probe(ctx, cfg.FFprobe, input)
	if err != nil {
		return Result{}, err
	}
	logger.Info("media.input", "duration_s", info.Duration, "width", info.Width, "height", info.Height, "fps", info.FPS, "has_audio", info.HasAudio)
	if !info.HasAudio {
		return Result{}, fmt.Errorf("o vídeo não possui áudio")
	}

	wav := filepath.Join(outDir, "audio.wav")
	p("audio", 8, "extraindo áudio 16 kHz")
	if err := media.ExtractAudio(ctx, cfg.FFmpeg, input, wav); err != nil {
		return Result{}, err
	}

	p("transcribe", 14, "transcrevendo com whisper.cpp")
	tr, trPath, err := transcribe.WhisperCPP(ctx, cfg.Whisper, wav, outDir)
	if err != nil {
		return Result{}, err
	}
	logger.Info("transcript.summary", "language", tr.Language, "segments", len(tr.Segments), "tokens", len(tr.Tokens), "text", transcriptText(tr))

	keep := []silence.Interval{{Start: 0, End: info.Duration}}
	if cfg.Cuts.Enabled {
		p("silence", 40, "detectando e encurtando silêncios")
		ss, detectErr := silence.Detect(ctx, cfg.FFmpeg, input, cfg.Cuts.NoiseDB, cfg.Cuts.MinSilence)
		if detectErr != nil {
			return Result{}, fmt.Errorf("detecção de silêncio: %w", detectErr)
		}
		logger.Debug("silence.detected", "intervals", jsonString(ss), "count", len(ss))
		keep = silence.KeepFromSilences(info.Duration, ss, cfg.Cuts.KeepSilence, cfg.Cuts.MinKeepSegment)
	}
	expectedCutDuration := intervalsDuration(keep)
	logger.Info("cut.plan", "keep_intervals", jsonString(keep), "expected_duration_s", expectedCutDuration, "removed_s", math.Max(0, info.Duration-expectedCutDuration))

	mapped := mapTranscript(tr, keep)
	cutPath := filepath.Join(outDir, "cut.mp4")
	p("cut", 50, fmt.Sprintf("aplicando %d segmentos preservados", len(keep)))
	if err := render.Cut(ctx, cfg, input, cutPath, keep); err != nil {
		return Result{}, err
	}
	cutInfo, err := media.Probe(ctx, cfg.FFprobe, cutPath)
	if err != nil {
		return Result{}, err
	}
	logger.Info("media.cut", "duration_s", cutInfo.Duration, "fps", cutInfo.FPS, "width", cutInfo.Width, "height", cutInfo.Height)

	p("plan", 62, "planejando ritmo, zooms, imagens e efeitos")
	plan := planner.Heuristic(mapped, cfg)
	plan = planner.WithOllama(ctx, plan, mapped, cfg)
	logger.Info("edit.plan.pre_assets", "zooms", len(plan.Zooms), "overlays", len(plan.Overlays), "sfx", len(plan.SFX), "emphasis", len(plan.Emphasis), "json", planner.DebugJSON(plan))

	p("assets", 68, "resolvendo imagens e B-roll gratuitos")
	resolver := assets.New(cfg.Broll.AssetDir, cfg.Broll.Manifest, filepath.Join(cfg.WorkDir, "asset-cache"))
	plan, resolvedAssets := resolver.ResolvePlan(ctx, plan)
	attributionPath := filepath.Join(outDir, "assets-attribution.json")
	if err := assets.WriteAttributions(attributionPath, resolvedAssets); err != nil {
		return Result{}, fmt.Errorf("salvar atribuições de assets: %w", err)
	}

	planPath := filepath.Join(outDir, "edit-plan.json")
	pb, _ := json.MarshalIndent(plan, "", "  ")
	if err := os.WriteFile(planPath, pb, 0644); err != nil {
		return Result{}, err
	}
	logger.Info("edit.plan", "zooms", len(plan.Zooms), "overlays", len(plan.Overlays), "sfx", len(plan.SFX), "emphasis", len(plan.Emphasis), "json", string(pb))

	ass := filepath.Join(outDir, "captions.ass")
	p("captions", 72, "gerando legendas ASS dinâmicas")
	if err := captions.GenerateASS(ass, mapped.Tokens, cfg.Captions, captions.EmphasisFromPlan(plan)); err != nil {
		return Result{}, err
	}

	final := filepath.Join(outDir, "final.mp4")
	p("render", 78, "renderizando vídeo final")
	if err := render.Final(ctx, cfg, cutPath, ass, final, plan, cutInfo.HasAudio); err != nil {
		return Result{}, err
	}

	finalInfo, err := media.Probe(ctx, cfg.FFprobe, final)
	if err != nil {
		return Result{}, err
	}
	logger.Info("media.final", "duration_s", finalInfo.Duration, "fps", finalInfo.FPS, "width", finalInfo.Width, "height", finalInfo.Height)

	// Never silently publish a render whose clock was altered by a filter. This
	// catches regressions such as zoompan changing a 24 fps iPhone clip into a
	// shorter 30 fps file. A few frames of container/audio rounding are fine.
	tolerance := math.Max(0.20, 3.0/float64(cfg.Output.FPS))
	durationDiff := math.Abs(finalInfo.Duration - cutInfo.Duration)
	logger.Info("duration.check", "cut_s", cutInfo.Duration, "final_s", finalInfo.Duration, "difference_s", durationDiff, "tolerance_s", tolerance)
	if durationDiff > tolerance {
		return Result{}, fmt.Errorf("render alterou a duração: intermediário %.3fs, final %.3fs (diferença %.3fs). Veja %s", cutInfo.Duration, finalInfo.Duration, durationDiff, debugLog)
	}

	p("done", 100, "concluído")
	return Result{
		Output:           final,
		ASS:              ass,
		TranscriptJSON:   trPath,
		PlanJSON:         planPath,
		DebugLog:         debugLog,
		AttributionJSON:  attributionPath,
		OriginalDuration: info.Duration,
		FinalDuration:    finalInfo.Duration,
	}, nil
}

func intervalsDuration(xs []silence.Interval) float64 {
	var total float64
	for _, x := range xs {
		if x.End > x.Start {
			total += x.End - x.Start
		}
	}
	return total
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func transcriptText(tr transcribe.Transcript) string {
	parts := make([]string, 0, len(tr.Segments))
	for _, s := range tr.Segments {
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func mapTranscript(tr transcribe.Transcript, keep []silence.Interval) transcribe.Transcript {
	out := transcribe.Transcript{Language: tr.Language}
	for _, s := range tr.Segments {
		ns, ne, ok := silence.MapRange(s.Start, s.End, keep)
		if !ok {
			continue
		}
		seg := transcribe.Segment{Text: s.Text, Start: ns, End: ne}
		for _, t := range s.Tokens {
			a, b, ok := silence.MapRange(t.Start, t.End, keep)
			if ok {
				nt := t
				nt.Start = a
				nt.End = b
				seg.Tokens = append(seg.Tokens, nt)
				out.Tokens = append(out.Tokens, nt)
			}
		}
		out.Segments = append(out.Segments, seg)
	}
	return out
}
