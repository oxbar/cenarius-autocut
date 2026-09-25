package planner

type ZoomEvent struct {
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Scale  float64 `json:"scale"`
	Reason string  `json:"reason,omitempty"`
}

type OverlayEvent struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Keyword     string  `json:"keyword"`
	Query       string  `json:"query,omitempty"`
	Position    string  `json:"position,omitempty"`
	Mode        string  `json:"mode,omitempty"` // bottom, fullscreen, card
	Reason      string  `json:"reason,omitempty"`
	Asset       string  `json:"asset,omitempty"`
	AssetSource string  `json:"asset_source,omitempty"`
	SourceURL   string  `json:"source_url,omitempty"`
	Attribution string  `json:"attribution,omitempty"`
}

type SFXEvent struct {
	Time   float64 `json:"time"`
	Name   string  `json:"name"` // pop, whoosh
	GainDB float64 `json:"gain_db,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

type Plan struct {
	Zooms    []ZoomEvent    `json:"zooms"`
	Overlays []OverlayEvent `json:"overlays"`
	SFX      []SFXEvent     `json:"sfx"`
	Emphasis []string       `json:"emphasis"`
}
