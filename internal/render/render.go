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
	"cenarius-autocut/internal/logx"
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
	_, err := execx.Run(ctx, nil, cfg.FFmpeg, "-y", "-i", input, "-filter_complex", fc.String(), "-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-c:a", "aac", "-b:a", "192k", output)
	return err
}

// Final renders a deterministic vertical timeline. Visual assets have already
// been resolved by the pipeline. If an asset could not be resolved, a branded
// context card is rendered instead of silently dropping the visual event.
func Final(ctx context.Context, cfg config.Config, input, assPath, output string, plan planner.Plan, hasAudio bool) error {
	logger := logx.From(ctx)
	args := []string{"-y", "-i", input}
	type ov struct {
		event planner.OverlayEvent
		idx   int
	}
	ovs := []ov{}
	for _, e := range plan.Overlays {
		if strings.TrimSpace(e.Asset) == "" {
			continue
		}
		if _, err := os.Stat(e.Asset); err != nil {
			logger.Warn("visual.effect.asset_missing", "asset", e.Asset, "keyword", e.Keyword, "error", err)
			continue
		}
		dur := e.End - e.Start
		if dur < 0.5 {
			dur = 0.5
		}
		idx := 1 + len(ovs)
		args = append(args, "-loop", "1", "-framerate", strconv.Itoa(cfg.Output.FPS), "-t", fmt.Sprintf("%.3f", dur), "-i", e.Asset)
		ovs = append(ovs, ov{event: e, idx: idx})
	}

	var fc strings.Builder
	base := "[0:v]"
	fmt.Fprintf(&fc, "%sscale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,eq=contrast=1.03:saturation=1.04,unsharp=5:5:0.25,fps=%d", base, cfg.Output.Width, cfg.Output.Height, cfg.Output.Width, cfg.Output.Height, cfg.Output.FPS)
	if cfg.Zoom.Enabled && len(plan.Zooms) > 0 {
		expr := zoomExpr(plan.Zooms, cfg.Output.FPS)
		fmt.Fprintf(&fc, ",zoompan=z='%s':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=1:s=%dx%d:fps=%d", expr, cfg.Output.Width, cfg.Output.Height, cfg.Output.FPS)
	}
	fc.WriteString("[v0];")
	prev := "[v0]"

	// Resolved image overlays.
	for i, o := range ovs {
		e := o.event
		dur := e.End - e.Start
		if dur <= 0 {
			continue
		}
		prep := fmt.Sprintf("[img%d]", i)
		fadeOut := dur - 0.14
		if fadeOut < 0.15 {
			fadeOut = dur * 0.55
		}
		switch e.Mode {
		case "fullscreen":
			fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,format=rgba,fade=t=in:st=0:d=0.12:alpha=1,fade=t=out:st=%.3f:d=0.14:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;", o.idx, cfg.Output.Width, cfg.Output.Height, cfg.Output.Width, cfg.Output.Height, fadeOut, e.Start, prep)
		case "card":
			cw := int(float64(cfg.Output.Width) * 0.82)
			ch := int(float64(cfg.Output.Height) * 0.28)
			fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,format=rgba,fade=t=in:st=0:d=0.12:alpha=1,fade=t=out:st=%.3f:d=0.14:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;", o.idx, cw, ch, cw, ch, fadeOut, e.Start, prep)
		default: // bottom split-screen style
			bh := int(float64(cfg.Output.Height) * 0.40)
			fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,format=rgba,fade=t=in:st=0:d=0.12:alpha=1,fade=t=out:st=%.3f:d=0.14:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;", o.idx, cfg.Output.Width, bh, cfg.Output.Width, bh, fadeOut, e.Start, prep)
		}
		next := fmt.Sprintf("[vimg%d]", i)
		x, y := "0", "H-h"
		if e.Mode == "card" {
			x = "(W-w)/2"
			y = "H-h-210"
		} else if e.Mode == "fullscreen" {
			y = "0"
		} else if e.Position == "top" {
			y = "0"
		}
		fmt.Fprintf(&fc, "%s%s overlay=%s:%s:eof_action=pass:shortest=0:enable='between(t,%.3f,%.3f)'%s;", prev, prep, x, y, e.Start, e.End, next)
		prev = next
		logger.Info("visual.effect.render", "type", "image-overlay", "keyword", e.Keyword, "mode", e.Mode, "start", e.Start, "end", e.End, "asset", e.Asset)
	}

	// Unresolved visual events still become an animated context card. This keeps
	// the edit alive offline and makes missing remote media obvious in debug.log.
	cardN := 0
	for _, e := range plan.Overlays {
		if strings.TrimSpace(e.Asset) != "" {
			continue
		}
		next := fmt.Sprintf("[vcard%d]", cardN)
		text := strings.ToUpper(strings.TrimSpace(e.Keyword))
		if text == "" {
			text = "CONTEXTO"
		}
		x := 70
		w := cfg.Output.Width - 140
		h := int(float64(cfg.Output.Height) * 0.18)
		y := cfg.Output.Height - h - 250
		fmt.Fprintf(&fc, "%sdrawbox=x=%d:y=%d:w=%d:h=%d:color=black@0.72:t=fill:enable='between(t,%.3f,%.3f)',drawbox=x=%d:y=%d:w=9:h=%d:color=0xFFD700@1:t=fill:enable='between(t,%.3f,%.3f)',drawtext=font='Arial':text='%s':fontcolor=white:fontsize=64:borderw=2:bordercolor=black@0.9:x=(w-text_w)/2:y=%d+(%d-text_h)/2:enable='between(t,%.3f,%.3f)'%s;",
			prev, x, y, w, h, e.Start, e.End, x, y, h, e.Start, e.End, escapeDrawText(text), y, h, e.Start, e.End, next)
		prev = next
		cardN++
		logger.Info("visual.effect.render", "type", "generated-card", "keyword", e.Keyword, "start", e.Start, "end", e.End)
	}

	assEsc := escapeFilterPath(assPath)
	if cfg.Captions.Enabled && assPath != "" {
		fmt.Fprintf(&fc, "%sass='%s'[vout]", prev, assEsc)
	} else {
		fmt.Fprintf(&fc, "%snull[vout]", prev)
	}

	// Generated SFX are intentionally subtle and require no downloaded audio
	// pack. They are mixed underneath speech and then the whole mix is loudness
	// normalized. This is fully local/free.
	audioMap := "0:a?"
	if hasAudio && len(plan.SFX) > 0 {
		labels := []string{"[0:a]"}
		for i, s := range plan.SFX {
			label := fmt.Sprintf("[sfx%d]", i)
			delay := int(s.Time * 1000)
			gain := s.GainDB
			if gain == 0 {
				gain = -22
			}
			if s.Name == "whoosh" {
				fmt.Fprintf(&fc, ";anoisesrc=color=pink:sample_rate=48000:duration=0.22,highpass=f=650,lowpass=f=5200,volume=%.1fdB,afade=t=in:st=0:d=0.04,afade=t=out:st=0.10:d=0.12,adelay=%d|%d%s", gain, delay, delay, label)
			} else {
				fmt.Fprintf(&fc, ";sine=frequency=920:sample_rate=48000:duration=0.09,volume=%.1fdB,afade=t=out:st=0.035:d=0.055,adelay=%d|%d%s", gain, delay, delay, label)
			}
			labels = append(labels, label)
			logger.Info("audio.sfx.render", "name", s.Name, "time", s.Time, "gain_db", gain)
		}
		fmt.Fprintf(&fc, ";%samix=inputs=%d:duration=first:normalize=0,loudnorm=I=-16:TP=-1.5:LRA=11[aout]", strings.Join(labels, ""), len(labels))
		audioMap = "[aout]"
	}

	script := output + ".filter"
	if err := os.WriteFile(script, []byte(fc.String()), 0644); err != nil {
		return err
	}
	args = append(args, "-filter_complex", fc.String(), "-map", "[vout]", "-map", audioMap)
	if !(hasAudio && len(plan.SFX) > 0) {
		args = append(args, "-af", "loudnorm=I=-16:TP=-1.5:LRA=11")
	}
	args = append(args, "-c:v", "libx264", "-preset", cfg.Output.Preset, "-crf", strconv.Itoa(cfg.Output.CRF), "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", cfg.Output.AudioBitrate, "-movflags", "+faststart", "-shortest", output)
	_, err := execx.Run(ctx, nil, cfg.FFmpeg, args...)
	return err
}

func zoomExpr(zs []planner.ZoomEvent, fps int) string {
	expr := "1.0"
	for i := len(zs) - 1; i >= 0; i-- {
		a := int(zs[i].Start * float64(fps))
		b := int(zs[i].End * float64(fps))
		if b <= a {
			continue
		}
		// Smooth in/out instead of an abrupt digital zoom jump.
		delta := zs[i].Scale - 1.0
		expr = fmt.Sprintf("if(between(on,%d,%d),1+%.5f*(0.5-0.5*cos(2*PI*(on-%d)/(%d-%d))),%s)", a, b, delta, a, b, a, expr)
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

func escapeDrawText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, ":", "\\:")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return s
}
