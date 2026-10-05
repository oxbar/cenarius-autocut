package render

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/media"
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

// Final renders the vertical timeline: A-roll (with intent-driven zooms), real
// B-roll in reaction / fullscreen / card / PiP layouts, dynamic captions on top
// of everything (B-roll never covers captions) and discrete SFX. Every B-roll
// asset is first normalised by PrepareClip into a finite, silent clip, so the
// final graph never has infinite inputs and cannot change the video duration.
func Final(ctx context.Context, cfg config.Config, input, assPath, output string, plan planner.Plan, hasAudio bool) error {
	logger := logx.From(ctx)
	started := time.Now()
	planner.MigrateLegacy(&plan)
	g := ComputeGeometry(cfg)
	W, H, fps := cfg.Output.Width, cfg.Output.Height, cfg.Output.FPS
	parts := filepath.Join(filepath.Dir(output), "render-parts")
	if err := os.MkdirAll(parts, 0755); err != nil {
		return err
	}
	events := append([]planner.VisualEvent(nil), plan.VisualEvents...)
	sort.Slice(events, func(i, j int) bool { return events[i].Start < events[j].Start })
	logger.Info("visual.render.start", "events", len(events), "zooms", len(plan.Zooms), "sfx", len(plan.SFX), "width", W, "height", H, "fps", fps)

	args := []string{"-y", "-i", input}
	next := 1
	type item struct {
		e                         planner.VisualEvent
		clip, mask, shadow, audio int
		self                      bool
		labelFile                 string
	}
	var items []item
	for _, e := range events {
		dur := e.End - e.Start
		if e.Asset == nil || dur < 0.3 {
			logger.Warn("visual.render.event", "event", e.ID, "skipped", true, "reason", "sem asset ou duração curta")
			continue
		}
		it := item{e: e, mask: -1, shadow: -1, audio: -1}
		if e.Asset.Source == planner.SourceSelfBroll || e.Asset.Path == "" {
			it.self = true
		} else {
			spec := ClipSpec{Width: W, Height: H - g.Split, Duration: dur, Motion: len(items)}
			switch e.Layout {
			case planner.LayoutFullscreen:
				spec.Width, spec.Height = W, H
			case planner.LayoutCard:
				spec.Width, spec.Height, spec.Fit = g.Card.W, g.Card.H, true
			case planner.LayoutPIP:
				spec.Width, spec.Height, spec.Fit = g.PIP.W, g.PIP.H, true
			}
			if e.Asset.Source == planner.SourceProcedure {
				// Generated graphics are centred designs: fill the frame
				// instead of shrinking them inside a blurred copy.
				spec.Fit = false
			}
			clip := filepath.Join(parts, fmt.Sprintf("%s-%s.mp4", e.ID, e.Layout))
			t0 := time.Now()
			if err := PrepareClip(ctx, cfg, *e.Asset, spec, clip); err != nil {
				logger.Warn("visual.render.event", "event", e.ID, "prepare_failed", true, "error", err, "fallback", planner.SourceSelfBroll)
				it.self = true
			} else {
				logger.Debug("visual.render.prepare", "event", e.ID, "clip", clip, "duration_ms", time.Since(t0).Milliseconds())
				args = append(args, "-i", clip)
				it.clip = next
				next++
				if e.Asset.Source == planner.SourceManual && e.Asset.AudioMode != "" && e.Asset.AudioMode != "muted" {
					audioPath := filepath.Join(parts, fmt.Sprintf("%s-reaction-audio.m4a", e.ID))
					if err := PrepareReactionAudio(ctx, cfg, *e.Asset, dur, audioPath); err != nil {
						logger.Warn("reaction.audio.prepare_failed", "event", e.ID, "error", err)
					} else {
						args = append(args, "-i", audioPath)
						it.audio = next
						next++
					}
				}
				if e.Layout == planner.LayoutCard || e.Layout == planner.LayoutPIP {
					r := g.Card
					if e.Layout == planner.LayoutPIP {
						r = g.PIP
					}
					radius := g.Radius
					if e.Layout == planner.LayoutPIP {
						radius = 26
					}
					maskP := filepath.Join(parts, fmt.Sprintf("mask-%dx%d.png", r.W, r.H))
					shadowP := filepath.Join(parts, fmt.Sprintf("shadow-%dx%d.png", r.W, r.H))
					if err := WriteRoundedMask(maskP, r.W, r.H, radius); err != nil {
						return err
					}
					if err := WriteShadow(shadowP, r.W, r.H, radius, g.ShadowPad, 0.55); err != nil {
						return err
					}
					for _, p := range []string{maskP, shadowP} {
						args = append(args, "-loop", "1", "-framerate", strconv.Itoa(fps), "-t", fmt.Sprintf("%.3f", dur), "-i", p)
					}
					it.mask, it.shadow = next, next+1
					next += 2
				}
			}
		}
		if it.self {
			lf := filepath.Join(parts, e.ID+"-label.txt")
			label := strings.ToUpper(strings.TrimSpace(firstNonEmpty(e.Label, e.Concept, "CONTEXTO")))
			if err := os.WriteFile(lf, []byte(label), 0644); err != nil {
				return err
			}
			it.labelFile = lf
		}
		items = append(items, it)
	}

	// Music bed: looped exactly to the timeline length (finite input, so it
	// can never extend the video), only when the voice track exists.
	musicIdx := -1
	timeline := 0.0
	musicLUFS := -14.0 // typical mastered track, used only if measuring fails
	voiceLUFS := math.NaN()
	var musicCue *planner.MusicCue
	if hasAudio && plan.Music != nil && plan.Music.Asset != nil && plan.Music.Asset.Path != "" {
		if info, err := media.Probe(ctx, cfg.FFprobe, input); err == nil && info.Duration > 0 {
			musicCue, timeline = plan.Music, info.Duration
			if v, verr := MeasureLUFS(ctx, cfg.FFmpeg, input); verr == nil {
				voiceLUFS = v
			} else {
				logger.Warn("audio.voice.loudness_unknown", "error", verr, "voice_gain_db", 0)
			}
			if lufs, lerr := MeasureLUFS(ctx, cfg.FFmpeg, plan.Music.Asset.Path); lerr == nil {
				musicLUFS = lufs
			} else {
				logger.Warn("audio.music.loudness_unknown", "error", lerr, "assumed_lufs", musicLUFS)
			}
			args = append(args, "-stream_loop", "-1", "-t", fmt.Sprintf("%.3f", info.Duration+0.5), "-i", plan.Music.Asset.Path)
			musicIdx = next
			next++
			logger.Info("audio.music.render", "mood", musicCue.Mood, "path", musicCue.Asset.Path, "gain_db", musicCue.GainDB,
				"duck", cfg.Music.Duck, "drops", len(musicCue.Drops), "timeline_s", info.Duration)
		} else {
			logger.Warn("audio.music.skipped", "reason", "não foi possível medir a timeline", "error", err)
		}
	}

	var fc strings.Builder
	fmt.Fprintf(&fc, "[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,eq=contrast=1.03:saturation=1.04,unsharp=5:5:0.25,fps=%d", W, H, W, H, fps)
	if cfg.Zoom.Enabled && len(plan.Zooms) > 0 {
		focus := cfg.Broll.ReactionFocusY
		if focus <= 0 || focus >= 1 {
			focus = 0.4
		}
		fmt.Fprintf(&fc, ",zoompan=z='%s':x='iw/2-(iw/zoom/2)':y='%.3f*(ih-ih/zoom)':d=1:s=%dx%d:fps=%d", ZoomExpr(plan.Zooms, fps), focus, W, H, fps)
	}
	fc.WriteString("[v0];")
	prev := "[v0]"
	font := assets.DrawtextFont()
	hasDrawtext := assets.FFmpegHasFilter(cfg.FFmpeg, "drawtext")
	if !hasDrawtext {
		logger.Warn("visual.render.capability", "ffmpeg", cfg.FFmpeg, "drawtext", false, "effect", "self-broll sem rótulo; instale ffmpeg-full")
	}

	for i, it := range items {
		e := it.e
		s, en := e.Start, e.End
		dur := en - s
		enable := fmt.Sprintf("enable='between(t,%.3f,%.3f)'", s, en)
		fadeOut := math.Max(0.05, dur-0.12)
		lbl := func(name string) string { return fmt.Sprintf("[%s%d]", name, i) }
		// splitTop reframes the creator into the top panel (hard cut in/out).
		splitTop := func() {
			fmt.Fprintf(&fc, "%ssplit=2%s%s;", prev, lbl("ma"), lbl("mb"))
			fmt.Fprintf(&fc, "%scrop=%d:%d:0:%d,setsar=1%s;", lbl("mb"), W, g.Split, g.CropY, lbl("top"))
			fmt.Fprintf(&fc, "%s%soverlay=0:0:%s%s;", lbl("ma"), lbl("top"), enable, lbl("m1"))
			prev = lbl("m1")
		}
		mediaType := "video"
		if e.Asset != nil && e.Asset.Type != "" {
			mediaType = e.Asset.Type
		}
		switch {
		case it.self:
			splitTop()
			fmt.Fprintf(&fc, "%ssplit=2%s%s;", prev, lbl("sa"), lbl("sb"))
			fmt.Fprintf(&fc, "%scrop=%d:%d:0:%d,boxblur=16:2,eq=brightness=-0.22:saturation=1.2,setsar=1%s;", lbl("sb"), W, H-g.Split, g.Split/2, lbl("sp"))
			label := ""
			if hasDrawtext {
				label = fmt.Sprintf(",drawtext=textfile='%s':%s:fontsize=78:fontcolor=white:borderw=5:bordercolor=black@0.85:x=(w-text_w)/2:y=%d:%s",
					escapeFilterPathQuoted(it.labelFile), font, g.Split+(H-g.Split)/2-40, enable)
			}
			fmt.Fprintf(&fc, "%s%soverlay=0:%d:%s%s%s;", lbl("sa"), lbl("sp"), g.Split, enable, label, lbl("v"))
		case e.Layout == planner.LayoutFullscreen:
			fmt.Fprintf(&fc, "[%d:v]format=yuva420p,fade=t=in:st=0:d=0.12:alpha=1,fade=t=out:st=%.3f:d=0.12:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;", it.clip, fadeOut, s, lbl("b"))
			fmt.Fprintf(&fc, "%s%soverlay=0:0:eof_action=pass:%s%s;", prev, lbl("b"), enable, lbl("v"))
		case e.Layout == planner.LayoutCard || e.Layout == planner.LayoutPIP:
			r := g.Card
			if e.Layout == planner.LayoutCard {
				splitTop()
				fmt.Fprintf(&fc, "%sdrawbox=x=0:y=%d:w=%d:h=%d:color=0x0E0F14@0.94:t=fill:%s%s;", prev, g.Split, W, H-g.Split, enable, lbl("dk"))
				prev = lbl("dk")
			} else {
				r = g.PIP
			}
			slide := fmt.Sprintf("%d+42*max(0\\,1-(t-%.3f)/0.22)", r.Y, s)
			fmt.Fprintf(&fc, "[%d:v]format=rgba%s;[%d:v]format=gray%s;%s%salphamerge,fade=t=in:st=0:d=0.14:alpha=1,fade=t=out:st=%.3f:d=0.12:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;",
				it.clip, lbl("c"), it.mask, lbl("k"), lbl("c"), lbl("k"), fadeOut, s, lbl("card"))
			fmt.Fprintf(&fc, "[%d:v]format=rgba,fade=t=in:st=0:d=0.14:alpha=1,fade=t=out:st=%.3f:d=0.12:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;", it.shadow, fadeOut, s, lbl("sh"))
			shadowY := fmt.Sprintf("%d+42*max(0\\,1-(t-%.3f)/0.22)", r.Y-g.ShadowPad+14, s)
			fmt.Fprintf(&fc, "%s%soverlay=x=%d:y='%s':eof_action=pass:%s%s;", prev, lbl("sh"), r.X-g.ShadowPad, shadowY, enable, lbl("m2"))
			fmt.Fprintf(&fc, "%s%soverlay=x=%d:y='%s':eof_action=pass:%s%s;", lbl("m2"), lbl("card"), r.X, slide, enable, lbl("v"))
		default: // reaction
			splitTop()
			fmt.Fprintf(&fc, "[%d:v]format=yuva420p,fade=t=in:st=0:d=0.12:alpha=1,fade=t=out:st=%.3f:d=0.12:alpha=1,setpts=PTS-STARTPTS+%.3f/TB%s;", it.clip, fadeOut, s, lbl("b"))
			fmt.Fprintf(&fc, "%s%soverlay=0:%d:eof_action=pass:%s,drawbox=x=0:y=%d:w=%d:h=4:color=white@0.85:t=fill:%s%s;", prev, lbl("b"), g.Split, enable, g.Split-2, W, enable, lbl("v"))
		}
		prev = lbl("v")
		source := ""
		if e.Asset != nil {
			source = e.Asset.Source
		}
		logger.Info("visual.render.event", "event", e.ID, "layout", e.Layout, "type", e.Type, "concept", e.Concept, "source", source, "media_type", mediaType, "self_broll", it.self, "start", s, "end", en)
	}

	// Captions are burned last so no B-roll ever covers them.
	if cfg.Captions.Enabled && assPath != "" {
		fmt.Fprintf(&fc, "%sass='%s'[vout]", prev, escapeFilterPath(assPath))
	} else {
		fmt.Fprintf(&fc, "%snull[vout]", prev)
	}

	audioMap := "0:a?"
	if hasAudio {
		duckItems := make([]item, 0)
		for _, it := range items {
			if it.audio >= 0 && it.e.Asset != nil && it.e.Asset.AudioMode == "duck" {
				duckItems = append(duckItems, it)
			}
		}
		// Balanced mode (music present): the voice is normalized on its own
		// first, then everything else is placed relative to it.
		balanced := musicIdx >= 0
		voiceSrc := "[0:a]"
		if balanced {
			// Static gain from a measured loudness: no look-ahead delay (a
			// loudnorm here would drop the last ~3 s through amix) and the
			// natural dynamics of the speech are kept.
			fmt.Fprintf(&fc, ";[0:a]volume=%.2fdB[voicen]", VoiceGainDB(voiceLUFS, cfg.Music))
			voiceSrc = "[voicen]"
		}
		voiceLabel := voiceSrc
		duckKeys := make([]string, len(duckItems))
		musicKey := ""
		keys := len(duckItems)
		// Without speech ranges (old plans), fall back to a sidechain key.
		sidechainMusic := balanced && cfg.Music.Duck && len(musicCue.Speech) == 0
		if sidechainMusic {
			keys++
		}
		if keys > 0 {
			fmt.Fprintf(&fc, ";%sasplit=%d[voicebase]", voiceSrc, keys+1)
			for i := range duckItems {
				duckKeys[i] = fmt.Sprintf("[voicekey%d]", i)
				fc.WriteString(duckKeys[i])
			}
			if sidechainMusic {
				musicKey = "[voicekeymusic]"
				fc.WriteString(musicKey)
			}
			voiceLabel = "[voicebase]"
		}

		labels := []string{voiceLabel}
		duckIndex := 0
		for _, it := range items {
			if it.audio < 0 || it.e.Asset == nil || it.e.Asset.AudioMode == "muted" {
				continue
			}
			delay := int(math.Round(it.e.Start * 1000))
			base := fmt.Sprintf("[reaction%d]", it.audio)
			switch it.e.Asset.AudioMode {
			case "duck":
				raw := fmt.Sprintf("[reactionraw%d]", it.audio)
				fmt.Fprintf(&fc, ";[%d:a]volume=-5dB,adelay=%d|%d%s;%s%ssidechaincompress=threshold=0.025:ratio=12:attack=15:release=280:makeup=1%s", it.audio, delay, delay, raw, raw, duckKeys[duckIndex], base)
				duckIndex++
			default: // original: audible mix while preserving creator intelligibility
				fmt.Fprintf(&fc, ";[%d:a]volume=-8dB,adelay=%d|%d%s", it.audio, delay, delay, base)
			}
			labels = append(labels, base)
			logger.Info("reaction.audio.event", "event", it.e.ID, "mode", it.e.Asset.AudioMode, "start", it.e.Start)
		}

		if balanced {
			gain := MusicGainDB(musicLUFS, cfg.Music)
			fmt.Fprintf(&fc, ";%s", MusicChain(musicIdx, musicCue, cfg.Music, timeline, musicLUFS, musicKey))
			labels = append(labels, "[music]")
			logger.Info("audio.music.balance", "voice_measured_lufs", voiceLUFS, "voice_gain_db", VoiceGainDB(voiceLUFS, cfg.Music),
				"voice_target_lufs", config.VoiceLUFS+cfg.Music.VoiceGainDB, "music_measured_lufs", musicLUFS,
				"music_gain_db", gain, "music_below_voice_lu", -cfg.Music.GainDB, "duck", cfg.Music.Duck, "duck_db", cfg.Music.DuckDB,
				"speech_ranges", len(musicCue.Speech), "sfx_offset_db", cfg.Music.SFXGainDB)
		}

		for i, sfx := range plan.SFX {
			label := fmt.Sprintf("[sfx%d]", i)
			delay := int(math.Round(sfx.Time * 1000))
			gain := ClampSFXGain(sfx.GainDB) + cfg.Music.SFXGainDB
			fmt.Fprintf(&fc, ";%s,volume=%.1fdB,adelay=%d|%d%s", sfxSource(sfx.Name), gain, delay, delay, label)
			labels = append(labels, label)
			logger.Info("audio.sfx.event", "name", sfx.Name, "time", sfx.Time, "gain_db", gain, "reason", sfx.Reason)
		}
		if balanced {
			// No loudness re-normalization of the mix (it would lift the music
			// in the pauses); only a peak limiter without auto-gain.
			// duration=first ends ~0.13 s early in the full graph; mix the
			// longest input and cut exactly at the timeline length instead.
			fmt.Fprintf(&fc, ";%samix=inputs=%d:duration=longest:normalize=0,atrim=end=%.3f,alimiter=limit=0.89:level=0[aout]", strings.Join(labels, ""), len(labels), timeline)
			audioMap = "[aout]"
		} else if len(labels) > 1 {
			fmt.Fprintf(&fc, ";%samix=inputs=%d:duration=first:normalize=0,loudnorm=I=-16:TP=-1.5:LRA=11[aout]", strings.Join(labels, ""), len(labels))
			audioMap = "[aout]"
		}
	}

	script := output + ".filter"
	if err := os.WriteFile(script, []byte(fc.String()), 0644); err != nil {
		return err
	}
	args = append(args, "-filter_complex", fc.String(), "-map", "[vout]", "-map", audioMap)
	if hasAudio && audioMap == "0:a?" {
		args = append(args, "-af", "loudnorm=I=-16:TP=-1.5:LRA=11")
	}
	args = append(args, "-r", strconv.Itoa(fps), "-c:v", "libx264", "-profile:v", "high", "-preset", cfg.Output.Preset, "-crf", strconv.Itoa(cfg.Output.CRF), "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", cfg.Output.AudioBitrate, "-ar", "48000", "-movflags", "+faststart", "-shortest", output)
	if _, err := execx.Run(ctx, nil, cfg.FFmpeg, args...); err != nil {
		return err
	}
	logger.Info("visual.render.complete", "output", output, "events_rendered", len(items), "duration_ms", time.Since(started).Milliseconds())
	return nil
}

