// Package queue gestiona la cola de descargas: un pool de workers con concurrencia
// configurable, cancelación, reintentos y persistencia para reanudar al reabrir la app.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"musicdl/src/backend/settings"
	"musicdl/src/backend/ytdlp"
)

type Status string

const (
	Queued      Status = "queued"
	Downloading Status = "downloading"
	Converting  Status = "converting"
	Done        Status = "done"
	Failed      Status = "failed"
	Canceled    Status = "canceled"
)

const (
	emitInterval = 150 * time.Millisecond // máximo de actualizaciones de progreso hacia la interfaz
	maxFinished  = 200                    // trabajos terminados que se conservan en el historial
)

// Job es una canción en la cola.
type Job struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Uploader  string    `json:"uploader"`
	Thumbnail string    `json:"thumbnail"`
	Folder    string    `json:"folder"`   // subcarpeta (nombre de la playlist); vacío para tracks sueltos
	Index     int       `json:"index"`    // posición en la playlist; 0 si no aplica
	Search    bool      `json:"search"`   // true: hay que buscar la canción en YouTube antes de bajarla (Spotify)
	Duration  float64   `json:"duration"` // segundos esperados (para emparejar la búsqueda); 0 si se desconoce
	OutName   string    `json:"outName"`  // nombre de archivo fijo (sin extensión); vacío = título del video
	Status    Status    `json:"status"`
	Percent   float64   `json:"percent"`
	Speed     float64   `json:"speed"` // bytes/s
	ETA       int       `json:"eta"`   // segundos; -1 desconocido
	Error     string    `json:"error"`
	FilePath  string    `json:"filePath"`
	AddedAt   time.Time `json:"addedAt"`
}

// NewJob es lo que envía la interfaz para encolar una canción.
type NewJob struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Uploader  string `json:"uploader"`
	Thumbnail string `json:"thumbnail"`
	Folder    string `json:"folder"`
	Index     int    `json:"index"`
	// Solo para canciones de Spotify, que no se bajan de ahí sino de la mejor coincidencia en YouTube:
	Search   bool    `json:"search"`
	Duration float64 `json:"duration"`
	OutName  string  `json:"outName"`
}

// Downloader es lo que la cola necesita de yt-dlp (permite probar con un doble).
type Downloader interface {
	Download(ctx context.Context, opts ytdlp.DownloadOptions, onProgress func(ytdlp.Progress)) (ytdlp.Result, error)
}

// ResolveRequest describe una canción conocida que hay que encontrar en otra fuente.
type ResolveRequest struct {
	Title    string
	Artist   string // artistas separados por coma
	Duration float64
}

// Resolver busca la mejor coincidencia de una canción y devuelve su URL.
type Resolver interface {
	Resolve(ctx context.Context, req ResolveRequest) (string, error)
}

type Manager struct {
	dl        Downloader
	resolver  Resolver
	settings  func() settings.Settings
	statePath string
	onChange  func([]Job)

	mu        sync.Mutex
	root      context.Context
	paused    bool
	jobs      []*Job
	running   map[string]context.CancelFunc
	canceling map[string]bool
	lastEmit  time.Time
	timerSet  bool
	emitCh    chan []Job
	wg        sync.WaitGroup
}

// New crea la cola. onChange recibe una copia de todos los trabajos cada vez que algo cambia
// (el progreso se limita a unas ~6 veces por segundo). statePath puede ser "" para no persistir.
func New(dl Downloader, get func() settings.Settings, statePath string, onChange func([]Job)) *Manager {
	return &Manager{
		dl:        dl,
		settings:  get,
		statePath: statePath,
		onChange:  onChange,
		running:   map[string]context.CancelFunc{},
		canceling: map[string]bool{},
		emitCh:    make(chan []Job, 1),
	}
}

// SetResolver define cómo se encuentran las canciones marcadas con Search. Debe llamarse antes de Start.
func (m *Manager) SetResolver(r Resolver) { m.resolver = r }

