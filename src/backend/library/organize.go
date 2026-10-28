package library

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// cleanFolderName deja un nombre usable como carpeta en Windows y macOS (una sola carpeta, sin rutas).
func cleanFolderName(name string) (string, error) {
	var b strings.Builder
	for _, r := range name {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r), unicode.IsControl(r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	if out == "" || out == RootName || ignorable(out) {
		return "", errors.New("nombre de playlist no válido")
	}
	if r := []rune(out); len(r) > 120 {
		out = strings.TrimRight(string(r[:120]), ". ")
	}
	return out, nil
}

// CreateFolder crea una playlist nueva (carpeta) dentro de la biblioteca y devuelve su ruta relativa.
func CreateFolder(root, name string) (string, error) {
	if root == "" {
		return "", errors.New("primero elige la carpeta de la biblioteca")
	}
	clean, err := cleanFolderName(name)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", errors.New("la carpeta de la biblioteca no existe")
	}
	full := filepath.Join(root, clean)
	if err := os.Mkdir(full, 0o755); err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("ya existe una playlist llamada «%s»", clean)
		}
		return "", err
	}
	return clean, nil
}

// trackPaths resuelve lo común de mover y copiar: la canción de origen (ResolveFile: dentro de la raíz, solo audio y
// archivo regular, nunca una carpeta ni una tubería con nombre de .mp3) y la carpeta de destino, que debe existir.
// destDir "" es la raíz de la biblioteca.
func trackPaths(root, rel, destDir string) (realRoot, src, dst string, err error) {
	if src, err = ResolveFile(root, rel); err != nil {
		return "", "", "", err
	}
	if realRoot, err = filepath.EvalSymlinks(root); err != nil {
		return "", "", "", err
	}
	dst = realRoot
	if destDir != "" {
		if dst, err = resolveIn(root, destDir); err != nil {
			return "", "", "", err
		}
	}
	if st, err := os.Stat(dst); err != nil || !st.IsDir() {
		return "", "", "", errors.New("la playlist de destino no existe")
	}
	return realRoot, src, dst, nil
}

// relFromRoot devuelve la ruta relativa (con «/») de full dentro de la raíz.
func relFromRoot(realRoot, full string) (string, error) {
	back, err := filepath.Rel(realRoot, full)
	if err != nil {
		return "", err
	}
	return path.Clean(filepath.ToSlash(back)), nil
}

// MoveTrack mueve un archivo de audio a otra playlist (carpeta). destDir "" significa la raíz de la
// biblioteca. Nunca sobrescribe y no sale de la raíz. Devuelve la nueva ruta relativa.
func MoveTrack(root, rel, destDir string) (string, error) {
	realRoot, src, dst, err := trackPaths(root, rel, destDir)
	if err != nil {
		return "", err
	}
	newFull := filepath.Join(dst, filepath.Base(src))
	if newFull == src {
		return rel, nil
	}
	if _, err := os.Lstat(newFull); err == nil {
		return "", errors.New("ya hay un archivo con ese nombre en la playlist de destino")
	}
	if err := os.Rename(src, newFull); err != nil {
		return "", err
	}
	return relFromRoot(realRoot, newFull)
}

// CopyTrack copia un archivo de audio a otra playlist dejando también el original. destName "" conserva el nombre;
// si se da, es solo el nombre del archivo (la extensión se conserva). Nunca sobrescribe. Devuelve la nueva ruta relativa.
func CopyTrack(root, rel, destDir, destName string) (string, error) {
	realRoot, src, dst, err := trackPaths(root, rel, destDir)
	if err != nil {
		return "", err
	}
	name := filepath.Base(src)
	if destName != "" {
		clean, err := cleanFolderName(strings.TrimSuffix(destName, filepath.Ext(destName)))
		if err != nil {
			return "", err
		}
		name = clean + filepath.Ext(src)
	}
	newFull := filepath.Join(dst, name)
	if newFull == src {
		return "", errors.New("la canción ya está en esa playlist")
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(newFull, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", errors.New("ya hay un archivo con ese nombre en la playlist de destino")
		}
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(newFull)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(newFull)
		return "", err
	}
	if st, err := os.Stat(src); err == nil {
		_ = os.Chtimes(newFull, st.ModTime(), st.ModTime())
	}
	return relFromRoot(realRoot, newFull)
}

// ResolveFile devuelve la ruta real de una canción de la biblioteca (ruta relativa) con las mismas
// garantías de seguridad que el servidor: dentro de la raíz, sin ocultos y solo audio.
func ResolveFile(root, rel string) (string, error) {
	full, err := resolveIn(root, rel)
	if err != nil {
		return "", err
	}
	if ContentType(full) == "" {
		return "", errOutside
	}
	st, err := os.Stat(full)
	if err != nil || !st.Mode().IsRegular() {
		return "", errOutside
	}
	return full, nil
}

// MaxChunk es el tamaño máximo de un trozo de ReadChunk.
const MaxChunk = 4 << 20

// ReadChunk lee hasta length bytes de una canción desde offset (para pasarla al webview por trozos, con progreso).
func ReadChunk(root, rel string, offset int64, length int) ([]byte, error) {
	if offset < 0 || length <= 0 || length > MaxChunk {
		return nil, errors.New("rango no válido")
	}
	full, err := ResolveFile(root, rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, offset)
	if err != nil && n == 0 {
		if errors.Is(err, io.EOF) {
			return []byte{}, nil
		}
		return nil, err
	}
	return buf[:n], nil
}