// ClampSFXGain keeps sound design discreet: -28..-18 dB under the voice.
func ClampSFXGain(g float64) float64 {
	if g == 0 {
		g = -24
	}
	return math.Max(-28, math.Min(-18, g))
}

func sfxSource(name string) string {
	switch name {
	case "whoosh":
		return "anoisesrc=color=pink:sample_rate=48000:duration=0.30,highpass=f=500,lowpass=f=6000,afade=t=in:st=0:d=0.05,afade=t=out:st=0.12:d=0.17"
	case "click":
		return "sine=frequency=2600:sample_rate=48000:duration=0.03,afade=t=out:st=0.005:d=0.025"
	case "hit": // short punchy low hit for the hook
		return "aevalsrc='0.8*sin(2*PI*80*t)*exp(-9*t)':s=48000:d=0.35,afade=t=out:st=0.2:d=0.15"
	case "impact": // cinematic boom that lands a punchline
		return "aevalsrc='0.9*sin(2*PI*50*t)*exp(-3.5*t)+0.35*sin(2*PI*100*t)*exp(-6*t)':s=48000:d=1.1,lowpass=f=900,afade=t=out:st=0.7:d=0.4"
	case "riser": // rising sweep that builds tension into a drop
		return "aevalsrc='0.35*sin(2*PI*(220*t+700*t*t))*(t/0.9)':s=48000:d=0.9,highpass=f=150,afade=t=in:st=0:d=0.3"
	case "beep", "error":
		return "sine=frequency=1000:sample_rate=48000:duration=0.14,afade=t=in:st=0:d=0.01,afade=t=out:st=0.09:d=0.05"
	default: // pop
		return "sine=frequency=920:sample_rate=48000:duration=0.09,afade=t=out:st=0.035:d=0.055"
	}
}

