package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/silence"
)

func Cut(ctx context.Context, cfg config.Config, input, output string, keep []silence.Interval) error {
	if len(keep) == 1 && keep[0].Start < 0.001 { // still re-encode to a stable intermediate
		_, err := execx.Run(ctx, nil, cfg.FFmpeg, "-y", "-i", input, "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-c:a", "aac", "-b:a", "192k", output)
		return err
	}
	var fc strings.Builder
	labels := make([]string, 0, len(keep)*2)
	for i, k := range keep {
		fmt.Fprintf(&fc, "[0:v]trim=start=%.3f:end=%.3f,setpts=PTS-STARTPTS[v%d];", k.Start, k.End, i)
		fmt.Fprintf(&fc, "[0:a]atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS[a%d];", k.Start, k.End, i)
		labels = append(labels, fmt.Sprintf("[v%d][a%d]", i, i))
	}
	fc.WriteString(strings.Join(labels, ""))
	fmt.Fprintf(&fc, "concat=n=%d:v=1:a=1[v][a]", len(keep))
	script := output + ".filter"
	if err := os.WriteFile(script, []byte(fc.String()), 0644); err != nil {
		return err
	}
	// FFmpeg 9 removed -filter_complex_script. Keep the .filter file as a
	// debugging artifact, but pass the graph directly with -filter_complex.
	_, err := execx.Run(ctx, nil, cfg.FFmpeg, "-y", "-i", input, "-filter_complex", fc.String(), "-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-c:a", "aac", "-b:a", "192k", output)
	return err
}

func Final(ctx context.Context, cfg config.Config, input, assPath, output string, plan planner.Plan) error {
	manifest, _ := LoadManifest(cfg.Broll.Manifest, cfg.Broll.AssetDir)
	args := []string{"-y", "-i", input}
	type ov struct {
		event planner.OverlayEvent
		idx   int
		file  string
	}
	ovs := []ov{}
	for _, e := range plan.Overlays {
		if p, ok := findAsset(manifest, e.Keyword); ok {
			if _, err := os.Stat(p); err == nil {
				idx := len(args)
				_ = idx
				args = append(args, "-loop", "1", "-i", p)
				ovs = append(ovs, ov{e, len(ovs) + 1, p})
			}
		}
	}
	var fc strings.Builder
	base := "[0:v]"
	// normalize canvas then zoompan. zoom expression uses frame number at configured fps.
	fmt.Fprintf(&fc, "%sscale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,eq=contrast=1.03:saturation=1.04,unsharp=5:5:0.25", base, cfg.Output.Width, cfg.Output.Height, cfg.Output.Width, cfg.Output.Height)
	if cfg.Zoom.Enabled && len(plan.Zooms) > 0 {
		expr := zoomExpr(plan.Zooms, cfg.Output.FPS)
		fmt.Fprintf(&fc, ",zoompan=z='%s':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=1:s=%dx%d:fps=%d", expr, cfg.Output.Width, cfg.Output.Height, cfg.Output.FPS)
	} else {
		fmt.Fprintf(&fc, ",fps=%d", cfg.Output.FPS)
	}
	fc.WriteString("[v0];")
	prev := "[v0]"
	for i, o := range ovs {
		in := fmt.Sprintf("[%d:v]", o.idx)
		prep := fmt.Sprintf("[img%d]", i)
		// Reference-style contextual B-roll: fill the width and occupy roughly
		// the lower 40%% of a 9:16 canvas instead of floating as a tiny sticker.
		brollH := int(float64(cfg.Output.Height) * 0.40)
		fmt.Fprintf(&fc, "%sscale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d%s;", in, cfg.Output.Width, brollH, cfg.Output.Width, brollH, prep)
		next := fmt.Sprintf("[v%d]", i+1)
		y := "H-h"
		if o.event.Position == "top" {
			y = "0"
		}
		fmt.Fprintf(&fc, "%s%s overlay=0:%s:enable='between(t,%.3f,%.3f)'%s;", prev, prep, y, o.event.Start, o.event.End, next)
		prev = next
	}
	// ASS path escaping for ffmpeg filter.
	assEsc := escapeFilterPath(assPath)
	if cfg.Captions.Enabled && assPath != "" {
		fmt.Fprintf(&fc, "%sass='%s'[vout]", prev, assEsc)
	} else {
		fmt.Fprintf(&fc, "%snull[vout]", prev)
	}
	script := output + ".filter"
	if err := os.WriteFile(script, []byte(fc.String()), 0644); err != nil {
		return err
	}
	// FFmpeg 9 removed -filter_complex_script. Passing the graph as one argv
	// value is safe here because exec.Command does not invoke a shell.
	args = append(args, "-filter_complex", fc.String(), "-map", "[vout]", "-map", "0:a?", "-af", "loudnorm=I=-16:TP=-1.5:LRA=11", "-c:v", "libx264", "-preset", cfg.Output.Preset, "-crf", strconv.Itoa(cfg.Output.CRF), "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", cfg.Output.AudioBitrate, "-movflags", "+faststart", "-shortest", output)
	_, err := execx.Run(ctx, nil, cfg.FFmpeg, args...)
	return err
}

func findAsset(m map[string]string, kw string) (string, bool) {
	q := strings.ToLower(strings.TrimSpace(kw))
	if p, ok := m[q]; ok {
		return p, true
	}
	for k, p := range m {
		if strings.Contains(q, k) || strings.Contains(k, q) {
			return p, true
		}
	}
	return "", false
}
func zoomExpr(zs []planner.ZoomEvent, fps int) string {
	expr := "1.0"
	for i := len(zs) - 1; i >= 0; i-- {
		a := int(zs[i].Start * float64(fps))
		b := int(zs[i].End * float64(fps))
		expr = fmt.Sprintf("if(between(on,%d,%d),%.4f,%s)", a, b, zs[i].Scale, expr)
	}
	return expr
}
func escapeFilterPath(p string) string {
	p, _ = filepath.Abs(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.ReplaceAll(p, "'", "\\'")
	p = strings.ReplaceAll(p, ":", "\\:")
	return p
}
