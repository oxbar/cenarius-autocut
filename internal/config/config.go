package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	WorkDir  string        `json:"work_dir"`
	FFmpeg   string        `json:"ffmpeg"`
	FFprobe  string        `json:"ffprobe"`
	Whisper  WhisperConfig `json:"whisper"`
	Ollama   OllamaConfig  `json:"ollama"`
	Output   OutputConfig  `json:"output"`
	Cuts     CutConfig     `json:"cuts"`
	Captions CaptionConfig `json:"captions"`
	Zoom     ZoomConfig    `json:"zoom"`
	Broll    BrollConfig   `json:"broll"`
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
	Enabled         bool   `json:"enabled"`
	FontName        string `json:"font_name"`
	FontSize        int    `json:"font_size"`
	PrimaryColor    string `json:"primary_color"`
	HighlightColor  string `json:"highlight_color"`
	OutlineColor    string `json:"outline_color"`
	Outline         int    `json:"outline"`
	Shadow          int    `json:"shadow"`
	MarginV         int    `json:"margin_v"`
	SafeMargin      int    `json:"safe_margin"`
	MaxWords        int    `json:"max_words"`
	MaxCharsPerLine int    `json:"max_chars_per_line"`
	MaxLines        int    `json:"max_lines"`
	Uppercase       bool   `json:"uppercase"`
	ActiveWord      bool   `json:"active_word"`
}

type ZoomConfig struct {
	Enabled  bool    `json:"enabled"`
	Mild     float64 `json:"mild"`
	Punch    float64 `json:"punch"`
	MinGap   float64 `json:"min_gap"`
	Duration float64 `json:"duration"`
}

type BrollConfig struct {
	Enabled   bool   `json:"enabled"`
	AssetDir  string `json:"asset_dir"`
	Manifest  string `json:"manifest"`
	MaxEvents int    `json:"max_events"`
}

func Default() Config {
	return Config{
		WorkDir:  "./data",
		FFmpeg:   "ffmpeg",
		FFprobe:  "ffprobe",
		Whisper:  WhisperConfig{Binary: "whisper-cli", Model: "./models/ggml-large-v3-turbo.bin", Language: "pt", Prompt: "Português brasileiro. Tecnologia, inteligência artificial, IA, programação, software, engenharia de software, Java, Spring Boot, ChatGPT, OpenAI, Claude, Gemini, Docker, Kubernetes, GitHub, LinkedIn, Instagram, TikTok, YouTube.", Threads: 0},
		Ollama:   OllamaConfig{Enabled: true, URL: "http://127.0.0.1:11434", Model: "qwen3:8b"},
		Output:   OutputConfig{Width: 1080, Height: 1920, FPS: 30, CRF: 18, Preset: "medium", AudioBitrate: "192k"},
		Cuts:     CutConfig{Enabled: true, NoiseDB: -42, MinSilence: 1.10, KeepSilence: 0.25, MinKeepSegment: 0.20},
		Captions: CaptionConfig{Enabled: true, FontName: "Arial", FontSize: 82, PrimaryColor: "&H00FFFFFF", HighlightColor: "&H0000D7FF", OutlineColor: "&H00000000", Outline: 6, Shadow: 0, MarginV: 560, SafeMargin: 96, MaxWords: 6, MaxCharsPerLine: 24, MaxLines: 2, Uppercase: true, ActiveWord: true},
		Zoom:     ZoomConfig{Enabled: true, Mild: 1.035, Punch: 1.08, MinGap: 5.0, Duration: 1.0},
		Broll:    BrollConfig{Enabled: true, AssetDir: "./assets/broll", Manifest: "./assets/manifest.json", MaxEvents: 3},
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
		c.Captions.SafeMargin = 96
	}
	if c.Whisper.Language == "" {
		c.Whisper.Language = "pt"
	}
	// Keep relative paths relative to the process cwd by default; this makes CLI behavior predictable.
	_ = base
	return nil
}
