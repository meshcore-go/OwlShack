package repeater

import "testing"

// loopThreshold values are ported from the firmware's max_loop_* tables
// (examples/simple_repeater MyMesh.cpp). Pin them so a transcription slip is
// caught — a wrong threshold silently changes relay behaviour on the air.
func TestLoopThreshold(t *testing.T) {
	cases := []struct {
		level string
		size  int
		want  int
	}{
		{"off", 1, 0},
		{"off", 2, 0},
		{"minimal", 1, 4},
		{"minimal", 2, 2},
		{"minimal", 3, 1},
		{"moderate", 1, 2},
		{"moderate", 2, 1},
		{"moderate", 3, 1},
		{"strict", 1, 1},
		{"strict", 2, 1},
		{"strict", 3, 1},
		{"minimal", 0, 0}, // out-of-range hash size → no check
		{"minimal", 4, 0},
		{"bogus", 1, 0}, // unknown level → no check
	}
	for _, c := range cases {
		if got := loopThreshold(c.level, c.size); got != c.want {
			t.Errorf("loopThreshold(%q, %d) = %d, want %d", c.level, c.size, got, c.want)
		}
	}
}
