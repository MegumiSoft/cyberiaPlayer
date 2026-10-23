// Package trash envía archivos a la papelera del sistema (recuperables), nunca los borra de forma definitiva.
package trash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Move manda un archivo regular a la papelera del sistema operativo.
func Move(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("solo se pueden enviar archivos a la papelera")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return moveToTrash(abs)
}

// uniqueIn devuelve un nombre libre dentro de dir ("a.mp3", "a 2.mp3", "a 3.mp3"…).
func uniqueIn(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	cand := name
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(dir, cand)); os.IsNotExist(err) {
			return cand
		}
		cand = fmt.Sprintf("%s %d%s", stem, i, ext)
	}
}
