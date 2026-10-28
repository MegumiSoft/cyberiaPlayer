package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"musicdl/src/backend/library"
	"musicdl/src/backend/match"
	"musicdl/src/backend/queue"
	"musicdl/src/backend/settings"
	"musicdl/src/backend/spotify"
	"musicdl/src/backend/trackmeta"
	"musicdl/src/backend/trash"
	"musicdl/src/backend/ytdlp"
)

const appDirName = "Cyberia"

// ToolStatus describe el estado de las herramientas externas para mostrarlo en la interfaz.
type ToolStatus struct {
	YtDlpPath    string `json:"ytDlpPath"`
	YtDlpVersion string `json:"ytDlpVersion"`
	YtDlpManaged bool   `json:"ytDlpManaged"` // true si es la copia propia de la app (se actualiza sola)
	FFmpegPath   string `json:"ffmpegPath"`
	FFmpegOK     bool   `json:"ffmpegOk"`
	Busy         bool   `json:"busy"`
	Message      string `json:"message"`
	Platform     string `json:"platform"`    // windows, darwin o linux
	RedirectURI  string `json:"redirectUri"` // URI que se registra en la app de Spotify
}

// PreviewResult es el resultado de analizar una URL: o hay Preview o hay Error.
type PreviewResult struct {
	URL     string         `json:"url"`
	Preview *ytdlp.Preview `json:"preview"`
	Error   string         `json:"error"`
}

// LibraryResult es lo que ve el reproductor: la carpeta, cómo pedir los archivos y las playlists.
type LibraryResult struct {
	Root      string             `json:"root"`
	BaseURL   string             `json:"baseUrl"`
	Playlists []library.Playlist `json:"playlists"`
}

type App struct {
	wails  context.Context // contexto de Wails (necesario para eventos y diálogos)
	ctx    context.Context // contexto propio: se cancela al cerrar la app
	cancel context.CancelFunc

	settings *settings.Store
	yt       *ytdlp.Client
	sp       *spotify.Client
	q        *queue.Manager
	meta     *trackmeta.Store
	dataDir  string

	libSrv    *library.Server
	libSrvErr error

	mu      sync.Mutex
	busy    bool
	message string
	version string
}

// ytResolver encuentra en YouTube la mejor coincidencia de una canción de Spotify.
type ytResolver struct{ yt *ytdlp.Client }

func splitArtists(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func (r ytResolver) Resolve(ctx context.Context, req queue.ResolveRequest) (string, error) {
	want := match.Want{Title: req.Title, Artists: splitArtists(req.Artist), Duration: req.Duration}
	entries, err := r.yt.Search(ctx, match.Query(want), 8)
	if err != nil {
		return "", err
	}
	cands := make([]match.Candidate, 0, len(entries))
	for _, e := range entries {
		cands = append(cands, match.Candidate{Title: e.Title, Uploader: e.Uploader, URL: e.URL, Duration: e.Duration})
	}
	best, _, ok := match.Best(cands, want)
	if !ok {
		return "", fmt.Errorf("sin coincidencia clara en YouTube para «%s» (no se descargó nada para evitar bajar otra canción)", match.Query(want))
	}
	return best.URL, nil
}

func NewApp() *App {
	cfgDir, err := os.UserConfigDir()
	if err != nil || cfgDir == "" {
		cfgDir = os.TempDir()
	}
	dir := filepath.Join(cfgDir, appDirName)
	// Las versiones anteriores guardaban sus datos en "MusicDL": conservarlos.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err := os.Stat(filepath.Join(cfgDir, "MusicDL")); err == nil {
			_ = os.Rename(filepath.Join(cfgDir, "MusicDL"), dir)
		}
	}

	a := &App{
		settings: settings.Load(filepath.Join(dir, "settings.json")),
		yt:       ytdlp.New(filepath.Join(dir, "bin")),
		sp:       spotify.New(),
		meta:     trackmeta.Open(dir),
		dataDir:  dir,
	}
	a.yt.SetFFmpegPath(a.settings.Get().FFmpegPath)
	a.yt.SetCookiesBrowser(a.settings.Get().CookiesBrowser)
	a.q = queue.New(a.yt, a.settings.Get, filepath.Join(dir, "queue.json"), a.emitQueue)
	a.q.SetResolver(ytResolver{yt: a.yt})
	return a
}

