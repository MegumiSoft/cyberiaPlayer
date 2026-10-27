package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"musicdl/src/backend/decode"
	"musicdl/src/backend/library"
	"musicdl/src/backend/ytdlp"
)

// Para analizar WAV, FLAC, AIFF y archivos grandes la interfaz no los decodifica ella misma (el webview se
// quedaría sin memoria): el backend los convierte con ffmpeg a PCM mono y la interfaz lo lee por trozos.

var (
	pcmMu    sync.Mutex
	pcmFiles = map[string]string{} // ruta relativa → archivo temporal con el PCM
	pcmSeq   int
)

func (a *App) pcmDir() string { return filepath.Join(a.dataDir, "tmp") }

// PrepareAnalysis decodifica la canción a PCM mono de 16 bits (22050 Hz) y devuelve su tamaño en bytes.
// Hay que pedir ReleaseAnalysis al terminar de leerlo.
func (a *App) PrepareAnalysis(rel string) (int64, error) {
	full, err := library.ResolveFile(a.settings.Get().OutDir, rel)
	if err != nil {
		return 0, err
	}
	ff, ok := a.yt.FFmpeg()
	if !ok {
		return 0, errors.New("ffmpeg no encontrado: instálalo o indica su ruta en CONFIG")
	}
	if err := os.MkdirAll(a.pcmDir(), 0o755); err != nil {
		return 0, err
	}
	pcmMu.Lock()
	pcmSeq++
	out := filepath.Join(a.pcmDir(), "analysis-"+strconv.Itoa(pcmSeq)+".pcm")
	if old, ok := pcmFiles[rel]; ok {
		os.Remove(old)
	}
	pcmFiles[rel] = out
	pcmMu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	n, err := decode.ToPCM(ctx, ff, full, out, ytdlp.ConfigureCmd)
	if err != nil {
		pcmMu.Lock()
		delete(pcmFiles, rel)
		pcmMu.Unlock()
	}
	return n, err
}

// ReadAnalysisChunk lee un trozo (hasta 4 MB) del PCM preparado con PrepareAnalysis.
func (a *App) ReadAnalysisChunk(rel string, offset int64, length int) ([]byte, error) {
	pcmMu.Lock()
	p, ok := pcmFiles[rel]
	pcmMu.Unlock()
	if !ok {
		return nil, errors.New("no hay análisis preparado para esa canción")
	}
	if offset < 0 || length <= 0 || length > library.MaxChunk {
		return nil, errors.New("rango no válido")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, offset)
	if n == 0 && err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// ReleaseAnalysis borra el PCM temporal de una canción.
func (a *App) ReleaseAnalysis(rel string) {
	pcmMu.Lock()
	p, ok := pcmFiles[rel]
	delete(pcmFiles, rel)
	pcmMu.Unlock()
	if ok {
		os.Remove(p)
	}
}
