// Package normalize mide el volumen de una canción (LUFS integrados, ITU-R BS.1770 vía ffmpeg) y la reescribe con
// la ganancia necesaria para igualar el volumen de otra de referencia.
package normalize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Runner crea los procesos de ffmpeg; la app le pone la configuración propia de cada sistema.
type Runner struct {
	FFmpeg    string
	Configure func(*exec.Cmd)
}

// Loudness es lo medido en un archivo.
type Loudness struct {
	LUFS float64 `json:"lufs"` // volumen integrado
	Peak float64 `json:"peak"` // pico real en dBTP
}

func (r Runner) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, r.FFmpeg, append([]string{"-hide_banner", "-nostdin"}, args...)...)
	if r.Configure != nil {
		r.Configure(c)
	}
	return c
}

var (
	reI    = regexp.MustCompile(`(?m)^\s*I:\s+(-?[\d.]+|-inf)\s+LUFS`)
	rePeak = regexp.MustCompile(`(?m)^\s*Peak:\s+(-?[\d.]+|-inf)\s+dBFS`)
)

func parseLast(re *regexp.Regexp, s string) (float64, bool) {
	m := re.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		return 0, false
	}
	v := m[len(m)-1][1]
	if v == "-inf" {
		return math.Inf(-1), true
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// Measure mide el volumen integrado y el pico real.
func (r Runner) Measure(ctx context.Context, path string) (Loudness, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var stderr bytes.Buffer
	c := r.cmd(ctx, "-nostats", "-i", path, "-vn", "-af", "ebur128=peak=true", "-f", "null", "-")
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return Loudness{}, fmt.Errorf("ffmpeg no pudo analizar el audio: %w", err)
	}
	out := stderr.String()
	// El resumen final va tras "Summary:"; las líneas de progreso se descartan.
	if i := strings.LastIndex(out, "Summary:"); i >= 0 {
		out = out[i:]
	}
	lufs, ok := parseLast(reI, out)
	if !ok || math.IsInf(lufs, -1) {
		return Loudness{}, errors.New("no se pudo medir el volumen (¿la canción está en silencio?)")
	}
	peak, ok := parseLast(rePeak, out)
	if !ok {
		peak = 0
	}
	return Loudness{LUFS: lufs, Peak: peak}, nil
}

type streamInfo struct {
	bitrateK int
	bits     int
}

var (
	reBitrate = regexp.MustCompile(`bitrate:\s*(\d+)\s*kb/s`)
	reBits    = regexp.MustCompile(`Audio:.*?(\d+)\s*bit`)
	reFmt     = regexp.MustCompile(`Audio:.*?\b(s16|s32|flt|dbl|u8|s64)p?\b`)
)

func (r Runner) info(ctx context.Context, path string) streamInfo {
	var stderr bytes.Buffer
	c := r.cmd(ctx, "-i", path)
	c.Stderr = &stderr
	_ = c.Run() // sin salida ffmpeg termina con error: lo útil está en stderr
	out := stderr.String()
	si := streamInfo{bits: 16}
	if m := reBitrate.FindStringSubmatch(out); m != nil {
		si.bitrateK, _ = strconv.Atoi(m[1])
	}
	if m := reBits.FindStringSubmatch(out); m != nil {
		si.bits, _ = strconv.Atoi(m[1])
	} else if m := reFmt.FindStringSubmatch(out); m != nil {
		switch m[1] {
		case "s32", "flt", "dbl", "s64":
			si.bits = 24
		}
	}
	return si
}

// Result es lo que se hizo.
type Result struct {
	GainDB    float64 `json:"gainDb"`
	Before    float64 `json:"before"`    // LUFS antes
	After     float64 `json:"after"`     // LUFS medidos después
	Reference float64 `json:"reference"` // LUFS de la referencia
	Peak      float64 `json:"peak"`      // pico real después, dBTP
	Limited   bool    `json:"limited"`   // el limitador tuvo que contener picos
	NewSize   int64   `json:"newSize"`
	OldSize   int64   `json:"oldSize"`
	Backup    string  `json:"backup"`
}

// MaxGain es la subida máxima de volumen permitida.
const MaxGain = 24.0

