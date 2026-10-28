package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// lineWriter parte lo que se le escribe en líneas completas y llama a fn con cada una.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf = append(w.buf, p...)
	var lines []string
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 1<<20 { // una "línea" de más de 1 MB no es salida de yt-dlp: descartarla
		w.buf = nil
	}
	w.mu.Unlock()
	for _, l := range lines {
		w.fn(l)
	}
	return len(p), nil
}

// Flush entrega la última línea si el proceso terminó sin salto de línea final.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	rest := strings.TrimRight(string(w.buf), "\r\n")
	w.buf = nil
	w.mu.Unlock()
	if rest != "" {
		w.fn(rest)
	}
}

// Prefijos de las líneas que le pedimos a yt-dlp imprimir, para distinguirlas de cualquier otra salida.
const (
	prefixDL   = "mdl:dl"
	prefixPP   = "mdl:pp"
	prefixFile = "mdl:file"
)

type DownloadOptions struct {
	URL     string
	OutDir  string // carpeta base
	Folder  string // subcarpeta opcional (p. ej. nombre de la playlist)
	Index   int    // si > 0, se antepone "NN - " al nombre del archivo
	Name    string // nombre de archivo fijo (sin extensión); vacío = título del video
	Format  string // mp3, m4a, opus, flac, wav
	Quality string // "0" (mejor) o bitrate como "192K"
	FFmpeg  string // ruta a ffmpeg (opcional)
}

type Progress struct {
	Phase   string  // "downloading" o "converting"
	Percent float64 // 0-100
	Speed   float64 // bytes/s, 0 si se desconoce
	ETA     int     // segundos, -1 si se desconoce
}

type Result struct {
	Path string
}

// SanitizeName deja un nombre seguro para usar como carpeta en Windows, macOS y Linux.
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r), unicode.IsControl(r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(strings.TrimSpace(b.String()), ". ") // Windows no admite punto/espacio final
	if r := []rune(out); len(r) > 120 {
		out = strings.TrimRight(string(r[:120]), ". ")
	}
	if out == "" {
		return "Descargas"
	}
	return out
}

// escapeTemplate duplica los % para que yt-dlp los trate como texto literal en la plantilla de salida.
func escapeTemplate(s string) string { return strings.ReplaceAll(s, "%", "%%") }

func supportsEmbeddedCover(format string) bool {
	switch format {
	case "mp3", "m4a", "opus", "flac":
		return true
	}
	return false
}

func buildDownloadArgs(o DownloadOptions) []string {
	dir := o.OutDir
	if o.Folder != "" {
		dir = filepath.Join(dir, SanitizeName(o.Folder))
	}
	name := "%(title)s.%(ext)s"
	if o.Name != "" {
		name = escapeTemplate(SanitizeName(o.Name)) + ".%(ext)s"
	}
	if o.Index > 0 {
		name = fmt.Sprintf("%02d - %s", o.Index, name)
	}
	tmpl := filepath.Join(escapeTemplate(dir), name)

	args := []string{
		"--ignore-config", "--no-playlist", "--no-warnings", "--no-colors",
		"--newline", "--progress", "--windows-filenames",
		"--extract-audio", "--audio-format", o.Format, "--audio-quality", o.Quality,
		"--embed-metadata",
		"--progress-template", "download:" + prefixDL +
			" %(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s" +
			" %(progress.fragment_index)s %(progress.fragment_count)s %(progress.speed)s %(progress.eta)s",
		"--progress-template", "postprocess:" + prefixPP + " %(progress.status)s",
		"--print", "after_move:" + prefixFile + " %(filepath)s",
		"-o", tmpl,
	}
	if supportsEmbeddedCover(o.Format) {
		args = append(args, "--embed-thumbnail")
	}
	if o.FFmpeg != "" {
		args = append(args, "--ffmpeg-location", o.FFmpeg)
	}
	return append(args, "--", o.URL)
}

func parseNum(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0 // "NA" y valores inválidos cuentan como desconocidos
	}
	return f
}

// parseProgressLine interpreta una línea de salida de yt-dlp. ok es false si no es de progreso.
func parseProgressLine(line string) (p Progress, ok bool) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return p, false
	}
	switch f[0] {
	case prefixDL:
		if len(f) < 8 {
			return p, false
		}
		downloaded, total, estimate := parseNum(f[1]), parseNum(f[2]), parseNum(f[3])
		fragIdx, fragCount := parseNum(f[4]), parseNum(f[5])
		if total == 0 {
			total = estimate
		}
		p = Progress{Phase: "downloading", Speed: parseNum(f[6]), ETA: -1}
		if eta := parseNum(f[7]); f[7] != "NA" && eta >= 0 {
			p.ETA = int(eta)
		}
		switch {
		case total > 0:
			p.Percent = downloaded / total * 100
		case fragCount > 0:
			p.Percent = fragIdx / fragCount * 100
		}
		if p.Percent > 100 {
			p.Percent = 100
		}
		return p, true
	case prefixPP:
		if len(f) >= 2 && f[1] == "started" {
			return Progress{Phase: "converting", Percent: 100, ETA: -1}, true
		}
	}
	return p, false
}

// Download baja una canción, la convierte al formato pedido y devuelve la ruta final.
// Cancelar ctx termina el proceso de yt-dlp. onProgress puede llamarse desde varias goroutines
// porque yt-dlp reparte su salida entre stdout (descarga, ruta final) y stderr (conversión).
func (c *Client) Download(ctx context.Context, opts DownloadOptions, onProgress func(Progress)) (Result, error) {
	opts.URL = strings.TrimSpace(opts.URL)
	if err := validateURL(opts.URL); err != nil {
		return Result{}, err
	}
	path, _, err := c.Resolve()
	if err != nil {
		return Result{}, err
	}
	if opts.FFmpeg == "" {
		if ff, ok := c.FFmpeg(); ok {
			opts.FFmpeg = ff
		}
	}

	var (
		mu    sync.Mutex
		final string
	)
	handle := func(line string) {
		if rest, ok := strings.CutPrefix(line, prefixFile+" "); ok {
			mu.Lock()
			final = strings.TrimSpace(rest)
			mu.Unlock()
			return
		}
		if p, ok := parseProgressLine(line); ok && onProgress != nil {
			onProgress(p)
		}
	}
	outW := &lineWriter{fn: handle}
	errW := &lineWriter{fn: handle}
	var tail tailBuffer

	args := buildDownloadArgs(opts)
	// Las opciones comunes van antes del "--" final que separa la URL.
	extra := c.commonArgs(ctx, path)
	args = append(args[:len(args)-2:len(args)-2], append(extra, args[len(args)-2:]...)...)
	cmd := c.command(ctx, path, args...)
	cmd.Stdout = outW
	cmd.Stderr = io.MultiWriter(&tail, errW)
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("no se pudo ejecutar yt-dlp: %w", err)
	}
	waitErr := cmd.Wait()
	outW.Flush()
	errW.Flush()

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if waitErr != nil {
		return Result{}, errors.New(errorMessage(tail.String()))
	}
	mu.Lock()
	defer mu.Unlock()
	if final == "" {
		return Result{}, errors.New("yt-dlp terminó sin generar un archivo")
	}
	return Result{Path: final}, nil
}