// Start carga el estado guardado y empieza a procesar. Cancelar ctx detiene las descargas en curso;
// esas canciones quedan en cola para el próximo arranque.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.root = ctx
	m.loadLocked()
	m.scheduleLocked()
	m.emitLocked()
	m.mu.Unlock()

	if m.onChange != nil {
		go func() {
			for {
				select {
				case snap := <-m.emitCh:
					m.onChange(snap)
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

// Wait espera a que terminen los procesos de descarga tras cancelar el contexto raíz.
func (m *Manager) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isActive(s Status) bool { return s == Queued || s == Downloading || s == Converting }

func (m *Manager) findLocked(id string) *Job {
	for _, j := range m.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (m *Manager) snapshotLocked() []Job {
	out := make([]Job, len(m.jobs))
	for i, j := range m.jobs {
		out[i] = *j
	}
	return out
}

// Snapshot devuelve una copia del estado actual.
func (m *Manager) Snapshot() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

// Add encola canciones. Se ignoran las que ya están en cola o descargándose.
func (m *Manager) Add(items []NewJob) []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	var added []Job
	for _, it := range items {
		u := strings.TrimSpace(it.URL)
		if u == "" || m.duplicateLocked(u, it.Folder) {
			continue
		}
		title := strings.TrimSpace(it.Title)
		if title == "" {
			title = u
		}
		j := &Job{
			ID: newID(), URL: u, Title: title, Uploader: it.Uploader, Thumbnail: it.Thumbnail,
			Folder: it.Folder, Index: it.Index, Search: it.Search, Duration: it.Duration, OutName: it.OutName,
			Status: Queued, ETA: -1, AddedAt: time.Now(),
		}
		m.jobs = append(m.jobs, j)
		added = append(added, *j)
	}
	if len(added) > 0 {
		m.saveLocked()
		m.emitLocked()
		m.scheduleLocked()
	}
	return added
}

func (m *Manager) duplicateLocked(url, folder string) bool {
	for _, j := range m.jobs {
		if j.URL == url && j.Folder == folder && isActive(j.Status) {
			return true
		}
	}
	return false
}

// Cancel detiene una descarga en curso o saca una canción de la cola.
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.findLocked(id)
	if j == nil || !isActive(j.Status) {
		return
	}
	if cancel, running := m.running[id]; running {
		m.canceling[id] = true
		cancel()
	}
	j.Status = Canceled
	j.Speed, j.ETA = 0, -1
	m.saveLocked()
	m.emitLocked()
}

// Retry vuelve a encolar una canción fallida o cancelada. Devuelve false si no se pudo.
func (m *Manager) Retry(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.findLocked(id)
	if j == nil || (j.Status != Failed && j.Status != Canceled) {
		return false
	}
	if _, stillRunning := m.running[id]; stillRunning {
		return false // el proceso cancelado aún no terminó de morir
	}
	m.requeueLocked(j)
	m.saveLocked()
	m.emitLocked()
	m.scheduleLocked()
	return true
}

// RetryFailed reencola todas las canciones fallidas.
func (m *Manager) RetryFailed() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if j.Status == Failed {
			if _, stillRunning := m.running[j.ID]; !stillRunning {
				m.requeueLocked(j)
				n++
			}
		}
	}
	if n > 0 {
		m.saveLocked()
		m.emitLocked()
		m.scheduleLocked()
	}
	return n
}

func (m *Manager) requeueLocked(j *Job) {
	j.Status, j.Percent, j.Speed, j.ETA, j.Error = Queued, 0, 0, -1, ""
}

// Remove quita una canción que no esté activa.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, j := range m.jobs {
		if j.ID == id && !isActive(j.Status) {
			if _, stillRunning := m.running[id]; stillRunning {
				return
			}
			m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
			m.saveLocked()
			m.emitLocked()
			return
		}
	}
}

// ClearFinished quita del listado las canciones terminadas, fallidas y canceladas.
func (m *Manager) ClearFinished() {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.jobs[:0]
	for _, j := range m.jobs {
		_, stillRunning := m.running[j.ID]
		if isActive(j.Status) || stillRunning {
			kept = append(kept, j)
		}
	}
	m.jobs = kept
	m.saveLocked()
	m.emitLocked()
}

// Kick vuelve a evaluar cuántas descargas deben correr (p. ej. tras cambiar la concurrencia).
func (m *Manager) Kick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduleLocked()
}

// Pause evita que arranquen trabajos nuevos (los que ya corren siguen). Sirve para esperar a que
// las herramientas (yt-dlp) estén listas antes de reanudar la cola guardada.
func (m *Manager) Pause() {
	m.mu.Lock()
	m.paused = true
	m.mu.Unlock()
}

// Resume permite arrancar trabajos de nuevo.
func (m *Manager) Resume() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = false
	m.scheduleLocked()
}

// scheduleLocked arranca trabajos en cola hasta llenar la concurrencia configurada.
func (m *Manager) scheduleLocked() {
	if m.root == nil || m.root.Err() != nil || m.paused {
		return
	}
	limit := m.settings().Concurrency
	limit = max(settings.MinConcurrency, min(limit, settings.MaxConcurrency))
	started := false
	for len(m.running) < limit {
		var next *Job
		for _, j := range m.jobs {
			if j.Status == Queued {
				next = j
				break
			}
		}
		if next == nil {
			break
		}
		next.Status, next.Percent, next.Speed, next.ETA, next.Error = Downloading, 0, 0, -1, ""
		ctx, cancel := context.WithCancel(m.root)
		m.running[next.ID] = cancel
		m.wg.Add(1)
		go m.run(ctx, *next)
		started = true
	}
	if started {
		m.emitLocked()
	}
}

