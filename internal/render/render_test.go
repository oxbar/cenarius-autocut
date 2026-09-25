package render

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/captions"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/transcribe"
)

func TestGeometryKeepsCardsAwayFromCaptions(t *testing.T) {
	cfg := config.Default()
	g := ComputeGeometry(cfg)
	block := int(float64(cfg.Captions.FontSize*cfg.Captions.MaxLines) * 1.18)
	seamBand := Rect{0, g.SeamCaptionY - block/2, g.W, block}
	if Intersects(g.Card, seamBand) {
		t.Fatalf("card %+v overlaps seam captions %+v", g.Card, seamBand)
	}
	if Intersects(g.PIP, g.CaptionZone) || Intersects(g.PIP, seamBand) {
		t.Fatalf("pip %+v overlaps captions", g.PIP)
	}
	if g.Card.Bottom() > g.H-int(float64(g.H)*0.10) {
		t.Fatalf("card enters platform UI area: %+v", g.Card)
	}
	if g.Card.W%2 != 0 || g.Card.H%2 != 0 || g.Split%2 != 0 {
		t.Fatalf("odd dimensions break yuv420p: %+v", g)
	}
}

func TestZoomExprKinds(t *testing.T) {
	zs := []planner.ZoomEvent{
		{Start: 0, End: 0.9, Scale: 1.08, Kind: planner.ZoomPunch},
		{Start: 5, End: 8, Scale: 1.06, Kind: planner.ZoomSlowPush},
		{Start: 10, End: 11.5, Scale: 1.065, Kind: planner.ZoomReframe},
	}
	e := ZoomExpr(zs, 30)
	for _, want := range []string{"between(on,0,27)", "between(on,150,240)", "cos(PI", "between(on,300,345),1+0.0650*1"} {
		if !strings.Contains(e, want) {
			t.Fatalf("missing %q in %s", want, e)
		}
	}
	if ClampSFXGain(-10) != -18 || ClampSFXGain(-40) != -28 || ClampSFXGain(0) != -24 {
		t.Fatal("SFX gain not clamped to -28..-18 dB")
	}
}

var ffBin, fpBin string

