// Package ytdlp envuelve el binario de yt-dlp: lo localiza o lo descarga,
// detecta ffmpeg, lista el contenido de playlists y descarga canciones.
package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var ErrNotInstalled = errors.New("yt-dlp no está instalado")

// DownloadBase es la URL base de los binarios oficiales. Es una variable para poder cambiarla en tests.
var DownloadBase = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/"

type Client struct {
	// Dir es la carpeta donde la app guarda su propia copia de yt-dlp (y opcionalmente ffmpeg).
	Dir string

	mu          sync.Mutex
	ffmpegPath  string // ruta fijada por el usuario en Ajustes
	cookiesFrom string // navegador del que leer cookies ("" = ninguno)
	jsRuntime   string // "node" o "deno" si hay uno y este yt-dlp lo soporta
	jsChecked   bool
	binOverride string // solo para tests
}

func New(dir string) *Client { return &Client{Dir: dir} }

// SetFFmpegPath fija una ruta de ffmpeg elegida por el usuario ("" para autodetectar).
func (c *Client) SetFFmpegPath(p string) {
	c.mu.Lock()
	c.ffmpegPath = strings.TrimSpace(p)
	c.mu.Unlock()
}

// Navegadores aceptados por --cookies-from-browser.
var CookieBrowsers = []string{"chrome", "firefox", "brave", "edge", "chromium", "opera", "vivaldi"}

// SetCookiesBrowser elige el navegador del que yt-dlp leerá las cookies ("" para no usar ninguna).
func (c *Client) SetCookiesBrowser(b string) {
	b = strings.ToLower(strings.TrimSpace(b))
	ok := false
	for _, x := range CookieBrowsers {
		if x == b {
			ok = true
		}
	}
	if !ok {
		b = ""
	}
	c.mu.Lock()
	c.cookiesFrom = b
	c.mu.Unlock()
}

// commonArgs son las opciones que se añaden a toda llamada que habla con YouTube:
// cookies del navegador (evita "confirma que no eres un bot") y un runtime de JavaScript,
// que yt-dlp necesita hoy para resolver los retos de YouTube.
func (c *Client) commonArgs(ctx context.Context, path string) []string {
	c.mu.Lock()
	browser := c.cookiesFrom
	checked := c.jsChecked
	c.mu.Unlock()
	if !checked {
		rt := ""
		if help, err := c.command(ctx, path, "--ignore-config", "--help").Output(); err == nil && strings.Contains(string(help), "--js-runtimes") {
			for _, name := range []string{"deno", "node"} {
				if _, ok := findTool(name); ok {
					rt = name
					break
				}
			}
		}
		c.mu.Lock()
		c.jsRuntime, c.jsChecked = rt, true
		c.mu.Unlock()
	}
	c.mu.Lock()
	rt := c.jsRuntime
	c.mu.Unlock()
	var args []string
	if browser != "" {
		args = append(args, "--cookies-from-browser", browser)
	}
	if rt != "" {
		if p, ok := findTool(rt); ok {
			args = append(args, "--js-runtimes", rt+":"+p)
		}
	}
	return args
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// assetName devuelve el nombre del binario oficial de yt-dlp para un sistema.
func assetName(goos, goarch string) (string, error) {
	switch goos {
	case "windows":
		if goarch == "arm64" {
			return "yt-dlp_arm64.exe", nil
		}
		return "yt-dlp.exe", nil
	case "darwin":
		return "yt-dlp_macos", nil // binario universal (Intel + Apple Silicon)
	case "linux":
		if goarch == "arm64" {
			return "yt-dlp_linux_aarch64", nil
		}
		return "yt-dlp_linux", nil
	}
	return "", fmt.Errorf("sistema no soportado: %s/%s", goos, goarch)
}

func (c *Client) managedPath() string { return filepath.Join(c.Dir, exeName("yt-dlp")) }

// searchDirs lista carpetas donde suelen vivir las herramientas. Es necesario porque las apps
// gráficas de macOS arrancan con un PATH mínimo que no incluye Homebrew.
func searchDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	case "windows":
		var dirs []string
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "Microsoft", "WinGet", "Links"))
		}
		dirs = append(dirs,
			filepath.Join(home, "scoop", "shims"),
			`C:\ProgramData\chocolatey\bin`,
			`C:\ffmpeg\bin`,
			`C:\Program Files\ffmpeg\bin`,
		)
		return dirs
	default:
		return []string{"/usr/local/bin", "/usr/bin", "/snap/bin", filepath.Join(home, ".local", "bin")}
	}
}