// Apply reescribe path con gainDB de ganancia (y un limitador para no pasar de -0.5 dBFS), conservando etiquetas y
// carátula, y reemplaza el archivo. Antes guarda una copia del original en backupDir (se conservan las 20 últimas).
func (r Runner) Apply(ctx context.Context, path string, before, gainDB float64, backupDir string) (Result, error) {
	res := Result{Before: before, GainDB: gainDB}
	if gainDB > MaxGain || gainDB < -MaxGain {
		return res, fmt.Errorf("la diferencia de volumen (%.1f dB) es demasiado grande", gainDB)
	}
	ext := strings.ToLower(filepath.Ext(path))
	codec, ok := codecArgs(ext)
	if !ok {
		return res, fmt.Errorf("formato %s no soportado para normalizar", ext)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	st, err := os.Stat(path)
	if err != nil {
		return res, err
	}
	res.OldSize = st.Size()
	si := r.info(ctx, path)

	dir, base := filepath.Dir(path), filepath.Base(path)
	tmp := filepath.Join(dir, "."+strings.TrimSuffix(base, ext)+".normalizing"+ext)
	defer os.Remove(tmp)

	filter := fmt.Sprintf("volume=%.3fdB,alimiter=limit=0.944:level=0:attack=3:release=40", gainDB)
	args := []string{"-y", "-i", path, "-map", "0:a:0"}
	if ext == ".mp3" || ext == ".m4a" || ext == ".aac" || ext == ".flac" {
		args = append(args, "-map", "0:v:0?", "-c:v", "copy")
	}
	args = append(args, "-map_metadata", "0", "-af", filter)
	args = append(args, codec(si)...)
	args = append(args, tmp)
	var stderr bytes.Buffer
	c := r.cmd(ctx, args...)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return res, fmt.Errorf("ffmpeg falló al reescribir la canción: %w\n%s", err, lastLines(stderr.String(), 4))
	}
	nst, err := os.Stat(tmp)
	if err != nil || nst.Size() < 1024 {
		return res, errors.New("ffmpeg no generó un archivo válido; el original no se tocó")
	}
	// Comprobación: el nuevo archivo debe medirse como se espera antes de reemplazar nada.
	after, err := r.Measure(ctx, tmp)
	if err != nil {
		return res, fmt.Errorf("no se pudo verificar el archivo nuevo; el original no se tocó: %w", err)
	}
	res.After, res.Peak = after.LUFS, after.Peak
	res.Limited = math.Abs(after.LUFS-(before+gainDB)) > 0.7

	if backupDir != "" {
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return res, fmt.Errorf("no se pudo crear la copia de seguridad: %w", err)
		}
		bk := filepath.Join(backupDir, time.Now().Format("20060102-150405")+"_"+base)
		if err := copyFile(path, bk); err != nil {
			return res, fmt.Errorf("no se pudo guardar la copia del original; no se modificó nada: %w", err)
		}
		res.Backup = bk
		prune(backupDir, 20)
	}
	if err := os.Rename(tmp, path); err != nil {
		return res, fmt.Errorf("no se pudo reemplazar el archivo: %w", err)
	}
	res.NewSize = nst.Size()
	return res, nil
}

func codecArgs(ext string) (func(streamInfo) []string, bool) {
	kbps := func(si streamInfo, def, lo, hi int) string {
		b := si.bitrateK
		if b == 0 {
			b = def
		}
		return strconv.Itoa(max(lo, min(hi, b))) + "k"
	}
	switch ext {
	case ".mp3":
		return func(si streamInfo) []string {
			return []string{"-c:a", "libmp3lame", "-b:a", kbps(si, 320, 128, 320), "-id3v2_version", "3"}
		}, true
	case ".m4a", ".aac":
		return func(si streamInfo) []string {
			a := []string{"-c:a", "aac", "-b:a", kbps(si, 256, 128, 320)}
			if ext == ".m4a" {
				a = append(a, "-movflags", "+faststart")
			}
			return a
		}, true
	case ".flac":
		return func(si streamInfo) []string {
			f := "s16"
			if si.bits > 16 {
				f = "s32"
			}
			a := []string{"-c:a", "flac", "-compression_level", "5", "-sample_fmt", f}
			if si.bits > 16 {
				a = append(a, "-bits_per_raw_sample", "24")
			}
			return a
		}, true
	case ".wav":
		return func(si streamInfo) []string {
			switch {
			case si.bits >= 32:
				return []string{"-c:a", "pcm_s32le"}
			case si.bits > 16:
				return []string{"-c:a", "pcm_s24le"}
			}
			return []string{"-c:a", "pcm_s16le"}
		}, true
	case ".opus":
		return func(si streamInfo) []string { return []string{"-c:a", "libopus", "-b:a", kbps(si, 160, 64, 256)} }, true
	}
	return nil, false
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

func prune(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) <= keep {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // empiezan por la fecha: las más viejas primero
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
