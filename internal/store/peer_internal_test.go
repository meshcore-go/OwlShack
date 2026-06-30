package store

import (
	"bytes"
	"testing"
)

func TestPrefixUpperBound(t *testing.T) {
	cases := []struct{ in, want []byte }{
		{[]byte{0x01}, []byte{0x02}},
		{[]byte{0xaa, 0xbb}, []byte{0xaa, 0xbc}},
		{[]byte{0xaa, 0xff}, []byte{0xab}}, // carry past trailing 0xFF
		{[]byte{0xff, 0xff}, nil},          // all 0xFF: no upper bound
	}
	for _, c := range cases {
		if got := prefixUpperBound(c.in); !bytes.Equal(got, c.want) {
			t.Errorf("prefixUpperBound(% x) = % x, want % x", c.in, got, c.want)
		}
	}
	// Range must bracket only values starting with the prefix.
	p := []byte{0xaa, 0xbb}
	u := prefixUpperBound(p)
	if !(bytes.Compare(p, append(p, 0x00)) <= 0 && bytes.Compare(append(p, 0xff), u) < 0) {
		t.Errorf("range [% x, % x) does not bracket the prefix", p, u)
	}
}
