package backend

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// Run abre la ventana de Wails con la interfaz (assets) y los métodos de App disponibles para Vue.
func Run(assets embed.FS) error {
	app := NewApp()
	return wails.Run(&options.App{
		Title:     "Cyberia Library Management System",
		Width:     1180,
		Height:    780,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 18, B: 26, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
	})
}
