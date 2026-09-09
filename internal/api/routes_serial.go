package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.bug.st/serial"
)

// SerialPortInfo is one candidate device for the KISS connection picker.
type SerialPortInfo struct {
	// Path is what to store: a by-id path where the OS publishes one, because /dev/ttyACM0 is an
	// enumeration order, not an identity, and moves when the device re-enumerates.
	Path string `json:"path"`
	// Device is the tty Path currently resolves to, and is Path itself when there is no stable name.
	Device string `json:"device"`
	// Label is the USB descriptor, absent for a plain tty.
	Label string `json:"label,omitempty"`
	// Stable is false when only the enumeration-order path exists, so the UI can say so.
	Stable bool `json:"stable"`
}

// handleSerialPorts lists the host's serial devices. This is an OS query with no app state behind
// it, so it answers during first-run setup, when there is no backend yet and the picker is needed most.
func (s *Server) handleSerialPorts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, listSerialPorts())
}

func listSerialPorts() []SerialPortInfo {
	names, err := serial.GetPortsList()
	if err != nil {
		return []SerialPortInfo{}
	}
	sort.Strings(names)
	stable := stablePaths()
	out := make([]SerialPortInfo, 0, len(names))
	for _, n := range names {
		p := SerialPortInfo{Path: n, Device: n}
		if s, ok := stable[n]; ok {
			p.Path, p.Label, p.Stable = s.path, s.label, true
		}
		out = append(out, p)
	}
	return out
}

type stablePath struct{ path, label string }

// stablePaths maps each tty to the persistent name Linux publishes for it. The serial library can
// also report a product string, but only with active USB probing, which its own docs warn can
// disturb a device mid-session — and the device in question is usually the modem we are talking to.
// Other platforms publish no such directory, and there the tty path is all there is.
func stablePaths() map[string]stablePath { return stablePathsIn("/dev/serial/by-id") }

func stablePathsIn(dir string) map[string]stablePath {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[string]stablePath, len(entries))
	for _, e := range entries {
		// Every real by-id entry is a symlink; anything else would map to itself and be published as
		// a stable name for a device it is not.
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(dir, e.Name())
		target, err := filepath.EvalSymlinks(link)
		if err != nil {
			continue
		}
		// Several by-id names can point at one tty on a composite device; the first sorted name wins
		// so the answer does not change between two calls that saw the same hardware.
		if prev, ok := out[target]; ok && prev.path <= link {
			continue
		}
		out[target] = stablePath{path: link, label: prettyByID(e.Name())}
	}
	return out
}

// prettyByID turns "usb-Seeed_Studio_XIAO_nRF52840_9D3B...-if00" into "Seeed Studio XIAO nRF52840 9D3B...".
func prettyByID(name string) string {
	name = strings.TrimPrefix(name, "usb-")
	if i := strings.LastIndex(name, "-if"); i > 0 {
		name = name[:i]
	}
	return strings.ReplaceAll(name, "_", " ")
}