// VoiceGainDB brings the measured voice to the target loudness plus the user
// trim. Unknown loudness (NaN) keeps the voice untouched apart from the trim.
func VoiceGainDB(voiceLUFS float64, mc config.MusicConfig) float64 {
	if math.IsNaN(voiceLUFS) {
		return mc.VoiceGainDB
	}
	return math.Max(-20, math.Min(30, config.VoiceLUFS+mc.VoiceGainDB-voiceLUFS))
}

// MusicGainDB is the static gain that places a track measured at musicLUFS
// exactly mc.GainDB LU below the (trimmed) voice target.
func MusicGainDB(musicLUFS float64, mc config.MusicConfig) float64 {
	target := config.VoiceLUFS + mc.VoiceGainDB + mc.GainDB
	return math.Max(-50, math.Min(10, target-musicLUFS))
}

// MusicChain builds the music bed filter: stereo 48 kHz, loudness-matched
// gain (relative to the voice), 1.2 s fade-in, 1.8 s fade-out, drops at
// punchlines and ducking while the voice speaks. When the cue has speech
// ranges the ducking is a deterministic envelope of exactly mc.DuckDB (no
// pumping); otherwise, if key is set, a sidechain compressor is used.
// Output label: [music].
func MusicChain(idx int, cue *planner.MusicCue, mc config.MusicConfig, timeline, musicLUFS float64, key string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%d:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,volume=%.1fdB,afade=t=in:st=0:d=1.2", idx, MusicGainDB(musicLUFS, mc))
	if timeline > 3 {
		fmt.Fprintf(&b, ",afade=t=out:st=%.3f:d=1.8", timeline-1.8)
	}
	if expr := DropExpr(cue.Drops); expr != "1" {
		fmt.Fprintf(&b, ",volume='%s':eval=frame", expr)
	}
	switch {
	case mc.Duck && len(cue.Speech) > 0:
		fmt.Fprintf(&b, ",volume='%s':eval=frame[music]", DuckExpr(cue.Speech, mc.DuckDB))
	case mc.Duck && key != "":
		b.WriteString("[musicraw];[musicraw]" + key + "sidechaincompress=threshold=0.02:ratio=8:attack=20:release=350:makeup=1[music]")
	default:
		b.WriteString("[music]")
	}
	return b.String()
}

