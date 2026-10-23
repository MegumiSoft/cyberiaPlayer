package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Format != "mp3" || d.Quality != "0" || d.Concurrency != 2 || !d.AutoUpdate {
		t.Errorf("defaults inesperados: %+v", d)
	}
	if filepath.Base(d.OutDir) != "Downloads" {
		t.Errorf("la carpeta por defecto debe ser Downloads: %q", d.OutDir)
	}
}

func TestNormalize(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "musica")
	got, err := Normalize(Settings{OutDir: "  " + abs + "  ", Format: "ogg", Quality: "999K", Concurrency: 99, FFmpegPath: "  /x/ffmpeg "})
	if err != nil {
		t.Fatal(err)
	}
	if got.OutDir != abs || got.Format != "mp3" || got.Quality != "0" || got.Concurrency != MaxConcurrency || got.FFmpegPath != "/x/ffmpeg" {
		t.Errorf("normalizado: %+v", got)
	}
	if got, _ := Normalize(Settings{OutDir: abs, Format: "flac", Quality: "192K", Concurrency: -3}); got.Format != "flac" || got.Quality != "192K" || got.Concurrency != MinConcurrency {
		t.Errorf("valores válidos deben conservarse: %+v", got)
	}
	if got, err := Normalize(Settings{}); err != nil || got.OutDir != Defaults().OutDir {
		t.Errorf("carpeta vacía debe usar el default: %+v err=%v", got, err)
	}
	if _, err := Normalize(Settings{OutDir: "relativa/ruta"}); err == nil {
		t.Error("una ruta relativa debe rechazarse")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "settings.json")
	st := Load(path) // no existe: defaults
	if st.Get() != Defaults() {
		t.Errorf("sin archivo debe devolver los defaults: %+v", st.Get())
	}
	out := filepath.Join(t.TempDir(), "mis canciones")
	saved, err := st.Set(Settings{OutDir: out, Format: "m4a", Quality: "256K", Concurrency: 4, AutoUpdate: false})
	if err != nil {
		t.Fatal(err)
	}
	reloaded := Load(path).Get()
	if reloaded != saved || reloaded.Format != "m4a" || reloaded.AutoUpdate {
		t.Errorf("recargado: %+v; guardado: %+v", reloaded, saved)
	}
	if _, err := st.Set(Settings{OutDir: "relativa"}); err == nil {
		t.Error("Set con ruta relativa debe fallar")
	}
	if st.Get() != saved {
		t.Error("un Set inválido no debe cambiar la configuración")
	}
}

func TestSpotifyFieldsRoundTripAndTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	st := Load(path)
	out := filepath.Join(t.TempDir(), "musica")
	saved, err := st.Set(Settings{OutDir: out, Format: "mp3", Quality: "0", Concurrency: 2,
		SpotifyClientID: "  abc  ", SpotifyClientSecret: " sec\n", SpotifyRefreshToken: " rt ", SpotifyUser: " Santi "})
	if err != nil {
		t.Fatal(err)
	}
	if saved.SpotifyClientID != "abc" || saved.SpotifyClientSecret != "sec" || saved.SpotifyRefreshToken != "rt" || saved.SpotifyUser != "Santi" {
		t.Errorf("no se recortaron: %+v", saved)
	}
	if got := Load(path).Get(); got != saved {
		t.Errorf("recargado %+v != %+v", got, saved)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("permisos = %v; want 0600", info.Mode().Perm())
	}
}

func TestLoadCorruptFileFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{no es json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Load(path).Get() != Defaults() {
		t.Error("un archivo corrupto debe volver a los defaults")
	}
}

func TestLoadMissingFieldsKeepDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"format":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load(path).Get()
	if got.Format != "opus" || got.Concurrency != 2 || !got.AutoUpdate {
		t.Errorf("los campos ausentes deben conservar su default: %+v", got)
	}
}

func TestNormalizeCookiesBrowser(t *testing.T) {
	for in, want := range map[string]string{"": "", "chrome": "chrome", "Firefox": "firefox", "safari": "", "netscape": ""} {
		s := Defaults()
		s.CookiesBrowser = in
		got, _ := Normalize(s)
		if got.CookiesBrowser != want {
			t.Errorf("%q -> %q, quería %q", in, got.CookiesBrowser, want)
		}
	}
}

func TestNormalizeCharacter(t *testing.T) {
	base := Defaults()
	for in, want := range map[string]string{"": "miku", "gir": "gir", "pumpkin": "miku", "cat": "cat", "none": "none", "miku": "miku", "dib": "miku"} {
		s := base
		s.Character = in
		got, err := Normalize(s)
		if err != nil || got.Character != want {
			t.Errorf("Character %q -> %q (%v), quería %q", in, got.Character, err, want)
		}
	}
}
