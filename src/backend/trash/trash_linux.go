//go:build !windows && !darwin

package trash

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Especificación FreeDesktop: ~/.local/share/Trash/{files,info}.
func moveToTrash(abs string) error {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share")
	}
	files := filepath.Join(data, "Trash", "files")
	info := filepath.Join(data, "Trash", "info")
	for _, d := range []string{files, info} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	name := uniqueIn(files, filepath.Base(abs))
	parts := strings.Split(filepath.ToSlash(abs), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	meta := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", strings.Join(parts, "/"), time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(filepath.Join(info, name+".trashinfo"), []byte(meta), 0o600); err != nil {
		return err
	}
	if err := os.Rename(abs, filepath.Join(files, name)); err != nil {
		_ = os.Remove(filepath.Join(info, name+".trashinfo"))
		return err // p. ej. otro disco: mejor fallar que borrar sin poder recuperar
	}
	return nil
}
