package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
)

const (
	writeBufferSize  = 1024 * 1024      // 1MB write chunks
	progressInterval = 10 * 1024 * 1024  // report progress every 10MB
)

var cancelFlag atomic.Bool

func cancelCurrentOperation() {
	cancelFlag.Store(true)
}

func resetCancelFlag() {
	cancelFlag.Store(false)
}

func isCancelled() bool {
	return cancelFlag.Load()
}

// getSystemDisk resolves the root filesystem's underlying block device.
// Returns the parent disk (e.g., "/dev/sda" for "/dev/sda1").
func getSystemDisk() (string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", fmt.Errorf("open /proc/mounts: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		device, mountpoint := fields[0], fields[1]
		if mountpoint == "/" && strings.HasPrefix(device, "/dev/") {
			// Resolve symlinks (e.g., /dev/mapper/* -> /dev/dm-0)
			resolved, err := filepath.EvalSymlinks(device)
			if err != nil {
				resolved = device
			}
			return stripPartition(resolved), nil
		}
	}

	return "", fmt.Errorf("could not determine system disk from /proc/mounts")
}

// stripPartition removes the partition suffix from a device path.
// "/dev/sda1"      -> "/dev/sda"
// "/dev/nvme0n1p1" -> "/dev/nvme0n1"
// "/dev/mmcblk0p1" -> "/dev/mmcblk0"
func stripPartition(device string) string {
	base := filepath.Base(device)

	// NVMe and mmcblk: partition is "pN" suffix after a digit
	// e.g., nvme0n1p1, mmcblk0p1
	if strings.HasPrefix(base, "nvme") || strings.HasPrefix(base, "mmcblk") {
		if idx := strings.LastIndex(base, "p"); idx > 0 {
			// Make sure everything after 'p' is digits
			suffix := base[idx+1:]
			if len(suffix) > 0 && isDigits(suffix) {
				return filepath.Join(filepath.Dir(device), base[:idx])
			}
		}
		return device
	}

	// Standard sd/vd/hd devices: strip trailing digits
	// e.g., sda1 -> sda, vdb2 -> vdb
	trimmed := strings.TrimRight(base, "0123456789")
	if trimmed != base && len(trimmed) > 0 {
		return filepath.Join(filepath.Dir(device), trimmed)
	}

	return device
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isSystemDisk checks whether the given device is the system disk.
func isSystemDisk(device string) bool {
	sysDisk, err := getSystemDisk()
	if err != nil {
		// If we can't determine the system disk, err on the side of caution
		log.Printf("WARNING: could not determine system disk: %v — blocking operation for safety", err)
		return true
	}

	// Resolve symlinks on the target device too
	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		resolved = device
	}

	target := stripPartition(resolved)
	return target == sysDisk
}

// unmountPartitions unmounts all mounted partitions of the given device.
func unmountPartitions(device string) error {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return fmt.Errorf("open /proc/mounts: %w", err)
	}
	defer f.Close()

	devBase := filepath.Base(device)

	var toUnmount []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		mountedDev, mountpoint := fields[0], fields[1]
		mountedBase := filepath.Base(mountedDev)

		// Match partitions of this device:
		// For /dev/sdb: match sdb, sdb1, sdb2, ...
		// For /dev/nvme0n1: match nvme0n1, nvme0n1p1, nvme0n1p2, ...
		if mountedBase == devBase || strings.HasPrefix(mountedBase, devBase) {
			// For nvme/mmcblk, partition suffix starts with 'p'
			// For sd/vd/hd, partition suffix is just digits
			suffix := strings.TrimPrefix(mountedBase, devBase)
			if suffix == "" || isPartitionSuffix(devBase, suffix) {
				toUnmount = append(toUnmount, mountpoint)
			}
		}
	}

	if len(toUnmount) == 0 {
		log.Printf("no mounted partitions found for %s", device)
		return nil
	}

	for _, mp := range toUnmount {
		log.Printf("unmounting %s", mp)
		if err := syscall.Unmount(mp, 0); err != nil {
			// Try lazy unmount as fallback
			if err := syscall.Unmount(mp, 2); err != nil { // MNT_DETACH = 2
				return fmt.Errorf("failed to unmount %s: %w", mp, err)
			}
		}
	}

	return nil
}

// isPartitionSuffix checks if suffix is a valid partition suffix for the given base device.
func isPartitionSuffix(devBase, suffix string) bool {
	if strings.HasPrefix(devBase, "nvme") || strings.HasPrefix(devBase, "mmcblk") {
		// Partitions look like: nvme0n1p1, mmcblk0p1
		return strings.HasPrefix(suffix, "p") && len(suffix) > 1 && isDigits(suffix[1:])
	}
	// Standard devices: partitions are just digits (sdb1, sdc2)
	return isDigits(suffix)
}

// partitionPath returns the first partition path for a device.
// /dev/sdb -> /dev/sdb1, /dev/nvme0n1 -> /dev/nvme0n1p1
func partitionPath(device string, partNum int) string {
	base := filepath.Base(device)
	if strings.HasPrefix(base, "nvme") || strings.HasPrefix(base, "mmcblk") {
		return fmt.Sprintf("%sp%d", device, partNum)
	}
	return fmt.Sprintf("%s%d", device, partNum)
}

