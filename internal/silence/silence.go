package silence

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"cenarius-autocut/internal/execx"
)

type Interval struct{ Start, End float64 }

var startRE = regexp.MustCompile(`silence_start:\s*([0-9.]+)`)
var endRE = regexp.MustCompile(`silence_end:\s*([0-9.]+)`)

func Detect(ctx context.Context, ffmpeg, input string, noiseDB int, min float64) ([]Interval, error) {
	args := []string{"-hide_banner", "-i", input, "-af", fmt.Sprintf("silencedetect=noise=%ddB:d=%.3f", noiseDB, min), "-f", "null", "-"}
	r, err := execx.Run(ctx, nil, ffmpeg, args...)
	if err != nil {
		return nil, err
	}
	ss := startRE.FindAllStringSubmatch(r.Stderr, -1)
	es := endRE.FindAllStringSubmatch(r.Stderr, -1)
	n := len(ss)
	if len(es) < n {
		n = len(es)
	}
	out := make([]Interval, 0, n)
	for i := 0; i < n; i++ {
		a, _ := strconv.ParseFloat(ss[i][1], 64)
		b, _ := strconv.ParseFloat(es[i][1], 64)
		if b > a {
			out = append(out, Interval{a, b})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}

func KeepFromSilences(duration float64, silences []Interval, keepPad, minKeep float64) []Interval {
	removals := make([]Interval, 0, len(silences))
	for _, s := range silences {
		a := s.Start + keepPad
		b := s.End - keepPad
		if b-a > 0.05 {
			removals = append(removals, Interval{a, b})
		}
	}
	if len(removals) == 0 {
		return []Interval{{0, duration}}
	}
	keep := []Interval{}
	cur := 0.0
	for _, r := range removals {
		if r.Start > cur && r.Start-cur >= minKeep {
			keep = append(keep, Interval{cur, r.Start})
		}
		if r.End > cur {
			cur = r.End
		}
	}
	if duration-cur >= minKeep {
		keep = append(keep, Interval{cur, duration})
	}
	if len(keep) == 0 {
		return []Interval{{0, duration}}
	}
	return keep
}

func MapTime(t float64, keep []Interval) (float64, bool) {
	out := 0.0
	for _, k := range keep {
		if t < k.Start {
			return out, false
		}
		if t <= k.End {
			return out + (t - k.Start), true
		}
		out += k.End - k.Start
	}
	return out, false
}

func MapRange(start, end float64, keep []Interval) (float64, float64, bool) {
	ns, ok1 := MapTime(start, keep)
	ne, ok2 := MapTime(end, keep)
	if ok1 && ok2 && ne > ns {
		return ns, ne, true
	}
	// If a boundary lands inside a removed silence, clamp it to the nearest preserved point.
	var first, last *float64
	acc := 0.0
	for _, k := range keep {
		if end >= k.Start && start <= k.End {
			s := start
			if s < k.Start {
				s = k.Start
			}
			e := end
			if e > k.End {
				e = k.End
			}
			if e > s {
				a := acc + (s - k.Start)
				b := acc + (e - k.Start)
				if first == nil {
					x := a
					first = &x
				}
				y := b
				last = &y
			}
		}
		acc += k.End - k.Start
	}
	if first != nil && last != nil && *last > *first {
		return *first, *last, true
	}
	return 0, 0, false
}
