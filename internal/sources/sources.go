// Package sources probes an image file and derives which installer can use it.
package sources

import (
	"errors"
	"fmt"
	"os"
	"strings"
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
type SourceInfo struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Kind    Kind   `json:"kind"`
	Label   string `json:"label"`
	Hybrid  bool   `json:"hybrid"`
	HasWIM  bool   `json:"hasWim"`
	WIMSize int64  `json:"wimSize"`
	Reason  string `json:"reason,omitempty"`
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

	info := SourceInfo{Path: path, Size: st.Size(), Kind: Unknown}

	vol, err := readVolume(f, st.Size())
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

	size, found, err := findWIM(vol.lookup)
	if err != nil {
		return info, err
	}
	if !found {
		udf, err := readUDF(f, st.Size())
		if err != nil && !errors.Is(err, errNoUDF) {
			return info, err
		}
		if udf != nil {
			if size, found, err = findWIM(udf.lookup); err != nil {
				return info, err
			}
		}
	}
	info.HasWIM = found
	info.WIMSize = size

	switch {
	case info.HasWIM:
		info.Kind = WindowsISO
	case info.Hybrid:
		info.Kind = LinuxISO
	default:
		info.Reason = "not a hybrid ISO and no Windows sources"
	}
	return info, nil
}

func findWIM(lookup func(path ...string) (int64, bool, error)) (int64, bool, error) {
	for _, name := range wimNames {
		size, found, err := lookup("sources", name)
		if err != nil || found {
			return size, found, err
		}
	}
	return 0, false, nil
}

// probeError is a verdict about the file's contents, as opposed to a read
// failure; Probe reports it as an Unknown source with that reason.
type probeError struct{ reason string }

func (e *probeError) Error() string { return e.reason }

func (k Kind) String() string {
	switch k {
	case LinuxISO:
		return "Linux ISO"
	case WindowsISO:
		return "Windows ISO"
	}
	return strings.ToUpper(string(k[:1])) + string(k[1:])
}
