package queue

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"musicdl/src/backend/settings"
	"musicdl/src/backend/ytdlp"
)

// fakeDL es un descargador de mentira: cada llamada espera a que el test la libere.
type fakeDL struct {
	mu      sync.Mutex
	calls   []ytdlp.DownloadOptions
	current int32
	maxSeen int32
	release chan struct{} // se cierra para dejar terminar todas las descargas bloqueadas
	failFor map[string]error
	block   bool
}

func newFake(block bool) *fakeDL {
	return &fakeDL{release: make(chan struct{}), failFor: map[string]error{}, block: block}
}

func (f *fakeDL) Download(ctx context.Context, o ytdlp.DownloadOptions, onProgress func(ytdlp.Progress)) (ytdlp.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, o)
	err := f.failFor[o.URL]
	f.mu.Unlock()

	n := atomic.AddInt32(&f.current, 1)
	for {
		old := atomic.LoadInt32(&f.maxSeen)
		if n <= old || atomic.CompareAndSwapInt32(&f.maxSeen, old, n) {
			break
		}
	}
	defer atomic.AddInt32(&f.current, -1)

	onProgress(ytdlp.Progress{Phase: "downloading", Percent: 42, Speed: 1000, ETA: 5})
	if f.block {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ytdlp.Result{}, ctx.Err()
		}
	}
	if err != nil {
		return ytdlp.Result{}, err
	}
	onProgress(ytdlp.Progress{Phase: "converting", Percent: 100})
	return ytdlp.Result{Path: filepath.Join(o.OutDir, o.Folder, "x.mp3")}, nil
}

func (f *fakeDL) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func cfg(concurrency int) func() settings.Settings {
	return func() settings.Settings {
		s := settings.Defaults()
		s.OutDir = "/out"
		s.Concurrency = concurrency
		return s
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tiempo agotado esperando: %s", what)
}

func statusOf(m *Manager, id string) Status {
	for _, j := range m.Snapshot() {
		if j.ID == id {
			return j.Status
		}
	}
	return ""
}

func countStatus(m *Manager, s Status) int {
	n := 0
	for _, j := range m.Snapshot() {
		if j.Status == s {
			n++
		}
	}
	return n
}

func start(t *testing.T, m *Manager) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	// Al terminar el test, parar y esperar a los trabajos: si no, siguen guardando el estado
	// mientras Go borra la carpeta temporal.
	t.Cleanup(func() {
		cancel()
		m.Wait(5 * time.Second)
	})
	return cancel
}