func (a *App) startup(ctx context.Context) {
	a.wails = ctx
	a.ctx, a.cancel = context.WithCancel(ctx)
	// Restos de un cierre brusco o de una recarga de la interfaz a media decodificación: nada los está usando todavía.
	_ = os.RemoveAll(a.pcmDir())
	a.libSrv, a.libSrvErr = library.StartServer(func() string { return a.settings.Get().OutDir })
	a.q.Pause() // no arrancar la cola guardada hasta confirmar que yt-dlp está disponible
	a.q.Start(a.ctx)
	go a.bootstrap()
}

func (a *App) shutdown(ctx context.Context) {
	a.cancel()
	_ = os.RemoveAll(a.pcmDir())
	a.q.Wait(5 * time.Second)
	if a.libSrv != nil {
		_ = a.libSrv.Close()
	}
}

// bootstrap instala yt-dlp la primera vez y lo actualiza en los arranques siguientes.
func (a *App) bootstrap() {
	_, managed, err := a.yt.Resolve()
	switch {
	case errors.Is(err, ytdlp.ErrNotInstalled):
		a.setBusy(true, "Descargando yt-dlp…")
		if ierr := a.yt.Install(a.ctx); ierr != nil {
			a.setBusy(false, "No se pudo instalar yt-dlp: "+ierr.Error())
			return
		}
	case managed && a.settings.Get().AutoUpdate:
		a.setBusy(true, "Buscando actualizaciones de yt-dlp…")
		_, _ = a.yt.Update(a.ctx) // si falla (sin internet) seguimos con la versión actual
	}
	a.refreshVersion()
	a.setBusy(false, "")
	if _, _, err := a.yt.Resolve(); err == nil {
		a.q.Resume()
	}
}

func (a *App) setBusy(busy bool, msg string) {
	a.mu.Lock()
	a.busy, a.message = busy, msg
	a.mu.Unlock()
	a.emitStatus()
}

func (a *App) refreshVersion() {
	v, _ := a.yt.Version(a.ctx)
	a.mu.Lock()
	a.version = v
	a.mu.Unlock()
}

func (a *App) emitStatus() {
	if a.wails != nil {
		wruntime.EventsEmit(a.wails, "status", a.Status())
	}
}

func (a *App) emitQueue(jobs []queue.Job) {
	if a.wails != nil {
		wruntime.EventsEmit(a.wails, "queue", jobs)
	}
}

// ---------------------------------------------------------------------------
// Métodos expuestos a la interfaz (Wails los publica como window.go.main.App.*)
// ---------------------------------------------------------------------------

func (a *App) Status() ToolStatus {
	a.mu.Lock()
	st := ToolStatus{Busy: a.busy, Message: a.message, YtDlpVersion: a.version, Platform: runtime.GOOS, RedirectURI: spotify.RedirectURI()}
	a.mu.Unlock()
	if p, managed, err := a.yt.Resolve(); err == nil {
		st.YtDlpPath, st.YtDlpManaged = p, managed
	} else {
		st.YtDlpVersion = ""
	}
	st.FFmpegPath, st.FFmpegOK = a.yt.FFmpeg()
	return st
}

// publicSettings oculta el token de sesión de Spotify: la interfaz solo necesita saber quién inició sesión.
func publicSettings(s settings.Settings) settings.Settings {
	s.SpotifyRefreshToken = ""
	return s
}

func (a *App) GetSettings() settings.Settings { return publicSettings(a.settings.Get()) }

func (a *App) SaveSettings(s settings.Settings) (settings.Settings, error) {
	cur := a.settings.Get()
	// La sesión de Spotify solo la cambian Login/Logout, nunca el formulario...
	s.SpotifyRefreshToken, s.SpotifyUser = cur.SpotifyRefreshToken, cur.SpotifyUser
	// ...salvo que cambie el Client ID: el token pertenece a esa app y deja de servir.
	if strings.TrimSpace(s.SpotifyClientID) != cur.SpotifyClientID {
		s.SpotifyRefreshToken, s.SpotifyUser = "", ""
	}
	saved, err := a.settings.Set(s)
	if err != nil {
		return a.GetSettings(), err
	}
	a.yt.SetFFmpegPath(saved.FFmpegPath)
	a.yt.SetCookiesBrowser(saved.CookiesBrowser)
	a.q.Kick() // por si subió la concurrencia
	a.emitStatus()
	return publicSettings(saved), nil
}

