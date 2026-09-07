// Package buildinfo holds build-time metadata set by -ldflags (-X ...buildinfo.Version=v1.2.3).
package buildinfo

// Version is the build version, set at release time and defaulting to "dev".
var Version = "dev"

// Date is the build date, reported by the repeater's `ver` command in the firmware format "<ver> (Build: <date>)".
var Date = "unknown"
