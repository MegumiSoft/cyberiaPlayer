// Package library escanea la carpeta de música (cada carpeta con audio es una playlist) y sirve
// los archivos al reproductor a través de un servidor local protegido.
package library

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	maxDepth = 4
	maxFiles = 50000
	// RootName es el nombre de la playlist formada por los archivos sueltos en la raíz.
	RootName = "(sueltas)"
)

var audioExt = map[string]string{
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".opus": "audio/ogg; codecs=opus",
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".flac": "audio/flac", ".wav": "audio/wav",
}

var coverExt = map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

var coverNames = []string{"cover", "folder", "front", "album", "artwork"}

type Track struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"` // ruta relativa a la raíz, con "/" como separador
	Ext   string `json:"ext"`  // sin punto, en minúsculas
	Size  int64  `json:"size"`
}

type Playlist struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Path   string  `json:"path"`  // carpeta relativa ("" para la raíz)
	Cover  string  `json:"cover"` // imagen relativa a la raíz, o ""
	Tracks []Track `json:"tracks"`
}

// IsAudio indica si el nombre de archivo es un audio reproducible.
func IsAudio(name string) bool { _, ok := audioExt[strings.ToLower(filepath.Ext(name))]; return ok }

// ContentType devuelve el tipo MIME para audio o imágenes de portada; "" si no está permitido.
func ContentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := audioExt[ext]; ok {
		return t
	}
	return coverExt[ext]
}

func ignorable(name string) bool {
	return strings.HasPrefix(name, ".") || // ocultos y los "._archivo" de macOS
		strings.Contains(strings.ToLower(name), ".temp.") || // temporales de ffmpeg/yt-dlp
		strings.HasSuffix(strings.ToLower(name), ".part")
}

var leadingNumber = regexp.MustCompile(`^\s*\d{1,3}\s*[-._)]\s*`)

// TitleFromFile obtiene el título mostrado a partir del nombre de archivo.
func TitleFromFile(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if t := strings.TrimSpace(leadingNumber.ReplaceAllString(base, "")); t != "" {
		return t
	}
	return strings.TrimSpace(base)
}

func hashID(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// NaturalLess ordena respetando números ("2" antes que "10") e ignorando mayúsculas.
func NaturalLess(a, b string) bool {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na, nb := strings.TrimLeft(string(ra[si:i]), "0"), strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ra[i] != rb[j] {
			return ra[i] < rb[j]
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}

// Scan recorre root y devuelve una playlist por cada carpeta que contenga audio (hasta 4 niveles).
// Los archivos sueltos en la raíz forman la playlist RootName. Una raíz inexistente da una lista vacía.
func Scan(root string) ([]Playlist, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	byDir := map[string]*Playlist{}
	dirs := map[string]bool{} // carpetas vistas (ruta relativa con /)
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir // carpeta sin permiso: seguir con el resto
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if ignorable(d.Name()) || strings.Count(filepath.ToSlash(rel), "/")+1 > maxDepth {
				return fs.SkipDir
			}
			dirs[filepath.ToSlash(rel)] = true
			return nil
		}
		if ignorable(d.Name()) || files >= maxFiles {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = ""
		}
		pl := byDir[dir]
		if pl == nil {
			name := strings.ReplaceAll(dir, "/", " / ")
			if dir == "" {
				name = RootName
			}
			pl = &Playlist{ID: hashID("pl:" + dir), Name: name, Path: dir}
			byDir[dir] = pl
		}
		if _, ok := audioExt[ext]; ok {
			info, ierr := d.Info()
			if ierr != nil || !info.Mode().IsRegular() {
				return nil
			}
			files++
			relSlash := filepath.ToSlash(rel)
			pl.Tracks = append(pl.Tracks, Track{
				ID: hashID(relSlash), Title: TitleFromFile(d.Name()), Path: relSlash,
				Ext: strings.TrimPrefix(ext, "."), Size: info.Size(),
			})
		} else if _, ok := coverExt[ext]; ok && pl.Cover == "" {
			stem := strings.ToLower(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())))
			for _, n := range coverNames {
				if stem == n {
					pl.Cover = filepath.ToSlash(rel)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Una carpeta vacía y sin subcarpetas también es una playlist (la que acabas de crear).
	for dir := range dirs {
		if byDir[dir] != nil {
			continue
		}
		hasChild := false
		for other := range dirs {
			if strings.HasPrefix(other, dir+"/") {
				hasChild = true
				break
			}
		}
		if !hasChild {
			byDir[dir] = &Playlist{ID: hashID("pl:" + dir), Name: strings.ReplaceAll(dir, "/", " / "), Path: dir}
		}
	}
	out := make([]Playlist, 0, len(byDir))
	for _, pl := range byDir {
		if pl.Tracks == nil {
			pl.Tracks = []Track{}
		}
		sort.Slice(pl.Tracks, func(i, j int) bool { return NaturalLess(pl.Tracks[i].Path, pl.Tracks[j].Path) })
		out = append(out, *pl)
	}
	sort.Slice(out, func(i, j int) bool { return NaturalLess(out[i].Name, out[j].Name) })
	return out, nil
}