// ChooseFolder abre el selector de carpetas del sistema. Devuelve "" si se cancela.
func (a *App) ChooseFolder() (string, error) {
	return wruntime.OpenDirectoryDialog(a.wails, wruntime.OpenDialogOptions{
		Title:            "Carpeta de la biblioteca",
		DefaultDirectory: a.settings.Get().OutDir,
	})
}

// ChooseFFmpeg abre el selector de archivos para indicar dónde está ffmpeg.
func (a *App) ChooseFFmpeg() (string, error) {
	return wruntime.OpenFileDialog(a.wails, wruntime.OpenDialogOptions{Title: "Selecciona el ejecutable de ffmpeg"})
}

// CopyText pone texto en el portapapeles del sistema (el webview de macOS no siempre deja hacerlo desde JS).
func (a *App) CopyText(text string) error {
	return wruntime.ClipboardSetText(a.wails, text)
}

// OpenDownloads abre la carpeta de la biblioteca en el explorador de archivos.
func (a *App) OpenDownloads() error {
	dir := a.settings.Get().OutDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return openWithSystem(dir)
}

// RevealFile muestra un archivo descargado en el explorador (o su carpeta si no se puede resaltar).
func (a *App) RevealFile(path string) error {
	if path == "" {
		return errors.New("ruta vacía")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("el archivo ya no existe: %s", path)
	}
	switch runtime.GOOS {
	case "darwin":
		return startDetached(exec.Command("open", "-R", path))
	case "windows":
		return startDetached(exec.Command("explorer", "/select,"+path))
	default:
		return openWithSystem(filepath.Dir(path))
	}
}

func openWithSystem(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return startDetached(exec.Command("open", path))
	case "windows":
		return startDetached(exec.Command("explorer", path))
	default:
		return startDetached(exec.Command("xdg-open", path))
	}
}

func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // evita procesos zombi; explorer.exe devuelve 1 aunque funcione
	return nil
}

// UpdateTools actualiza (o instala) yt-dlp y devuelve el mensaje de resultado.
func (a *App) UpdateTools() (string, error) {
	a.setBusy(true, "Actualizando yt-dlp…")
	msg, err := a.yt.Update(a.ctx)
	a.refreshVersion()
	a.setBusy(false, "")
	if err != nil {
		return msg, err
	}
	a.q.Resume()
	return msg, nil
}

// ---------------------------------------------------------------------------
// Biblioteca / reproductor
// ---------------------------------------------------------------------------

// Library escanea la carpeta de música: cada carpeta con audio es una playlist.
func (a *App) Library() (LibraryResult, error) {
	if a.libSrvErr != nil {
		return LibraryResult{}, fmt.Errorf("no se pudo iniciar el servidor de audio: %w", a.libSrvErr)
	}
	if a.libSrv == nil {
		return LibraryResult{}, errors.New("el servidor de audio aún no está listo")
	}
	root := a.settings.Get().OutDir
	pls, err := library.Scan(root)
	if err != nil {
		return LibraryResult{}, err
	}
	if pls == nil {
		pls = []library.Playlist{}
	}
	return LibraryResult{Root: root, BaseURL: a.libSrv.BaseURL(), Playlists: pls}, nil
}

// CreatePlaylist crea una carpeta nueva en la biblioteca (una playlist vacía) y devuelve su ruta.
func (a *App) CreatePlaylist(name string) (string, error) {
	return library.CreateFolder(a.settings.Get().OutDir, name)
}

// MoveTrack mueve una canción (ruta relativa) a otra playlist; destDir "" es la raíz. Devuelve la nueva ruta.
func (a *App) MoveTrack(rel, destDir string) (string, error) {
	nuevo, err := library.MoveTrack(a.settings.Get().OutDir, rel, destDir)
	if err == nil {
		_ = a.meta.Rename(rel, filepath.ToSlash(nuevo)) // los cues y la rejilla viajan con la canción
	}
	return nuevo, err
}

// CopyTrack copia una canción a otra playlist y la deja también en la original (con sus cues y rejilla).
func (a *App) CopyTrack(rel, destDir string) (string, error) {
	nuevo, err := library.CopyTrack(a.settings.Get().OutDir, rel, destDir, "")
	if err == nil {
		_ = a.meta.Copy(rel, filepath.ToSlash(nuevo))
	}
	return nuevo, err
}

