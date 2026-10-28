// Package settings guarda y valida la configuración de la app en un JSON.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Formatos de audio permitidos.
var Formats = []string{"mp3", "m4a", "opus", "flac", "wav"}

// Calidades permitidas ("0" = la mejor, VBR). Se ignoran en formatos sin pérdida.
var Qualities = []string{"0", "320K", "256K", "192K", "128K"}

const (
	MinConcurrency = 1
	MaxConcurrency = 6
)

type Settings struct {
	OutDir      string `json:"outDir"`
	Format      string `json:"format"`
	Quality     string `json:"quality"`
	Concurrency int    `json:"concurrency"`
	FFmpegPath  string `json:"ffmpegPath"`
	AutoUpdate  bool   `json:"autoUpdate"`
	Character   string `json:"character"` // mascota que baila en el visualizador: "miku", "gir", "cat" o "none"
	// CookiesBrowser: navegador del que yt-dlp toma las cookies para YouTube ("" = ninguno).
	CookiesBrowser string `json:"cookiesBrowser"`
	// KeyNotation: "camelot" (8A) o "note" (A menor).
	KeyNotation string `json:"keyNotation"`

	// Spotify: cada persona usa su propia app de desarrollador. Se guardan en texto plano en el
	// archivo de ajustes de su usuario (no salen de su equipo).
	SpotifyClientID     string `json:"spotifyClientId"`
	SpotifyClientSecret string `json:"spotifyClientSecret"`
	SpotifyRefreshToken string `json:"spotifyRefreshToken"`
	SpotifyUser         string `json:"spotifyUser"` // nombre mostrado de la sesión iniciada
}

// Characters son las mascotas disponibles para el visualizador.
var Characters = []string{"miku", "gir", "cat", "none"}

// CookiesBrowsers son los navegadores admitidos para leer cookies.
var CookiesBrowsers = []string{"chrome", "firefox", "brave", "edge", "chromium", "opera", "vivaldi"}

// Defaults devuelve la configuración inicial: carpeta Descargas del usuario, MP3 de la mejor calidad.
func Defaults() Settings {
	home, _ := os.UserHomeDir()
	return Settings{
		OutDir:      filepath.Join(home, "Downloads"),
		Format:      "mp3",
		Quality:     "0",
		Concurrency: 2,
		AutoUpdate:  true,
		Character:   "miku",
		KeyNotation: "camelot",
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Normalize corrige valores inválidos usando los defaults y devuelve un error
// solo cuando el valor no se puede arreglar de forma razonable.
func Normalize(s Settings) (Settings, error) {
	d := Defaults()
	s.OutDir = strings.TrimSpace(s.OutDir)
	if s.OutDir == "" {
		s.OutDir = d.OutDir
	}
	if !filepath.IsAbs(s.OutDir) {
		return s, errors.New("la carpeta de destino debe ser una ruta absoluta")
	}
	if !contains(Formats, s.Format) {
		s.Format = d.Format
	}
	if !contains(Qualities, s.Quality) {
		s.Quality = d.Quality
	}
	if s.Concurrency < MinConcurrency {
		s.Concurrency = MinConcurrency
	}
	if s.Concurrency > MaxConcurrency {
		s.Concurrency = MaxConcurrency
	}
	if !contains(Characters, s.Character) {
		s.Character = d.Character
	}
	s.CookiesBrowser = strings.ToLower(strings.TrimSpace(s.CookiesBrowser))
	if s.CookiesBrowser != "" && !contains(CookiesBrowsers, s.CookiesBrowser) {
		s.CookiesBrowser = ""
	}
	if s.KeyNotation != "camelot" && s.KeyNotation != "note" {
		s.KeyNotation = d.KeyNotation
	}
	s.FFmpegPath = strings.TrimSpace(s.FFmpegPath)
	s.SpotifyClientID = strings.TrimSpace(s.SpotifyClientID)
	s.SpotifyClientSecret = strings.TrimSpace(s.SpotifyClientSecret)
	s.SpotifyRefreshToken = strings.TrimSpace(s.SpotifyRefreshToken)
	s.SpotifyUser = strings.TrimSpace(s.SpotifyUser)
	return s, nil
}

// Store es un contenedor seguro para concurrencia que persiste en disco.
type Store struct {
	path string
	mu   sync.RWMutex
	s    Settings
}

// Load lee el archivo (si existe). Si no existe o está corrupto usa los defaults.
func Load(path string) *Store {
	st := &Store{path: path, s: Defaults()}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	loaded := Defaults() // los campos ausentes en el JSON conservan su default
	if err := json.Unmarshal(data, &loaded); err != nil {
		return st
	}
	if n, err := Normalize(loaded); err == nil {
		st.s = n
	}
	return st
}

func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// Set valida, guarda en memoria y escribe a disco. Devuelve los valores finales.
func (st *Store) Set(s Settings) (Settings, error) {
	n, err := Normalize(s)
	if err != nil {
		return st.Get(), err
	}
	st.mu.Lock()
	st.s = n
	st.mu.Unlock()
	return n, st.save(n)
}

func (st *Store) save(s Settings) error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	// 0600: el archivo guarda credenciales de Spotify; que no lo lean otros usuarios del equipo.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}