func findTool(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, d := range searchDirs() {
		p := filepath.Join(d, exeName(name))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Resolve devuelve el yt-dlp a usar. Prefiere la copia gestionada por la app porque es la que
// se puede actualizar sola; si no existe usa la del sistema.
func (c *Client) Resolve() (path string, managed bool, err error) {
	c.mu.Lock()
	override := c.binOverride
	c.mu.Unlock()
	if override != "" {
		return override, false, nil
	}
	if st, e := os.Stat(c.managedPath()); e == nil && !st.IsDir() {
		return c.managedPath(), true, nil
	}
	if p, ok := findTool("yt-dlp"); ok {
		return p, false, nil
	}
	return "", false, ErrNotInstalled
}

// FFmpeg devuelve la ruta de ffmpeg si se encuentra.
func (c *Client) FFmpeg() (string, bool) {
	c.mu.Lock()
	user := c.ffmpegPath
	c.mu.Unlock()
	if user != "" {
		if st, err := os.Stat(user); err == nil && !st.IsDir() {
			return user, true
		}
		return "", false // el usuario fijó una ruta inválida: no adivinar otra en silencio
	}
	own := filepath.Join(c.Dir, exeName("ffmpeg"))
	if st, err := os.Stat(own); err == nil && !st.IsDir() {
		return own, true
	}
	return findTool("ffmpeg")
}

// Install descarga la última versión oficial de yt-dlp a la carpeta de la app.
func (c *Client) Install(ctx context.Context) error {
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DownloadBase+asset, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo descargar yt-dlp: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("no se pudo descargar yt-dlp: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(c.Dir, "yt-dlp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op si el rename tuvo éxito
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("descarga de yt-dlp interrumpida: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, c.managedPath())
}

// Update actualiza yt-dlp. Si usamos nuestra copia, ejecuta `yt-dlp -U`; si no hay copia propia
// (no hay yt-dlp, o solo existe el del sistema) instala una propia, que a partir de ahí tiene prioridad.
func (c *Client) Update(ctx context.Context) (string, error) {
	c.mu.Lock()
	c.jsChecked = false
	c.mu.Unlock()
	path, managed, err := c.Resolve()
	if err != nil && !errors.Is(err, ErrNotInstalled) {
		return "", err
	}
	if err != nil || !managed {
		if err := c.Install(ctx); err != nil {
			return "", err
		}
		return "Se instaló la última versión de yt-dlp.", nil
	}
	cmd := c.command(ctx, path, "--ignore-config", "-U")
	out, runErr := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if runErr != nil {
		return msg, fmt.Errorf("no se pudo actualizar yt-dlp: %w", runErr)
	}
	return msg, nil
}

// Version devuelve la versión de yt-dlp en uso.
func (c *Client) Version(ctx context.Context) (string, error) {
	path, _, err := c.Resolve()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := c.command(ctx, path, "--ignore-config", "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// command crea un proceso con el entorno y la configuración de plataforma correctos
// (UTF-8 en las tuberías, sin ventana de consola en Windows, cancelación que mata el árbol).
func (c *Client) command(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	configure(cmd)
	return cmd
}

func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("URL no válida: %q", raw)
	}
	return nil
}

// tailBuffer guarda solo los últimos bytes escritos (para mostrar el final de stderr en errores).
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 8192

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = t.buf[len(t.buf)-tailMax:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// errorMessage resume el stderr de yt-dlp en una frase útil para mostrar al usuario.
func errorMessage(stderr string) string {
	var errs, all []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		all = append(all, line)
		if strings.HasPrefix(line, "ERROR:") {
			errs = append(errs, strings.TrimSpace(strings.TrimPrefix(line, "ERROR:")))
		}
	}
	msg := ""
	switch {
	case len(errs) > 0:
		if len(errs) > 2 {
			errs = errs[len(errs)-2:]
		}
		msg = strings.Join(errs, " | ")
	case len(all) > 0:
		msg = all[len(all)-1]
	default:
		msg = "yt-dlp terminó con error"
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "ffmpeg") && (strings.Contains(lower, "not found") || strings.Contains(lower, "not installed")) {
		return "ffmpeg no encontrado. Instálalo (brew install ffmpeg / winget install ffmpeg) o indica su ruta en Ajustes."
	}
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return msg
}

// ConfigureCmd prepara un proceso auxiliar (ffmpeg) como los de yt-dlp: sin ventana de consola en Windows y
// cancelable por contexto.
func ConfigureCmd(cmd *exec.Cmd) { configure(cmd) }