func writeISO(sess *session, req *Request) {
	resetCancelFlag()

	device := req.Device
	isoPath := req.ISOPath

	// Validate device path
	if !strings.HasPrefix(device, "/dev/") {
		sess.sendError(req.ID, fmt.Sprintf("invalid device path: %s", device))
		return
	}

	// Safety: refuse to write to system disk
	if isSystemDisk(device) {
		log.Printf("BLOCKED: refusing to write to system disk %s", device)
		sess.sendError(req.ID, fmt.Sprintf("refusing to write to %s (system disk)", device))
		return
	}

	// Unmount all partitions on the target device
	log.Printf("unmounting partitions on %s", device)
	if err := unmountPartitions(device); err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to unmount: %v", err))
		return
	}

	// Open the source ISO
	src, err := os.Open(isoPath)
	if err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to open ISO: %v", err))
		return
	}
	defer src.Close()

	stat, err := src.Stat()
	if err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to stat ISO: %v", err))
		return
	}
	totalBytes := uint64(stat.Size())

	// Open the target device for raw writing
	dst, err := os.OpenFile(device, os.O_WRONLY|syscall.O_EXCL, 0)
	if err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to open device %s: %v", device, err))
		return
	}
	defer dst.Close()

	log.Printf("writing %s (%d bytes) to %s", isoPath, totalBytes, device)

	buf := make([]byte, writeBufferSize)
	var bytesWritten uint64
	var lastReport uint64

	for {
		if isCancelled() {
			log.Printf("write cancelled at %d bytes", bytesWritten)
			sess.sendError(req.ID, "cancelled: operation was cancelled")
			return
		}

		n, err := src.Read(buf)
		if n > 0 {
			written, werr := dst.Write(buf[:n])
			if werr != nil {
				sess.sendError(req.ID, fmt.Sprintf("write error at %d bytes: %v", bytesWritten, werr))
				return
			}
			bytesWritten += uint64(written)

			if bytesWritten-lastReport >= progressInterval {
				sess.sendProgress(req.ID, bytesWritten, totalBytes)
				lastReport = bytesWritten
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			sess.sendError(req.ID, fmt.Sprintf("read error at %d bytes: %v", bytesWritten, err))
			return
		}
	}

	// Ensure all data is flushed to disk
	if err := dst.Sync(); err != nil {
		sess.sendError(req.ID, fmt.Sprintf("sync error: %v", err))
		return
	}
	syscall.Sync()

	// Send final progress
	sess.sendProgress(req.ID, bytesWritten, totalBytes)

	log.Printf("write complete: %d bytes written to %s", bytesWritten, device)
	sess.sendResult(req.ID, true)
}

func formatDisk(sess *session, req *Request) {
	resetCancelFlag()

	device := req.Device
	filesystem := strings.ToUpper(req.Filesystem)
	volumeName := req.VolumeName

	// Validate device path
	if !strings.HasPrefix(device, "/dev/") {
		sess.sendError(req.ID, fmt.Sprintf("invalid device path: %s", device))
		return
	}

	// Safety: refuse to format system disk
	if isSystemDisk(device) {
		log.Printf("BLOCKED: refusing to format system disk %s", device)
		sess.sendError(req.ID, fmt.Sprintf("refusing to format %s (system disk)", device))
		return
	}

	// Unmount all partitions
	if err := unmountPartitions(device); err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to unmount: %v", err))
		return
	}

	if isCancelled() {
		sess.sendError(req.ID, "cancelled: operation was cancelled")
		return
	}

	// Determine partition table type
	tableType := "gpt"
	if filesystem == "FAT32" || filesystem == "EXFAT" {
		tableType = "msdos" // MBR — better Windows/BIOS compatibility
	}

	// Create partition table
	log.Printf("creating %s partition table on %s", tableType, device)
	if out, err := runCommand("parted", "--script", device, "mklabel", tableType); err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to create partition table: %v\n%s", err, out))
		return
	}

	if isCancelled() {
		sess.sendError(req.ID, "cancelled: operation was cancelled")
		return
	}

	// Create a single partition spanning the full disk
	log.Printf("creating partition on %s", device)
	if out, err := runCommand("parted", "--script", "--align", "optimal", device, "mkpart", "primary", "1MiB", "100%"); err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to create partition: %v\n%s", err, out))
		return
	}

	partition := partitionPath(device, 1)

	// Wait briefly for the kernel to recognize the new partition
	runCommand("partprobe", device)
	runCommand("udevadm", "settle", "--timeout=5")

	if isCancelled() {
		sess.sendError(req.ID, "cancelled: operation was cancelled")
		return
	}

	// Format the partition
	log.Printf("formatting %s as %s (label: %s)", partition, filesystem, volumeName)
	var out string
	var err error
	switch filesystem {
	case "FAT32":
		out, err = runCommand("mkfs.vfat", "-F", "32", "-n", volumeName, partition)
	case "EXFAT":
		out, err = runCommand("mkfs.exfat", "-n", volumeName, partition)
	case "NTFS":
		out, err = runCommand("mkfs.ntfs", "--fast", "-L", volumeName, partition)
	default:
		sess.sendError(req.ID, fmt.Sprintf("unsupported filesystem: %s", filesystem))
		return
	}

	if err != nil {
		sess.sendError(req.ID, fmt.Sprintf("failed to format partition: %v\n%s", err, out))
		return
	}

	log.Printf("format complete: %s -> %s (%s)", partition, filesystem, volumeName)
	sess.sendResult(req.ID, true)
}

// runCommand executes a command and returns combined stdout+stderr output.
func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
