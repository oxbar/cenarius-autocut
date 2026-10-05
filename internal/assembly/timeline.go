package assembly

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/transcribe"
)

// placed is a segment positioned on the output timeline.
type placed struct {
	Segment
	At float64 // timeline start
}

// Layout snaps every segment to the frame grid and computes its position on
// the timeline (so audio, video, captions and SFX agree to the frame).
func Layout(p Plan, fps int) []placed {
	if fps <= 0 {
		fps = 30
	}
	var out []placed
	at := 0.0
	for _, s := range p.Segments {
		frames := math.Round(s.Duration() * float64(fps))
		if frames < 1 {
			continue
		}
		s.End = s.Start + frames/float64(fps)
		out = append(out, placed{Segment: s, At: at})
		at += frames / float64(fps)
	}
	return out
}

// segmentFilter normalizes any clip (portrait, landscape, square, rotated
// iPhone MOV) to the vertical canvas: a blurred "cover" background plus the
// full picture "fit" on top. A 9:16 clip simply fills the frame.
func segmentFilter(w, h, fps int, dur float64, keepAudio bool) string {
	v := fmt.Sprintf("[0:v]split=2[bgi][fgi];[bgi]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,boxblur=24:2,eq=brightness=-0.18:saturation=1.1[bg];"+
		"[fgi]scale=%d:%d:force_original_aspect_ratio=decrease[fg];[bg][fg]overlay=(W-w)/2:(H-h)/2,fps=%d,setsar=1,format=yuv420p[v]", w, h, w, h, w, h, fps)
	fadeOut := math.Max(0, dur-0.05)
	a := fmt.Sprintf("[1:a]atrim=0:%.3f,asetpts=PTS-STARTPTS[a]", dur)
	if keepAudio {
		a = fmt.Sprintf("[0:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,apad,atrim=0:%.3f,asetpts=PTS-STARTPTS,afade=t=in:st=0:d=0.03,afade=t=out:st=%.3f:d=0.05[a]", dur, fadeOut)
	}
	return v + ";" + a
}

// BuildTimeline renders every segment to a normalized part and concatenates
// them into outDir/cut.mp4 (the "A-roll" that the regular render finishes).
func BuildTimeline(ctx context.Context, cfg config.Config, p Plan, outDir string) (string, []placed, error) {
	logger := logx.From(ctx)
	parts := filepath.Join(outDir, "assembly-parts")
	if err := os.MkdirAll(parts, 0755); err != nil {
		return "", nil, err
	}
	byID := map[string]Clip{}
	for _, c := range p.Clips {
		byID[c.ID] = c
	}
	W, H, fps := cfg.Output.Width, cfg.Output.Height, cfg.Output.FPS
	lay := Layout(p, fps)
	var list strings.Builder
	for i, s := range lay {
		c := byID[s.ClipID]
		dur := s.Duration()
		part := filepath.Join(parts, fmt.Sprintf("seg%02d.mp4", i+1))
		keep := s.KeepAudio && c.HasAudio
		args := []string{"-hide_banner", "-y", "-ss", fmt.Sprintf("%.3f", s.Start), "-t", fmt.Sprintf("%.3f", dur+0.1), "-i", c.Path,
			"-f", "lavfi", "-t", fmt.Sprintf("%.3f", dur+0.1), "-i", "anullsrc=r=48000:cl=stereo",
			"-filter_complex", segmentFilter(W, H, fps, dur, keep), "-map", "[v]", "-map", "[a]",
			"-frames:v", fmt.Sprintf("%d", int(math.Round(dur*float64(fps)))), "-r", fmt.Sprintf("%d", fps),
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-t", fmt.Sprintf("%.3f", dur), part}
		if _, err := execx.Run(ctx, nil, cfg.FFmpeg, args...); err != nil {
			return "", nil, fmt.Errorf("trecho %d (%s): %w", i+1, c.Name, err)
		}
		logger.Info("assembly.segment.rendered", "index", i+1, "clip", c.ID, "name", c.Name, "clip_start", s.Start, "clip_end", s.End,
			"timeline_at", round3(s.At), "keep_audio", keep, "role", s.Role, "text", s.Text)
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(part, "'", `'\''`))
	}
	if len(lay) == 0 {
		return "", nil, fmt.Errorf("roteiro sem trechos")
	}
	listPath := filepath.Join(parts, "concat.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0644); err != nil {
		return "", nil, err
	}
	cut := filepath.Join(outDir, "cut.mp4")
	if _, err := execx.Run(ctx, nil, cfg.FFmpeg, "-hide_banner", "-y", "-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy", "-movflags", "+faststart", cut); err != nil {
		return "", nil, fmt.Errorf("concatenar trechos: %w", err)
	}
	info, err := media.Probe(ctx, cfg.FFprobe, cut)
	if err != nil {
		return "", nil, err
	}
	want := lay[len(lay)-1].At + lay[len(lay)-1].Duration()
	logger.Info("assembly.timeline", "segments", len(lay), "planned_s", round3(want), "actual_s", round3(info.Duration))
	if math.Abs(info.Duration-want) > 0.25 {
		return "", nil, fmt.Errorf("timeline com duração inesperada: planejado %.2fs, gerado %.2fs", want, info.Duration)
	}
	return cut, lay, nil
}

