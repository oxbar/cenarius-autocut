package planner

type ZoomEvent struct {
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Scale  float64 `json:"scale"`
	Reason string  `json:"reason,omitempty"`
}
type OverlayEvent struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Keyword  string  `json:"keyword"`
	Position string  `json:"position,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}
type Plan struct {
	Zooms    []ZoomEvent    `json:"zooms"`
	Overlays []OverlayEvent `json:"overlays"`
	Emphasis []string       `json:"emphasis"`
}
