package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cenarius-autocut/internal/app"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
	"cenarius-autocut/internal/shorts"
	"cenarius-autocut/internal/webui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "edit":
		edit(os.Args[2:])
	case "react":
		react(os.Args[2:])
	case "shorts":
		shortsCmd(os.Args[2:])
	case "doctor":
		doctor(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

// musicOpts are the emotional-audio flags shared by edit and shorts.
type musicOpts struct {
	file, mood *string
	off        *bool
}

func musicFlags(fs *flag.FlagSet) musicOpts {
	return musicOpts{
		file: fs.String("music", "", "trilha de fundo específica (arquivo licenciado: sua, YouTube Audio Library, etc.)"),
		mood: fs.String("mood", "", "emoção da trilha: auto|energetic|inspiring|tense|dramatic|chill|funny"),
		off:  fs.Bool("no-music", false, "desativa trilha e efeitos emocionais"),
	}
}

func (m musicOpts) apply(cfg *config.Config) {
	if *m.off {
		cfg.Music.Enabled, cfg.Music.EmotionSFX = false, false
		return
	}
	if *m.file != "" {
		if _, err := os.Stat(*m.file); err != nil {
			fatal(fmt.Errorf("trilha -music não encontrada: %w", err))
		}
		cfg.Music.File = *m.file
		cfg.Music.Enabled = true
	}
	if *m.mood != "" {
		cfg.Music.Mood = *m.mood
		_ = cfg.Normalize("") // validates the mood (unknown -> auto)
	}
}

func load(path string) config.Config {
	// Optional local secrets. Existing shell environment wins over .env.
	if err := config.LoadEnvFile(filepath.Join(filepath.Dir(path), ".env")); err != nil {
		fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(cfg.WorkDir, 0755); err != nil {
		fatal(err)
	}
	return cfg
}
func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	c := fs.String("config", "config.json", "config")
	addr := fs.String("addr", "127.0.0.1:8484", "endereço")
	fs.Parse(args)
	cfg := load(*c)
	fatal(webui.Listen(*addr, cfg))
}
func edit(args []string) {
	fs := flag.NewFlagSet("edit", flag.ExitOnError)
	c := fs.String("config", "config.json", "config")
	in := fs.String("i", "", "vídeo de entrada")
	out := fs.String("o", "", "diretório de saída")
	mf := musicFlags(fs)
	fs.Parse(args)
	if *in == "" {
		fatal(fmt.Errorf("use -i video.mp4"))
	}
	cfg := load(*c)
	mf.apply(&cfg)
	if *out == "" {
		*out = filepath.Join(cfg.WorkDir, "cli")
	}
	res, err := app.Run(context.Background(), cfg, *in, *out, func(s string, p int, d string) { fmt.Printf("[%3d%%] %-12s %s\n", p, s, d) })
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nOK: %s\n", res.Output)
	fmt.Printf("duração: %.1fs -> %.1fs\n", res.OriginalDuration, res.FinalDuration)
	fmt.Printf("debug: %s\n", res.DebugLog)
}

func react(args []string) {
	fs := flag.NewFlagSet("react", flag.ExitOnError)
	c := fs.String("config", "config.json", "config")
	in := fs.String("i", "", "seu vídeo / A-roll")
	reaction := fs.String("reaction", "", "vídeo para reagir")
	audio := fs.String("audio", "duck", "áudio do reagido: duck|muted|original")
	out := fs.String("o", "", "diretório de saída")
	fs.Parse(args)
	if *in == "" || *reaction == "" {
		fatal(fmt.Errorf("use react -i creator.mov -reaction video.mp4"))
	}
	cfg := load(*c)
	if *out == "" {
		*out = filepath.Join(cfg.WorkDir, "reaction-cli")
	}
	res, err := app.RunWithOptions(context.Background(), cfg, *in, *out, func(s string, p int, d string) {
		fmt.Printf("[%3d%%] %-16s %s\n", p, s, d)
	}, app.RunOptions{Mode: app.ModeReaction, Reaction: &app.ReactionOptions{Path: *reaction, AudioMode: *audio}})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nOK: %s\n", res.Output)
}

func shortsCmd(args []string) {
	fs := flag.NewFlagSet("shorts", flag.ExitOnError)
	c := fs.String("config", "config.json", "config")
	in := fs.String("i", "", "vídeo longo local")
	url := fs.String("url", "", "URL do YouTube")
	out := fs.String("o", "", "diretório de saída")
	count := fs.Int("count", 5, "quantidade de cortes")
	generate := fs.Bool("generate", false, "gerar/editar todos os cortes selecionados")
	mf := musicFlags(fs)
	fs.Parse(args)
	if *in == "" && *url == "" {
		fatal(fmt.Errorf("use shorts -i video.mp4 ou shorts -url https://youtube.com/..."))
	}
	cfg := load(*c)
	mf.apply(&cfg)
	if *out == "" {
		*out = filepath.Join(cfg.WorkDir, "shorts-cli")
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fatal(err)
	}
	source := *in
	if source == "" {
		var err error
		source, err = shorts.DownloadYouTube(context.Background(), cfg, *url, filepath.Join(*out, "youtube"), func(stage string, p int, d string) {
			fmt.Printf("[%3d%%] %-16s %s\n", p, stage, d)
		})
		if err != nil {
			fatal(err)
		}
	}
	analysis, err := shorts.Analyze(context.Background(), cfg, source, filepath.Join(*out, "analysis"), *count, func(stage string, p int, d string) {
		fmt.Printf("[%3d%%] %-16s %s\n", p, stage, d)
	})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\n%d cortes encontrados:\n", len(analysis.Candidates))
	for _, x := range analysis.Candidates {
		fmt.Printf("- %s %.1fs-%.1fs (%0.1fs) score=%d/100 — %s\n", x.ID, x.Start, x.End, x.Duration, x.Scores.Total, x.Title)
	}
	fmt.Printf("highlights: %s\n", analysis.HighlightsJSON)
	if !*generate {
		return
	}
	for i, x := range analysis.Candidates {
		dir := filepath.Join(*out, "generated", x.ID)
		gen, err := shorts.Generate(context.Background(), cfg, source, x, dir, func(stage string, p int, d string) {
			fmt.Printf("[%d/%d %3d%%] %-16s %s\n", i+1, len(analysis.Candidates), p, stage, d)
		})
		if err != nil {
			fatal(err)
		}
		fmt.Printf("  -> %s\n", gen.Result.Output)
	}
}

func hasFFmpegFilter(output, name string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

func doctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	c := fs.String("config", "config.json", "config")
	fs.Parse(args)
	cfg := load(*c)
	checks := []struct{ name, path string }{{"ffmpeg", cfg.FFmpeg}, {"ffprobe", cfg.FFprobe}, {"whisper-cli", cfg.Whisper.Binary}}
	bad := false
	for _, x := range checks {
		if err := execx.LookPath(x.path); err != nil {
			fmt.Printf("✗ %s: %v\n", x.name, err)
			bad = true
		} else {
			fmt.Printf("✓ %s\n", x.name)
		}
	}
	if err := execx.LookPath(cfg.YTDLP); err != nil {
		fmt.Printf("! yt-dlp: %v (necessário apenas para Longo → Shorts via YouTube)\n", err)
	} else {
		fmt.Println("✓ yt-dlp (YouTube)")
	}
	if r, err := execx.Run(context.Background(), nil, cfg.FFmpeg, "-hide_banner", "-filters"); err != nil {
		fmt.Println("✗ não foi possível listar filtros do FFmpeg")
		bad = true
	} else {
		filters := r.Stdout + r.Stderr
		if !hasFFmpegFilter(filters, "ass") {
			fmt.Println("✗ FFmpeg sem filtro ASS/libass; legendas queimadas não funcionarão")
			fmt.Println("  macOS: rode ./scripts/fix-ffmpeg-macos.sh")
			bad = true
		} else {
			fmt.Println("✓ FFmpeg com ASS/libass")
		}
		if !hasFFmpegFilter(filters, "drawtext") {
			fmt.Println("✗ FFmpeg sem drawtext; cards visuais de fallback não funcionarão")
			bad = true
		} else {
			fmt.Println("✓ FFmpeg com drawtext")
		}
	}
	if _, err := os.Stat(cfg.Whisper.Model); err != nil {
		fmt.Printf("✗ modelo Whisper: %s\n", cfg.Whisper.Model)
		bad = true
	} else {
		fmt.Printf("✓ modelo Whisper: %s\n", cfg.Whisper.Model)
	}
	if bad {
		os.Exit(1)
	}
}
func usage() {
	fmt.Println("CENARIUS Studio\n\n  cenarius doctor [-config config.json]\n  cenarius edit -i video.mp4 [-o data/cli]\n  cenarius react -i creator.mov -reaction video.mp4 [-audio duck]\n  cenarius shorts -i longo.mp4 [-count 5] [-generate]\n  cenarius shorts -url https://youtube.com/... [-count 5] [-generate]\n  cenarius serve [-addr 127.0.0.1:8484]")
}
func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERRO:", err)
		os.Exit(1)
	}
}
