package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	WorkDir  string        `json:"work_dir"`
	FFmpeg   string        `json:"ffmpeg"`
	FFprobe  string        `json:"ffprobe"`
	YTDLP    string        `json:"yt_dlp"`
	Whisper  WhisperConfig `json:"whisper"`
	Ollama   OllamaConfig  `json:"ollama"`
	Output   OutputConfig  `json:"output"`
	Cuts     CutConfig     `json:"cuts"`
	Captions CaptionConfig `json:"captions"`
	Zoom     ZoomConfig    `json:"zoom"`
	Broll    BrollConfig   `json:"broll"`
	Shorts   ShortsConfig  `json:"shorts"`
	Music    MusicConfig   `json:"music"`
}

// MusicConfig controls the emotional sound layer: a mood-matched music bed
// ducked under the voice, "drops" at punchlines and riser/impact accents.
// Sources are licence-safe only: a local library (e.g. tracks you downloaded
// from the YouTube Audio Library or bought) and Openverse CC audio.
type MusicConfig struct {
	Enabled    bool    `json:"enabled"`
	File       string  `json:"file"`        // force a specific track (CLI -music)
	Mood       string  `json:"mood"`        // auto | energetic | inspiring | tense | dramatic | chill | funny
	Library    string  `json:"library"`     // folder with local tracks
	Manifest   string  `json:"manifest"`    // moods/licence of local tracks
	Remote     bool    `json:"remote"`      // Openverse CC music (no key)
	GainDB     float64 `json:"gain_db"`     // bed level before ducking (-30..-14)
	Duck       bool    `json:"duck"`        // sidechain the bed under the voice
	Drops      bool    `json:"drops"`       // cut the music at punchlines
	EmotionSFX bool    `json:"emotion_sfx"` // riser before / impact on punchlines
}

type WhisperConfig struct {
	Binary   string `json:"binary"`
	Model    string `json:"model"`
	Language string `json:"language"`
	Prompt   string `json:"prompt"`
	Threads  int    `json:"threads"`
}

type OllamaConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Model   string `json:"model"`
}

type OutputConfig struct {
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FPS          int    `json:"fps"`
	CRF          int    `json:"crf"`
	Preset       string `json:"preset"`
	AudioBitrate string `json:"audio_bitrate"`
}

type CutConfig struct {
	Enabled        bool    `json:"enabled"`
	NoiseDB        int     `json:"noise_db"`
	MinSilence     float64 `json:"min_silence"`
	KeepSilence    float64 `json:"keep_silence"`
	MinKeepSegment float64 `json:"min_keep_segment"`
}

type CaptionConfig struct {
	Enabled         bool    `json:"enabled"`
	FontName        string  `json:"font_name"`
	FontSize        int     `json:"font_size"`
	PrimaryColor    string  `json:"primary_color"`
	HighlightColor  string  `json:"highlight_color"`
	OutlineColor    string  `json:"outline_color"`
	Outline         int     `json:"outline"`
	Shadow          int     `json:"shadow"`
	MarginV         int     `json:"margin_v"`
	SafeMargin      int     `json:"safe_margin"`
	MaxWords        int     `json:"max_words"`
	MaxCharsPerLine int     `json:"max_chars_per_line"`
	MaxLines        int     `json:"max_lines"`
	Uppercase       bool    `json:"uppercase"`
	ActiveWord      bool    `json:"active_word"`
	TimingOffset    float64 `json:"timing_offset"`   // seconds; positive delays captions slightly behind ASR timestamps
	ActiveScale     float64 `json:"active_scale"`    // active-word scale, e.g. 1.05
	MaxWidthRatio   float64 `json:"max_width_ratio"` // usable caption width as ratio of PlayResX
}

type ZoomConfig struct {
	Enabled  bool    `json:"enabled"`
	Mild     float64 `json:"mild"`     // 1.055-1.07: slow push / reframe
	Emphasis float64 `json:"emphasis"` // 1.08-1.10: hook / question
	Punch    float64 `json:"punch"`    // <= 1.12: punchline
	MinGap   float64 `json:"min_gap"`
	Duration float64 `json:"duration"`
}

