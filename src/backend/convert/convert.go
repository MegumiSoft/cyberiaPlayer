// Package convert pasa una canción de un formato de audio a otro con ffmpeg (por ejemplo WAV → MP3),
// conservando etiquetas y carátula. Nunca toca el archivo original ni sobrescribe el destino.
package convert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Runner crea los procesos de ffmpeg; la app le pone la configuración propia de cada sistema.
type Runner struct {
	FFmpeg    string
	Configure func(*exec.Cmd)
}

// Format describe un formato de salida.
type Format struct {
	Ext        string // con punto: ".mp3"
	Lossy      bool
	DefaultK   int   // kbps por defecto (0 en los formatos sin pérdida)
	Bitrates   []int // kbps ofrecidos
	KeepsCover bool
}

var formats = map[string]Format{
	"mp3":  {".mp3", true, 320, []int{320, 256, 192, 128}, true},
	"m4a":  {".m4a", true, 256, []int{320, 256, 192, 128}, true},
	"opus": {".opus", true, 160, []int{256, 192, 160, 128, 96}, false},
	"flac": {".flac", false, 0, nil, true},
	"wav":  {".wav", false, 0, nil, false},
}

// Lookup devuelve el formato de salida (acepta «mp3», «.MP3»…).
func Lookup(name string) (Format, bool) {
	f, ok := formats[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), ".")]
	return f, ok
}

// IsLossy dice si la extensión (con o sin punto) es un formato con pérdida.
func IsLossy(ext string) bool {
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "mp3", "m4a", "aac", "ogg", "opus", "wma":
		return true
	}
	return false
}

var (
	reBits = regexp.MustCompile(`Audio:.*?(\d+)\s*bit`)
	reFmt  = regexp.MustCompile(`Audio:.*?\b(s16|s32|flt|dbl|s64)p?\b`)
)

func (r Runner) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, r.FFmpeg, append([]string{"-hide_banner", "-nostdin"}, args...)...)
	if r.Configure != nil {
		r.Configure(c)
	}
	return c
}

// sourceBits mira cuántos bits tiene el audio de origen (16 si es con pérdida o no se puede saber).
func (r Runner) sourceBits(ctx context.Context, in string) int {
	if IsLossy(filepath.Ext(in)) {
		return 16
	}
	var stderr bytes.Buffer
	c := r.cmd(ctx, "-i", in)
	c.Stderr = &stderr
	_ = c.Run() // sin salida ffmpeg termina con error: lo útil está en stderr
	out := stderr.String()
	if m := reBits.FindStringSubmatch(out); m != nil {
		if b, _ := strconv.Atoi(m[1]); b > 0 {
			return b
		}
	}
	if m := reFmt.FindStringSubmatch(out); m != nil && m[1] != "s16" {
		return 24
	}
	return 16
}

func codecArgs(f Format, kbps, bits int) []string {
	if kbps <= 0 {
		kbps = f.DefaultK
	}
	br := strconv.Itoa(kbps) + "k"
	switch f.Ext {
	case ".mp3":
		return []string{"-c:a", "libmp3lame", "-b:a", br, "-id3v2_version", "3"}
	case ".m4a":
		return []string{"-c:a", "aac", "-b:a", br, "-movflags", "+faststart"}
	case ".opus":
		return []string{"-c:a", "libopus", "-b:a", br}
	case ".flac":
		if bits > 16 {
			return []string{"-c:a", "flac", "-compression_level", "5", "-sample_fmt", "s32", "-bits_per_raw_sample", "24"}
		}
		return []string{"-c:a", "flac", "-compression_level", "5", "-sample_fmt", "s16"}
	default: // .wav
		switch {
		case bits >= 32:
			return []string{"-c:a", "pcm_s32le"}
		case bits > 16:
			return []string{"-c:a", "pcm_s24le"}
		}
		return []string{"-c:a", "pcm_s16le"}
	}
}

// Convert escribe en out (que no debe existir) el audio de in en el formato dado. kbps 0 = el predeterminado del
// formato; se ignora en los formatos sin pérdida. Se escribe primero a un temporal y solo al final se renombra,
// así un fallo o una cancelación nunca deja un archivo a medias.
func (r Runner) Convert(ctx context.Context, in, out, format string, kbps int) error {
	f, ok := Lookup(format)
	if !ok {
		return fmt.Errorf("formato desconocido: %s", format)
	}
	if !strings.EqualFold(filepath.Ext(out), f.Ext) {
		return fmt.Errorf("el destino debe terminar en %s", f.Ext)
	}
	if _, err := os.Lstat(out); err == nil {
		return errors.New("ya existe un archivo con ese nombre")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	bits := r.sourceBits(ctx, in)
	base := filepath.Base(out)
	tmp := filepath.Join(filepath.Dir(out), "."+strings.TrimSuffix(base, f.Ext)+".converting"+f.Ext)
	defer os.Remove(tmp)

	run := func(withCover bool) (string, error) {
		args := []string{"-y", "-i", in, "-map", "0:a:0"}
		if withCover && f.KeepsCover {
			args = append(args, "-map", "0:v:0?", "-c:v", "copy", "-disposition:v:0", "attached_pic")
		} else {
			args = append(args, "-vn")
		}
		args = append(args, "-map_metadata", "0")
		args = append(args, codecArgs(f, kbps, bits)...)
		args = append(args, tmp)
		var stderr bytes.Buffer
		c := r.cmd(ctx, args...)
		c.Stderr = &stderr
		err := c.Run()
		return stderr.String(), err
	}
	msg, err := run(true)
	if err != nil && ctx.Err() == nil {
		// Algunas carátulas no caben en el contenedor de destino: se reintenta sin ella antes de rendirse.
		_ = os.Remove(tmp)
		msg, err = run(false)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg falló: %w\n%s", err, lastLines(msg, 3))
	}
	st, err := os.Stat(tmp)
	if err != nil || st.Size() < 1024 {
		return errors.New("ffmpeg no generó un archivo válido")
	}
	if _, err := os.Lstat(out); err == nil {
		return errors.New("ya existe un archivo con ese nombre")
	}
	return os.Rename(tmp, out)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
