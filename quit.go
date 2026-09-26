package main

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/kyleaupton/flashit/internal/jobs"
	"github.com/kyleaupton/flashit/internal/logger"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// stopTimeout bounds how long "Stop and quit" waits for the cancelled job's
// cleanup before quitting anyway.
const stopTimeout = 15 * time.Second

// quitGuard asks before a quit or window close would kill a running job.
type quitGuard struct {
	app    *application.App
	window *application.WebviewWindow
	jobs   *jobs.Manager

	allowed atomic.Bool // set once the user chose to stop, or no job ran
	asking  atomic.Bool
}

// shouldQuit is Wails' ShouldQuit, called on the main thread for every
// quit (menu, ⌘Q, dock, app.Quit), so the question is asked from a
// goroutine and the quit re-issued once the job is gone.
func (g *quitGuard) shouldQuit() bool {
	if g == nil || g.allowed.Load() || !g.jobs.Active() {
		return true
	}
	go g.ask()
	return false
}

func (g *quitGuard) onClose(e *application.WindowEvent) {
	if g.allowed.Load() || !g.jobs.Active() {
		return
	}
	e.Cancel()
	go g.ask()
}

func (g *quitGuard) ask() {
	if !g.asking.CompareAndSwap(false, true) {
		return
	}
	drive := "The drive"
	if p := g.jobs.ActivePlan(); p != nil && p.Drive != "" {
		drive = p.Drive
	}
	stopLabel, keepLabel, message := "Stop and quit", "Keep flashing", drive+" won't be bootable."
	if runtime.GOOS == "windows" {
		// Wails shows a Windows question as a Yes/No MessageBox and only
		// calls back buttons labelled exactly that.
		stopLabel, keepLabel = "Yes", "No"
		message = "Stop flashing and quit? " + message
	}
	d := g.app.Dialog.Question().
		SetTitle("Stop flashing?").
		SetMessage(message).
		AttachToWindow(g.window)
	// macOS lays buttons out right to left in the order added.
	stop := d.AddButton(stopLabel)
	keep := d.AddButton(keepLabel)
	d.SetDefaultButton(keep)
	d.SetCancelButton(keep)
	keep.OnClick(func() { g.asking.Store(false) })
	stop.OnClick(func() { go g.stopAndQuit() })
	d.Show()
}

func (g *quitGuard) stopAndQuit() {
	select {
	case <-g.jobs.CancelActive():
	case <-time.After(stopTimeout):
		logger.Warn("quitting before the cancelled job finished its cleanup")
	}
	g.allowed.Store(true)
	g.app.Quit()
}

// quitKeyBinding gives Windows and Linux the Ctrl+Q that the macOS app menu
// already has.
func quitKeyBinding(app *application.App) map[string]func(application.Window) {
	if runtime.GOOS == "darwin" {
		return nil
	}
	return map[string]func(application.Window){
		"CmdOrCtrl+Q": func(application.Window) { app.Quit() },
	}
}
