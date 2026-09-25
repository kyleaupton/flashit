package main

import (
	"embed"
	_ "embed"
	"log"
	"log/slog"
	"os"
	"regexp"
	"runtime"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/eventbus"
	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/service"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
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

	app.RegisterService(application.NewService(service.NewJobsService()))
	app.RegisterService(application.NewService(service.NewDrivesService()))
	app.RegisterService(application.NewService(service.NewPrivService()))
	app.RegisterService(application.NewService(service.NewSourcesService()))

	if updaterEnabled(runtime.GOOS, Version) {
		if err := setupUpdater(app); err != nil {
			logger.Error("updater disabled", "error", err)
		}
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

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Only a macOS bundle can swap itself in place; Linux lives in root-owned
// /usr/bin. Any other version string sorts below every release tag, so a dev
// build would be offered each one.
func updaterEnabled(goos, version string) bool {
	return goos == "darwin" && releaseVersion.MatchString(version)
}

func setupUpdater(app *application.App) error {
	gh, err := github.New(github.Config{
		Repository:    "kyleaupton/flashit",
		ChecksumAsset: "SHA256SUMS",
	})
	if err != nil {
		return err
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: Version,
		Providers:      []updater.Provider{gh},
	}); err != nil {
		return err
	}
	app.Menu.Set(macMenu(app))

	// Check silently first: CheckAndInstall opens its window even when there
	// is nothing to install.
	go func() {
		time.Sleep(5 * time.Second)
		rel, err := app.Updater.Check(app.Context())
		if err != nil {
			logger.Error("update check failed", "error", err)
			return
		}
		if rel != nil {
			checkAndInstall(app)
		}
	}()
	return nil
}

func checkAndInstall(app *application.App) {
	if err := app.Updater.CheckAndInstall(app.Context()); err != nil {
		logger.Error("update failed", "error", err)
	}
}

// macMenu is the default menu with Check for Updates… in the app menu.
func macMenu(app *application.App) *application.Menu {
	menu := app.NewMenu()
	appMenu := menu.AddSubmenu("FlashIt")
	appMenu.AddRole(application.About)
	appMenu.Add("Check for Updates…").OnClick(func(*application.Context) {
		go checkAndInstall(app)
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
