package main

import (
	"embed"
	_ "embed"
	"log"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/eventbus"
	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/service"
	"github.com/kyleaupton/flashit/internal/update"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Check for dry-run mode via environment variable
	if os.Getenv("DRY_RUN") == "1" || os.Getenv("DRY_RUN") == "true" {
		core.DryRun = true
		log.Println("DRY-RUN MODE: Using mock drives, no real disk operations")
		drives.SetProvider(drives.MockProvider{Drives: drives.DefaultMockDrives()})
	}

	app := application.New(application.Options{
		Name:        "FlashIt",
		Description: "Create bootable USB OS installers",
		LogLevel:    slog.LevelInfo,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// Also the Wayland app_id, which is how the shell matches the window
		// to dev.kyleupton.flashit.desktop and its icon.
		Linux: application.LinuxOptions{
			ApplicationID: "dev.kyleupton.flashit",
		},
	})

	// Use Wails' logger for unified log format
	logger.SetLogger(app.Logger)
	logger.Info("FlashIt starting", "version", Version)

	// Set global event emitter for backend modules
	eventbus.SetEmitter(func(name string, data any) { app.Event.Emit(name, data) })

	jobsSvc := service.NewJobsService()
	app.RegisterService(application.NewService(jobsSvc))
	drivesSvc := service.NewDrivesService()
	app.RegisterService(application.NewService(drivesSvc))
	app.RegisterService(application.NewService(service.NewPrivService()))
	app.RegisterService(application.NewService(service.NewSourcesService()))

	updates := setupUpdates(app, service.NewJobGate(jobsSvc))
	app.RegisterService(application.NewService(service.NewUpdateService(app.Context(), updates, app.Browser.OpenURL)))
	go updates.Run(app.Context(), 10*time.Second, 6*time.Hour)
	if runtime.GOOS == "darwin" && updates.Mode() != update.Off {
		app.Menu.Set(macMenu(app))
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:          "FlashIt",
		Width:          800,
		Height:         500,
		DisableResize:  true,
		EnableFileDrop: true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(27, 38, 54),
		URL:              "/",
	})

	// Wails delivers dropped files to Go listeners only; the frontend gets
	// them through this relay.
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(ev *application.WindowEvent) {
		app.Event.Emit("files:dropped", map[string]any{"files": ev.Context().DroppedFiles()})
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func setupUpdates(app *application.App, gate update.JobGate) *update.Manager {
	manifestURL, artifactURL, publicKey := updateSource()
	var observe func(update.State)
	mgr, err := update.New(app.Updater, update.Config{
		GOOS:        runtime.GOOS,
		Version:     Version,
		ManifestURL: manifestURL,
		ArtifactURL: artifactURL,
		PublicKey:   publicKey,
		Gate:        gate,
		Emit: func(s update.State) {
			app.Event.Emit("update:state", s)
			if observe != nil {
				observe(s)
			}
		},
	})
	if err != nil {
		logger.Error("updater disabled", "error", err)
		return update.NewManager(update.ManagerConfig{Mode: update.Off, Current: Version})
	}
	observe = harnessHooks(mgr)
	return mgr
}

// macMenu is the default menu with Check for Updates… in the app menu. The
// frontend runs the check so it can say "up to date" or show the error.
func macMenu(app *application.App) *application.Menu {
	menu := app.NewMenu()
	appMenu := menu.AddSubmenu("FlashIt")
	appMenu.AddRole(application.About)
	appMenu.Add("Check for Updates…").OnClick(func(*application.Context) {
		app.Event.Emit("update:check-requested")
	})
	appMenu.AddSeparator()
	appMenu.AddRole(application.ServicesMenu)
	appMenu.AddSeparator()
	appMenu.AddRole(application.Hide)
	appMenu.AddRole(application.HideOthers)
	appMenu.AddRole(application.UnHide)
	appMenu.AddSeparator()
	appMenu.AddRole(application.Quit)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	return menu
}
