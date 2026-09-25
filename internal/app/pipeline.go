package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cenarius-autocut/internal/captions"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/render"
	"cenarius-autocut/internal/silence"
	"cenarius-autocut/internal/transcribe"
)

type Progress func(stage string, percent int, detail string)

type Result struct {
	Output, ASS, TranscriptJSON, PlanJSON string
	OriginalDuration, FinalDuration       float64
}

func Run(ctx context.Context, cfg config.Config, input, outDir string, progress Progress) (Result, error) {
	p := func(s string, n int, d string) {
		if progress != nil {
			progress(s, n, d)
		}
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return Result{}, err
	}
	p("probe", 3, "analisando mídia")
	info, err := media.Probe(ctx, cfg.FFprobe, input)
	if err != nil {
		return Result{}, err
	}
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
	// Detect silences on original media.
	keep := []silence.Interval{{Start: 0, End: info.Duration}}
	if cfg.Cuts.Enabled {
		p("silence", 40, "detectando e encurtando silêncios")
		ss, _ := silence.Detect(ctx, cfg.FFmpeg, input, cfg.Cuts.NoiseDB, cfg.Cuts.MinSilence)
		keep = silence.KeepFromSilences(info.Duration, ss, cfg.Cuts.KeepSilence, cfg.Cuts.MinKeepSegment)
	}
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
	p("plan", 62, "planejando ritmo, zooms e B-roll")
	plan := planner.Heuristic(mapped, cfg)
	plan = planner.WithOllama(ctx, plan, mapped, cfg)
	planPath := filepath.Join(outDir, "edit-plan.json")
	pb, _ := json.MarshalIndent(plan, "", "  ")
	_ = os.WriteFile(planPath, pb, 0644)
	ass := filepath.Join(outDir, "captions.ass")
	p("captions", 72, "gerando legendas ASS dinâmicas")
	if err := captions.GenerateASS(ass, mapped.Tokens, cfg.Captions, captions.EmphasisFromPlan(plan)); err != nil {
		return Result{}, err
	}
	final := filepath.Join(outDir, "final.mp4")
	p("render", 78, "renderizando vídeo final")
	if err := render.Final(ctx, cfg, cutPath, ass, final, plan); err != nil {
		return Result{}, err
	}
	p("done", 100, "concluído")
	return Result{Output: final, ASS: ass, TranscriptJSON: trPath, PlanJSON: planPath, OriginalDuration: info.Duration, FinalDuration: cutInfo.Duration}, nil
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
