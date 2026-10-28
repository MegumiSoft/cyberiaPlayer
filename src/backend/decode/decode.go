// Package decode convierte cualquier audio (WAV, FLAC, AIFF, MP3…) a PCM mono de 16 bits con ffmpeg, para analizarlo
// sin cargar el archivo entero en la memoria del webview (un WAV de 5 minutos pesa ~50 MB).
package decode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// SampleRate es la frecuencia del PCM que se entrega; debe coincidir con ANALYSIS_SR de la interfaz.
const SampleRate = 22050

// MaxSeconds es la duración máxima que se analiza (más allá es un mix largo y no vale la pena).
const MaxSeconds = 30 * 60

// ErrTooLong indica que el audio dura más de MaxSeconds.
var ErrTooLong = errors.New("la canción dura más de 30 minutos: no se analiza")

// ToPCM decodifica in a out (s16le, mono, SampleRate) y devuelve los bytes escritos.
func ToPCM(ctx context.Context, ffmpeg, in, out string, configure func(*exec.Cmd)) (int64, error) {
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
