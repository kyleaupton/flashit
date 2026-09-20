package iso

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// createTestFile creates a temporary file with specific bytes at specific offsets.
func createTestFile(t *testing.T, size int64, patches map[int64][]byte) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.iso")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer f.Close()

	// Create file of specified size
	if err := f.Truncate(size); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	// Apply patches
	for offset, data := range patches {
		if _, err := f.WriteAt(data, offset); err != nil {
			t.Fatalf("failed to write patch at offset %d: %v", offset, err)
		}
	}

	return path
}

func TestInspect_ValidHybridISO(t *testing.T) {
	// Create a valid hybrid ISO: ISO 9660 signature + MBR boot signature
	patches := map[int64][]byte{
		pvdOffset:              {0x01},                                     // Volume descriptor type
		iso9660SignatureOffset: []byte("CD001"),                            // ISO 9660 signature
		volumeLabelOffset:      []byte("UBUNTU 24.04                    "), // Volume label (32 bytes, space-padded)
		mbrSignatureOffset:     {0x55, 0xAA},                               // MBR boot signature
	}
	path := createTestFile(t, 40000, patches)

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if !info.IsISO9660 {
		t.Error("expected IsISO9660 to be true")
	}
	if !info.IsHybrid {
		t.Error("expected IsHybrid to be true")
	}
	if info.VolumeLabel != "UBUNTU 24.04" {
		t.Errorf("expected volume label 'UBUNTU 24.04', got %q", info.VolumeLabel)
	}
	if info.Size != 40000 {
		t.Errorf("expected size 40000, got %d", info.Size)
	}
}

func TestInspect_NonHybridISO(t *testing.T) {
	// Create a valid ISO 9660 but without MBR signature (non-hybrid)
	patches := map[int64][]byte{
		pvdOffset:              {0x01},
		iso9660SignatureOffset: []byte("CD001"),
		volumeLabelOffset:      []byte("DATADISC                        "),
		// No MBR signature - bytes 510-511 will be zeros
	}
	path := createTestFile(t, 40000, patches)

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if !info.IsISO9660 {
		t.Error("expected IsISO9660 to be true")
	}
	if info.IsHybrid {
		t.Error("expected IsHybrid to be false")
	}
}

func TestInspect_NotISO9660(t *testing.T) {
	// Create a file without the CD001 signature
	patches := map[int64][]byte{
		iso9660SignatureOffset: []byte("XXXXX"), // Invalid signature
		mbrSignatureOffset:     {0x55, 0xAA},
	}
	path := createTestFile(t, 40000, patches)

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if info.IsISO9660 {
		t.Error("expected IsISO9660 to be false")
	}
}

func TestInspect_FileTooSmall(t *testing.T) {
	// Create a file too small to contain the PVD
	path := createTestFile(t, 100, nil)

	_, err := Inspect(path)
	if !errors.Is(err, ErrFileTooSmall) {
		t.Errorf("expected ErrFileTooSmall, got %v", err)
	}
}

func TestInspect_FileNotFound(t *testing.T) {
	_, err := Inspect("/nonexistent/path/file.iso")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestValidateHybrid_Valid(t *testing.T) {
	patches := map[int64][]byte{
		iso9660SignatureOffset: []byte("CD001"),
		mbrSignatureOffset:     {0x55, 0xAA},
	}
	path := createTestFile(t, 40000, patches)

	err := ValidateHybrid(path)
	if err != nil {
		t.Errorf("ValidateHybrid failed: %v", err)
	}
}

func TestValidateHybrid_NotISO9660(t *testing.T) {
	patches := map[int64][]byte{
		iso9660SignatureOffset: []byte("XXXXX"),
		mbrSignatureOffset:     {0x55, 0xAA},
	}
	path := createTestFile(t, 40000, patches)

	err := ValidateHybrid(path)
	if !errors.Is(err, ErrNotISO9660) {
		t.Errorf("expected ErrNotISO9660, got %v", err)
	}
}

func TestValidateHybrid_NotHybrid(t *testing.T) {
	patches := map[int64][]byte{
		iso9660SignatureOffset: []byte("CD001"),
		// No MBR signature
	}
	path := createTestFile(t, 40000, patches)

	err := ValidateHybrid(path)
	if !errors.Is(err, ErrNotHybrid) {
		t.Errorf("expected ErrNotHybrid, got %v", err)
	}
}

func TestValidateHybrid_TooSmall(t *testing.T) {
	path := createTestFile(t, 100, nil)

	err := ValidateHybrid(path)
	if !errors.Is(err, ErrFileTooSmall) {
		t.Errorf("expected ErrFileTooSmall, got %v", err)
	}
}
