// Package convert pasa una canción de un formato de audio a otro con ffmpeg (por ejemplo WAV → MP3),
// conservando etiquetas y carátula. Nunca toca el archivo original ni sobrescribe el destino (salvo una ventana mínima
// en sistemas de archivos sin enlaces duros, ver publish).
package convert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// Format describe un formato de salida. La interfaz lo recibe tal cual (ConvertFormats) para armar el diálogo, así la
// lista vive solo aquí.
type Format struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Ext        string `json:"-"` // con punto: ".mp3"
	Lossy      bool   `json:"lossy"`
	Default    int    `json:"default"`  // kbps por defecto (0 en los formatos sin pérdida)
	Bitrates   []int  `json:"bitrates"` // kbps de tasa fija ofrecidos
	VBR        []int  `json:"vbr"`      // calidades VBR ofrecidas (LAME: 0 = la mejor); solo MP3
	KeepsCover bool   `json:"-"`
}

var formats = []Format{
	{ID: "mp3", Label: "MP3", Ext: ".mp3", Lossy: true, Default: 320, Bitrates: []int{320, 256, 192, 128}, VBR: []int{0, 2}, KeepsCover: true},
	{ID: "m4a", Label: "M4A (AAC)", Ext: ".m4a", Lossy: true, Default: 256, Bitrates: []int{320, 256, 192, 128}, KeepsCover: true},
	{ID: "opus", Label: "OPUS", Ext: ".opus", Lossy: true, Default: 160, Bitrates: []int{256, 192, 160, 128, 96}},
	{ID: "aac", Label: "AAC (.aac, sin etiquetas)", Ext: ".aac", Lossy: true, Default: 256, Bitrates: []int{320, 256, 192, 128}},
	{ID: "flac", Label: "FLAC", Ext: ".flac", KeepsCover: true},
	{ID: "wav", Label: "WAV", Ext: ".wav"},
}

// lossyExts son las extensiones con pérdida que la biblioteca reconoce (como origen), sin punto.
var lossyExts = []string{"mp3", "m4a", "aac", "opus", "ogg", "oga"}

// Formats devuelve los formatos de salida, en el orden en que se ofrecen.
func Formats() []Format { return append([]Format(nil), formats...) }

// LossyExts devuelve las extensiones con pérdida (sin punto).
func LossyExts() []string { return append([]string(nil), lossyExts...) }

// Lookup devuelve el formato de salida (acepta «mp3», «.MP3»…).
func Lookup(name string) (Format, bool) {
	id := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), ".")
	for _, f := range formats {
		if f.ID == id {
			return f, true
		}
	}
	return Format{}, false
}

// IsLossy dice si la extensión (con o sin punto) es un formato con pérdida.
func IsLossy(ext string) bool {
	e := strings.TrimPrefix(strings.ToLower(ext), ".")
	for _, l := range lossyExts {
		if l == e {
			return true
		}
	}
	return false
}

// Options es la calidad pedida. Los dos son opcionales (nil = lo predeterminado del formato) y no se combinan; se
// ignoran en los formatos sin pérdida.
type Options struct {
	Bitrate *int // kbps de tasa fija
	VBR     *int // calidad VBR (solo MP3)
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Validate comprueba que la calidad pedida exista en ese formato, con un mensaje claro (mejor que un fallo de ffmpeg).
func Validate(format string, o Options) error {
	f, ok := Lookup(format)
	if !ok {
		return fmt.Errorf("formato desconocido: %s", format)
	}
	if !f.Lossy {
		return nil
	}
	if o.Bitrate != nil && o.VBR != nil {
		return errors.New("elige una tasa fija (kbps) o VBR, no las dos")
	}
	if o.VBR != nil && !contains(f.VBR, *o.VBR) {
		if len(f.VBR) == 0 {
			return fmt.Errorf("%s no ofrece VBR", f.Label)
		}
		return fmt.Errorf("calidad VBR no admitida: V%d (opciones en %s: %s)", *o.VBR, f.Label, joinV(f.VBR))
	}
	if o.Bitrate != nil && !contains(f.Bitrates, *o.Bitrate) {
		return fmt.Errorf("bitrate no admitido: %d kbps (opciones en %s: %s)", *o.Bitrate, f.Label, joinK(f.Bitrates))
	}
	return nil
}

func joinK(v []int) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ", ") + " kbps"
}

func joinV(v []int) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = "V" + strconv.Itoa(x)
	}
	return strings.Join(s, ", ")
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

func codecArgs(f Format, o Options, bits int) []string {
	kbps := f.Default
	if o.Bitrate != nil {
		kbps = *o.Bitrate
	}
	br := strconv.Itoa(kbps) + "k"
	switch f.ID {
	case "mp3":
		if o.VBR != nil {
			return []string{"-c:a", "libmp3lame", "-q:a", strconv.Itoa(*o.VBR), "-id3v2_version", "3"}
		}
		return []string{"-c:a", "libmp3lame", "-b:a", br, "-id3v2_version", "3"}
	case "m4a":
		return []string{"-c:a", "aac", "-b:a", br, "-movflags", "+faststart"}
	case "aac":
		return []string{"-c:a", "aac", "-b:a", br}
	case "opus":
		return []string{"-c:a", "libopus", "-b:a", br}
	case "flac":
		if bits > 16 {
			return []string{"-c:a", "flac", "-compression_level", "5", "-sample_fmt", "s32", "-bits_per_raw_sample", "24"}
		}
		return []string{"-c:a", "flac", "-compression_level", "5", "-sample_fmt", "s16"}
	default: // wav
		switch {
		case bits >= 32:
			return []string{"-c:a", "pcm_s32le"}
		case bits > 16:
			return []string{"-c:a", "pcm_s24le"}
		}
		return []string{"-c:a", "pcm_s16le"}
	}
}

var errExists = errors.New("ya existe un archivo con ese nombre")

// publish deja tmp como out sin sobrescribir nada: un enlace duro falla con «ya existe» si out apareció entretanto
// (os.Rename, en cambio, reemplaza en silencio). En sistemas de archivos sin enlaces duros (FAT/exFAT de un USB) se
// cae a Rename tras comprobar antes: ahí queda una ventana mínima.
func publish(tmp, out string) error {
	err := os.Link(tmp, out)
	if err == nil {
		return os.Remove(tmp)
	}
	if errors.Is(err, fs.ErrExist) {
		return errExists
	}
	if _, err := os.Lstat(out); err == nil {
		return errExists
	}
	return os.Rename(tmp, out)
}

// Convert escribe en out (que no debe existir) el audio de in en el formato dado, con la calidad de o (campos nil =
// lo predeterminado). Se escribe primero a un temporal y solo al final se publica, así un fallo o una cancelación nunca
// deja un archivo a medias, y nunca se sobrescribe un archivo existente (ver publish para la excepción en FAT).
func (r Runner) Convert(ctx context.Context, in, out, format string, o Options) error {
	if err := Validate(format, o); err != nil {
		return err
	}
	f, _ := Lookup(format)
	if !strings.EqualFold(filepath.Ext(out), f.Ext) {
		return fmt.Errorf("el destino debe terminar en %s", f.Ext)
	}
	if _, err := os.Lstat(out); err == nil {
		return errExists
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
		args = append(args, codecArgs(f, o, bits)...)
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
	return publish(tmp, out)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