func TestJobCompletesWithOptions(t *testing.T) {
	f := newFake(false)
	m := New(f, cfg(2), "", nil)
	start(t, m)

	added := m.Add([]NewJob{{URL: "https://e.com/1", Title: "Uno", Folder: "Mi Lista", Index: 4}})
	if len(added) != 1 {
		t.Fatalf("added = %d", len(added))
	}
	waitFor(t, "job Done", func() bool { return statusOf(m, added[0].ID) == Done })

	j := m.Snapshot()[0]
	if j.Percent != 100 || j.FilePath != filepath.Join("/out", "Mi Lista", "x.mp3") || j.Error != "" {
		t.Errorf("job final: %+v", j)
	}
	f.mu.Lock()
	o := f.calls[0]
	f.mu.Unlock()
	if o.OutDir != "/out" || o.Folder != "Mi Lista" || o.Index != 4 || o.Format != "mp3" {
		t.Errorf("opciones enviadas al descargador: %+v", o)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	f := newFake(true)
	m := New(f, cfg(2), "", nil)
	start(t, m)

	var items []NewJob
	for i := 1; i <= 5; i++ {
		items = append(items, NewJob{URL: "https://e.com/" + string(rune('0'+i))})
	}
	m.Add(items)
	waitFor(t, "2 descargando", func() bool { return countStatus(m, Downloading) == 2 })
	time.Sleep(50 * time.Millisecond) // darle oportunidad a que arranque una tercera (no debe)
	if got := countStatus(m, Downloading); got != 2 {
		t.Fatalf("descargando = %d; want 2", got)
	}
	if got := countStatus(m, Queued); got != 3 {
		t.Fatalf("en cola = %d; want 3", got)
	}
	close(f.release)
	waitFor(t, "las 5 terminadas", func() bool { return countStatus(m, Done) == 5 })
	if max := atomic.LoadInt32(&f.maxSeen); max != 2 {
		t.Errorf("máximo simultáneo = %d; want 2", max)
	}
}

func TestFailureAndRetry(t *testing.T) {
	f := newFake(false)
	f.failFor["https://e.com/bad"] = errors.New("Track not found")
	m := New(f, cfg(1), "", nil)
	start(t, m)

	added := m.Add([]NewJob{{URL: "https://e.com/bad"}})
	id := added[0].ID
	waitFor(t, "Failed", func() bool { return statusOf(m, id) == Failed })
	if m.Snapshot()[0].Error != "Track not found" {
		t.Errorf("error = %q", m.Snapshot()[0].Error)
	}
	if m.Retry("no-existe") {
		t.Error("Retry de un id inexistente debe devolver false")
	}

	f.mu.Lock()
	delete(f.failFor, "https://e.com/bad")
	f.mu.Unlock()
	if !m.Retry(id) {
		t.Fatal("Retry debería aceptar un trabajo fallido")
	}
	waitFor(t, "Done tras reintento", func() bool { return statusOf(m, id) == Done })
	if m.Snapshot()[0].Error != "" {
		t.Error("el error debe limpiarse tras un reintento exitoso")
	}
}

func TestRetryFailedOnlyTouchesFailed(t *testing.T) {
	f := newFake(false)
	f.failFor["https://e.com/a"] = errors.New("boom")
	f.failFor["https://e.com/b"] = errors.New("boom")
	m := New(f, cfg(2), "", nil)
	start(t, m)
	m.Add([]NewJob{{URL: "https://e.com/a"}, {URL: "https://e.com/b"}, {URL: "https://e.com/ok"}})
	waitFor(t, "2 fallidas y 1 hecha", func() bool { return countStatus(m, Failed) == 2 && countStatus(m, Done) == 1 })

	f.mu.Lock()
	f.failFor = map[string]error{}
	f.mu.Unlock()
	if n := m.RetryFailed(); n != 2 {
		t.Errorf("RetryFailed = %d; want 2", n)
	}
	waitFor(t, "todas Done", func() bool { return countStatus(m, Done) == 3 })
}

func TestCancelRunningFreesSlot(t *testing.T) {
	f := newFake(true)
	m := New(f, cfg(1), "", nil)
	start(t, m)

	added := m.Add([]NewJob{{URL: "https://e.com/a"}, {URL: "https://e.com/b"}})
	waitFor(t, "a descargando", func() bool { return statusOf(m, added[0].ID) == Downloading })

	m.Cancel(added[0].ID)
	if statusOf(m, added[0].ID) != Canceled {
		t.Errorf("tras Cancel el estado debe ser canceled, es %s", statusOf(m, added[0].ID))
	}
	waitFor(t, "b arranca al liberarse el hueco", func() bool { return statusOf(m, added[1].ID) == Downloading })
	if statusOf(m, added[0].ID) != Canceled {
		t.Error("el trabajo cancelado no debe cambiar de estado por eventos tardíos")
	}
	close(f.release)
	waitFor(t, "b Done", func() bool { return statusOf(m, added[1].ID) == Done })
}

func TestCancelQueuedNeverRuns(t *testing.T) {
	f := newFake(true)
	m := New(f, cfg(1), "", nil)
	start(t, m)
	added := m.Add([]NewJob{{URL: "https://e.com/a"}, {URL: "https://e.com/b"}})
	waitFor(t, "a descargando", func() bool { return statusOf(m, added[0].ID) == Downloading })

	m.Cancel(added[1].ID) // b sigue en cola
	close(f.release)
	waitFor(t, "a Done", func() bool { return statusOf(m, added[0].ID) == Done })
	time.Sleep(50 * time.Millisecond)
	if f.callCount() != 1 {
		t.Errorf("descargas iniciadas = %d; want 1 (la cancelada no debe correr)", f.callCount())
	}
	if statusOf(m, added[1].ID) != Canceled {
		t.Errorf("b = %s; want canceled", statusOf(m, added[1].ID))
	}
	if !m.Retry(added[1].ID) {
		t.Fatal("una cancelada debe poder reintentarse")
	}
	waitFor(t, "b Done tras reintento", func() bool { return statusOf(m, added[1].ID) == Done })
}

func TestDedupe(t *testing.T) {
	f := newFake(true)
	m := New(f, cfg(1), "", nil)
	start(t, m)
	first := m.Add([]NewJob{{URL: "https://e.com/a", Folder: "L1"}})
	again := m.Add([]NewJob{{URL: "https://e.com/a", Folder: "L1"}, {URL: "https://e.com/a", Folder: "L2"}, {URL: "  "}})
	if len(first) != 1 || len(again) != 1 || again[0].Folder != "L2" {
		t.Errorf("first=%d again=%+v (misma URL en otra carpeta sí se acepta; vacías no)", len(first), again)
	}
	close(f.release)
}

func TestRemoveAndClearFinished(t *testing.T) {
	f := newFake(true)
	f.failFor["https://e.com/bad"] = errors.New("x")
	m := New(f, cfg(1), "", nil)
	start(t, m)
	added := m.Add([]NewJob{{URL: "https://e.com/bad"}, {URL: "https://e.com/ok"}, {URL: "https://e.com/pending"}})
	waitFor(t, "bad descargando", func() bool { return statusOf(m, added[0].ID) == Downloading })

	m.Remove(added[0].ID) // activo: no se puede quitar
	if len(m.Snapshot()) != 3 {
		t.Fatal("Remove no debe quitar un trabajo activo")
	}
	close(f.release)
	waitFor(t, "todo procesado", func() bool { return countStatus(m, Failed) == 1 && countStatus(m, Done) == 2 })

	m.Remove(added[0].ID)
	if len(m.Snapshot()) != 2 {
		t.Errorf("tras Remove quedan %d; want 2", len(m.Snapshot()))
	}
	m.ClearFinished()
	if len(m.Snapshot()) != 0 {
		t.Errorf("tras ClearFinished quedan %d; want 0", len(m.Snapshot()))
	}
}

func TestPersistenceResumesAfterShutdown(t *testing.T) {
	state := filepath.Join(t.TempDir(), "queue.json")

	// Sesión 1: una descarga en curso, otra en cola y una ya terminada.
	f1 := newFake(false)
	m1 := New(f1, cfg(1), state, nil)
	ctx1, stop1 := context.WithCancel(context.Background())
	m1.Start(ctx1)
	doneJob := m1.Add([]NewJob{{URL: "https://e.com/done", Title: "Hecha"}})
	waitFor(t, "hecha", func() bool { return statusOf(m1, doneJob[0].ID) == Done })

	f1.block = true
	pend := m1.Add([]NewJob{{URL: "https://e.com/a", Title: "A"}, {URL: "https://e.com/b", Title: "B"}})
	waitFor(t, "a descargando", func() bool { return statusOf(m1, pend[0].ID) == Downloading })

	stop1() // el usuario cierra la app
	if !m1.Wait(5 * time.Second) {
		t.Fatal("los procesos no terminaron al cerrar")
	}
	for _, j := range m1.Snapshot() {
		if j.URL != "https://e.com/done" && j.Status != Queued {
			t.Errorf("tras cerrar, %s debe quedar en cola, está %s", j.Title, j.Status)
		}
	}

	// Sesión 2: mismo archivo de estado; debe reanudar A y B y conservar la hecha.
	f2 := newFake(false)
	m2 := New(f2, cfg(2), state, nil)
	start(t, m2)
	waitFor(t, "A y B terminan", func() bool { return countStatus(m2, Done) == 3 })
	if f2.callCount() != 2 {
		t.Errorf("la segunda sesión descargó %d canciones; want 2 (la ya hecha no se repite)", f2.callCount())
	}
}

func TestOnChangeDeliversFinalState(t *testing.T) {
	f := newFake(false)
	var mu sync.Mutex
	var last []Job
	m := New(f, cfg(1), "", func(s []Job) {
		mu.Lock()
		last = s
		mu.Unlock()
	})
	start(t, m)
	m.Add([]NewJob{{URL: "https://e.com/1"}, {URL: "https://e.com/2"}})
	waitFor(t, "la interfaz recibe el estado final", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(last) != 2 {
			return false
		}
		return last[0].Status == Done && last[1].Status == Done
	})
}

