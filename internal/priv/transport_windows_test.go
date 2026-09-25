package priv

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/proto"
)

// squat creates the pipe in this process, as someone who guessed or saw the
// name would, and accepts whatever connects.
func squat(t *testing.T, pipe string) {
	t.Helper()
	ln, err := winio.ListenPipe(pipe, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
}

func processHandle(t *testing.T, pid uint32) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDialRefusesPipeServedByAnotherProcess(t *testing.T) {
	pipe, err := newPipeName()
	if err != nil {
		t.Fatal(err)
	}
	if !helper.PipeNameRe.MatchString(pipe) {
		t.Fatalf("%s is not a name the helper accepts", pipe)
	}
	squat(t, pipe)

	// The "helper" UAC started is our parent; the pipe is served by us.
	ppid := uint32(os.Getppid())
	h := &helperProcess{pid: ppid, process: processHandle(t, ppid)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialHelper(ctx, pipe, h)
	if err == nil {
		conn.Close()
		t.Fatal("connected to a pipe the helper does not serve")
	}
	if !strings.Contains(err.Error(), "not the helper") {
		t.Fatalf("got %v", err)
	}

	// The same pipe is accepted when its server is the expected process.
	self := uint32(os.Getpid())
	h = &helperProcess{pid: self, process: processHandle(t, self)}
	conn, err = dialHelper(ctx, pipe, h)
	if err != nil {
		t.Fatalf("expected server refused: %v", err)
	}
	conn.Close()
}

func TestDialGivesUpWhenTheHelperExits(t *testing.T) {
	pipe, _ := newPipeName()
	h := &helperProcess{pid: 1, exited: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := dialHelper(ctx, pipe, h); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("got %v", err)
	}
}

// The image goes to the helper as this process's handle value.
func TestWriteImageSendsHandle(t *testing.T) {
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cc, sc := net.Pipe()
	defer sc.Close()
	c := NewClient(cc)
	defer c.Close()

	got := make(chan proto.WriteImageParams, 1)
	go func() {
		line, err := bufio.NewReader(sc).ReadBytes('\n')
		if err != nil {
			return
		}
		var req proto.Request
		var p proto.WriteImageParams
		json.Unmarshal(line, &req)
		json.Unmarshal(req.Params, &p)
		got <- p
		resp, _ := proto.ResultResponse(req.ID, nil)
		b, _ := json.Marshal(resp)
		sc.Write(append(b, '\n'))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WriteImage(ctx, proto.WriteImageParams{Device: `\\.\PhysicalDrive9`, Size: 1}, f, nil); err != nil {
		t.Fatal(err)
	}
	if p := <-got; p.Handle != uint64(f.Fd()) || p.Handle == 0 {
		t.Fatalf("sent handle %#x, file is %#x", p.Handle, f.Fd())
	}
}