func (m *Manager) run(ctx context.Context, job Job) {
	defer m.wg.Done()
	id := job.ID
	s := m.settings()
	opts := ytdlp.DownloadOptions{
		URL: job.URL, OutDir: s.OutDir, Folder: job.Folder, Index: job.Index, Name: job.OutName,
		Format: s.Format, Quality: s.Quality, FFmpeg: s.FFmpegPath,
	}

	var res ytdlp.Result
	var err error
	if job.Search {
		if m.resolver == nil {
			err = errors.New("esta canción necesita búsqueda pero no hay buscador configurado")
		} else {
			opts.URL, err = m.resolver.Resolve(ctx, ResolveRequest{Title: job.Title, Artist: job.Uploader, Duration: job.Duration})
		}
	}
	if err == nil {
		res, err = m.download(ctx, id, opts)
	}
	m.finish(id, res, err)
}

func (m *Manager) download(ctx context.Context, id string, opts ytdlp.DownloadOptions) (ytdlp.Result, error) {
	return m.dl.Download(ctx, opts, func(p ytdlp.Progress) {
		m.mu.Lock()
		defer m.mu.Unlock()
		j := m.findLocked(id)
		if j == nil || m.canceling[id] {
			return
		}
		switch {
		case p.Phase == "converting":
			j.Status, j.Percent, j.Speed, j.ETA = Converting, 100, 0, -1
		case j.Status != Converting: // los eventos de stdout y stderr pueden llegar desordenados
			j.Status, j.Percent, j.Speed, j.ETA = Downloading, p.Percent, p.Speed, p.ETA
		}
		m.notifyLocked()
	})
}

// finish registra el resultado de un trabajo y libera su hueco.
func (m *Manager) finish(id string, res ytdlp.Result, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.running[id]; ok {
		cancel()
		delete(m.running, id)
	}
	userCanceled := m.canceling[id]
	delete(m.canceling, id)

	if j := m.findLocked(id); j != nil {
		switch {
		case userCanceled:
			j.Status = Canceled
		case err == nil:
			j.Status, j.Percent, j.FilePath, j.Error = Done, 100, res.Path, ""
		case m.root.Err() != nil:
			m.requeueLocked(j) // la app se está cerrando: se reanuda en el próximo arranque
		default:
			j.Status, j.Error = Failed, err.Error()
		}
		if j.Status != Queued {
			j.Speed, j.ETA = 0, -1
		}
	}
	m.trimLocked()
	m.saveLocked()
	m.emitLocked()
	m.scheduleLocked()
}

// notifyLocked emite el progreso como máximo cada emitInterval; un temporizador garantiza
// que la última actualización siempre llegue.
func (m *Manager) notifyLocked() {
	if time.Since(m.lastEmit) >= emitInterval {
		m.emitLocked()
		return
	}
	if m.timerSet {
		return
	}
	m.timerSet = true
	time.AfterFunc(emitInterval, func() {
		m.mu.Lock()
		m.timerSet = false
		m.emitLocked()
		m.mu.Unlock()
	})
}

// emitLocked deja la última foto del estado en un canal de tamaño 1: si la interfaz va más lenta,
// las fotos viejas se descartan en lugar de acumularse.
func (m *Manager) emitLocked() {
	m.lastEmit = time.Now()
	if m.onChange == nil {
		return
	}
	snap := m.snapshotLocked()
	for {
		select {
		case m.emitCh <- snap:
			return
		default:
			select {
			case <-m.emitCh:
			default:
			}
		}
	}
}

// trimLocked limita el historial de canciones terminadas.
func (m *Manager) trimLocked() {
	finished := 0
	for _, j := range m.jobs {
		if !isActive(j.Status) {
			finished++
		}
	}
	drop := finished - maxFinished
	if drop <= 0 {
		return
	}
	kept := m.jobs[:0]
	for _, j := range m.jobs {
		if drop > 0 && !isActive(j.Status) {
			if _, stillRunning := m.running[j.ID]; !stillRunning {
				drop--
				continue
			}
		}
		kept = append(kept, j)
	}
	m.jobs = kept
}

func (m *Manager) saveLocked() {
	if m.statePath == "" {
		return
	}
	data, err := json.Marshal(m.jobs)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o755); err != nil {
		return
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, m.statePath)
}

func (m *Manager) loadLocked() {
	if m.statePath == "" {
		return
	}
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return
	}
	for _, j := range jobs {
		if j == nil || j.ID == "" {
			continue
		}
		if isActive(j.Status) { // lo que estaba en curso al cerrar vuelve a la cola
			m.requeueLocked(j)
		}
		m.jobs = append(m.jobs, j)
	}
}
