package proto

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func roundTrip[T any](t *testing.T, in T) T {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return out
}

func TestRequestRoundTrip(t *testing.T) {
	params := []any{
		nil,
		PingParams{Protocol: ProtocolVersion},
		WriteImageParams{Device: "/dev/sdb", Source: "/tmp/x.iso", Size: 3145728000},
		WriteImageParams{Device: "/dev/disk4", Source: "/tmp/x.iso", Size: 1, Authorization: "AAAA"},
		FormatDiskParams{Device: "/dev/sdb", Filesystem: "fat32", Label: "FLASHIT"},
		FormatDiskParams{Device: "/dev/disk4", Filesystem: "fat32", Label: "FLASHIT", Authorization: "AAAA"},
		MountISOParams{Path: "/tmp/x.iso"},
		UnmountParams{Mountpoint: "/run/media/flashit/FLASHIT"},
		UnmountParams{Device: "/dev/sdb"},
		EjectParams{Device: "/dev/sdb"},
	}
	ops := []Op{OpPing, OpPing, OpWriteImage, OpWriteImage, OpFormatDisk, OpFormatDisk, OpMountISO, OpUnmount, OpUnmount, OpEject}

	for i, p := range params {
		req, err := NewRequest("7", ops[i], p)
		if err != nil {
			t.Fatalf("NewRequest(%v): %v", p, err)
		}
		got := roundTrip(t, req)
		if got.ID != "7" || got.Op != ops[i] {
			t.Fatalf("envelope changed: %+v", got)
		}
		if p == nil {
			if len(got.Params) != 0 {
				t.Fatalf("expected empty params, got %s", got.Params)
			}
			continue
		}
		back := reflect.New(reflect.TypeOf(p))
		if err := json.Unmarshal(got.Params, back.Interface()); err != nil {
			t.Fatalf("decode params: %v", err)
		}
		if !reflect.DeepEqual(back.Elem().Interface(), p) {
			t.Fatalf("params changed: got %+v want %+v", back.Elem().Interface(), p)
		}
	}
}

func TestRequestFieldNames(t *testing.T) {
	req, _ := NewRequest("7", OpWriteImage, WriteImageParams{Device: "/dev/sdb", Source: "/tmp/x.iso", Size: 1})
	raw, _ := json.Marshal(req)
	want := `{"id":"7","op":"write_image","params":{"device":"/dev/sdb","source":"/tmp/x.iso","size":1}}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
	req, _ = NewRequest("8", OpFormatDisk, FormatDiskParams{Device: "/dev/disk4", Filesystem: "fat32", Label: "X", Authorization: "AAAA"})
	raw, _ = json.Marshal(req)
	want = `{"id":"8","op":"format_disk","params":{"device":"/dev/disk4","filesystem":"fat32","label":"X","authorization":"AAAA"}}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
}

func TestProgressRoundTrip(t *testing.T) {
	got := roundTrip(t, ProgressResponse("7", 1048576, 3145728000))
	want := Response{ID: "7", Type: TypeProgress, Written: 1048576, Total: 3145728000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if got.Err() != nil {
		t.Fatal("progress must not decode as an error")
	}
}

func TestResultRoundTrip(t *testing.T) {
	results := []any{
		nil,
		PingResult{Protocol: ProtocolVersion, Version: "0.1.0", EUID: 0},
		FormatDiskResult{Mountpoint: "/run/media/flashit/FLASHIT"},
		MountISOResult{Mountpoint: "/run/media/flashit/iso"},
	}
	for _, r := range results {
		resp, err := ResultResponse("7", r)
		if err != nil {
			t.Fatalf("ResultResponse(%v): %v", r, err)
		}
		got := roundTrip(t, resp)
		if got.ID != "7" || got.Type != TypeResult || got.Err() != nil {
			t.Fatalf("envelope changed: %+v", got)
		}
		if r == nil {
			if len(got.Data) != 0 {
				t.Fatalf("expected empty data, got %s", got.Data)
			}
			continue
		}
		back := reflect.New(reflect.TypeOf(r))
		if err := json.Unmarshal(got.Data, back.Interface()); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		if !reflect.DeepEqual(back.Elem().Interface(), r) {
			t.Fatalf("data changed: got %+v want %+v", back.Elem().Interface(), r)
		}
	}
}

func TestResultFieldNames(t *testing.T) {
	resp, _ := ResultResponse("7", PingResult{Protocol: 2, Version: "0.1.0", EUID: 0})
	raw, _ := json.Marshal(resp)
	want := `{"id":"7","type":"result","data":{"protocol":2,"version":"0.1.0","euid":0}}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
}

func TestErrorRoundTrip(t *testing.T) {
	codes := []ErrorCode{
		CodeInvalidRequest, CodeVersionMismatch, CodeBusy, CodeInvalidDevice,
		CodeNotRemovable, CodeSystemDisk, CodeDeviceBusy, CodeInvalidSource,
		CodeSizeMismatch, CodeInsufficientCapacity, CodeInvalidLabel,
		CodeCancelled, CodeUnauthorized, CodeInternal,
	}
	for _, code := range codes {
		got := roundTrip(t, ErrorResponse("7", NewError(code, "refusing to write to /dev/nvme0n1")))
		if got.Type != TypeError || got.Code != code {
			t.Fatalf("code %s changed: %+v", code, got)
		}
		e := got.Err()
		if e == nil || e.Code != code || e.Message != "refusing to write to /dev/nvme0n1" {
			t.Fatalf("Err() lost data: %+v", e)
		}
		var target *Error
		if !errors.As(e, &target) {
			t.Fatal("Error must be usable with errors.As")
		}
		if !strings.Contains(e.Error(), string(code)) {
			t.Fatalf("Error() should mention the code: %q", e.Error())
		}
	}
}

func TestErrorFieldNames(t *testing.T) {
	raw, _ := json.Marshal(ErrorResponse("7", Errorf(CodeSystemDisk, "refusing to write to %s", "/dev/nvme0n1")))
	want := `{"id":"7","type":"error","code":"system_disk","message":"refusing to write to /dev/nvme0n1"}`
	if string(raw) != want {
		t.Fatalf("got %s\nwant %s", raw, want)
	}
}

func TestDecodeWireExamples(t *testing.T) {
	lines := []string{
		`{"id":"7","op":"write_image","params":{"device":"/dev/sdb","source":"/tmp/x.iso","size":3145728000}}`,
	}
	var req Request
	if err := json.Unmarshal([]byte(lines[0]), &req); err != nil {
		t.Fatal(err)
	}
	var p WriteImageParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Size != 3145728000 || p.Device != "/dev/sdb" {
		t.Fatalf("decoded %+v", p)
	}

	var resp Response
	if err := json.Unmarshal([]byte(`{"id":"7","type":"error","code":"system_disk","message":"refusing to write to /dev/nvme0n1"}`), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Err().Code != CodeSystemDisk {
		t.Fatalf("decoded %+v", resp)
	}
}
