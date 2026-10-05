// Package assembly implements "Montagem IA": several raw clips plus a prompt
// become one edited vertical video. Each clip is analysed ("watched"), an
// editorial director (Ollama, or a heuristic fallback) orders and trims the
// pieces like a puzzle and adds on-screen text and sound, and the timeline is
// rendered by the same engine as the other modes (zooms, SFX, music mixer,
// captions).
package assembly

import "cenarius-autocut/internal/transcribe"

// Clip is what the editor learned from one uploaded file.
type Clip struct {
	ID         string             `json:"id"` // c01, c02... in upload order
	Name       string             `json:"name"`
	Path       string             `json:"-"`
	Duration   float64            `json:"duration"`
	Width      int                `json:"width"`
	Height     int                `json:"height"`
	HasAudio   bool               `json:"has_audio"`
	Transcript string             `json:"transcript,omitempty"`
	Words      []transcribe.Token `json:"-"`
	Visual     string             `json:"visual,omitempty"` // what happens on screen
	Tags       []string           `json:"tags,omitempty"`
	Emotion    string             `json:"emotion,omitempty"`
	VisualBy   string             `json:"visual_by,omitempty"` // vision model name or "heuristic"
	Energy     float64            `json:"energy"`              // 0..1 audio loudness
	Motion     float64            `json:"motion"`              // 0..1 scene-change rate
}

// Segment is one piece of the final video, taken from a clip.
type Segment struct {
	ClipID    string  `json:"clip_id"`
	Start     float64 `json:"start"` // clip-local seconds
	End       float64 `json:"end"`
	Role      string  `json:"role,omitempty"` // hook | build | punchline | outro
	Text      string  `json:"text,omitempty"` // on-screen text (meme/title style)
	TextPos   string  `json:"text_pos,omitempty"`
	SFX       string  `json:"sfx,omitempty"` // whoosh | pop | hit | impact | riser | click
	Zoom      bool    `json:"zoom,omitempty"`
	KeepAudio bool    `json:"keep_audio"`
	Reason    string  `json:"reason,omitempty"`
}

func (s Segment) Duration() float64 { return s.End - s.Start }

// Plan is the editor's cut ("roteiro de montagem").
type Plan struct {
	Title     string    `json:"title"`
	Prompt    string    `json:"prompt"`
	MusicMood string    `json:"music_mood"`
	Captions  bool      `json:"captions"`
	Origin    string    `json:"origin"` // ollama | heuristic
	Segments  []Segment `json:"segments"`
	Clips     []Clip    `json:"clips"`
	Notes     string    `json:"notes,omitempty"`
}

// Duration is the total length of the planned timeline.
func (p Plan) Duration() float64 {
	d := 0.0
	for _, s := range p.Segments {
		d += s.Duration()
	}
	return d
}

// Options are the per-job choices of the user.
type Options struct {
	Prompt   string
	Duration float64 // target seconds (0 = config default)
	Captions *bool   // nil = let the director decide
}

var (
	validRoles = map[string]bool{"hook": true, "build": true, "punchline": true, "outro": true}
	validPos   = map[string]bool{"top": true, "center": true, "bottom": true}
	validSFX   = map[string]bool{"whoosh": true, "pop": true, "hit": true, "impact": true, "riser": true, "click": true, "beep": true}
)
