package api

import (
	"os"
	"path/filepath"
	"testing"
)

// The by-id name is the only human-readable thing about a tty, and it is what tells two identical
// boards apart; a wrong trim turns it back into noise.
func TestPrettyByID(t *testing.T) {
	for in, want := range map[string]string{
		"usb-Seeed_Studio_XIAO_nRF52840_9D3BDDC665320872-if00": "Seeed Studio XIAO nRF52840 9D3BDDC665320872",
		"usb-RAKwireless_RAK4631-if00":                         "RAKwireless RAK4631",
		"usb-1a86_USB_Single_Serial_58A3016462-if00":           "1a86 USB Single Serial 58A3016462",
		// No prefix, no interface suffix: leave it alone rather than trimming something real.
		"platform-fe201000.serial": "platform-fe201000.serial",
		// A hyphenated model name must not be mistaken for the -ifNN suffix.
		"usb-Some_Vendor_Model-X-if02": "Some Vendor Model-X",
	} {
		if got := prettyByID(in); got != want {
			t.Errorf("prettyByID(%q) = %q, want %q", in, got, want)
		}
	}
}

// A composite device publishes one by-id name per interface, all pointing at the same tty. Which one
// we store must not depend on directory order, or two calls a minute apart disagree about the device.
func TestStablePathsIn_PicksOneNameDeterministically(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "by-id")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tty := filepath.Join(root, "ttyACM0")
	if err := os.WriteFile(tty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"usb-Vendor_Board_ABC-if02", "usb-Vendor_Board_ABC-if00"} {
		if err := os.Symlink(tty, filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	// A plain file here must be ignored: mapped to itself it would be published as a stable name.
	if err := os.WriteFile(filepath.Join(dir, "README"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got := stablePathsIn(dir)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want the two interfaces folded into one device: %+v", len(got), got)
	}
	sp := got[tty]
	if want := filepath.Join(dir, "usb-Vendor_Board_ABC-if00"); sp.path != want {
		t.Errorf("path = %q, want %q (the lowest-sorting name)", sp.path, want)
	}
	if sp.label != "Vendor Board ABC" {
		t.Errorf("label = %q, want %q", sp.label, "Vendor Board ABC")
	}
	if stablePathsIn(filepath.Join(root, "nope")) != nil {
		t.Error("a missing by-id directory must map nothing, not panic or invent a path")
	}
}
