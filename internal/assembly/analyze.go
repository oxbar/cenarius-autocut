package assembly

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/render"
	"cenarius-autocut/internal/transcribe"
)

// Analyze "watches" one clip: probe, speech (Whisper), keyframes described by
// the vision model (when installed), loudness and scene-change rate. Every
// step except probing is best-effort: a clip without speech or without a
// vision model is still usable.
func Analyze(ctx context.Context, cfg config.Config, id, name, path, dir string, vision bool) (Clip, error) {
	logger := logx.From(ctx)
	started := time.Now()
	c := Clip{ID: id, Name: name, Path: path}
	info, err := media.Probe(ctx, cfg.FFprobe, path)
	if err != nil {
		return c, fmt.Errorf("%s: não é um vídeo válido: %w", name, err)
	}
	if info.Duration < 0.5 || info.Width == 0 {
		return c, fmt.Errorf("%s: vídeo sem imagem ou curto demais", name)
	}
	c.Duration, c.Width, c.Height, c.HasAudio = info.Duration, info.Width, info.Height, info.HasAudio
	if err := os.MkdirAll(dir, 0755); err != nil {
		return c, err
	}

	if c.HasAudio {
		wav := filepath.Join(dir, "audio.wav")
		if err := media.ExtractAudio(ctx, cfg.FFmpeg, path, wav); err == nil {
			if tr, _, terr := transcribe.WhisperCPP(ctx, cfg.Whisper, wav, dir); terr == nil {
				c.Words = usefulWords(tr.Tokens, c.Duration)
				c.Transcript = strings.TrimSpace(wordsText(c.Words))
			} else {
				logger.Warn("assembly.clip.transcribe_skipped", "clip", id, "error", terr)
			}
		}
		if lufs, lerr := render.MeasureLUFS(ctx, cfg.FFmpeg, path); lerr == nil {
			c.Energy = clamp01((lufs + 50) / 40) // -50 LUFS -> 0, -10 LUFS -> 1
		}
	}
	c.Motion = sceneRate(ctx, cfg.FFmpeg, path, c.Duration)

	frames := keyframes(ctx, cfg.FFmpeg, path, dir, c.Duration, cfg.Assembly.Keyframes)
	if vision && len(frames) > 0 {
		if d, err := describeFrames(ctx, cfg, frames); err == nil {
			c.Visual, c.Tags, c.Emotion, c.VisualBy = d.Description, d.Tags, d.Emotion, cfg.Assembly.VisionModel
		} else {
			logger.Warn("assembly.clip.vision_failed", "clip", id, "error", err)
		}
	}
	if c.Visual == "" {
		c.Visual, c.VisualBy = heuristicVisual(c), "heuristic"
	}
	logger.Info("assembly.clip.analyzed", "clip", id, "name", name, "duration_s", round2(c.Duration), "has_audio", c.HasAudio,
		"words", len(c.Words), "energy", round2(c.Energy), "motion", round2(c.Motion), "visual_by", c.VisualBy,
		"visual", truncate(c.Visual, 160), "transcript", truncate(c.Transcript, 160), "duration_ms", time.Since(started).Milliseconds())
	return c, nil
}

func usefulWords(ts []transcribe.Token, dur float64) []transcribe.Token {
	out := make([]transcribe.Token, 0, len(ts))
	for _, t := range ts {
		if strings.TrimSpace(t.Text) == "" || t.End <= t.Start || t.Start >= dur {
			continue
		}
		out = append(out, t)
	}
	return out
}

