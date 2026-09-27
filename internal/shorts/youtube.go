package shorts

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
)

func ValidateYouTubeURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("URL do YouTube inválida")
	}
	h := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if h != "youtube.com" && h != "m.youtube.com" && h != "youtu.be" && h != "music.youtube.com" {
		return fmt.Errorf("somente URLs do YouTube são aceitas")
	}
	return nil
}

func DownloadYouTube(ctx context.Context, cfg config.Config, rawURL, outDir string, progress Progress) (string, error) {
	if err := ValidateYouTubeURL(rawURL); err != nil {
		return "", err
	}
	if err := execx.LookPath(cfg.YTDLP); err != nil {
		return "", fmt.Errorf("yt-dlp é necessário para URL do YouTube: %w", err)
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	if progress != nil {
		progress("youtube", 3, "baixando vídeo do YouTube com yt-dlp")
	}
	tpl := filepath.Join(outDir, "youtube-source.%(ext)s")
	res, err := execx.Run(ctx, nil, cfg.YTDLP,
		"--no-playlist", "--no-write-info-json", "--no-write-thumbnail", "--merge-output-format", "mp4",
		"--format", "bv*+ba/b", "--output", tpl, "--print", "after_move:filepath", strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		p := strings.TrimSpace(lines[i])
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "youtube-source.*"))
	for _, p := range matches {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("yt-dlp terminou sem informar o arquivo baixado")
}
