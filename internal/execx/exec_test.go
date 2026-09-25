package execx

import (
	"strings"
	"testing"
)

func TestSanitizeLogRemovesLocation(t *testing.T) {
	in := `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'IMG_6823.MOV':
  Metadata:
    com.apple.quicktime.location.accuracy.horizontal: 4.766145
    com.apple.quicktime.location.ISO6709: -26.9194-048.6622+012.345/
    location        : -26.9194-048.6622+012.345/
    location-eng    : -26.9194-048.6622/
    GPSLatitude     : 26 deg 55'
    latitude=-26.91
    longitude=-48.66
    com.apple.quicktime.make: Apple
  Duration: 00:00:23.08, start: 0.000000, bitrate: 11264 kb/s
stray coordinates +12.3456-045.6789/ here`
	out := sanitizeLog(in)
	for _, bad := range []string{"-26.9194", "-048.6622", "ISO6709", "GPSLatitude", "latitude=", "longitude=", "+12.3456"} {
		if strings.Contains(out, bad) {
			t.Fatalf("location leaked (%q):\n%s", bad, out)
		}
	}
	for _, keep := range []string{"com.apple.quicktime.make: Apple", "Duration: 00:00:23.08"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("useful metadata lost (%q):\n%s", keep, out)
		}
	}
}