// SetResult es lo que pasó al crear una playlist a partir de un set.
type SetResult struct {
	Folder  string   `json:"folder"`
	Copied  int      `json:"copied"`
	Skipped []string `json:"skipped"`
}

// setPrefix es SOLO el prefijo «NN - » que esta misma función escribe (y que también ponen las descargas de playlists).
// Es a propósito estricto: títulos como «24K Magic», «808s», «4 Minutes», «99 Luftballons» o «50 Cent - In Da Club»
// empiezan con números que forman parte del nombre y no deben tocarse.
var setPrefix = regexp.MustCompile(`^\d{1,3} - `)

// CreatePlaylistFromSet crea una playlist nueva (carpeta) con COPIAS de las canciones del set, en el orden del set:
// los archivos se numeran «01 - », «02 - »… para que la lista respete ese orden. Los originales no se tocan.
func (a *App) CreatePlaylistFromSet(name string, rels []string) (SetResult, error) {
	var res SetResult
	if len(rels) == 0 {
		return res, errors.New("el set está vacío")
	}
	root := a.settings.Get().OutDir
	folder, err := library.CreateFolder(root, name)
	if err != nil {
		return res, err
	}
	res.Folder = folder
	width := len(strconv.Itoa(len(rels)))
	if width < 2 {
		width = 2
	}
	for i, rel := range rels {
		base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		if stripped := strings.TrimSpace(setPrefix.ReplaceAllString(base, "")); stripped != "" {
			base = stripped
		}
		newName := fmt.Sprintf("%0*d - %s", width, i+1, base) + path.Ext(rel)
		nuevo, err := library.CopyTrack(root, rel, folder, newName)
		if err != nil {
			res.Skipped = append(res.Skipped, path.Base(rel)+": "+err.Error())
			continue
		}
		_ = a.meta.Copy(rel, filepath.ToSlash(nuevo))
		res.Copied++
	}
	if res.Copied == 0 {
		_ = os.Remove(filepath.Join(root, folder)) // carpeta vacía: no dejarla
		return res, errors.New("no se pudo copiar ninguna canción")
	}
	return res, nil
}

// ReadTrackChunk entrega un trozo (hasta 4 MB) de una canción: así el reproductor puede mostrar el progreso
// de carga y la interfaz no se congela con archivos grandes.
func (a *App) ReadTrackChunk(rel string, offset int64, length int) ([]byte, error) {
	return library.ReadChunk(a.settings.Get().OutDir, rel, offset, length)
}

// ---------------------------------------------------------------------------
// Metadatos por canción (análisis, rejilla, cues)
// ---------------------------------------------------------------------------

// TrackMeta devuelve los metadatos de todas las canciones, por ruta relativa.
func (a *App) TrackMeta() map[string]trackmeta.Meta { return a.meta.All() }

// SaveTrackMeta guarda el análisis y las ediciones (rejilla, cues) de una canción.
func (a *App) SaveTrackMeta(rel string, m trackmeta.Meta) error { return a.meta.Set(rel, m) }

// SaveWave guarda la forma de onda calculada de una canción.
func (a *App) SaveWave(rel string, size int64, data []byte) error {
	return a.meta.SaveWave(rel, size, data)
}

// LoadWave devuelve la forma de onda guardada (vacío si no hay).
func (a *App) LoadWave(rel string, size int64) []byte { return a.meta.LoadWave(rel, size) }

// TrashTrack manda una canción a la papelera del sistema (se puede recuperar desde ahí).
func (a *App) TrashTrack(rel string) error {
	full, err := library.ResolveFile(a.settings.Get().OutDir, rel)
	if err != nil {
		return err
	}
	if err := trash.Move(full); err != nil {
		return err
	}
	_ = a.meta.Delete(rel)
	return nil
}

// ---------------------------------------------------------------------------
// Spotify
// ---------------------------------------------------------------------------

func (a *App) spotifyCreds() spotify.Creds {
	s := a.settings.Get()
	return spotify.Creds{ClientID: s.SpotifyClientID, ClientSecret: s.SpotifyClientSecret, RefreshToken: s.SpotifyRefreshToken}
}

// SpotifyTest comprueba las credenciales (o la sesión, si hay una iniciada).
func (a *App) SpotifyTest() (string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.sp.Test(ctx, a.spotifyCreds())
}

