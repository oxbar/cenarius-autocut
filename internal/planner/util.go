package planner

import "strconv"

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func itoa3(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
