package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/render"
)

// AudioSettings is the "mixer" exposed to the CLI and the web UI. Nil fields
// keep the current configuration. Every value goes through
// config.ClampMusicBalance, so no combination can put the music over the voice.
type AudioSettings struct {
	MusicEnabled *bool    `json:"music_enabled,omitempty"`
	MusicDB      *float64 `json:"music_db,omitempty"` // music loudness relative to the voice (LU, negative)
	DuckDB       *float64 `json:"duck_db,omitempty"`  // extra reduction while speaking
	VoiceDB      *float64 `json:"voice_db,omitempty"` // voice trim
	SFXDB        *float64 `json:"sfx_db,omitempty"`   // effects offset
	Mood         string   `json:"mood,omitempty"`     // auto | energetic | ...
}

// Apply writes the settings into cfg and enforces the safe ranges.
func (a AudioSettings) Apply(cfg *config.Config) {
	if a.MusicEnabled != nil {
		cfg.Music.Enabled = *a.MusicEnabled
	}
	if a.MusicDB != nil {
		cfg.Music.GainDB = *a.MusicDB
	}
	if a.DuckDB != nil {
		cfg.Music.DuckDB = *a.DuckDB
		cfg.Music.Duck = *a.DuckDB > 0
	}
	if a.VoiceDB != nil {
		cfg.Music.VoiceGainDB = *a.VoiceDB
	}
	if a.SFXDB != nil {
		cfg.Music.SFXGainDB = *a.SFXDB
	}
	if m := strings.ToLower(strings.TrimSpace(a.Mood)); m != "" {
		cfg.Music.Mood = m
	}
	duck := cfg.Music.Duck
	cfg.Music.GainDB, cfg.Music.DuckDB, cfg.Music.VoiceGainDB, cfg.Music.SFXGainDB = config.ClampMusicBalance(
		cfg.Music.GainDB, cfg.Music.DuckDB, cfg.Music.VoiceGainDB, cfg.Music.SFXGainDB)
	cfg.Music.Duck = duck
	if !planner.IsMood(cfg.Music.Mood) {
		cfg.Music.Mood = "auto"
	}
}

// Remix re-renders only the final video of an existing job with new audio
// settings. It reuses cut.mp4, captions.ass and edit-plan.json from outDir:
// no transcription, planning or visual asset search is repeated. A mood
// change picks a new track; everything else is a pure re-mix (~ render time).
func Remix(ctx context.Context, cfg config.Config, outDir string, s AudioSettings, progress Progress) (Result, error) {
	if progress == nil {
		progress = func(string, int, string) {}
	}
	s.Apply(&cfg)
	ctx, debugLog, closer, err := logx.ContinueJob(ctx, outDir)
	if err != nil {
		return Result{}, err
	}
	defer closer.Close()
	logger := logx.From(ctx)
	started := time.Now()
	cutPath := filepath.Join(outDir, "cut.mp4")
	planPath := filepath.Join(outDir, "edit-plan.json")
	for _, p := range []string{cutPath, planPath} {
		if _, err := os.Stat(p); err != nil {
			return Result{}, fmt.Errorf("remix precisa de um job concluído (%s ausente)", filepath.Base(p))
		}
	}
	var plan planner.Plan
	b, err := os.ReadFile(planPath)
	if err != nil {
		return Result{}, err
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return Result{}, fmt.Errorf("edit-plan.json inválido: %w", err)
	}
	logger.Info("audio.remix.start", "music_enabled", cfg.Music.Enabled, "music_db", cfg.Music.GainDB, "duck", cfg.Music.Duck,
		"duck_db", cfg.Music.DuckDB, "voice_db", cfg.Music.VoiceGainDB, "sfx_db", cfg.Music.SFXGainDB, "mood", cfg.Music.Mood)
	progress("mix", 10, "aplicando novo balanço de áudio")

	if !cfg.Music.Enabled {
		plan.Music = nil
	} else if plan.Music != nil && planner.IsMood(cfg.Music.Mood) && cfg.Music.Mood != plan.Music.Mood {
		// New mood: keep drops/speech, choose a new track.
		plan.Music.Mood, plan.Music.Queries, plan.Music.Asset = cfg.Music.Mood, planner.MoodQueries(cfg.Music.Mood), nil
		plan.Music.Reason = "mood alterado no mixer"
		progress("music", 25, "buscando trilha "+cfg.Music.Mood)
		if ref, a, merr := assets.NewFromConfig(cfg).ResolveMusic(ctx, plan.Music, cfg.Music); merr == nil {
			plan.Music.Asset = ref
			appendAttribution(filepath.Join(outDir, "assets-attribution.json"), *a)
		} else {
			logger.Warn("audio.music.fallback", "mood", cfg.Music.Mood, "fallback", "sem trilha", "error", merr)
		}
	}
	if plan.Music != nil {
		plan.Music.GainDB = cfg.Music.GainDB
		if len(plan.Music.Speech) == 0 {
			plan.Music.Speech = planner.SpeechRanges(plan.SemanticUnits, 0.45)
		}
	}

	info, err := media.Probe(ctx, cfg.FFprobe, cutPath)
	if err != nil {
		return Result{}, err
	}
	ass := filepath.Join(outDir, "captions.ass")
	if _, err := os.Stat(ass); err != nil {
		ass = ""
	}
	final := filepath.Join(outDir, "final.mp4")
	tmp := filepath.Join(outDir, "final.remix.mp4")
	progress("render", 40, "renderizando com o novo mix")
	if err := render.Final(ctx, cfg, cutPath, ass, tmp, plan, info.HasAudio); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	out, err := media.Probe(ctx, cfg.FFprobe, tmp)
	if err != nil {
		return Result{}, err
	}
	if d := out.Duration - info.Duration; d > 0.25 || d < -0.25 {
		_ = os.Remove(tmp)
		return Result{}, fmt.Errorf("remix alterou a duração: %.3fs -> %.3fs", info.Duration, out.Duration)
	}
	if err := os.Rename(tmp, final); err != nil {
		return Result{}, err
	}
	_ = os.Rename(tmp+".filter", final+".filter") // keep one debug graph, named after the output
	pb, _ := json.MarshalIndent(plan, "", "  ")
	_ = os.WriteFile(planPath, pb, 0644)
	mix, _ := json.MarshalIndent(cfg.Music, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "audio-mix.json"), mix, 0644)
	logger.Info("audio.remix.done", "output", final, "duration_s", out.Duration, "duration_ms", time.Since(started).Milliseconds())
	progress("done", 100, "mix atualizado")
	return Result{Output: final, ASS: ass, PlanJSON: planPath, DebugLog: debugLog,
		AttributionJSON: filepath.Join(outDir, "assets-attribution.json"), FinalDuration: out.Duration}, nil
}

func appendAttribution(path string, a assets.Asset) {
	var xs []assets.Asset
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &xs)
	}
	_ = assets.WriteAttributions(path, append(xs, a))
}
