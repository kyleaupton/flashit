//go:build windows

package drives

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type windowsProvider struct{}

func platformProvider() Provider { return &windowsProvider{} }

// ---------- PowerShell JSON models ----------

type psDisk struct {
	Number       int      `json:"Number"`
	Model        string   `json:"Model"`
	SerialNumber string   `json:"SerialNumber"`
	Size         uint64   `json:"Size"`
	BusType      string   `json:"BusType"`
	IsRemovable  bool     `json:"IsRemovable"`
	Mountpoints  []string `json:"Mountpoints"`
}

// ---------- public API ----------

func (p *windowsProvider) IsSupported() bool { return true }

func (p *windowsProvider) ListRemovable(ctx context.Context) ([]Drive, error) {
	// Ensure timeout
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	// The helper's gate is the real check; this keeps the list to what it
	// accepts: USB, SD and MMC disks that neither boot nor hold Windows.
	// BusType is matched as a name and as its STORAGE_BUS_TYPE number, in
	// case it comes back as either.
	script := `
$result = @()
Get-Disk | Where-Object { @("USB", "SD", "MMC", "7", "12", "13") -contains [string]$_.BusType -and -not $_.IsBoot -and -not $_.IsSystem } | ForEach-Object {
    $disk = $_
    $mountpoints = @()
    Get-Partition -DiskNumber $disk.Number -ErrorAction SilentlyContinue | ForEach-Object {
        $vol = Get-Volume -Partition $_ -ErrorAction SilentlyContinue
        if ($vol.DriveLetter) { $mountpoints += "$($vol.DriveLetter):\" }
    }
    $result += [PSCustomObject]@{
        Number = $disk.Number
        Model = $disk.Model
        SerialNumber = $disk.SerialNumber
        Size = $disk.Size
        BusType = [string]$disk.BusType
        IsRemovable = $true
        Mountpoints = $mountpoints
    }
}
$result | ConvertTo-Json -Depth 3 -Compress
`

	out, err := p.runPS(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("ListRemovable: %w", err)
	}

	// Empty output means no removable disks
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return []Drive{}, nil
	}

	// Parse JSON - handle both array and single object cases
	var disks []psDisk
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &disks); err != nil {
			return nil, fmt.Errorf("parse disk list: %w", err)
		}
	} else {
		// Single disk returns as object, not array
		var single psDisk
		if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
			return nil, fmt.Errorf("parse single disk: %w", err)
		}
		disks = []psDisk{single}
	}

	// Convert to Drive structs
	var drives []Drive
	for _, d := range disks {
		drv := Drive{
			Device:      fmt.Sprintf(`\\.\PhysicalDrive%d`, d.Number),
			BSDName:     strconv.Itoa(d.Number),
			SizeBytes:   d.Size,
			Model:       strings.TrimSpace(d.Model),
			Serial:      strings.TrimSpace(d.SerialNumber),
			Protocol:    d.BusType,
			IsRemovable: true,
			IsEjectable: true,
			Mountpoints: d.Mountpoints,
		}
		drives = append(drives, drv)
	}

	return drives, nil
}

func (p *windowsProvider) Unmount(ctx context.Context, device string) error {
	diskNum, err := p.diskNumberFromDevice(device)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`
Get-Partition -DiskNumber %d -ErrorAction SilentlyContinue | ForEach-Object {
    $vol = Get-Volume -Partition $_ -ErrorAction SilentlyContinue
    if ($vol.DriveLetter) {
        $cimVol = Get-CimInstance Win32_Volume | Where-Object {$_.DriveLetter -eq "$($vol.DriveLetter):"}
        if ($cimVol) {
            Invoke-CimMethod -InputObject $cimVol -MethodName Dismount -Arguments @{Force=$true;Permanent=$false} | Out-Null
        }
    }
}
`, diskNum)

	_, err = p.runPS(ctx, script)
	if err != nil {
		return fmt.Errorf("unmount %s: %w", device, err)
	}
	return nil
}

func (p *windowsProvider) Eject(ctx context.Context, device string) error {
	diskNum, err := p.diskNumberFromDevice(device)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`
$drive = (Get-Partition -DiskNumber %d -ErrorAction SilentlyContinue | Get-Volume -ErrorAction SilentlyContinue | Where-Object {$_.DriveLetter} | Select-Object -First 1).DriveLetter
if ($drive) {
    (New-Object -ComObject Shell.Application).Namespace(17).ParseName("$drive" + ":").InvokeVerb("Eject")
}
`, diskNum)

	_, err = p.runPS(ctx, script)
	if err != nil {
		return fmt.Errorf("eject %s: %w", device, err)
	}
	return nil
}

func (p *windowsProvider) FindMountPoint(ctx context.Context, device string) (string, error) {
	diskNum, err := p.diskNumberFromDevice(device)
	if err != nil {
		return "", err
	}

	script := fmt.Sprintf(`
(Get-Partition -DiskNumber %d -ErrorAction SilentlyContinue | Get-Volume -ErrorAction SilentlyContinue | Where-Object {$_.DriveLetter} | Select-Object -First 1).DriveLetter
`, diskNum)

	out, err := p.runPS(ctx, script)
	if err != nil {
		return "", fmt.Errorf("find mount point for %s: %w", device, err)
	}

	letter := strings.TrimSpace(string(out))
	if letter == "" {
		return "", fmt.Errorf("no mount point found for %s", device)
	}

	return letter + `:\`, nil
}

func (p *windowsProvider) WaitForMount(ctx context.Context, device string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		if mp, err := p.FindMountPoint(ctx, device); err == nil && mp != "" {
			return mp, nil
		}

		time.Sleep(pollInterval)
	}

	return "", fmt.Errorf("timeout waiting for %s to mount", device)
}

// ---------- helpers ----------

// runPS executes a PowerShell script and returns stdout.
func (p *windowsProvider) runPS(ctx context.Context, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-Command", script,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, wrapExecErr("powershell", err, out)
	}
	return out, nil
}

// diskNumberFromDevice extracts the disk number from a device path like \\.\PhysicalDrive2
func (p *windowsProvider) diskNumberFromDevice(device string) (int, error) {
	// Match \\.\PhysicalDrive<N> or just a number
	re := regexp.MustCompile(`(?i)PhysicalDrive(\d+)$|^(\d+)$`)
	matches := re.FindStringSubmatch(device)
	if matches == nil {
		return 0, fmt.Errorf("invalid device path: %s", device)
	}

	numStr := matches[1]
	if numStr == "" {
		numStr = matches[2]
	}

	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid disk number in %s: %w", device, err)
	}
	return num, nil
}

func wrapExecErr(what string, err error, output []byte) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		stderr := strings.TrimSpace(string(ee.Stderr))
		if stderr == "" && len(output) > 0 {
			stderr = strings.TrimSpace(string(output))
		}
		if stderr != "" {
			return fmt.Errorf("%s failed: %s", what, stderr)
		}
	}
	return fmt.Errorf("%s error: %w", what, err)
}
