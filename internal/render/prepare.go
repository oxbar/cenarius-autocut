package render

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/planner"
)

// ClipSpec describes how a B-roll asset must be normalised before the final
// composition: exact size, exact duration, 30 fps, no audio.
type ClipSpec struct {
	Width, Height int
	Duration      float64
	Fit           bool // cards/PiP: contain over a blurred fill instead of crop
	Motion        int  // Ken Burns variant for still images (0 zoom-in, 1 pan)
}

// PrepareClip turns any asset (video or still) into a silent H.264 clip of
// exactly spec.Duration seconds at the output frame rate. Video is trimmed from
// a good offset, looped only when shorter than needed, and cropped to fill the
// layout while keeping its aspect ratio (never stretched). Still images get a
// discreet Ken Burns move (1.00 -> ~1.05 zoom or a slow pan).
func PrepareClip(ctx context.Context, cfg config.Config, a planner.AssetRef, spec ClipSpec, out string) error {
	w, h := even(spec.Width), even(spec.Height)
	fps := cfg.Output.FPS
	dur := spec.Duration
	frames := int(math.Ceil(dur * float64(fps)))
	if frames < 1 {
		return fmt.Errorf("duração inválida para clip: %.3f", dur)
	}
	isVideo := a.Type == "video" || isVideoPath(a.Path)
	args := []string{"-hide_banner", "-y"}
	var vf string
	if isVideo {
		src := a.Duration
		if src <= 0 {
			if info, err := media.Probe(ctx, cfg.FFprobe, a.Path); err == nil {
				src = info.Duration
			}
		}
		offset := 0.0
		if src > dur+0.6 {
			// Skip the typical fade-in/slate at the head of stock footage.
			offset = math.Min(1.0, (src-dur)*0.25)
		}
		if src > 0 && src-offset < dur {
			loops := int(math.Ceil(dur/src)) + 1
			args = append(args, "-stream_loop", strconv.Itoa(loops))
			offset = 0
		}
		args = append(args, "-ss", fmt.Sprintf("%.3f", offset), "-i", a.Path)
		vf = fmt.Sprintf("fps=%d", fps)
	} else {
		args = append(args, "-loop", "1", "-framerate", strconv.Itoa(fps), "-t", fmt.Sprintf("%.3f", dur+0.1), "-i", a.Path)
		// Pre-scale 2x so zoompan's integer positions don't jitter.
		W2, H2 := w*2, h*2
		kb := fmt.Sprintf("zoompan=z='1+0.05*on/%d':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=1:s=%dx%d:fps=%d", frames, w, h, fps)
		if spec.Motion%2 == 1 {
			kb = fmt.Sprintf("zoompan=z='1.06':x='(iw-iw/zoom)*on/%d':y='ih/2-(ih/zoom/2)':d=1:s=%dx%d:fps=%d", frames, w, h, fps)
		}
		if spec.Fit {
			vf = "null"
		} else {
			vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,%s", W2, H2, W2, H2, kb)
		}
	}

	var chain string
	if spec.Fit {
		// Card look: the content is fully visible (contain) over a blurred,
		// darkened version of itself; a small pop-in scale at the start.
		pop := fmt.Sprintf("zoompan=z='max(1\\,1.06-0.06*on/9)':x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)':d=1:s=%dx%d:fps=%d", w, h, fps)
		chain = fmt.Sprintf("[0:v]%s,split=2[a][b];[a]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,boxblur=18:2,eq=brightness=-0.22:saturation=0.9[bg];"+
			"[b]scale=%d:%d:force_original_aspect_ratio=decrease,setsar=1[fg];[bg][fg]overlay=(W-w)/2:(H-h)/2,%s,setsar=1,format=yuv420p[v]",
			vfOrFPS(vf, fps), w, h, w, h, w-28, h-28, pop)
	} else if isVideo {
		chain = fmt.Sprintf("[0:v]%s,scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,eq=contrast=1.04:saturation=1.06,format=yuv420p[v]", vf, w, h, w, h)
	} else {
		chain = fmt.Sprintf("[0:v]%s,eq=contrast=1.03:saturation=1.05,format=yuv420p[v]", vf)
	}
	args = append(args, "-filter_complex", chain, "-map", "[v]", "-an", "-frames:v", strconv.Itoa(frames),
		"-r", strconv.Itoa(fps), "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-pix_fmt", "yuv420p", out)
	if _, err := execx.Run(ctx, nil, cfg.FFmpeg, args...); err != nil {
		_ = os.Remove(out)
		return err
	}
	return nil
}

func vfOrFPS(vf string, fps int) string {
	if vf == "" || vf == "null" {
		return fmt.Sprintf("fps=%d", fps)
	}
	return vf
}

func even(n int) int {
	if n%2 != 0 {
		return n - 1
	}
	return n
}
