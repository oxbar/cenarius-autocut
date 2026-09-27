package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Media describes a validated asset file.
type Media struct {
	Type     string  // video | image
	Width    int     // pixels
	Height   int     // pixels
	Duration float64 // seconds (video only)
}

// Validator checks a downloaded/local file before it is ever used by the
// renderer. HTML error pages, truncated downloads and undecodable media are
// rejected so the resolver can try the next candidate.
type Validator interface {
	Validate(ctx context.Context, path, kind string) (Media, error)
}

// FFValidator validates video with ffprobe + a one-frame decode, and images
// with Go's decoders (PNG/JPEG/GIF) or ffprobe (WebP).
type FFValidator struct {
	FFprobe string
	FFmpeg  string
}

// Sniff returns the detected content type of the first bytes of a file.
func Sniff(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return "", fmt.Errorf("arquivo vazio")
	}
	buf = buf[:n]
	trim := bytes.ToLower(bytes.TrimSpace(buf))
	if bytes.HasPrefix(trim, []byte("<!doctype")) || bytes.HasPrefix(trim, []byte("<html")) || bytes.HasPrefix(trim, []byte("<?xml")) || bytes.HasPrefix(trim, []byte("{")) {
		return "text/html", nil
	}
	// WebM/Matroska and ISO-BMFF are not in net/http's table for all Go versions.
	if len(buf) >= 4 && bytes.Equal(buf[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		return "video/webm", nil
	}
	if len(buf) >= 12 && string(buf[4:8]) == "ftyp" {
		return "video/mp4", nil
	}
	if len(buf) >= 12 && string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WEBP" {
		return "image/webp", nil
	}
	if bytes.HasPrefix(buf, []byte("OggS")) {
		return "video/ogg", nil
	}
	return http.DetectContentType(buf), nil
}

func (v FFValidator) Validate(ctx context.Context, path, kind string) (Media, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Media{}, err
	}
	if st.IsDir() || st.Size() < 64 {
		return Media{}, fmt.Errorf("arquivo pequeno demais (%d bytes)", st.Size())
	}
	ct, err := Sniff(path)
	if err != nil {
		return Media{}, err
	}
	if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "html") || strings.Contains(ct, "json") {
		return Media{}, fmt.Errorf("conteúdo não é mídia (%s)", ct)
	}
	if kind == "" {
		kind = mediaTypeForPath(path)
	}
	if kind == "image" {
		return v.validateImage(ctx, path, ct)
	}
	if kind == "audio" {
		return v.validateAudio(ctx, path)
	}
	return v.validateVideo(ctx, path)
}

// validateAudio accepts a music track only if ffprobe finds an audio stream,
// it lasts long enough to be a bed and a second of it really decodes.
func (v FFValidator) validateAudio(ctx context.Context, path string) (Media, error) {
	bin := v.FFprobe
	if bin == "" {
		bin = "ffprobe"
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "-v", "error", "-show_streams", "-show_format", "-of", "json", path).Output()
	if err != nil {
		return Media{}, fmt.Errorf("áudio inválido: ffprobe: %w", err)
	}
	var pj probeJSON
	if err := json.Unmarshal(out, &pj); err != nil {
		return Media{}, err
	}
	found := false
	for _, st := range pj.Streams {
		if st.CodecType == "audio" {
			found = true
			break
		}
	}
	if !found {
		return Media{}, fmt.Errorf("áudio inválido: nenhum stream de áudio")
	}
	d, _ := strconv.ParseFloat(pj.Format.Duration, 64)
	if d < 5 {
		return Media{}, fmt.Errorf("áudio curto demais para trilha: %.2fs", d)
	}
	ff := v.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	defer dcancel()
	cmd := exec.CommandContext(dctx, ff, "-v", "error", "-xerror", "-t", "1", "-i", path, "-vn", "-f", "null", "-")
	var er bytes.Buffer
	cmd.Stderr = &er
	if err := cmd.Run(); err != nil {
		return Media{}, fmt.Errorf("áudio não decodifica: %w: %s", err, strings.TrimSpace(er.String()))
	}
	return Media{Type: "audio", Duration: d}, nil
}

func (v FFValidator) validateImage(ctx context.Context, path, ct string) (Media, error) {
	if ct == "image/png" || ct == "image/jpeg" || ct == "image/gif" {
		f, err := os.Open(path)
		if err != nil {
			return Media{}, err
		}
		defer f.Close()
		img, _, err := image.Decode(f) // full decode catches truncated files
		if err != nil {
			return Media{}, fmt.Errorf("imagem inválida: %w", err)
		}
		b := img.Bounds()
		if b.Dx() < 64 || b.Dy() < 64 {
			return Media{}, fmt.Errorf("imagem pequena demais: %dx%d", b.Dx(), b.Dy())
		}
		return Media{Type: "image", Width: b.Dx(), Height: b.Dy()}, nil
	}
	if ct == "image/webp" || strings.HasPrefix(ct, "image/") {
		m, err := v.probe(ctx, path)
		if err != nil {
			return Media{}, fmt.Errorf("imagem inválida: %w", err)
		}
		if err := v.decodeFrame(ctx, path); err != nil {
			return Media{}, fmt.Errorf("imagem não decodifica: %w", err)
		}
		m.Type, m.Duration = "image", 0
		return m, nil
	}
	return Media{}, fmt.Errorf("tipo de imagem não suportado: %s", ct)
}

func (v FFValidator) validateVideo(ctx context.Context, path string) (Media, error) {
	m, err := v.probe(ctx, path)
	if err != nil {
		return Media{}, fmt.Errorf("vídeo inválido: %w", err)
	}
	if m.Duration < 0.5 {
		return Media{}, fmt.Errorf("vídeo curto demais: %.2fs", m.Duration)
	}
	if m.Width < 160 || m.Height < 120 {
		return Media{}, fmt.Errorf("vídeo pequeno demais: %dx%d", m.Width, m.Height)
	}
	if err := v.decodeFrame(ctx, path); err != nil {
		return Media{}, fmt.Errorf("vídeo não decodifica: %w", err)
	}
	m.Type = "video"
	return m, nil
}

type probeJSON struct {
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Duration    string `json:"duration"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (v FFValidator) probe(ctx context.Context, path string) (Media, error) {
	bin := v.FFprobe
	if bin == "" {
		bin = "ffprobe"
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "-v", "error", "-show_streams", "-show_format", "-of", "json", path).Output()
	if err != nil {
		return Media{}, fmt.Errorf("ffprobe: %w", err)
	}
	var pj probeJSON
	if err := json.Unmarshal(out, &pj); err != nil {
		return Media{}, err
	}
	for _, s := range pj.Streams {
		if s.CodecType != "video" || s.Disposition.AttachedPic == 1 {
			continue
		}
		d, _ := strconv.ParseFloat(pj.Format.Duration, 64)
		if d == 0 {
			d, _ = strconv.ParseFloat(s.Duration, 64)
		}
		return Media{Width: s.Width, Height: s.Height, Duration: d}, nil
	}
	return Media{}, fmt.Errorf("nenhum stream de vídeo")
}

func (v FFValidator) decodeFrame(ctx context.Context, path string) error {
	bin := v.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-v", "error", "-xerror", "-i", path, "-frames:v", "1", "-f", "null", "-")
	var er bytes.Buffer
	cmd.Stderr = &er
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(er.String()))
	}
	return nil
}
