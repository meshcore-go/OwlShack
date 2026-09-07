package advert

import "testing"

// The top 2 bits of a flood advert's PathLength carry (pathHashSize - 1), the
// low 6 the hop count (0 for one we originate). A 2-byte region sends 0x40.
func TestFloodPathLength(t *testing.T) {
	for _, c := range []struct {
		size int
		want byte
	}{
		{1, 0x00}, {2, 0x40}, {3, 0x80},
		{0, 0x00}, // would underflow to 0xC0 unclamped
		{4, 0x00}, // 2 bits cannot carry it
		{-1, 0x00},
	} {
		if got := floodPathLength(c.size); got != c.want {
			t.Errorf("floodPathLength(%d) = %#02x, want %#02x", c.size, got, c.want)
		}
	}
	// The decode the rest of the tree uses must round-trip the encode.
	for size := 1; size <= 3; size++ {
		if got := int(floodPathLength(size)>>6)&3 + 1; got != size {
			t.Errorf("%d encodes then decodes to %d", size, got)
		}
	}
}
