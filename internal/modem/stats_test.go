package modem

import "testing"

// Zero radio params must give 0 rather than a fabricated number the caller would publish.
func TestKissStatsProvider_DerivedValues(t *testing.T) {
	p := &kissStatsProvider{radio: RadioInfo{FreqHz: 917_375_000, BwHz: 62_500, SF: 7, CR: 5}}

	// 16 bytes at SF7/62.5 kHz is tens of ms; the exact figure is the library's.
	if ms := p.EstAirtimeMs(16); ms < 10 || ms > 500 {
		t.Errorf("EstAirtimeMs(16) = %d, want a plausible tens-of-ms figure", ms)
	}
	if score := p.PacketScore(5, 16); score <= 0 || score > 1 {
		t.Errorf("PacketScore(5dB, 16) = %v, want a 0-1 score above 0", score)
	}
	// SF below 7 is out of the score table's range and reports 0.
	if score := p.PacketScore(5, 16); (&kissStatsProvider{}).PacketScore(5, 16) >= score {
		t.Error("an unconfigured radio must score 0, below a configured one")
	}

	if ms := (&kissStatsProvider{}).EstAirtimeMs(16); ms != 0 {
		t.Errorf("EstAirtimeMs with no radio params = %d, want 0", ms)
	}
}
