// Cyberia Library Management System: punto de entrada de Wails (debe estar en la raíz del proyecto).
// Toda la lógica está en src/backend (Go) y la interfaz en src/frontend (Vue 3).
package main

import (
	"embed"
	"log"

	"musicdl/src/backend"
)

//go:embed all:src/frontend/dist
var assets embed.FS

func main() {
	if err := backend.Run(assets); err != nil {
		log.Fatal(err)
	}
}
