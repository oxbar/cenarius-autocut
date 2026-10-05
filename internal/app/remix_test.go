package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/planner"
)

func ffmpegOrSkip(t *testing.T) (string, string) {
	t.Helper()
	ff, fp := assets.FindFFmpeg()
	if ff == "" || fp == "" {
		t.Skip("ffmpeg não disponível")
	}
	return ff, fp
}

func sh(t *testing.T, bin string, args ...string) {
	t.Helper()
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestRemixChangesOnlyTheAudio(t *testing.T) {
	ff, fp := ffmpegOrSkip(t)
	if testing.Short() {
		t.Skip("integração")
	}
	dir := t.TempDir()
	sh(t, ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=540x960:rate=30:duration=6",
		"-f", "lavfi", "-i", "aevalsrc='0.05*sin(2*PI*300*t)*gt(mod(t,3),0.8)':s=48000:d=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", filepath.Join(dir, "cut.mp4"))
	track := filepath.Join(dir, "track.wav")
	sh(t, ff, "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=2000:sample_rate=48000:duration=10", track)
	plan := planner.Plan{
		SemanticUnits: []planner.SemanticUnit{{ID: "u01", Start: 0.8, End: 3}, {ID: "u02", Start: 3.8, End: 6}},
		Music:         &planner.MusicCue{Mood: planner.MoodEnergetic, Asset: &planner.AssetRef{Path: track, Type: "audio", Source: "music-local"}},
	}
	pb, _ := json.Marshal(plan)
	if err := os.WriteFile(filepath.Join(dir, "edit-plan.json"), pb, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "debug.log"), []byte("original run\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.FFmpeg, cfg.FFprobe = ff, fp
	cfg.Output.Preset = "ultrafast"
	cfg.Captions.Enabled = false

	quieter := -30.0
	res, err := Remix(context.Background(), cfg, dir, AudioSettings{MusicDB: &quieter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalDuration < 5.8 || res.FinalDuration > 6.2 {
		t.Fatalf("duration changed: %.3f", res.FinalDuration)
	}
	var mix config.MusicConfig
	b, _ := os.ReadFile(filepath.Join(dir, "audio-mix.json"))
	if err := json.Unmarshal(b, &mix); err != nil || mix.GainDB != -30 {
		t.Fatalf("mix not recorded: %+v %v", mix, err)
	}
	var saved planner.Plan
	b, _ = os.ReadFile(filepath.Join(dir, "edit-plan.json"))
	_ = json.Unmarshal(b, &saved)
	if saved.Music == nil || len(saved.Music.Speech) != 2 {
		t.Fatalf("speech ranges must be derived for older plans: %+v", saved.Music)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "debug.log"))
	if !strings.HasPrefix(string(log), "original run") || !strings.Contains(string(log), "msg=audio.remix.done") || !strings.Contains(string(log), "msg=audio.music.balance") {
		t.Fatalf("remix must append to the job log:\n%s", log)
	}
	for _, leftover := range []string{"final.remix.mp4", "final.remix.mp4.filter"} {
		if _, err := os.Stat(filepath.Join(dir, leftover)); !os.IsNotExist(err) {
			t.Fatalf("temporary remix file left behind: %s", leftover)
		}
	}

	off := false
	if _, err := Remix(context.Background(), cfg, dir, AudioSettings{MusicEnabled: &off}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "edit-plan.json"))
	saved = planner.Plan{}
	_ = json.Unmarshal(b, &saved)
	if saved.Music != nil {
		t.Fatal("music must be removed from the plan")
	}
}

func TestRemixNeedsAFinishedJob(t *testing.T) {
	if _, err := Remix(context.Background(), config.Default(), t.TempDir(), AudioSettings{}, nil); err == nil || !strings.Contains(err.Error(), "job concluído") {
		t.Fatalf("expected clear error, got %v", err)
	}
}
