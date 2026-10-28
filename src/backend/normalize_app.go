package backend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"musicdl/src/backend/library"
	"musicdl/src/backend/normalize"
	"musicdl/src/backend/ytdlp"
)

// LoudnessInfo es el volumen medido de una canción.
type LoudnessInfo struct {
	LUFS float64 `json:"lufs"`
	Peak float64 `json:"peak"`
}

// NormalizeResult es el resultado de igualar el volumen de una canción con el de otra.
type NormalizeResult struct {
	normalize.Result
	Rel string `json:"rel"`
}

var (
	normMu    sync.Mutex // una sola normalización a la vez
	loudCache sync.Map   // clave ruta|tamaño|fecha → normalize.Loudness
)

func (a *App) normalizer() (normalize.Runner, error) {
	ff, ok := a.yt.FFmpeg()
	if !ok {
		return normalize.Runner{}, errors.New("ffmpeg no encontrado: instálalo o indica su ruta en CONFIG")
	}
	return normalize.Runner{FFmpeg: ff, Configure: ytdlp.ConfigureCmd}, nil
}

func (a *App) measure(ctx context.Context, rel string) (string, normalize.Loudness, error) {
	full, err := library.ResolveFile(a.settings.Get().OutDir, rel)
	if err != nil {
		return "", normalize.Loudness{}, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", normalize.Loudness{}, err
	}
	key := fmt.Sprintf("%s|%d|%d", full, st.Size(), st.ModTime().UnixNano())
	if v, ok := loudCache.Load(key); ok {
		return full, v.(normalize.Loudness), nil
	}
	r, err := a.normalizer()
	if err != nil {
		return "", normalize.Loudness{}, err
	}
	l, err := r.Measure(ctx, full)
	if err != nil {
		return "", l, err
	}
	loudCache.Store(key, l)
	return full, l, nil
}

// MeasureLoudness mide el volumen integrado (LUFS) de una canción de la biblioteca.
func (a *App) MeasureLoudness(rel string) (LoudnessInfo, error) {
	_, l, err := a.measure(a.ctx, rel)
	return LoudnessInfo{LUFS: l.LUFS, Peak: l.Peak}, err
}

// NormalizeTrack reescribe la canción rel con la ganancia necesaria para igualar el volumen de refRel.
// SOBRESCRIBE el archivo (guarda antes una copia del original en la carpeta de datos de la app).
func (a *App) NormalizeTrack(rel, refRel string) (NormalizeResult, error) {
	var out NormalizeResult
	if rel == refRel {
		return out, errors.New("elige una canción de referencia distinta")
	}
	if !normMu.TryLock() {
		return out, errors.New("ya se está normalizando otra canción")
	}
	defer normMu.Unlock()
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".mp3", ".m4a", ".aac", ".flac", ".wav", ".opus":
	default:
		return out, fmt.Errorf("el formato %s no se puede normalizar aquí (usa MP3, M4A, FLAC, WAV u Opus)", ext)
	}
	full, tgt, err := a.measure(a.ctx, rel)
	if err != nil {
		return out, err
	}
	_, ref, err := a.measure(a.ctx, refRel)
	if err != nil {
		return out, fmt.Errorf("referencia: %w", err)
	}
	gain := ref.LUFS - tgt.LUFS
	if math.Abs(gain) < 0.3 {
		return out, fmt.Errorf("ya suenan igual de fuerte (%.1f y %.1f LUFS)", tgt.LUFS, ref.LUFS)
	}
	r, err := a.normalizer()
	if err != nil {
		return out, err
	}
	res, err := r.Apply(a.ctx, full, tgt.LUFS, gain, filepath.Join(a.dataDir, "normalize-backups"))
	if err != nil {
		return out, err
	}
	res.Reference = ref.LUFS
	// El análisis, la rejilla y los cues siguen valiendo: solo cambió el tamaño del archivo.
	_ = a.meta.Resize(rel, res.OldSize, res.NewSize)
	return NormalizeResult{Result: res, Rel: rel}, nil
}
