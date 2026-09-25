package silence

import "testing"

func TestKeepAndMap(t *testing.T) {
	k := KeepFromSilences(10, []Interval{{2, 4}, {7, 8}}, .2, .1)
	if len(k) != 3 {
		t.Fatalf("keep=%v", k)
	}
	x, ok := MapTime(5, k)
	if !ok || x >= 5 {
		t.Fatalf("map %.2f %v", x, ok)
	}
}