// DuckExpr lowers the music by duckDB while the voice speaks, with 0.2 s
// ramps. Ranges closer than the ramps are merged so the envelope never
// flutters; the sum of non-overlapping trapezoids stays in [0,1].
func DuckExpr(speech []planner.TimeRange, duckDB float64) string {
	const ramp = 0.2
	var merged []planner.TimeRange
	for _, r := range speech {
		if r.End <= r.Start {
			continue
		}
		if n := len(merged); n > 0 && r.Start-merged[n-1].End < 2*ramp+0.05 {
			if r.End > merged[n-1].End {
				merged[n-1].End = r.End
			}
			continue
		}
		merged = append(merged, r)
	}
	if len(merged) == 0 || duckDB <= 0 {
		return "1"
	}
	terms := make([]string, 0, len(merged))
	for _, r := range merged {
		terms = append(terms, fmt.Sprintf("clip(min((t-%.3f)/%.2f,(%.3f-t)/%.2f),0,1)", r.Start-ramp, ramp, r.End+ramp, ramp))
	}
	return fmt.Sprintf("pow(10,-%.2f/20*min(1,%s))", duckDB, strings.Join(terms, "+"))
}

// MeasureLUFS returns the integrated loudness (EBU R128) of the first
// 3 minutes of an audio file.
func MeasureLUFS(ctx context.Context, ffmpeg, path string) (float64, error) {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	r, err := execx.Run(ctx, nil, ffmpeg, "-hide_banner", "-nostats", "-t", "180", "-i", path, "-vn", "-af", "ebur128=framelog=quiet", "-f", "null", "-")
	if err != nil {
		return 0, err
	}
	m := lufsRE.FindAllStringSubmatch(r.Stderr, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("loudness não encontrada na saída do ebur128")
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	if err != nil || v < -69 {
		return 0, fmt.Errorf("trilha silenciosa ou inválida (%s LUFS)", m[len(m)-1][1])
	}
	return v, nil
}

var lufsRE = regexp.MustCompile(`I:\s+(-?[0-9.]+) LUFS`)

// DropExpr is the volume envelope for music drops (1 = untouched).
func DropExpr(drops []planner.MusicDrop) string {
	expr := "1"
	for i := len(drops) - 1; i >= 0; i-- {
		d := drops[i]
		if d.End <= d.Start {
			continue
		}
		// Commas are safe: the whole expression is single-quoted in the graph.
		expr = fmt.Sprintf("if(between(t,%.3f,%.3f),0.05,if(between(t,%.3f,%.3f),0.05+0.95*(t-%.3f)/0.4,%s))",
			d.Start, d.End, d.End, d.End+0.4, d.End, expr)
	}
	return expr
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// ZoomExpr builds the zoompan z expression (output frame based). Each kind
// has its own curve; scales are clamped to tasteful ranges by the planner.
func ZoomExpr(zs []planner.ZoomEvent, fps int) string {
	expr := "1.0"
	sorted := append([]planner.ZoomEvent(nil), zs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	for i := len(sorted) - 1; i >= 0; i-- {
		z := sorted[i]
		a := int(math.Round(z.Start * float64(fps)))
		b := int(math.Round(z.End * float64(fps)))
		if b <= a+1 {
			continue
		}
		L := b - a
		delta := z.Scale - 1.0
		var curve string
		switch z.Kind {
		case planner.ZoomSlowPush:
			// smooth push-in across the whole interval
			curve = fmt.Sprintf("(0.5-0.5*cos(PI*(on-%d)/%d))", a, L)
		case planner.ZoomReframe:
			curve = "1"
		default: // punch_zoom: fast ease-in, hold, quick release
			at := int(math.Max(2, math.Min(0.15*float64(fps), 0.3*float64(L))))
			rl := int(math.Max(2, math.Min(0.18*float64(fps), 0.3*float64(L))))
			up := fmt.Sprintf("(on-%d)/%d", a, at)
			down := fmt.Sprintf("(%d-on)/%d", b, rl)
			curve = fmt.Sprintf("if(lt(on,%d),%s*%s*(3-2*%s),if(gt(on,%d),%s*%s*(3-2*%s),1))", a+at, up, up, up, b-rl, down, down, down)
		}
		expr = fmt.Sprintf("if(between(on,%d,%d),1+%.4f*%s,%s)", a, b, delta, curve, expr)
	}
	return expr
}

func isVideoPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".webm", ".mp4", ".m4v", ".mov", ".ogv", ".ogg", ".mpeg", ".mpg":
		return true
	default:
		return false
	}
}

func escapeFilterPath(p string) string {
	p, _ = filepath.Abs(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.ReplaceAll(p, "'", "\\'")
	p = strings.ReplaceAll(p, ":", "\\:")
	return p
}

// escapeFilterPathQuoted is for values inside single quotes in a filtergraph.
func escapeFilterPathQuoted(p string) string {
	p, _ = filepath.Abs(p)
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.ReplaceAll(p, "'", "'\\''")
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
