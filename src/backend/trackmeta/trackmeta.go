// Package trackmeta guarda por canción lo que la app calcula (BPM, tonalidad, forma de onda) y lo que el
// usuario edita (rejilla de pulsos, hot cues). Las canciones se identifican por su ruta relativa a la biblioteca.
package trackmeta

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// AnalysisVersion sube cuando cambia el algoritmo: los análisis guardados con otra versión se descartan.
const AnalysisVersion = 3

// MaxCues es el límite de cues por canción (8 hot cues + memory cues).
const MaxCues = 40

// Cue es un punto marcado. Slot 0-7 son los hot cues A-H; -1 es un memory cue.
type Cue struct {
	Slot   int    `json:"slot"`
	TimeMs int    `json:"timeMs"`
	Name   string `json:"name"`
	Color  int    `json:"color"` // índice 0-7 de la paleta neón de la app
}

// Analysis es lo detectado automáticamente.
type Analysis struct {
	Size       int64     `json:"size"` // tamaño del archivo cuando se analizó: si cambia, el análisis ya no vale
	Version    int       `json:"version"`
	Duration   float64   `json:"duration"`
	BPM        float64   `json:"bpm"`
	BpmConf    float64   `json:"bpmConf"`          // 0..1: seguridad del BPM (bajo = conviene revisarlo)
	BpmAlt     []float64 `json:"bpmAlt,omitempty"` // otros tempos plausibles
	BeatOffset float64   `json:"beatOffset"`       // segundos hasta el primer pulso
	KeyPC      int       `json:"keyPc"`
	KeyMinor   bool      `json:"keyMinor"`
	KeyConf    float64   `json:"keyConf"`
	HasKey     bool      `json:"hasKey"`
}

// Meta reúne el análisis y las ediciones del usuario.
type Meta struct {
	Analysis *Analysis `json:"analysis,omitempty"`
	// Rejilla editada a mano: si GridSet, manda sobre la detectada.
	GridSet    bool    `json:"gridSet"`
	GridBPM    float64 `json:"gridBpm"`
	GridOffset float64 `json:"gridOffset"`
	Cues       []Cue   `json:"cues"`
}

func (m Meta) empty() bool { return m.Analysis == nil && !m.GridSet && len(m.Cues) == 0 }

// Validate corrige o rechaza valores imposibles que vengan de la interfaz.
func Validate(m Meta) (Meta, error) {
	if m.GridSet {
		if m.GridBPM < 40 || m.GridBPM > 300 {
			return m, errors.New("el BPM de la rejilla debe estar entre 40 y 300")
		}
		if m.GridOffset < 0 || m.GridOffset > 60 {
			return m, errors.New("el primer pulso debe estar entre 0 y 60 s")
		}
	}
	if len(m.Cues) > MaxCues {
		return m, errors.New("demasiados cues en una canción")
	}
	seen := map[int]bool{}
	out := make([]Cue, 0, len(m.Cues))
	for _, c := range m.Cues {
		if c.Slot < -1 || c.Slot > 7 {
			return m, errors.New("hot cue fuera de rango (A-H)")
		}
		if c.TimeMs < 0 {
			return m, errors.New("tiempo de cue negativo")
		}
		if c.Slot >= 0 {
			if seen[c.Slot] {
				return m, errors.New("hot cue repetido")
			}
			seen[c.Slot] = true
		}
		if c.Color < 0 || c.Color > 7 {
			c.Color = 0
		}
		if r := []rune(c.Name); len(r) > 32 {
			c.Name = string(r[:32])
		}
		out = append(out, c)
	}
	m.Cues = out
	if m.Analysis != nil && m.Analysis.Version != AnalysisVersion {
		m.Analysis = nil
	}
	return m, nil
}

// Store persiste los metadatos en un JSON y las formas de onda en archivos binarios aparte.
type Store struct {
	path     string
	wavesDir string
	mu       sync.Mutex
	m        map[string]Meta
}

// Open lee el archivo (si falta o está corrupto, empieza vacío).
func Open(dir string) *Store {
	s := &Store{path: filepath.Join(dir, "trackmeta.json"), wavesDir: filepath.Join(dir, "waves"), m: map[string]Meta{}}
	if data, err := os.ReadFile(s.path); err == nil {
		var loaded map[string]Meta
		if json.Unmarshal(data, &loaded) == nil && loaded != nil {
			s.m = loaded
		}
	}
	return s
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s.m)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// All devuelve una copia de todos los metadatos.
func (s *Store) All() map[string]Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Meta, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Set guarda los metadatos de una canción (vacío = borrar la entrada).
func (s *Store) Set(rel string, m Meta) error {
	if rel == "" {
		return errors.New("ruta vacía")
	}
	m, err := Validate(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.empty() {
		delete(s.m, rel)
	} else {
		s.m[rel] = m
	}
	return s.save()
}

func (s *Store) waveFile(rel string, size int64) string {
	h := sha1.Sum([]byte(rel + "\x00" + strconv.FormatInt(size, 10)))
	return filepath.Join(s.wavesDir, hex.EncodeToString(h[:8])+".bin")
}

// SaveWave guarda la forma de onda ya calculada (3 bytes por cuadro).
func (s *Store) SaveWave(rel string, size int64, data []byte) error {
	if len(data) == 0 || len(data) > 8<<20 {
		return errors.New("forma de onda inválida")
	}
	if err := os.MkdirAll(s.wavesDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.waveFile(rel, size), data, 0o644)
}

// LoadWave devuelve la forma de onda guardada, o nil si no hay.
func (s *Store) LoadWave(rel string, size int64) []byte {
	data, err := os.ReadFile(s.waveFile(rel, size))
	if err != nil {
		return nil
	}
	return data
}

// Resize conserva el análisis y la forma de onda de una canción cuyo archivo se reescribió (cambió de tamaño).
func (s *Store) Resize(rel string, oldSize, newSize int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.m[rel]
	if !ok || m.Analysis == nil || m.Analysis.Size != oldSize {
		return nil
	}
	if data, err := os.ReadFile(s.waveFile(rel, oldSize)); err == nil {
		_ = os.WriteFile(s.waveFile(rel, newSize), data, 0o644)
		if oldSize != newSize {
			_ = os.Remove(s.waveFile(rel, oldSize))
		}
	}
	m.Analysis.Size = newSize
	s.m[rel] = m
	return s.save()
}

// Rename traslada los metadatos cuando una canción cambia de ruta (al moverla de playlist).
func (s *Store) Rename(oldRel, newRel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.m[oldRel]
	if !ok || oldRel == newRel {
		return nil
	}
	delete(s.m, oldRel)
	s.m[newRel] = m
	return s.save()
}

// Copy duplica los metadatos (análisis, rejilla, cues) y la forma de onda de una canción para su copia en otra ruta.
func (s *Store) Copy(srcRel, dstRel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.m[srcRel]
	if !ok || srcRel == dstRel {
		return nil
	}
	c := m
	c.Cues = append([]Cue(nil), m.Cues...)
	if m.Analysis != nil {
		a := *m.Analysis
		a.BpmAlt = append([]float64(nil), m.Analysis.BpmAlt...)
		c.Analysis = &a
		if data, err := os.ReadFile(s.waveFile(srcRel, a.Size)); err == nil {
			_ = os.WriteFile(s.waveFile(dstRel, a.Size), data, 0o644)
		}
	}
	s.m[dstRel] = c
	return s.save()
}

// Delete olvida una canción (al mandarla a la papelera).
func (s *Store) Delete(rel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[rel]; !ok {
		return nil
	}
	delete(s.m, rel)
	return s.save()
}
