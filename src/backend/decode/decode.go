// Package decode convierte cualquier audio (WAV, FLAC, AIFF, MP3…) a PCM mono de 16 bits con ffmpeg, para analizarlo
// sin cargar el archivo entero en la memoria del webview (un WAV de 5 minutos pesa ~50 MB).
package decode

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
)

// SampleRate es la frecuencia del PCM que se entrega; debe coincidir con ANALYSIS_SR de la interfaz.
const SampleRate = 22050

// MaxSeconds es la duración máxima que se analiza (más allá es un mix largo y no vale la pena).
const MaxSeconds = 30 * 60

// ErrTooLong indica que el audio dura más de MaxSeconds.
var ErrTooLong = errors.New("la canción dura más de 30 minutos: no se analiza")

var reDuration = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

// Duration lee de ffmpeg la duración declarada del archivo, en segundos, sin decodificar nada. ok es false si el
// archivo no la declara (algunos flujos), en cuyo caso no se sabe.
func Duration(ctx context.Context, ffmpeg, in string, configure func(*exec.Cmd)) (secs float64, ok bool) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-nostdin", "-i", in)
	if configure != nil {
		configure(cmd)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // sin salida ffmpeg termina con error: lo útil está en stderr
	m := reDuration.FindStringSubmatch(stderr.String())
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec, _ := strconv.ParseFloat(m[3], 64)
	return float64(h*3600+mi*60) + sec, true
}

// ToPCM decodifica in a out (s16le, mono, SampleRate) y devuelve los bytes escritos.
// Si el archivo declara más de MaxSeconds se rechaza de inmediato (ErrTooLong), sin decodificar.
func ToPCM(ctx context.Context, ffmpeg, in, out string, configure func(*exec.Cmd)) (int64, error) {
	if d, ok := Duration(ctx, ffmpeg, in, configure); ok && d > MaxSeconds {
		return 0, ErrTooLong
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-i", in, "-vn", "-ac", "1",
		"-ar", strconv.Itoa(SampleRate), "-t", strconv.Itoa(MaxSeconds+1), "-f", "s16le", "-y", out)
	if configure != nil {
		configure(cmd)
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		msg := string(b)
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return 0, errors.New("ffmpeg no pudo decodificar el audio: " + msg)
	}
	st, err := os.Stat(out)
	if err != nil {
		return 0, err
	}
	if st.Size() > int64(MaxSeconds)*SampleRate*2 {
		os.Remove(out)
		return 0, ErrTooLong
	}
	if st.Size() == 0 {
		os.Remove(out)
		return 0, errors.New("el audio no tiene datos")
	}
	return st.Size(), nil
}