type ShortsConfig struct {
	Enabled       bool    `json:"enabled"`
	MinDuration   float64 `json:"min_duration"`
	IdealMin      float64 `json:"ideal_min"`
	IdealMax      float64 `json:"ideal_max"`
	MaxDuration   float64 `json:"max_duration"`
	DefaultCount  int     `json:"default_count"`
	MaxCount      int     `json:"max_count"`
	MaxOverlap    float64 `json:"max_overlap"`
	UseOllamaRank bool    `json:"use_ollama_rank"`
}

type BrollConfig struct {
	Enabled   bool   `json:"enabled"`
	AssetDir  string `json:"asset_dir"`
	Manifest  string `json:"manifest"`
	MaxEvents int    `json:"max_events"`
	// v1.6 smart editor
	MinVisualGap   float64 `json:"min_visual_gap"`   // A-roll breathing space between inserts (s)
	Remote         bool    `json:"remote"`           // free remote search (Wikimedia Commons)
	Procedural     bool    `json:"procedural"`       // FFmpeg motion graphics before self-broll
	AllowSelfBroll bool    `json:"allow_self_broll"` // last-resort fallback
	MaxRemoteMB    int     `json:"max_remote_mb"`
	ReactionSplit  float64 `json:"reaction_split"`   // 0.5 = creator top half, B-roll bottom half
	ReactionFocusY float64 `json:"reaction_focus_y"` // vertical position of the face in the A-roll (0..1)
	SeamCaptions   bool    `json:"seam_captions"`    // move captions to the split seam during split layouts
}

