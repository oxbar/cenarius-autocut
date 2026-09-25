package assets

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// GenerateMotion renders a short procedural motion graphic with FFmpeg only:
// animated gradient, subtle tech grid, glow behind a big heavy label, accent
// bar and a gentle entrance. It is used before self-broll when no real
// footage/image was found, and it never looks like a flat black card.
func GenerateMotion(ctx context.Context, ffmpeg, label, out string, w, h int, dur float64) error {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	label = strings.ToUpper(strings.TrimSpace(label))
	if label == "" {
		return fmt.Errorf("label vazio")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	lines := wrapLabel(label, 14)
	longest := 0
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n > longest {
			longest = n
		}
	}
	// Heavy, high-contrast type sized to fit 86% of the width.
	fs := int(math.Min(150, math.Max(64, float64(w)*0.86/(float64(longest)*0.62))))
	lineH := int(float64(fs) * 1.12)
	top := h/2 - (lineH*len(lines))/2

	tmpDir, err := os.MkdirTemp(filepath.Dir(out), "motion-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	barY := top + lineH*len(lines) + 24
	bg := fmt.Sprintf("gradients=s=%dx%d:r=30:d=%.3f:c0=0x0B1026:c1=0x2B0F63:c2=0x06283D:x0=0:y0=0:x1=%d:y1=%d:speed=0.015,format=yuv420p,"+
		"drawgrid=w=90:h=90:t=2:c=white@0.06,vignette=PI/5", w, h, dur, w, h)
	bar := fmt.Sprintf("drawbox=x=(iw-min(iw*0.5\\,t*1400))/2:y=%d:w=min(iw*0.5\\,t*1400):h=10:color=0x5EE7FF@0.95:t=fill,format=yuv420p[v]", barY)

	var fc string
	switch {
	case FFmpegHasFilter(ffmpeg, "drawtext"):
		font := DrawtextFont()
		var glow, text strings.Builder
		for i, l := range lines {
			tf := filepath.Join(tmpDir, fmt.Sprintf("l%d.txt", i))
			if err := os.WriteFile(tf, []byte(l), 0644); err != nil {
				return err
			}
			y := top + i*lineH
			esc := escapePath(tf)
			fmt.Fprintf(&glow, ",drawtext=textfile='%s':%s:fontsize=%d:fontcolor=0x5EE7FF:borderw=14:bordercolor=0x5EE7FF@0.8:x=(w-text_w)/2:y=%d", esc, font, fs, y)
			fmt.Fprintf(&text, ",drawtext=textfile='%s':%s:fontsize=%d:fontcolor=white:borderw=4:bordercolor=black@0.55:x=(w-text_w)/2:y=%d+26*max(0\\,1-t/0.35):alpha='min(1,t/0.25)'", esc, font, fs, y)
		}
		fc = fmt.Sprintf("%s[bg];color=c=black@0.0:s=%dx%d:r=30:d=%.3f,format=rgba%s,boxblur=26:3[glow];[bg][glow]overlay=0:0:format=auto%s,%s",
			bg, w, h, dur, glow.String(), text.String(), bar)
	case FFmpegHasFilter(ffmpeg, "ass"):
		// FFmpeg without libfreetype/drawtext (e.g. Homebrew's plain ffmpeg):
		// draw the same heavy label with libass instead.
		assPath := filepath.Join(tmpDir, "label.ass")
		if err := os.WriteFile(assPath, []byte(motionASS(lines, w, h, fs, top+(lineH*len(lines))/2)), 0644); err != nil {
			return err
		}
		fc = fmt.Sprintf("%s,ass=filename='%s',%s", bg, escapePath(assPath), bar)
	default:
		return fmt.Errorf("ffmpeg (%s) não tem drawtext nem ass: use ffmpeg-full (./scripts/fix-ffmpeg-macos.sh)", ffmpeg)
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tmpOut := out + ".part.mp4"
	cmd := exec.CommandContext(cctx, ffmpeg, "-hide_banner", "-v", "error", "-y", "-filter_complex", fc, "-map", "[v]",
		"-t", fmt.Sprintf("%.3f", dur), "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-an", tmpOut)
	if b, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpOut)
		return fmt.Errorf("motion graphic: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return os.Rename(tmpOut, out)
}

// motionASS renders the label as an ASS script: a blurred cyan glow layer and
// a bold white layer with a short slide/fade entrance.
func motionASS(lines []string, w, h, fs, cy int) string {
	text := strings.Join(lines, "\\N")
	var b strings.Builder
	fmt.Fprintf(&b, "[Script Info]\nScriptType: v4.00+\nPlayResX: %d\nPlayResY: %d\nScaledBorderAndShadow: yes\n\n", w, h)
	b.WriteString("[V4+ Styles]\nFormat: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\n")
	fmt.Fprintf(&b, "Style: Glow,Arial,%d,&H00FFE75E,&H00FFE75E,&H33FFE75E,&H00000000,-1,0,0,0,100,100,0,0,1,9,0,5,20,20,20,1\n", fs)
	fmt.Fprintf(&b, "Style: Label,Arial,%d,&H00FFFFFF,&H00FFFFFF,&H73000000,&H00000000,-1,0,0,0,100,100,0,0,1,4,0,5,20,20,20,1\n\n", fs)
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	fmt.Fprintf(&b, "Dialogue: 0,0:00:00.00,9:59:59.00,Glow,,0,0,0,,{\\pos(%d,%d)\\blur14}%s\n", w/2, cy, text)
	fmt.Fprintf(&b, "Dialogue: 1,0:00:00.00,9:59:59.00,Label,,0,0,0,,{\\move(%d,%d,%d,%d,0,350)\\fad(250,0)}%s\n", w/2, cy+26, w/2, cy, text)
	return b.String()
}

func wrapLabel(s string, max int) []string {
	words := strings.Fields(s)
	if len(words) <= 1 || utf8.RuneCountInString(s) <= max {
		return []string{s}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if utf8.RuneCountInString(cur+" "+w) > max && len(lines) < 1 {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	return append(lines, cur)
}

func escapePath(p string) string {
	p, _ = filepath.Abs(p)
	p = strings.ReplaceAll(p, "\\", "/")
	// Inside single quotes the filtergraph parser keeps ':' and ',' literal;
	// only a quote needs special handling.
	return strings.ReplaceAll(p, "'", "'\\''")
}
