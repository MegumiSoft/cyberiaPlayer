//go:build darwin

package trash

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func moveToTrash(abs string) error {
	// Finder: es lo más fiable (permite «Poner de nuevo»). La primera vez macOS pide permiso de Automatización.
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(abs)
	script := `tell application "Finder" to delete (POSIX file "` + esc + `" as alias)`
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err == nil {
		if _, serr := os.Lstat(abs); os.IsNotExist(serr) {
			return nil
		}
	} else {
		_ = out
	}
	// Respaldo: mover directamente a ~/.Trash.
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".Trash")
	if _, err := os.Stat(dir); err != nil {
		return errors.New("no se pudo acceder a la papelera")
	}
	return os.Rename(abs, filepath.Join(dir, uniqueIn(dir, filepath.Base(abs))))
}
