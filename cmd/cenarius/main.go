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
	case "doctor":
		doctor(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}
func load(path string) config.Config {
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
	fs.Parse(args)
	if *in == "" {
		fatal(fmt.Errorf("use -i video.mp4"))
	}
	cfg := load(*c)
	if *out == "" {
		*out = filepath.Join(cfg.WorkDir, "cli")
	}
	res, err := app.Run(context.Background(), cfg, *in, *out, func(s string, p int, d string) { fmt.Printf("[%3d%%] %-12s %s\n", p, s, d) })
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nOK: %s\n", res.Output)
	fmt.Printf("duração: %.1fs -> %.1fs\n", res.OriginalDuration, res.FinalDuration)
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
	if r, err := execx.Run(context.Background(), nil, cfg.FFmpeg, "-hide_banner", "-filters"); err != nil || !hasFFmpegFilter(r.Stdout+r.Stderr, "ass") {
		fmt.Println("✗ FFmpeg sem filtro ASS/libass; legendas queimadas não funcionarão")
		fmt.Println("  macOS: rode ./scripts/fix-ffmpeg-macos.sh")
		bad = true
	} else {
		fmt.Println("✓ FFmpeg com ASS/libass")
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
	fmt.Println("CENARIUS AutoCut\n\n  cenarius doctor [-config config.json]\n  cenarius edit -i video.mp4 [-o data/cli]\n  cenarius serve [-addr 127.0.0.1:8484]")
}
func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERRO:", err)
		os.Exit(1)
	}
}