// TimelineWords maps the kept speech of every segment onto the timeline.
func TimelineWords(lay []placed, clips []Clip) []transcribe.Token {
	byID := map[string]Clip{}
	for _, c := range clips {
		byID[c.ID] = c
	}
	var out []transcribe.Token
	for _, s := range lay {
		if !s.KeepAudio {
			continue
		}
		for _, w := range byID[s.ClipID].Words {
			if w.Start < s.Start-0.02 || w.Start >= s.End {
				continue
			}
			t := w
			t.Start = s.At + math.Max(0, w.Start-s.Start)
			t.End = math.Min(s.At+s.Duration(), s.At+(w.End-s.Start))
			if t.End > t.Start {
				out = append(out, t)
			}
		}
	}
	return out
}

// ToEditPlan converts the cut into the render plan used by every mode, so
// zooms, SFX, the music mixer and remix work exactly as elsewhere.
func ToEditPlan(p Plan, lay []placed, words []transcribe.Token, cfg config.Config) planner.Plan {
	ep := planner.Plan{Version: planner.PlanVersion, Hook: p.Title}
	total := 0.0
	for _, s := range lay {
		total = s.At + s.Duration()
	}
	for i, s := range lay {
		if s.Zoom && cfg.Zoom.Enabled {
			ep.Zooms = append(ep.Zooms, planner.ZoomEvent{Start: round3(s.At), End: round3(math.Min(s.At+0.9, s.At+s.Duration())),
				Scale: planner.ClampZoomScale(planner.ZoomPunch, cfg.Zoom.Punch), Kind: planner.ZoomPunch, Reason: "ênfase do editor: " + s.Role})
		}
		switch s.SFX {
		case "":
		case "riser":
			ep.SFX = append(ep.SFX, planner.SFXEvent{Time: round3(math.Max(0, s.At-0.9)), Name: "riser", GainDB: -22, Reason: "tensão antes do trecho " + fmt.Sprint(i+1)})
		default:
			gain := map[string]float64{"impact": -18, "hit": -22, "whoosh": -22, "pop": -24, "click": -27, "beep": -26}[s.SFX]
			ep.SFX = append(ep.SFX, planner.SFXEvent{Time: round3(math.Max(0, s.At-0.03)), Name: s.SFX, GainDB: gain, Reason: "corte do editor no trecho " + fmt.Sprint(i+1)})
		}
		if len(s.Text) > 0 || s.Role != "" {
			ep.SemanticUnits = append(ep.SemanticUnits, planner.SemanticUnit{ID: fmt.Sprintf("s%02d", i+1), Start: round3(s.At), End: round3(s.At + s.Duration()), Text: s.Text, Role: s.Role})
		}
	}
	if cfg.Music.Enabled {
		cue := &planner.MusicCue{Mood: p.MusicMood, Queries: planner.MoodQueries(p.MusicMood), GainDB: cfg.Music.GainDB, Reason: "mood do diretor da montagem"}
		if planner.IsMood(cfg.Music.Mood) {
			cue.Mood, cue.Queries, cue.Reason = cfg.Music.Mood, planner.MoodQueries(cfg.Music.Mood), "mood escolhido no mixer"
		}
		for _, s := range lay {
			if cfg.Music.Drops && s.Role == "punchline" && s.At >= 3 && s.At < total-1 {
				cue.Drops = append(cue.Drops, planner.MusicDrop{Start: round3(s.At - 0.05), End: round3(math.Min(s.At+0.9, total)), Reason: "punchline"})
			}
		}
		cue.Speech = speechRanges(words)
		ep.Music = cue
	}
	return ep
}

func speechRanges(words []transcribe.Token) []planner.TimeRange {
	var out []planner.TimeRange
	for _, w := range words {
		if n := len(out); n > 0 && w.Start-out[n-1].End < 0.45 {
			out[n-1].End = math.Max(out[n-1].End, w.End)
			continue
		}
		out = append(out, planner.TimeRange{Start: w.Start, End: w.End})
	}
	for i := range out {
		out[i].Start, out[i].End = round3(out[i].Start), round3(out[i].End)
	}
	return out
}