// need picks FFMPEG_BIN / Homebrew ffmpeg-full / PATH and requires libass
// (captions). It is the same FFmpeg the smoke test and config.json use.
func need(t *testing.T) config.Config {
	t.Helper()
	ffBin, fpBin = assets.FindFFmpeg()
	if ffBin == "" || fpBin == "" {
		t.Skip("ffmpeg/ffprobe não disponível")
	}
	if !assets.FFmpegHasFilter(ffBin, "ass") {
		t.Skipf("%s sem libass: rode com FFMPEG_BIN=<ffmpeg-full>", ffBin)
	}
	cfg := config.Default()
	cfg.FFmpeg, cfg.FFprobe = ffBin, fpBin
	cfg.Output.Preset = "ultrafast"
	return cfg
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	switch name {
	case "ffmpeg":
		name = ffBin
	case "ffprobe":
		name = fpBin
	}
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

type probe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		RFR       string `json:"r_frame_rate"`
		PixFmt    string `json:"pix_fmt"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Integration: a 23.085 s / 24 fps source (same shape as IMG_6823.MOV) with
// reaction video B-roll, fullscreen Ken Burns image, card, PiP, self-broll,
// all zoom kinds, SFX and ASS captions must keep its duration at 30 fps.
func TestFinalKeepsDurationWithAllLayouts(t *testing.T) {
	cfg := need(t)
	if testing.Short() {
		t.Skip("integração pesada")
	}
	d := t.TempDir()
	in := filepath.Join(d, "in.mp4")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=720x1280:rate=24:duration=23.085",
		"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration=23.085", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", in)
	broll := filepath.Join(d, "broll.mp4") // 1.5 s: forces a loop for a 2.4 s event
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=25:duration=1.5",
		"-f", "lavfi", "-i", "sine=frequency=900:duration=1.5", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", broll)
	imgPath := filepath.Join(d, "still.png")
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1200; x++ {
			img.Set(x, y, color.RGBA{uint8(x / 5), uint8(y / 4), 160, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	_ = os.WriteFile(imgPath, b.Bytes(), 0644)

	plan := planner.Plan{
		VisualEvents: []planner.VisualEvent{
			{ID: "ve-001", Start: 2.0, End: 4.4, Layout: planner.LayoutReaction, Concept: "software development", Asset: &planner.AssetRef{Path: broll, Type: "video", Source: "local", Duration: 1.5}},
			{ID: "ve-002", Start: 6.5, End: 8.3, Layout: planner.LayoutFullscreen, Concept: "technology", Asset: &planner.AssetRef{Path: imgPath, Type: "image", Source: "local"}},
			{ID: "ve-003", Start: 10.0, End: 12.2, Layout: planner.LayoutCard, Concept: "chatgpt", Asset: &planner.AssetRef{Path: imgPath, Type: "image", Source: "local"}},
			{ID: "ve-004", Start: 14.0, End: 16.0, Layout: planner.LayoutPIP, Concept: "github", Asset: &planner.AssetRef{Path: broll, Type: "video", Source: "local"}},
			{ID: "ve-005", Start: 18.0, End: 20.0, Layout: planner.LayoutReaction, Concept: "servers", Label: "SERVIDORES", Asset: &planner.AssetRef{Source: planner.SourceSelfBroll, Type: "video", Fallback: true}},
		},
		Zooms: []planner.ZoomEvent{
			{Start: 0, End: 0.95, Scale: 1.085, Kind: planner.ZoomPunch},
			{Start: 4.8, End: 6.2, Scale: 1.065, Kind: planner.ZoomReframe},
			{Start: 20.5, End: 23.0, Scale: 1.06, Kind: planner.ZoomSlowPush},
		},
		SFX: []planner.SFXEvent{{Time: 6.45, Name: "whoosh", GainDB: -22}, {Time: 10.0, Name: "pop", GainDB: -24}, {Time: 2.0, Name: "click", GainDB: -27}},
	}
	var toks []transcribe.Token
	for i := 0; i < 40; i++ {
		s := 0.3 + float64(i)*0.55
		toks = append(toks, transcribe.Token{Text: "palavra" + strconv.Itoa(i), Start: s, End: s + 0.5})
	}
	ass := filepath.Join(d, "c.ass")
	if err := captions.GenerateASSWithLayout(ass, toks, cfg.Captions, nil, []captions.Window{{Start: 2, End: 4.4, Y: 960}}); err != nil {
		t.Fatal(err)
	}
	if ab, _ := os.ReadFile(ass); !bytes.Contains(ab, []byte(`\an5\pos(540,960)`)) {
		t.Fatal("seam caption override missing")
	}
	out := filepath.Join(d, "final.mp4")
	if err := Final(context.Background(), cfg, in, ass, out, plan, true); err != nil {
		t.Fatal(err)
	}
	if keep := os.Getenv("CENARIUS_KEEP_RENDER"); keep != "" {
		b, _ := os.ReadFile(out)
		_ = os.WriteFile(keep, b, 0644)
	}
	pb, err := exec.Command(fpBin, "-v", "error", "-show_streams", "-show_format", "-of", "json", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	var p probe
	_ = json.Unmarshal(pb, &p)
	var v, a bool
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			v = true
			if s.CodecName != "h264" || s.Width != 1080 || s.Height != 1920 || s.RFR != "30/1" || s.PixFmt != "yuv420p" {
				t.Fatalf("bad video stream: %+v", s)
			}
		case "audio":
			a = true
			if s.CodecName != "aac" {
				t.Fatalf("bad audio: %+v", s)
			}
		}
	}
	if !v || !a {
		t.Fatalf("missing streams: %+v", p.Streams)
	}
	dur, _ := strconv.ParseFloat(p.Format.Duration, 64)
	if math.Abs(dur-23.085) > 0.20 {
		t.Fatalf("duration changed: 23.085 -> %.3f", dur)
	}
	// Every prepared B-roll clip must exist and be silent (voice stays A-roll).
	for _, prepared := range []string{"ve-001-reaction.mp4", "ve-002-fullscreen.mp4", "ve-003-card.mp4", "ve-004-pip.mp4"} {
		pp := filepath.Join(d, "render-parts", prepared)
		ob, err := exec.Command(fpBin, "-v", "error", "-show_streams", "-of", "json", pp).Output()
		if err != nil {
			t.Fatalf("prepared clip %s missing: %v", prepared, err)
		}
		if bytes.Contains(ob, []byte(`"codec_type": "audio"`)) {
			t.Fatalf("B-roll clip %s kept audio", prepared)
		}
	}
}

// With an FFmpeg lacking drawtext, self-broll must still render (no label)
// instead of failing the whole job.
func TestFinalWithoutDrawtext(t *testing.T) {
	cfg := need(t)
	orig := assets.HasFilterFunc
	assets.HasFilterFunc = func(bin, name string) bool { return name != "drawtext" }
	defer func() { assets.HasFilterFunc = orig }()
	d := t.TempDir()
	in := filepath.Join(d, "in.mp4")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=720x1280:rate=24:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration=4", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", in)
	plan := planner.Plan{VisualEvents: []planner.VisualEvent{{ID: "ve-001", Start: 1.2, End: 3.0, Layout: planner.LayoutReaction, Label: "X",
		Asset: &planner.AssetRef{Source: planner.SourceSelfBroll, Type: "video", Fallback: true}}}}
	out := filepath.Join(d, "final.mp4")
	if err := Final(context.Background(), cfg, in, "", out, plan, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out + ".filter")
	if bytes.Contains(b, []byte("drawtext")) {
		t.Fatal("drawtext used although unavailable")
	}
}