type fakeResolver struct {
	mu   sync.Mutex
	reqs []ResolveRequest
	url  string
	err  error
}

func (f *fakeResolver) Resolve(ctx context.Context, r ResolveRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	return f.url, f.err
}

func TestSearchJobIsResolvedThenDownloaded(t *testing.T) {
	f := newFake(false)
	m := New(f, cfg(1), "", nil)
	res := &fakeResolver{url: "https://www.youtube.com/watch?v=abc"}
	m.SetResolver(res)
	start(t, m)

	added := m.Add([]NewJob{{
		URL: "https://open.spotify.com/track/1", Title: "Canción", Uploader: "Artista A, Artista B",
		Folder: "Mi Lista", Index: 3, Search: true, Duration: 201, OutName: "Artista A - Canción",
	}})
	waitFor(t, "Done", func() bool { return statusOf(m, added[0].ID) == Done })

	res.mu.Lock()
	req := res.reqs[0]
	res.mu.Unlock()
	if req.Title != "Canción" || req.Artist != "Artista A, Artista B" || req.Duration != 201 {
		t.Errorf("solicitud al buscador: %+v", req)
	}
	f.mu.Lock()
	o := f.calls[0]
	f.mu.Unlock()
	if o.URL != "https://www.youtube.com/watch?v=abc" {
		t.Errorf("debe bajar la URL encontrada, no la de Spotify: %q", o.URL)
	}
	if o.Name != "Artista A - Canción" || o.Folder != "Mi Lista" || o.Index != 3 {
		t.Errorf("opciones: %+v", o)
	}
}

