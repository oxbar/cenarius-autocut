package planner

// ZoomEvent is an A-roll camera move. Kind selects the motion curve:
//
//	punch_zoom: fast ease-in, hold, quick release (emphasis/punchline)
//	slow_push:  linear push-in across the interval (explanations, long A-roll)
//	reframe:    hard cut to a tighter frame and back (jump-cut feel)
//
// An empty Kind is treated as punch_zoom for backwards compatibility.
type ZoomEvent struct {
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Scale  float64 `json:"scale"`
	Kind   string  `json:"kind,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

const (
	ZoomPunch    = "punch_zoom"
	ZoomSlowPush = "slow_push"
	ZoomReframe  = "reframe"
)

// OverlayEvent is the legacy (<= v1.5) overlay schema. It is still accepted
// from Ollama and old edit plans and is migrated to VisualEvent by
// MigrateLegacy. New code must use VisualEvents.
type OverlayEvent struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Keyword     string  `json:"keyword"`
	Query       string  `json:"query,omitempty"`
	Position    string  `json:"position,omitempty"`
	Mode        string  `json:"mode,omitempty"` // bottom, reaction, fullscreen, card
	Reason      string  `json:"reason,omitempty"`
	Asset       string  `json:"asset,omitempty"`
	AssetSource string  `json:"asset_source,omitempty"`
	SourceURL   string  `json:"source_url,omitempty"`
	Attribution string  `json:"attribution,omitempty"`
	AssetType   string  `json:"asset_type,omitempty"` // image | video
}

// Visual event types.
const (
	TypeBroll  = "broll"
	TypeCard   = "card"
	TypePIP    = "pip"
	TypeMotion = "motion"
)

// Layouts.
const (
	LayoutReaction   = "reaction"
	LayoutFullscreen = "fullscreen"
	LayoutCard       = "card"
	LayoutPIP        = "pip"
)

// Asset sources, ordered by preference. Self-broll is never a resolver
// success: it only exists as the last fallback.
const (
	SourceLocal     = "local"
	SourceCache     = "cache"
	SourcePexels    = "pexels"
	SourcePixabay   = "pixabay"
	SourceManual    = "manual-reaction"
	SourceCommons   = "wikimedia-commons"
	SourceProcedure = "procedural"
	SourceSelfBroll = "self-broll"
)

// AssetRef is what the resolver attached to a visual event.
type AssetRef struct {
	Path      string  `json:"path,omitempty"`
	Type      string  `json:"type,omitempty"` // video | image
	Source    string  `json:"source"`
	Origin    string  `json:"origin,omitempty"` // original source when served from cache
	Query     string  `json:"query,omitempty"`  // query that matched
	URL       string  `json:"url,omitempty"`
	License   string  `json:"license,omitempty"`
	Author    string  `json:"author,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	Fallback  bool    `json:"fallback,omitempty"`
	AudioMode string  `json:"audio_mode,omitempty"` // muted | duck | original (manual reaction assets)
}

// VisualEvent is an editorial decision: "between start and end, show this
// concept with this layout, because of this reason".
type VisualEvent struct {
	ID           string    `json:"id"`
	Start        float64   `json:"start"`
	End          float64   `json:"end"`
	Type         string    `json:"type"`
	Concept      string    `json:"concept"`
	Label        string    `json:"label,omitempty"` // pt-BR on-screen label for motion graphics
	Queries      []string  `json:"queries"`
	Anchors      []string  `json:"anchors,omitempty"` // visual words a stock result must match
	Layout       string    `json:"layout"`
	Importance   float64   `json:"importance"`
	Relevance    float64   `json:"relevance,omitempty"`
	Role         string    `json:"role,omitempty"`
	UnitID       string    `json:"unit_id,omitempty"`
	Origin       string    `json:"origin,omitempty"` // heuristic | ollama | legacy
	AllowOverlap bool      `json:"allow_overlap,omitempty"`
	Asset        *AssetRef `json:"asset,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

func (e VisualEvent) Duration() float64 { return e.End - e.Start }

type SFXEvent struct {
	Time   float64 `json:"time"`
	Name   string  `json:"name"` // pop, whoosh, click, beep
	GainDB float64 `json:"gain_db,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

type Plan struct {
	Version       string         `json:"version,omitempty"`
	Hook          string         `json:"hook,omitempty"`
	CTA           string         `json:"cta,omitempty"`
	SemanticUnits []SemanticUnit `json:"semantic_units,omitempty"`
	VisualEvents  []VisualEvent  `json:"visual_events"`
	Zooms         []ZoomEvent    `json:"zooms"`
	// Overlays is the legacy schema; only read, never produced by v1.6.
	Overlays []OverlayEvent `json:"overlays,omitempty"`
	SFX      []SFXEvent     `json:"sfx"`
	Emphasis []string       `json:"emphasis"`
	Music    *MusicCue      `json:"music,omitempty"`
}

// Music moods (emotional direction of the whole reel).
const (
	MoodEnergetic = "energetic"
	MoodInspiring = "inspiring"
	MoodTense     = "tense"
	MoodDramatic  = "dramatic"
	MoodChill     = "chill"
	MoodFunny     = "funny"
)

// MusicDrop silences the music bed for a moment so a punchline lands.
type MusicDrop struct {
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Reason string  `json:"reason,omitempty"`
}

// TimeRange is a time interval on the edited timeline.
type TimeRange struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// MusicCue is the emotional sound direction: one mood-matched bed under the
// voice (ducked while speaking) with dramatic drops.
type MusicCue struct {
	Mood    string      `json:"mood"`
	Queries []string    `json:"queries,omitempty"`
	GainDB  float64     `json:"gain_db"`
	Drops   []MusicDrop `json:"drops,omitempty"`
	Speech  []TimeRange `json:"speech,omitempty"` // where the voice is: music ducks here
	Asset   *AssetRef   `json:"asset,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

const PlanVersion = "1.9"

// MigrateLegacy converts legacy overlays into visual events so old plans and
// old-style Ollama answers keep working.
func MigrateLegacy(p *Plan) {
	if len(p.Overlays) == 0 {
		return
	}
	for _, o := range p.Overlays {
		layout := o.Mode
		switch layout {
		case "", "bottom":
			layout = LayoutReaction
		case LayoutReaction, LayoutFullscreen, LayoutCard, LayoutPIP:
		default:
			layout = LayoutReaction
		}
		typ := TypeBroll
		if layout == LayoutCard {
			typ = TypeCard
		}
		q := []string{}
		if o.Query != "" {
			q = append(q, o.Query)
		}
		if o.Keyword != "" && o.Keyword != o.Query {
			q = append(q, o.Keyword)
		}
		p.VisualEvents = append(p.VisualEvents, VisualEvent{
			Start: o.Start, End: o.End, Type: typ, Concept: o.Keyword, Queries: q,
			Layout: layout, Importance: 0.6, Origin: "legacy", Reason: o.Reason,
		})
	}
	p.Overlays = nil
}