func Default() Config {
	return Config{
		WorkDir:  "./data",
		FFmpeg:   "ffmpeg",
		FFprobe:  "ffprobe",
		YTDLP:    "yt-dlp",
		Whisper:  WhisperConfig{Binary: "whisper-cli", Model: "./models/ggml-large-v3-turbo.bin", Language: "pt", Prompt: "Português brasileiro. Tecnologia, inteligência artificial, IA, programação, software, engenharia de software, Java, Spring Boot, ChatGPT, OpenAI, Claude, Gemini, Docker, Kubernetes, GitHub, LinkedIn, Instagram, TikTok, YouTube.", Threads: 0},
		Ollama:   OllamaConfig{Enabled: true, URL: "http://127.0.0.1:11434", Model: "qwen3:8b"},
		Output:   OutputConfig{Width: 1080, Height: 1920, FPS: 30, CRF: 18, Preset: "medium", AudioBitrate: "192k"},
		Cuts:     CutConfig{Enabled: true, NoiseDB: -42, MinSilence: 1.10, KeepSilence: 0.25, MinKeepSegment: 0.20},
		Captions: CaptionConfig{Enabled: true, FontName: "Arial", FontSize: 74, PrimaryColor: "&H00FFFFFF", HighlightColor: "&H0000D7FF", OutlineColor: "&H00000000", Outline: 5, Shadow: 0, MarginV: 560, SafeMargin: 112, MaxWords: 6, MaxCharsPerLine: 22, MaxLines: 2, Uppercase: true, ActiveWord: true, TimingOffset: 0.06, ActiveScale: 1.05, MaxWidthRatio: 0.80},
		Zoom:     ZoomConfig{Enabled: true, Mild: 1.06, Emphasis: 1.085, Punch: 1.10, MinGap: 4.0, Duration: 0.85},
		Shorts:   ShortsConfig{Enabled: true, MinDuration: 30, IdealMin: 40, IdealMax: 50, MaxDuration: 55, DefaultCount: 5, MaxCount: 10, MaxOverlap: 0.35, UseOllamaRank: true},
		Music: MusicConfig{Enabled: true, Mood: "auto", Library: "./assets/music", Manifest: "./assets/music/manifest.json",
			Remote: true, GainDB: -20, Duck: true, Drops: true, EmotionSFX: true},
		Broll: BrollConfig{Enabled: true, AssetDir: "./assets/broll", Manifest: "./assets/manifest.json", MaxEvents: 12,
			MinVisualGap: 0.35, Remote: true, Procedural: true, AllowSelfBroll: true, MaxRemoteMB: 60,
			ReactionSplit: 0.5, ReactionFocusY: 0.40, SeamCaptions: true},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, cfg.Normalize(filepath.Dir(path))
}

func (c *Config) Normalize(base string) error {
	if c.WorkDir == "" {
		c.WorkDir = "./data"
	}
	if c.Output.Width <= 0 || c.Output.Height <= 0 || c.Output.FPS <= 0 {
		return fmt.Errorf("output dimensions/fps inválidos")
	}
	if c.Captions.MaxWords <= 0 {
		c.Captions.MaxWords = 6
	}
	if c.Captions.MaxCharsPerLine <= 0 {
		c.Captions.MaxCharsPerLine = 24
	}
	if c.Captions.MaxLines <= 0 {
		c.Captions.MaxLines = 2
	}
	if c.Captions.SafeMargin <= 0 {
		c.Captions.SafeMargin = 112
	}
	if c.Captions.ActiveScale <= 1 || c.Captions.ActiveScale > 1.15 {
		c.Captions.ActiveScale = 1.05
	}
	if c.Captions.MaxWidthRatio <= 0 || c.Captions.MaxWidthRatio > 0.92 {
		c.Captions.MaxWidthRatio = 0.80
	}
	if c.Captions.TimingOffset < -0.20 || c.Captions.TimingOffset > 0.35 {
		c.Captions.TimingOffset = 0.06
	}
	if c.YTDLP == "" {
		c.YTDLP = "yt-dlp"
	}
	if c.Shorts.MinDuration <= 0 {
		c.Shorts.MinDuration = 30
	}
	if c.Shorts.IdealMin < c.Shorts.MinDuration {
		c.Shorts.IdealMin = mathMax(c.Shorts.MinDuration, 40)
	}
	if c.Shorts.IdealMax < c.Shorts.IdealMin {
		c.Shorts.IdealMax = mathMax(c.Shorts.IdealMin, 50)
	}
	if c.Shorts.MaxDuration < c.Shorts.IdealMax {
		c.Shorts.MaxDuration = mathMax(c.Shorts.IdealMax, 55)
	}
	if c.Shorts.DefaultCount <= 0 {
		c.Shorts.DefaultCount = 5
	}
	if c.Shorts.MaxCount < c.Shorts.DefaultCount {
		c.Shorts.MaxCount = 10
	}
	if c.Shorts.MaxOverlap <= 0 || c.Shorts.MaxOverlap >= 1 {
		c.Shorts.MaxOverlap = 0.35
	}
	if c.Broll.MinVisualGap <= 0 {
		c.Broll.MinVisualGap = 0.35
	}
	if c.Broll.ReactionSplit < 0.35 || c.Broll.ReactionSplit > 0.65 {
		c.Broll.ReactionSplit = 0.5
	}
	if c.Broll.ReactionFocusY <= 0 || c.Broll.ReactionFocusY >= 1 {
		c.Broll.ReactionFocusY = 0.40
	}
	if c.Broll.MaxRemoteMB <= 0 {
		c.Broll.MaxRemoteMB = 60
	}
	c.Music.Mood = strings.ToLower(strings.TrimSpace(c.Music.Mood))
	switch c.Music.Mood {
	case "auto", "energetic", "inspiring", "tense", "dramatic", "chill", "funny":
	default:
		c.Music.Mood = "auto"
	}
	if c.Music.GainDB == 0 || c.Music.GainDB < -30 || c.Music.GainDB > -14 {
		c.Music.GainDB = -20
	}
	if c.Music.Library == "" {
		c.Music.Library = "./assets/music"
	}
	if c.Music.Manifest == "" {
		c.Music.Manifest = filepath.Join(c.Music.Library, "manifest.json")
	}
	if c.Zoom.Emphasis <= 1 {
		c.Zoom.Emphasis = 1.085
	}
	if c.Whisper.Language == "" {
		c.Whisper.Language = "pt"
	}
	// Keep relative paths relative to the process cwd by default; this makes CLI behavior predictable.
	_ = base
	return nil
}

// LoadEnvFile loads simple KEY=VALUE pairs without overwriting variables that
// are already present in the process environment. It intentionally ignores
// malformed lines and never returns/prints secret values.
func LoadEnvFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		_ = os.Setenv(key, value)
	}
	return nil
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
