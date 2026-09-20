//go:build linux

package drives

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type linuxProvider struct{}

func platformProvider() Provider { return &linuxProvider{} }

// ---------- lsblk JSON models ----------
//
// lsblk's JSON output has changed value types across util-linux releases:
// versions before 2.37 emit every value as a string (e.g. "size":"123",
// "rm":"0"), while newer versions emit native JSON numbers/booleans. The
// jsonUint/jsonBool helpers below accept both so we work across distros.

type jsonUint uint64

func (j *jsonUint) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*j = 0
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("parse uint %q: %w", s, err)
	}
	*j = jsonUint(v)
	return nil
}

type jsonBool bool

func (j *jsonBool) UnmarshalJSON(b []byte) error {
	switch strings.Trim(string(b), `"`) {
	case "1", "true":
		*j = true
	case "0", "false", "", "null":
		*j = false
	default:
		return fmt.Errorf("invalid bool: %s", string(b))
	}
	return nil
}

type lsblkDevice struct {
	Name       string        `json:"name"`
	Size       jsonUint      `json:"size"`
	Type       string        `json:"type"`
	Tran       string        `json:"tran"`
	RM         jsonBool      `json:"rm"`
	Hotplug    jsonBool      `json:"hotplug"`
	RO         jsonBool      `json:"ro"`
	Model      string        `json:"model"`
	Vendor     string        `json:"vendor"`
	Serial     string        `json:"serial"`
	Mountpoint string        `json:"mountpoint"`
	Children   []lsblkDevice `json:"children"`
}

type lsblkOutput struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

// Columns supported by lsblk for well over a decade; MOUNTPOINT (singular) is
// used instead of MOUNTPOINTS so we don't depend on util-linux >= 2.37.
const lsblkColumns = "NAME,SIZE,TYPE,TRAN,RM,HOTPLUG,RO,MODEL,VENDOR,SERIAL,MOUNTPOINT"

// ---------- public API ----------

func (p *linuxProvider) IsSupported() bool { return true }

func (p *linuxProvider) ListRemovable(ctx context.Context) ([]Drive, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	devs, err := p.lsblk(ctx)
	if err != nil {
		return nil, err
	}
	return drivesFromDevices(devs), nil
}

// drivesFromDevices filters lsblk's block devices down to removable/hotplug USB
// whole disks and maps them to the cross-platform Drive shape. Kept separate
// from the exec so it can be unit-tested against captured lsblk JSON.
func drivesFromDevices(devs []lsblkDevice) []Drive {
	var out []Drive
	for _, d := range devs {
		if !isRemovableUSB(d) {
			continue
		}

		out = append(out, Drive{
			Device:      devPath(d.Name),
			BSDName:     d.Name,
			SizeBytes:   uint64(d.Size),
			Model:       strings.TrimSpace(d.Model),
			Vendor:      strings.TrimSpace(d.Vendor),
			Serial:      strings.TrimSpace(d.Serial),
			Protocol:    strings.ToUpper(d.Tran),
			IsRemovable: bool(d.RM),
			IsEjectable: true,
			Mountpoints: collectMountpoints(d),
		})
	}
	return out
}

// Unmount is a no-op on Linux: the privileged helper unmounts the target
// device's partitions immediately before writing (and opens it O_EXCL), so a
// separate unprivileged unmount here would be redundant.
func (p *linuxProvider) Unmount(ctx context.Context, device string) error {
	return nil
}

// Eject is a no-op on Linux. There is no safe unprivileged "power off the
// device" that we rely on; the user can remove the stick once the write and
// sync (done by the helper) have completed.
func (p *linuxProvider) Eject(ctx context.Context, device string) error {
	return nil
}

func (p *linuxProvider) FindMountPoint(ctx context.Context, device string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	devs, err := p.lsblk(ctx, device)
	if err != nil {
		return "", err
	}
	for _, d := range devs {
		if mps := collectMountpoints(d); len(mps) > 0 {
			return mps[0], nil
		}
	}
	return "", fmt.Errorf("no mount point found for %s", device)
}

func (p *linuxProvider) WaitForMount(ctx context.Context, device string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if mp, err := p.FindMountPoint(ctx, device); err == nil && mp != "" {
			return mp, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout waiting for %s to mount", device)
			}
		}
	}
}

// ---------- helpers ----------

// lsblk runs `lsblk -J -b -o <cols>` and returns the top-level block devices.
// If devices are provided, lsblk is scoped to just those.
func (p *linuxProvider) lsblk(ctx context.Context, devices ...string) ([]lsblkDevice, error) {
	args := append([]string{"-J", "-b", "-o", lsblkColumns}, devices...)
	raw, err := exec.CommandContext(ctx, "lsblk", args...).Output()
	if err != nil {
		return nil, wrapExecErr("lsblk", err)
	}
	return parseLsblk(raw)
}

// parseLsblk decodes the JSON emitted by `lsblk -J`. Split out from the exec so
// it can be unit-tested against both the legacy (string-typed) and modern
// (native-typed) lsblk JSON formats.
func parseLsblk(raw []byte) ([]lsblkDevice, error) {
	var parsed lsblkOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse lsblk output: %w", err)
	}
	return parsed.BlockDevices, nil
}

// isRemovableUSB reports whether a whole disk is a removable/hotplug USB drive
// that is safe to offer as a flash target.
func isRemovableUSB(d lsblkDevice) bool {
	if d.Type != "disk" {
		return false // skip loop, rom, partitions, lvm, etc.
	}
	if bool(d.RO) {
		return false
	}
	if !strings.EqualFold(d.Tran, "usb") {
		return false
	}
	return bool(d.RM) || bool(d.Hotplug)
}

// collectMountpoints gathers all non-empty mountpoints from a device and its
// child partitions.
func collectMountpoints(d lsblkDevice) []string {
	var mps []string
	if d.Mountpoint != "" {
		mps = append(mps, d.Mountpoint)
	}
	for _, c := range d.Children {
		mps = append(mps, collectMountpoints(c)...)
	}
	return mps
}

// devPath maps a kernel device name to its /dev path (sdb -> /dev/sdb,
// nvme0n1 -> /dev/nvme0n1). Avoids relying on lsblk's version-gated PATH column.
func devPath(name string) string {
	if strings.HasPrefix(name, "/dev/") {
		return name
	}
	return "/dev/" + name
}

func wrapExecErr(what string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%s failed: %s", what, strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Errorf("%s error: %w", what, err)
}