func TestSearchJobFailsWhenNoMatchAndCanRetry(t *testing.T) {
	f := newFake(false)
	m := New(f, cfg(1), "", nil)
	res := &fakeResolver{err: errors.New("sin coincidencia en YouTube")}
	m.SetResolver(res)
	start(t, m)

	added := m.Add([]NewJob{{URL: "https://open.spotify.com/track/2", Title: "Rara", Search: true}})
	waitFor(t, "Failed", func() bool { return statusOf(m, added[0].ID) == Failed })
	if got := m.Snapshot()[0].Error; got != "sin coincidencia en YouTube" {
		t.Errorf("error = %q", got)
	}
	if f.callCount() != 0 {
		t.Error("si no hay coincidencia no se debe descargar nada")
	}

	res.mu.Lock()
	res.err, res.url = nil, "https://www.youtube.com/watch?v=zzz"
	res.mu.Unlock()
	if !m.Retry(added[0].ID) {
		t.Fatal("Retry")
	}
	waitFor(t, "Done tras reintento", func() bool { return statusOf(m, added[0].ID) == Done })
}

func TestSearchJobWithoutResolverFails(t *testing.T) {
	m := New(newFake(false), cfg(1), "", nil)
	start(t, m)
	added := m.Add([]NewJob{{URL: "https://open.spotify.com/track/3", Title: "X", Search: true}})
	waitFor(t, "Failed", func() bool { return statusOf(m, added[0].ID) == Failed })
}

func TestSearchFieldsPersist(t *testing.T) {
	state := filepath.Join(t.TempDir(), "q.json")
	m1 := New(newFake(true), cfg(1), state, nil)
	ctx, stop := context.WithCancel(context.Background())
	m1.Start(ctx)
	m1.Add([]NewJob{{URL: "https://open.spotify.com/track/4", Title: "P", Search: true, Duration: 99, OutName: "A - P"}})
	stop()
	m1.Wait(5 * time.Second)

	m2 := New(newFake(false), cfg(1), state, nil)
	m2.SetResolver(&fakeResolver{url: "https://www.youtube.com/watch?v=q"})
	start(t, m2)
	j := m2.Snapshot()[0]
	if !j.Search || j.Duration != 99 || j.OutName != "A - P" {
		t.Errorf("campos de búsqueda no persistieron: %+v", j)
	}
}

func TestPauseHoldsQueueUntilResume(t *testing.T) {
	f := newFake(false)
	m := New(f, cfg(2), "", nil)
	m.Pause()
	start(t, m)
	m.Add([]NewJob{{URL: "https://e.com/1"}, {URL: "https://e.com/2"}})
	time.Sleep(80 * time.Millisecond)
	if f.callCount() != 0 || countStatus(m, Queued) != 2 {
		t.Fatalf("en pausa no debe arrancar nada: llamadas=%d en cola=%d", f.callCount(), countStatus(m, Queued))
	}
	m.Resume()
	waitFor(t, "ambas Done tras Resume", func() bool { return countStatus(m, Done) == 2 })
}

func TestKickAppliesHigherConcurrency(t *testing.T) {
	f := newFake(true)
	level := int32(1)
	m := New(f, func() settings.Settings {
		s := settings.Defaults()
		s.Concurrency = int(atomic.LoadInt32(&level))
		return s
	}, "", nil)
	start(t, m)
	m.Add([]NewJob{{URL: "https://e.com/1"}, {URL: "https://e.com/2"}, {URL: "https://e.com/3"}})
	waitFor(t, "1 descargando", func() bool { return countStatus(m, Downloading) == 1 })

	atomic.StoreInt32(&level, 3)
	m.Kick()
	waitFor(t, "3 descargando", func() bool { return countStatus(m, Downloading) == 3 })
	close(f.release)
}