// SpotifyLogin abre el navegador para que el usuario autorice la app y guarda la sesión.
func (a *App) SpotifyLogin() (string, error) {
	s := a.settings.Get()
	refresh, err := a.sp.Login(a.ctx, s.SpotifyClientID, spotify.RedirectPort, 3*time.Minute, func(u string) {
		wruntime.BrowserOpenURL(a.wails, u)
	})
	if err != nil {
		return "", err
	}
	cr := spotify.Creds{ClientID: s.SpotifyClientID, RefreshToken: refresh}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	name, nerr := a.sp.DisplayName(ctx, cr)
	if nerr != nil {
		name = "Spotify"
	}
	cur := a.settings.Get()
	cur.SpotifyRefreshToken, cur.SpotifyUser = refresh, name
	if _, err := a.settings.Set(cur); err != nil {
		return "", err
	}
	a.emitStatus()
	return name, nil
}

// SpotifyLogout borra la sesión guardada.
func (a *App) SpotifyLogout() error {
	cur := a.settings.Get()
	cur.SpotifyRefreshToken, cur.SpotifyUser = "", ""
	_, err := a.settings.Set(cur)
	return err
}

// spotifyPreview convierte una colección de Spotify en la misma estructura que usa la vista previa.
func (a *App) spotifyPreview(raw string) (*ytdlp.Preview, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	col, err := a.sp.Fetch(ctx, a.spotifyCreds(), raw)
	if err != nil {
		return nil, err
	}
	title := col.Name
	if col.Kind == "album" && col.Owner != "" {
		title = col.Owner + " - " + col.Name // la carpeta del álbum incluye al artista
	}
	p := &ytdlp.Preview{Source: "spotify", SourceURL: raw, Title: title, Uploader: col.Owner, IsPlaylist: col.Kind != "track"}
	for _, t := range col.Tracks {
		p.Entries = append(p.Entries, ytdlp.Entry{
			ID: t.ID, Title: t.Title, Uploader: t.Artist(), URL: t.URL, Thumbnail: t.Cover,
			Duration: float64(t.DurationMs) / 1000, Index: t.Number,
		})
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Vista previa y cola
// ---------------------------------------------------------------------------

// PreviewURLs analiza varias URLs (tracks o playlists) sin descargar nada, de a 3 en paralelo.
// El resultado conserva el orden de entrada.
func (a *App) PreviewURLs(urls []string) []PreviewResult {
	results := make([]PreviewResult, len(urls))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, raw := range urls {
		u := strings.TrimSpace(raw)
		results[i].URL = u
		if u == "" {
			results[i].Error = "URL vacía"
			continue
		}
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var p *ytdlp.Preview
			var err error
			if spotify.IsSpotifyURL(u) {
				p, err = a.spotifyPreview(u)
			} else {
				p, err = a.yt.Preview(a.ctx, u)
			}
			if err != nil {
				results[i].Error = err.Error()
				return
			}
			results[i].Preview = p
		}(i, u)
	}
	wg.Wait()
	return results
}

// Enqueue añade canciones a la cola y devuelve cuántas se agregaron realmente.
func (a *App) Enqueue(items []queue.NewJob) int { return len(a.q.Add(items)) }

func (a *App) Queue() []queue.Job      { return a.q.Snapshot() }
func (a *App) CancelJob(id string)     { a.q.Cancel(id) }
func (a *App) RetryJob(id string) bool { return a.q.Retry(id) }
func (a *App) RetryFailed() int        { return a.q.RetryFailed() }
func (a *App) RemoveJob(id string)     { a.q.Remove(id) }
func (a *App) ClearFinished()          { a.q.ClearFinished() }

// ToggleFullscreen alterna pantalla completa (macOS) o ventana maximizada (Windows/Linux). Devuelve si quedó en grande.
func (a *App) ToggleFullscreen() bool {
	if a.wails == nil {
		return false
	}
	if runtime.GOOS == "darwin" {
		if wruntime.WindowIsFullscreen(a.wails) {
			wruntime.WindowUnfullscreen(a.wails)
			return false
		}
		wruntime.WindowFullscreen(a.wails)
		return true
	}
	wruntime.WindowToggleMaximise(a.wails)
	return wruntime.WindowIsMaximised(a.wails)
}
