package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"musicdl/src/backend/decode"
	"musicdl/src/backend/library"
	"musicdl/src/backend/ytdlp"
)

// Para analizar WAV, FLAC, AIFF y archivos grandes la interfaz no los decodifica ella misma (el webview se
// quedaría sin memoria): el backend los convierte con ffmpeg a PCM mono y la interfaz lo lee por trozos.
//
// Cada análisis se identifica con un «handle» único que elige la interfaz (no con la ruta de la canción): así dos
// análisis de la misma canción a la vez (ANALIZAR TODAS + reproducirla) no se pisan los archivos, y un análisis que
// ya no interesa (se saltó a otra canción) se puede cancelar mientras ffmpeg aún decodifica.

type pcmEntry struct {
	path   string
	cancel context.CancelFunc
	ready  bool
}

var (
	pcmMu      sync.Mutex
	pcmEntries = map[string]*pcmEntry{} // handle → análisis en curso o listo
	pcmSeq     atomic.Int64
	tooLong    sync.Map // ruta|tamaño|fecha → true: ya se sabe que pasa de 30 min, no se vuelve a intentar
)

func (a *App) pcmDir() string { return filepath.Join(a.dataDir, "tmp") }

// PrepareAnalysis decodifica la canción rel a PCM mono de 16 bits (22050 Hz) y devuelve su tamaño en bytes.
// handle lo elige quien llama y debe ser único (p. ej. un contador); con él se lee, cancela y libera el análisis.
// Hay que pedir ReleaseAnalysis(handle) al terminar.
func (a *App) PrepareAnalysis(handle, rel string) (int64, error) {
	if handle == "" || len(handle) > 64 {
		return 0, errors.New("handle de análisis no válido")
	}
	full, err := library.ResolveFile(a.settings.Get().OutDir, rel)
	if err != nil {
		return 0, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	key := fmt.Sprintf("%s|%d|%d", full, st.Size(), st.ModTime().UnixNano())
	if _, long := tooLong.Load(key); long {
		return 0, decode.ErrTooLong
	}
	ff, ok := a.yt.FFmpeg()
	if !ok {
		return 0, errors.New("ffmpeg no encontrado: instálalo o indica su ruta en CONFIG")
	}
	if err := os.MkdirAll(a.pcmDir(), 0o755); err != nil {
		return 0, err
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	e := &pcmEntry{path: filepath.Join(a.pcmDir(), "analysis-"+strconv.FormatInt(pcmSeq.Add(1), 10)+".pcm"), cancel: cancel}
	pcmMu.Lock()
	if _, dup := pcmEntries[handle]; dup {
		pcmMu.Unlock()
		cancel()
		return 0, errors.New("ya hay un análisis con ese handle")
	}
	pcmEntries[handle] = e
	pcmMu.Unlock()

	n, err := decode.ToPCM(ctx, ff, full, e.path, ytdlp.ConfigureCmd)
	pcmMu.Lock()
	if err != nil {
		if pcmEntries[handle] == e { // si se liberó mientras tanto ya no está
			delete(pcmEntries, handle)
		}
	} else if pcmEntries[handle] == e {
		e.ready = true
	} else { // liberado durante la decodificación: no dejar el archivo
		err = context.Canceled
	}
	pcmMu.Unlock()
	if err != nil {
		cancel()
		os.Remove(e.path)
		if errors.Is(err, decode.ErrTooLong) {
			tooLong.Store(key, true)
		}
		return 0, err
	}
	return n, nil
}

// ReadAnalysisChunk lee un trozo (hasta 4 MB) del PCM preparado con PrepareAnalysis. Más allá del final devuelve un
// trozo vacío (igual que ReadTrackChunk).
func (a *App) ReadAnalysisChunk(handle string, offset int64, length int) ([]byte, error) {
	pcmMu.Lock()
	e, ok := pcmEntries[handle]
	ready := ok && e.ready
	pcmMu.Unlock()
	if !ready {
		return nil, errors.New("no hay análisis preparado para ese handle")
	}
	if offset < 0 || length <= 0 || length > library.MaxChunk {
		return nil, errors.New("rango no válido")
	}
	f, err := os.Open(e.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, offset)
	if n == 0 && err != nil {
		if errors.Is(err, io.EOF) {
			return []byte{}, nil
		}
		return nil, err
	}
	return buf[:n], nil
}

// CancelAnalysis detiene la decodificación en curso de ese handle (ffmpeg se termina). No libera nada: la interfaz
// igual debe llamar a ReleaseAnalysis.
func (a *App) CancelAnalysis(handle string) {
	pcmMu.Lock()
	e := pcmEntries[handle]
	pcmMu.Unlock()
	if e != nil {
		e.cancel()
	}
}

// ReleaseAnalysis cancela (si hace falta) y borra el PCM temporal de ese handle.
func (a *App) ReleaseAnalysis(handle string) {
	pcmMu.Lock()
	e, ok := pcmEntries[handle]
	delete(pcmEntries, handle)
	pcmMu.Unlock()
	if ok {
		e.cancel()
		os.Remove(e.path)
	}
}
