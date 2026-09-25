package planner

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MinEventDuration is the shortest visual event worth showing; anything
// shorter reads as a glitch rather than an edit.
const MinEventDuration = 1.0

// Conflict describes one scheduling decision, for visual.conflict.* logs.
type Conflict struct {
	Winner   string  `json:"winner"`
	Loser    string  `json:"loser"`
	Action   string  `json:"action"` // trimmed | dropped
	NewStart float64 `json:"new_start,omitempty"`
	NewEnd   float64 `json:"new_end,omitempty"`
}

// SourceQuality ranks resolved assets. Unresolved events are neutral so the
// planner can still schedule before the resolver runs.
func SourceQuality(a *AssetRef) float64 {
	if a == nil {
		return 0.5
	}
	if a.Source == SourceSelfBroll {
		return 0.05
	}
	if a.Source == SourceProcedure {
		return 0.35
	}
	if a.Type == "video" {
		return 1.0
	}
	return 0.8
}

func eventScore(e VisualEvent) float64 {
	rel := e.Relevance
	if rel == 0 {
		rel = e.Importance
	}
	return e.Importance*0.6 + rel*0.25 + SourceQuality(e.Asset)*0.15
}

func compatible(a, b VisualEvent) bool { return a.AllowOverlap && b.AllowOverlap }

func overlaps(a, b VisualEvent, gap float64) bool {
	return a.Start < b.End+gap && b.Start < a.End+gap
}

// ResolveVisualConflicts removes overlaps between visual events. Events are
// ranked by importance, semantic relevance and source quality. A lower-ranked
// event that collides with a better one is trimmed into the free window when
// it still lasts >= MinEventDuration; otherwise it is dropped. minGap keeps a
// breathing space of A-roll between two inserts.
func ResolveVisualConflicts(events []VisualEvent, minGap float64) ([]VisualEvent, []Conflict) {
	cands := append([]VisualEvent(nil), events...)
	sort.SliceStable(cands, func(i, j int) bool {
		si, sj := eventScore(cands[i]), eventScore(cands[j])
		if si != sj {
			return si > sj
		}
		return cands[i].Start < cands[j].Start
	})
	var accepted []VisualEvent
	var conflicts []Conflict
	for _, c := range cands {
		if c.End-c.Start < MinEventDuration*0.5 {
			continue
		}
		orig := c
		var blockers []VisualEvent
		for _, a := range accepted {
			if !compatible(a, c) && overlaps(a, c, minGap) {
				blockers = append(blockers, a)
			}
		}
		if len(blockers) == 0 {
			accepted = append(accepted, c)
			continue
		}
		// Trim towards the side of the original interval that has more room.
		for _, a := range blockers {
			mid := (orig.Start + orig.End) / 2
			if a.Start >= mid {
				c.End = math.Min(c.End, a.Start-minGap)
			} else {
				c.Start = math.Max(c.Start, a.End+minGap)
			}
		}
		ok := c.End-c.Start >= MinEventDuration
		if ok {
			for _, a := range accepted {
				if !compatible(a, c) && overlaps(a, c, minGap) {
					ok = false
					break
				}
			}
		}
		if ok {
			accepted = append(accepted, c)
			conflicts = append(conflicts, Conflict{Winner: blockers[0].ID, Loser: orig.ID, Action: "trimmed", NewStart: c.Start, NewEnd: c.End})
		} else {
			conflicts = append(conflicts, Conflict{Winner: blockers[0].ID, Loser: orig.ID, Action: "dropped"})
		}
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].Start < accepted[j].Start })
	return accepted, conflicts
}

// FindOverlaps returns every pair of incompatible events closer than gap.
// Used by tests and by visual.conflict.detected logging.
func FindOverlaps(events []VisualEvent, gap float64) [][2]string {
	var out [][2]string
	for i := range events {
		for j := i + 1; j < len(events); j++ {
			if !compatible(events[i], events[j]) && overlaps(events[i], events[j], gap-1e-9) {
				out = append(out, [2]string{events[i].ID, events[j].ID})
			}
		}
	}
	return out
}

// TargetVisualEvents is the B-roll/cutaway budget. Shorts stay around 3-5
// inserts; medium/long videos scale gradually instead of being hard-capped at
// five events for the entire timeline. maxEvents remains the user safety cap.
func TargetVisualEvents(duration float64, maxEvents int) int {
	if duration < 4 {
		return 0
	}
	var n int
	if duration <= 30 {
		n = int(math.Round(duration / 6.0))
		if n < 1 {
			n = 1
		}
		if n < 2 && duration >= 9 {
			n = 2
		}
		// Once a Short has enough timeline to breathe, three meaningful
		// visual changes is a better retention floor than two long static runs.
		if n < 3 && duration >= 12 {
			n = 3
		}
		if n > 5 {
			n = 5
		}
	} else {
		// Roughly one meaningful insert every 7.5 seconds, with enough A-roll
		// breathing room for captions/zooms between them.
		n = int(math.Round(duration / 7.5))
		if n < 5 {
			n = 5
		}
		if n > 12 {
			n = 12
		}
	}
	if maxEvents > 0 && n > maxEvents {
		n = maxEvents
	}
	return n
}

// Schedule resolves conflicts, applies the rhythm budget and assigns stable IDs.
func normalizeConcept(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func Schedule(events []VisualEvent, duration, minGap float64, maxEvents int) ([]VisualEvent, []Conflict) {
	for i := range events {
		if events[i].ID == "" {
			events[i].ID = fmt.Sprintf("cand-%03d", i+1)
		}
	}
	accepted, conflicts := ResolveVisualConflicts(events, minGap)
	budget := TargetVisualEvents(duration, maxEvents)
	if budget == 0 {
		for _, e := range accepted {
			conflicts = append(conflicts, Conflict{Loser: e.ID, Action: "dropped_rhythm_budget"})
		}
		accepted = nil
	} else if len(accepted) > budget {
		ranked := append([]VisualEvent(nil), accepted...)
		sort.SliceStable(ranked, func(i, j int) bool { return eventScore(ranked[i]) > eventScore(ranked[j]) })
		chosen := make([]VisualEvent, 0, budget)
		chosenID := map[string]bool{}
		concepts := map[string]bool{}
		// First pass prioritises distinct concepts. A second pass fills spare
		// budget only when the transcript genuinely has fewer alternatives.
		for _, e := range ranked {
			key := normalizeConcept(e.Concept)
			if key != "" && concepts[key] {
				continue
			}
			chosen = append(chosen, e)
			chosenID[e.ID] = true
			if key != "" {
				concepts[key] = true
			}
			if len(chosen) == budget {
				break
			}
		}
		for _, e := range ranked {
			if len(chosen) == budget {
				break
			}
			if chosenID[e.ID] {
				continue
			}
			chosen = append(chosen, e)
			chosenID[e.ID] = true
		}
		for _, e := range accepted {
			if !chosenID[e.ID] {
				conflicts = append(conflicts, Conflict{Loser: e.ID, Action: "dropped_rhythm_budget"})
			}
		}
		accepted = chosen
		sort.Slice(accepted, func(i, j int) bool { return accepted[i].Start < accepted[j].Start })
	}
	for i := range accepted {
		accepted[i].ID = fmt.Sprintf("ve-%03d", i+1)
	}
	return accepted, conflicts
}