func wordsText(ts []transcribe.Token) string {
	var b strings.Builder
	for _, t := range ts {
		w := strings.TrimSpace(t.Text)
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	return b.String()
}

var sceneRE = regexp.MustCompile(`lavfi\.scene_score=`)

// sceneRate measures how "busy" the footage is: scene changes per second on
// a small proxy, mapped to 0..1 (one change per second or more = 1).
func sceneRate(ctx context.Context, ffmpeg, path string, dur float64) float64 {
	if dur <= 0 {
		return 0
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, ffmpeg, "-hide_banner", "-nostats", "-i", path, "-an",
		"-vf", "scale=240:-2,select='gt(scene,0.25)',metadata=print", "-f", "null", "-")
	var er bytes.Buffer
	cmd.Stderr = &er
	if err := cmd.Run(); err != nil {
		return 0
	}
	n := len(sceneRE.FindAllIndex(er.Bytes(), -1))
	return clamp01(float64(n) / dur)
}

// keyframes extracts n evenly spaced JPEG frames (512 px wide).
func keyframes(ctx context.Context, ffmpeg, path, dir string, dur float64, n int) []string {
	if n < 1 {
		n = 3
	}
	var out []string
	for i := 0; i < n; i++ {
		t := dur * (float64(i) + 0.5) / float64(n)
		f := filepath.Join(dir, fmt.Sprintf("kf%d.jpg", i+1))
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := exec.CommandContext(cctx, ffmpeg, "-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", t), "-i", path,
			"-frames:v", "1", "-vf", "scale=512:-2", "-q:v", "4", f).Run()
		cancel()
		if err == nil {
			if st, e := os.Stat(f); e == nil && st.Size() > 0 {
				out = append(out, f)
			}
		}
	}
	return out
}

type frameDescription struct {
	Description string   `json:"descricao"`
	Tags        []string `json:"tags"`
	Emotion     string   `json:"emocao"`
}

const visionPrompt = `Estes são frames em ordem de UM mesmo clipe de vídeo curto. Descreva objetivamente em português do Brasil o que acontece: pessoas, ação, expressão/emoção, objetos, cenário e qualquer texto visível. Não invente nomes de pessoas. Responda SOMENTE com JSON: {"descricao":"uma ou duas frases","tags":["até 6 palavras-chave"],"emocao":"uma palavra"}`

// describeFrames asks the Ollama vision model to "watch" the keyframes.
func describeFrames(ctx context.Context, cfg config.Config, frames []string) (frameDescription, error) {
	imgs := make([]string, 0, len(frames))
	for _, f := range frames {
		b, err := os.ReadFile(f)
		if err != nil {
			return frameDescription{}, err
		}
		imgs = append(imgs, base64.StdEncoding.EncodeToString(b))
	}
	body, _ := json.Marshal(map[string]any{
		"model": cfg.Assembly.VisionModel, "prompt": visionPrompt, "images": imgs, "stream": false,
		"format": "json", "options": map[string]any{"temperature": 0.2},
	})
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return frameDescription{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return frameDescription{}, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return frameDescription{}, fmt.Errorf("ollama vision HTTP %s", resp.Status)
	}
	var wrap struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(rb, &wrap); err != nil {
		return frameDescription{}, err
	}
	var d frameDescription
	if err := json.Unmarshal([]byte(stripFences(wrap.Response)), &d); err != nil || strings.TrimSpace(d.Description) == "" {
		return frameDescription{}, fmt.Errorf("resposta de visão inválida")
	}
	if len(d.Tags) > 6 {
		d.Tags = d.Tags[:6]
	}
	d.Description = truncate(strings.TrimSpace(d.Description), 300)
	return d, nil
}

// VisionAvailable reports whether the configured vision model is installed
// in the local Ollama (so we never wait on a model that is not there).
func VisionAvailable(ctx context.Context, cfg config.Config) bool {
	if !cfg.Ollama.Enabled || strings.TrimSpace(cfg.Assembly.VisionModel) == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(cfg.Ollama.URL, "/")+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tags) != nil {
		return false
	}
	want := strings.ToLower(cfg.Assembly.VisionModel)
	for _, m := range tags.Models {
		n := strings.ToLower(m.Name)
		if n == want || n == want+":latest" || (!strings.Contains(want, ":") && strings.HasPrefix(n, want+":")) {
			return true
		}
	}
	return false
}

func heuristicVisual(c Clip) string {
	shape := "vertical"
	if c.Width > c.Height {
		shape = "horizontal"
	}
	speech := "sem fala"
	if len(c.Words) > 0 {
		speech = "com fala"
	}
	motion := "pouco movimento"
	switch {
	case c.Motion > 0.6:
		motion = "muito movimento/cortes"
	case c.Motion > 0.2:
		motion = "movimento moderado"
	}
	name := strings.TrimSuffix(c.Name, filepath.Ext(c.Name))
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	return fmt.Sprintf("clipe %s de %.1fs, %s, %s (arquivo: %s)", shape, c.Duration, speech, motion, strings.TrimSpace(name))
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
	return strings.TrimSpace(strings.TrimSuffix(s, "```"))
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
func round2(v float64) float64  { return math.Round(v*100) / 100 }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
