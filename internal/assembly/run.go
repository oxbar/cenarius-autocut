package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/captions"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/render"
)

// Input is one uploaded clip (Name is the original file name).
type Input struct {
	Path string
	Name string
}

// Result of a Montagem IA job.
type Result struct {
	Output   string  `json:"-"`
	PlanJSON string  `json:"-"`
	DebugLog string  `json:"-"`
	Duration float64 `json:"duration"`
	Plan     Plan    `json:"plan"`
}

// Progress reports stage, percent and a human detail.
type Progress func(stage string, percent int, detail string)

// Run watches every clip, directs the cut from the prompt, builds the
// timeline and finishes it with the regular render (text, SFX, music mixer).
// outDir ends with the same files as an "Edição IA" job (cut.mp4,
// edit-plan.json, captions.ass, final.mp4), so the audio mixer can remix it.
func Run(ctx context.Context, cfg config.Config, inputs []Input, opts Options, outDir string, progress Progress) (res Result, err error) {
	if progress == nil {
		progress = func(string, int, string) {}
	}
	if len(inputs) == 0 {
		return Result{}, fmt.Errorf("envie pelo menos um clipe")
	}
	if len(inputs) > cfg.Assembly.MaxClips {
		return Result{}, fmt.Errorf("máximo de %d clipes por montagem", cfg.Assembly.MaxClips)
	}
	if opts.Prompt == "" {
		return Result{}, fmt.Errorf("descreva no prompt o vídeo que você quer")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return Result{}, err
	}
	ctx, debugLog, closer, err := logx.StartJob(ctx, outDir)
	if err != nil {
		return Result{}, err
	}
	defer closer.Close()
	logger := logx.From(ctx)
	started := time.Now()
	defer func() {
		if err != nil {
			logger.Error("assembly.failed", "error", err)
		}
	}()
	logger.Info("assembly.start", "clips", len(inputs), "prompt", opts.Prompt, "target_s", opts.Duration)

	vision := VisionAvailable(ctx, cfg)
	logger.Info("assembly.vision", "enabled", vision, "model", cfg.Assembly.VisionModel,
		"hint", "sem modelo de visão os clipes são descritos por fala, movimento e nome do arquivo (ollama pull "+cfg.Assembly.VisionModel+")")
	clips := make([]Clip, 0, len(inputs))
	for i, in := range inputs {
		id := fmt.Sprintf("c%02d", i+1)
		pct := 5 + int(40*float64(i)/float64(len(inputs)))
		progress("analyze", pct, fmt.Sprintf("assistindo clipe %d de %d (%s)", i+1, len(inputs), in.Name))
		c, aerr := Analyze(ctx, cfg, id, in.Name, in.Path, filepath.Join(outDir, "clips", id), vision)
		if aerr != nil {
			return Result{}, aerr
		}
		clips = append(clips, c)
	}

	progress("direct", 50, "o editor está montando o roteiro")
	plan := Direct(ctx, cfg, clips, opts)
	for i, s := range plan.Segments {
		logger.Info("assembly.plan.segment", "index", i+1, "clip", s.ClipID, "start", s.Start, "end", s.End, "role", s.Role,
			"text", s.Text, "sfx", s.SFX, "zoom", s.Zoom, "keep_audio", s.KeepAudio, "reason", s.Reason)
	}
	logger.Info("assembly.plan", "origin", plan.Origin, "title", plan.Title, "mood", plan.MusicMood, "captions", plan.Captions,
		"segments", len(plan.Segments), "duration_s", round2(plan.Duration()), "notes", plan.Notes)

	progress("timeline", 60, fmt.Sprintf("juntando %d trechos", len(plan.Segments)))
	cut, lay, err := BuildTimeline(ctx, cfg, plan, outDir)
	if err != nil {
		return Result{}, err
	}
	words := TimelineWords(lay, clips)
	edit := ToEditPlan(plan, lay, words, cfg)

	var attributions []assets.Asset
	if edit.Music != nil {
		progress("music", 75, "escolhendo trilha ("+edit.Music.Mood+")")
		if ref, a, merr := assets.NewFromConfig(cfg).ResolveMusic(ctx, edit.Music, cfg.Music); merr == nil {
			edit.Music.Asset = ref
			attributions = append(attributions, *a)
		} else {
			logger.Warn("audio.music.fallback", "mood", edit.Music.Mood, "fallback", "sem trilha", "error", merr)
		}
	}
	if err := assets.WriteAttributions(filepath.Join(outDir, "assets-attribution.json"), attributions); err != nil {
		return Result{}, err
	}

	progress("captions", 78, "legenda e textos na tela")
	ass := filepath.Join(outDir, "captions.ass")
	_ = os.Remove(ass)
	if plan.Captions && cfg.Captions.Enabled && len(words) > 0 {
		if err := captions.GenerateASS(ass, words, cfg.Captions, nil); err != nil {
			return Result{}, err
		}
	}
	if err := WriteOverlayASS(ass, lay); err != nil {
		return Result{}, err
	}
	assArg := ass
	if _, err := os.Stat(ass); err != nil {
		assArg = ""
	}
	renderCfg := cfg
	renderCfg.Captions.Enabled = assArg != ""

	editPath := filepath.Join(outDir, "edit-plan.json")
	eb, _ := json.MarshalIndent(edit, "", "  ")
	if err := os.WriteFile(editPath, eb, 0644); err != nil {
		return Result{}, err
	}
	planPath := filepath.Join(outDir, "assembly-plan.json")
	pb, _ := json.MarshalIndent(plan, "", "  ")
	if err := os.WriteFile(planPath, pb, 0644); err != nil {
		return Result{}, err
	}

	progress("render", 80, "finalizando: zoom, efeitos, trilha e textos")
	final := filepath.Join(outDir, "final.mp4")
	if err := render.Final(ctx, renderCfg, cut, assArg, final, edit, true); err != nil {
		return Result{}, err
	}
	cutInfo, err := media.Probe(ctx, cfg.FFprobe, cut)
	if err != nil {
		return Result{}, err
	}
	finalInfo, err := media.Probe(ctx, cfg.FFprobe, final)
	if err != nil {
		return Result{}, err
	}
	diff := math.Abs(finalInfo.Duration - cutInfo.Duration)
	logger.Info("duration.check", "timeline_s", cutInfo.Duration, "final_s", finalInfo.Duration, "difference_s", diff, "tolerance_s", 0.2)
	if diff > 0.2 {
		return Result{}, fmt.Errorf("render alterou a duração da montagem: %.3fs -> %.3fs", cutInfo.Duration, finalInfo.Duration)
	}
	logger.Info("assembly.done", "output", final, "duration_s", finalInfo.Duration, "duration_ms", time.Since(started).Milliseconds())
	progress("done", 100, "montagem concluída")
	return Result{Output: final, PlanJSON: planPath, DebugLog: debugLog, Duration: finalInfo.Duration, Plan: plan}, nil
}
