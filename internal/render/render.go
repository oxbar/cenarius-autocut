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
		event   planner.OverlayEvent
		idx     int
		isVideo bool
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
		isVideo := e.AssetType == "video" || isVideoPath(e.Asset)
		idx := 1 + len(ovs)
		if isVideo {
			// Loop the clip so a short source can still cover the planned interval.
			// We discard B-roll audio and keep the presenter's original voice.
			args = append(args, "-stream_loop", "-1", "-ss", "0.50", "-i", e.Asset)
		} else {
			args = append(args, "-loop", "1", "-framerate", strconv.Itoa(cfg.Output.FPS), "-t", fmt.Sprintf("%.3f", dur), "-i", e.Asset)
		}
		ovs = append(ovs, ov{event: e, idx: idx, isVideo: isVideo})
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

	// Real visual assets: video is preferred. Still images get a Ken Burns push
	// so even an image feels like B-roll rather than a frozen card.
	for i, o := range ovs {
		e := o.event
		dur := e.End - e.Start
		if dur <= 0 {
			continue
		}
		mode := e.Mode
		if mode == "bottom" {
			mode = "reaction"
		}
		prep := fmt.Sprintf("[br%d]", i)
		fadeOut := dur - 0.16
		if fadeOut < 0.18 {
			fadeOut = dur * 0.55
		}

		panelH := int(float64(cfg.Output.Height) * 0.48)
		if mode == "fullscreen" {
			panelH = cfg.Output.Height
		} else if mode == "card" {
			panelH = int(float64(cfg.Output.Height) * 0.30)
		}
		panelW := cfg.Output.Width
		if mode == "card" {
			panelW = int(float64(cfg.Output.Width) * 0.86)
		}

		if o.isVideo {
			fmt.Fprintf(&fc, "[%d:v]trim=duration=%.3f,setpts=PTS-STARTPTS,scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,fps=%d,eq=contrast=1.05:saturation=1.08,format=rgba,fade=t=in:st=0:d=0.10:alpha=1,fade=t=out:st=%.3f:d=0.16:alpha=1,setpts=PTS+%.3f/TB%s;",
				o.idx, dur, panelW, panelH, panelW, panelH, cfg.Output.FPS, fadeOut, e.Start, prep)
		} else {
			// Scale a little larger and move slowly for a subtle Ken Burns effect.
			fmt.Fprintf(&fc, "[%d:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,zoompan=z='min(zoom+0.0012,1.075)':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=1:s=%dx%d:fps=%d,format=rgba,fade=t=in:st=0:d=0.10:alpha=1,fade=t=out:st=%.3f:d=0.16:alpha=1,setpts=PTS+%.3f/TB%s;",
				o.idx, panelW, panelH, panelW, panelH, panelW, panelH, cfg.Output.FPS, fadeOut, e.Start, prep)
		}

		next := fmt.Sprintf("[vbr%d]", i)
		x, y := "0", "H-h"
		if mode == "card" {
			x = "(W-w)/2"
			y = "H-h-190"
		} else if mode == "fullscreen" {
			y = "0"
		} else if e.Position == "top" {
			y = "0"
		}
		fmt.Fprintf(&fc, "%s%s overlay=%s:%s:eof_action=pass:shortest=0:enable='between(t,%.3f,%.3f)'%s;", prev, prep, x, y, e.Start, e.End, next)
		prev = next

		// Reaction split gets a branded separator so the composition reads as an
		// intentional two-source edit, like podcast/react Shorts.
		if mode == "reaction" {
			lineY := cfg.Output.Height - panelH
			lined := fmt.Sprintf("[vline%d]", i)
			fmt.Fprintf(&fc, "%sdrawbox=x=0:y=%d:w=%d:h=7:color=0xFFD700@0.95:t=fill:enable='between(t,%.3f,%.3f)'%s;", prev, lineY, cfg.Output.Width, e.Start, e.End, lined)
			prev = lined
		}
		logger.Info("visual.effect.render", "type", "broll", "keyword", e.Keyword, "mode", mode, "media_type", map[bool]string{true: "video", false: "image"}[o.isVideo], "start", e.Start, "end", e.End, "asset", e.Asset)
	}

	// If remote/local media could not be resolved, create an animated self-B-roll
	// split instead of a static black rectangle. It is intentionally a fallback:
	// debug.log still records self-broll so missing assets remain visible.
	fallbackN := 0
	for _, e := range plan.Overlays {
		if strings.TrimSpace(e.Asset) != "" {
			continue
		}
		panelH := int(float64(cfg.Output.Height) * 0.46)
		mainLabel := fmt.Sprintf("[fbmain%d]", fallbackN)
		srcLabel := fmt.Sprintf("[fbsrc%d]", fallbackN)
		panelLabel := fmt.Sprintf("[fbpanel%d]", fallbackN)
		next := fmt.Sprintf("[vfb%d]", fallbackN)
		text := strings.ToUpper(strings.TrimSpace(e.Keyword))
		if text == "" {
			text = "CONTEXTO"
		}
		// split current frame -> duplicate lower panel -> strong crop/blur/dim -> overlay
		fmt.Fprintf(&fc, "%ssplit=2%s%s;", prev, mainLabel, srcLabel)
		fmt.Fprintf(&fc, "%scrop=iw:ih*0.46:0:ih*0.27,scale=%d:%d,boxblur=12:2,eq=brightness=-0.18:saturation=1.25,format=rgba%s;", srcLabel, cfg.Output.Width, panelH, panelLabel)
		fmt.Fprintf(&fc, "%s%soverlay=0:H-h:enable='between(t,%.3f,%.3f)',drawbox=x=0:y=%d:w=%d:h=7:color=0xFFD700@0.95:t=fill:enable='between(t,%.3f,%.3f)',drawtext=font='Arial':text='%s':fontcolor=white:fontsize=56:borderw=3:bordercolor=black@0.9:x=(w-text_w)/2:y=%d:enable='between(t,%.3f,%.3f)'%s;",
			mainLabel, panelLabel, e.Start, e.End, cfg.Output.Height-panelH, cfg.Output.Width, e.Start, e.End, escapeDrawText(text), cfg.Output.Height-panelH+70, e.Start, e.End, next)
		prev = next
		fallbackN++
		logger.Info("visual.effect.render", "type", "self-broll-fallback", "keyword", e.Keyword, "start", e.Start, "end", e.End)
	}

	assEsc := escapeFilterPath(assPath)
	if cfg.Captions.Enabled && assPath != "" {
		fmt.Fprintf(&fc, "%sass='%s'[vout]", prev, assEsc)
	} else {
		fmt.Fprintf(&fc, "%snull[vout]", prev)
	}

	// Generated SFX are subtle and free/local. They sit under speech.
	audioMap := "0:a?"
	if hasAudio && len(plan.SFX) > 0 {
		labels := []string{"[0:a]"}
		for i, s := range plan.SFX {
			label := fmt.Sprintf("[sfx%d]", i)
			delay := int(s.Time * 1000)
			gain := s.GainDB
			if gain == 0 {
				gain = -24
			}
			if s.Name == "whoosh" {
				fmt.Fprintf(&fc, ";anoisesrc=color=pink:sample_rate=48000:duration=0.28,highpass=f=500,lowpass=f=6000,volume=%.1fdB,afade=t=in:st=0:d=0.035,afade=t=out:st=0.11:d=0.16,adelay=%d|%d%s", gain, delay, delay, label)
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

func isVideoPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".webm", ".mp4", ".m4v", ".mov", ".ogv", ".ogg", ".mpeg", ".mpg":
		return true
	default:
		return false
	}
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
