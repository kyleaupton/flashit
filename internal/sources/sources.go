// Package sources probes an image file and derives which installer can use it.
package sources

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Kind is the installer family an image belongs to.
type Kind string

const (
	LinuxISO   Kind = "linux-iso"
	WindowsISO Kind = "windows-iso"
	Unknown    Kind = "unknown"
)

// SourceInfo is what Probe learned about an image. Reason is set only when
// Kind is Unknown and says why the image cannot be used.
//
// The fields from Name on are for display and are empty when the image
// does not say: Name is the distribution or Windows release, Arch is
// "x86-64", "ARM64", "RISC-V" or "x86" for Linux and "x64", "ARM64" or "x86"
// for Windows, BIOS and UEFI come from the El Torito boot catalog, and
// Editions and Language (a tag such as "en-US") from the WIM's XML.
type SourceInfo struct {
	Path     string   `json:"path"`
	Size     int64    `json:"size"`
	Kind     Kind     `json:"kind"`
	Label    string   `json:"label"`
	Hybrid   bool     `json:"hybrid"`
	HasWIM   bool     `json:"hasWim"`
	WIMSize  int64    `json:"wimSize"`
	Reason   string   `json:"reason,omitempty"`
	Name     string   `json:"name"`
	Arch     string   `json:"arch"`
	BIOS     bool     `json:"bios"`
	UEFI     bool     `json:"uefi"`
	Editions []string `json:"editions"`
	Language string   `json:"language"`
}

var wimNames = []string{"install.wim", "install.esd"}

// Probe reads the image's volume descriptors and directory tree: the ISO
// 9660 primary volume descriptor for the label, the MBR signature for hybrid
// boot, and the Joliet, ISO 9660 and UDF trees, in that order, for Windows
// sources. It returns an error only when the file cannot be read; an image
// that is readable but unusable comes back as Unknown.
func Probe(path string) (SourceInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SourceInfo{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return SourceInfo{}, err
	}
	if st.IsDir() {
		return SourceInfo{}, fmt.Errorf("%s is a directory", path)
	}
	return probe(f, st.Size(), path)
}

func probe(f io.ReaderAt, size int64, path string) (SourceInfo, error) {
	info := SourceInfo{Path: path, Size: size, Kind: Unknown}

	vol, err := readVolume(f, size)
	if err != nil {
		var pe *probeError
		if errors.As(err, &pe) {
			info.Reason = pe.reason
			return info, nil
		}
		return info, err
	}
	info.Label = vol.label
	info.Hybrid = vol.hybrid

	wim, found, err := findWIM(vol.lookup)
	if err != nil {
		return info, err
	}
	if !found {
		udf, err := readUDF(f, size)
		switch {
		case err == nil:
			wim, found, err = findWIM(udf.lookup)
		case errors.Is(err, errNoUDF):
			err = nil
		}
		// UDF only adds Windows detection: a hybrid ISO is a Linux image
		// whatever its UDF side looks like, and sector 256 of one can hold
		// anything.
		if err != nil && !info.Hybrid {
			info.Reason = "not a hybrid ISO and the UDF file system could not be read: " + err.Error()
			return info, nil
		}
	}
	info.HasWIM = found

	switch {
	case info.HasWIM:
		info.Kind = WindowsISO
		info.WIMSize = wim.size
		windowsMeta(wim, &info)
	case info.Hybrid:
		info.Kind = LinuxISO
		linuxMeta(vol, &info)
	default:
		info.Reason = "not a hybrid ISO and no Windows sources"
	}
	if info.Kind != Unknown {
		info.BIOS, info.UEFI = vol.bootModes()
	}
	return info, nil
}

func findWIM(lookup func(path ...string) (*file, bool, error)) (*file, bool, error) {
	for _, name := range wimNames {
		f, found, err := lookup("sources", name)
		if err != nil || found {
			return f, found, err
		}
	}
	return nil, false, nil
}

// probeError is a verdict about the file's contents, as opposed to a read
// failure; Probe reports it as an Unknown source with that reason.
type probeError struct{ reason string }

func (e *probeError) Error() string { return e.reason }
