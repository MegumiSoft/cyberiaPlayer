package library

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTitleFromFile(t *testing.T) {
	cases := map[string]string{
		"01 - Artista - Canción.mp3": "Artista - Canción",
		"7. Solo título.m4a":         "Solo título",
		"12_Algo.flac":               "Algo",
		"Sin número.mp3":             "Sin número",
		"2 Fast 2 Furious.mp3":       "2 Fast 2 Furious", // no hay separador después del número
		"1999.mp3":                   "1999",
		"100 - .mp3":                 "100 -",
	}
	for in, want := range cases {
		if got := TitleFromFile(in); got != want {
			t.Errorf("TitleFromFile(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNaturalLess(t *testing.T) {
	in := []string{"10 - b", "2 - a", "1 - z", "02 - x", "Álbum", "a"}
	sorted := append([]string(nil), in...)
	// ordenar con la función
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if NaturalLess(sorted[j], sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if sorted[0] != "1 - z" || sorted[len(sorted)-1] != "Álbum" || strings.Index(strings.Join(sorted, "|"), "2 - a") > strings.Index(strings.Join(sorted, "|"), "10 - b") {
		t.Errorf("orden: %v", sorted)
	}
	if NaturalLess("a", "a") || !NaturalLess("A", "b") || NaturalLess("track 10", "track 9") {
		t.Error("comparaciones básicas")
	}
}

func TestScanBuildsPlaylistsFromFolders(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "kawaii 17", "02 - Dos.mp3"), "x")
	write(t, filepath.Join(root, "kawaii 17", "10 - Diez.mp3"), "x")
	write(t, filepath.Join(root, "kawaii 17", "01 - Uno.mp3"), "x")
	write(t, filepath.Join(root, "kawaii 17", "cover.jpg"), "img")
	write(t, filepath.Join(root, "kawaii 17", "notas.txt"), "no es audio")
	write(t, filepath.Join(root, "Rock", "Banda - Tema.flac"), "x")
	write(t, filepath.Join(root, "Rock", "En Vivo", "a.opus"), "x")
	write(t, filepath.Join(root, "Vacía", "foto.jpg"), "img")
	write(t, filepath.Join(root, "suelta.wav"), "x")
	// basura que debe ignorarse
	write(t, filepath.Join(root, "kawaii 17", "._01 - Uno.mp3"), "resource fork")
	write(t, filepath.Join(root, "kawaii 17", "03 - Bajando.mp3.part"), "parcial")
	write(t, filepath.Join(root, "kawaii 17", "04 - Convirtiendo.temp.mp3"), "tmp")
	write(t, filepath.Join(root, ".oculta", "x.mp3"), "x")

	pls, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, p := range pls {
		names = append(names, p.Name)
	}
	if strings.Join(names, "|") != "(sueltas)|kawaii 17|Rock|Rock / En Vivo|Vacía" {
		t.Fatalf("playlists: %v", names)
	}
	var kawaii Playlist
	for _, p := range pls {
		if p.Name == "kawaii 17" {
			kawaii = p
		}
	}
	titles := []string{}
	for _, tr := range kawaii.Tracks {
		titles = append(titles, tr.Title)
	}
	if strings.Join(titles, "|") != "Uno|Dos|Diez" {
		t.Errorf("tracks (orden natural, sin basura): %v", titles)
	}
	if kawaii.Cover != "kawaii 17/cover.jpg" || kawaii.Path != "kawaii 17" {
		t.Errorf("cover/path: %+v", kawaii)
	}
	if kawaii.Tracks[0].Path != "kawaii 17/01 - Uno.mp3" || kawaii.Tracks[0].Ext != "mp3" || kawaii.Tracks[0].ID == "" {
		t.Errorf("track: %+v", kawaii.Tracks[0])
	}
	ids := map[string]bool{}
	for _, p := range pls {
		for _, tr := range p.Tracks {
			if ids[tr.ID] {
				t.Errorf("id repetido: %s", tr.ID)
			}
			ids[tr.ID] = true
		}
	}
}

func TestScanDepthLimitAndMissingRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b", "c", "d", "ok.mp3"), "x")      // 4 niveles: sí
	write(t, filepath.Join(root, "a", "b", "c", "d", "e", "no.mp3"), "x") // 5 niveles: no
	pls, err := Scan(root)
	if err != nil || len(pls) != 1 || pls[0].Name != "a / b / c / d" {
		t.Errorf("pls=%+v err=%v", pls, err)
	}
	if pls, err := Scan(filepath.Join(root, "no-existe")); err != nil || len(pls) != 0 {
		t.Errorf("raíz inexistente: %v %v", pls, err)
	}
	if pls, err := Scan(""); err != nil || pls != nil {
		t.Errorf("raíz vacía: %v %v", pls, err)
	}
}

func TestContentTypeAllowList(t *testing.T) {
	if ContentType("a.MP3") != "audio/mpeg" || ContentType("c.png") != "image/png" || ContentType("x.m4a") != "audio/mp4" {
		t.Error("tipos permitidos")
	}
	for _, bad := range []string{"a.txt", "a.exe", "a.json", "a", "a.html", "a.svg"} {
		if ContentType(bad) != "" {
			t.Errorf("%s no debe estar permitido", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// Servidor
// ---------------------------------------------------------------------------

func startTestServer(t *testing.T, root string) *Server {
	t.Helper()
	s, err := StartServer(func() string { return root })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func get(t *testing.T, url string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestServerServesRangeAndCORS(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Lista ñ", "01 - Canción #1.mp3"), "0123456789ABCDEF")
	s := startTestServer(t, root)
	u := FileURL(s.BaseURL(), "Lista ñ/01 - Canción #1.mp3")

	resp, body := get(t, u, nil)
	if resp.StatusCode != 200 || body != "0123456789ABCDEF" {
		t.Fatalf("completo: %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Type") != "audio/mpeg" || resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Errorf("cabeceras: %v", resp.Header)
	}
	resp, body = get(t, u, map[string]string{"Range": "bytes=4-9"})
	if resp.StatusCode != 206 || body != "456789" || resp.Header.Get("Content-Range") != "bytes 4-9/16" {
		t.Errorf("rango: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Range"))
	}
	resp, _ = get(t, u, map[string]string{"Range": "bytes=100-200"})
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("rango imposible: %d", resp.StatusCode)
	}
}

func TestServerRejectsEverythingElse(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "ok.mp3"), "audio")
	write(t, filepath.Join(root, "a", "secreto.txt"), "no")
	write(t, filepath.Join(root, "a", "._ok.mp3"), "fork")
	outside := t.TempDir()
	write(t, filepath.Join(outside, "robado.mp3"), "fuera")
	s := startTestServer(t, root)
	base := s.BaseURL()

	notFound := []string{
		strings.Replace(base, "/"+s.token, "/token-malo", 1) + "/f/a/ok.mp3", // token incorrecto
		base + "/f/a/secreto.txt", // extensión no permitida
		base + "/f/a/._ok.mp3",    // archivo oculto
		base + "/f/a/no-existe.mp3",
		base + "/f/..%2f" + filepath.Base(outside) + "/robado.mp3", // recorrido con barra codificada
		base + "/f/a/..%2f..%2fetc/passwd",
		base + "/f/%2e%2e/x.mp3",
		base + "/f/a/",
		base + "/f/",
		base + "/f/a%5Cok.mp3", // barra invertida
		base + "/nada",
	}
	for _, u := range notFound {
		if resp, _ := get(t, u, nil); resp.StatusCode != 404 {
			t.Errorf("%s -> %d; want 404", u, resp.StatusCode)
		}
	}
	// Con rutas "crudas" (sin normalizar por el cliente) tampoco debe salir de la raíz.
	for _, raw := range []string{"/f/../../etc/passwd", "/f/a/../../x.mp3"} {
		req, _ := http.NewRequest(http.MethodGet, base, nil)
		req.URL.Path = strings.TrimSuffix(req.URL.Path, "/") + raw
		req.URL.RawPath = req.URL.Path
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Errorf("%s respondió 200", raw)
		}
	}
	if resp, _ := get(t, base+"/f/a/ok.mp3", nil); resp.StatusCode != 200 {
		t.Errorf("el archivo válido debe servirse: %d", resp.StatusCode)
	}
}

func TestServerRejectsBadHostAndMethods(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ok.mp3"), "x")
	s := startTestServer(t, root)
	req, _ := http.NewRequest(http.MethodGet, FileURL(s.BaseURL(), "ok.mp3"), nil)
	req.Host = "evil.example.com" // DNS rebinding
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("host ajeno: %d", resp.StatusCode)
	}
	post, _ := http.NewRequest(http.MethodPost, FileURL(s.BaseURL(), "ok.mp3"), strings.NewReader("x"))
	resp, err = http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
	opt, _ := http.NewRequest(http.MethodOptions, FileURL(s.BaseURL(), "ok.mp3"), nil)
	resp, err = http.DefaultClient.Do(opt)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("OPTIONS: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestServerBlocksSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("los symlinks en Windows requieren privilegios")
	}
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secreto.mp3"), "fuera de la biblioteca")
	write(t, filepath.Join(root, "dentro.mp3"), "dentro")
	if err := os.Symlink(outside, filepath.Join(root, "enlace")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secreto.mp3"), filepath.Join(root, "atajo.mp3")); err != nil {
		t.Fatal(err)
	}
	s := startTestServer(t, root)
	for _, rel := range []string{"enlace/secreto.mp3", "atajo.mp3"} {
		if resp, body := get(t, FileURL(s.BaseURL(), rel), nil); resp.StatusCode != 404 {
			t.Errorf("%s -> %d %q; un enlace simbólico no debe sacar de la raíz", rel, resp.StatusCode, body)
		}
	}
	if resp, _ := get(t, FileURL(s.BaseURL(), "dentro.mp3"), nil); resp.StatusCode != 200 {
		t.Errorf("archivo normal: %d", resp.StatusCode)
	}
}

func TestServerFollowsRootChange(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, filepath.Join(a, "x.mp3"), "desde A")
	write(t, filepath.Join(b, "x.mp3"), "desde B")
	cur := a
	s, err := StartServer(func() string { return cur })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u := FileURL(s.BaseURL(), "x.mp3")
	if _, body := get(t, u, nil); body != "desde A" {
		t.Errorf("body = %q", body)
	}
	cur = b
	if _, body := get(t, u, nil); body != "desde B" {
		t.Errorf("tras cambiar la raíz, body = %q", body)
	}
}

func TestFileURLEscapes(t *testing.T) {
	got := FileURL("http://127.0.0.1:1/tok", "Lista #1/50% & más?.mp3")
	if strings.ContainsAny(strings.TrimPrefix(got, "http://127.0.0.1:1/tok/f/"), "#?& %") && !strings.Contains(got, "%23") {
		t.Errorf("sin escapar: %s", got)
	}
	if !strings.Contains(got, "%23") || !strings.Contains(got, "%3F") || !strings.Contains(got, "%25") {
		t.Errorf("escape: %s", got)
	}
	// ida y vuelta por el servidor real con caracteres problemáticos
	root := t.TempDir()
	write(t, filepath.Join(root, "Lista #1", "50% & más?.mp3"), "ok")
	s := startTestServer(t, root)
	if resp, body := get(t, FileURL(s.BaseURL(), "Lista #1/50% & más?.mp3"), nil); resp.StatusCode != 200 || body != "ok" {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
	_ = httptest.NewRecorder
}

func TestScanIncludesEmptyLeafFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"vacia", "padre/hija", ".oculta"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pls, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range pls {
		names = append(names, p.Name)
		if p.Tracks == nil {
			t.Fatalf("%q: Tracks nil (el JSON saldría null)", p.Name)
		}
	}
	got := strings.Join(names, "|")
	if got != "padre / hija|vacia" {
		t.Fatalf("playlists = %q", got)
	}
}

func TestCreateFolderAndMoveTrack(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/uno.mp3")
	write("b/uno.mp3")
	write("a/dos.mp3")
	write("a/nota.txt")

	if _, err := CreateFolder(root, "a"); err == nil {
		t.Fatal("debía rechazar una playlist repetida")
	}
	for _, bad := range []string{"", "  ", "..", ".oculta", RootName} {
		if _, err := CreateFolder(root, bad); err == nil {
			t.Fatalf("aceptó el nombre %q", bad)
		}
	}
	p, err := CreateFolder(root, "mi/nueva:lista")
	if err != nil || p != "mi_nueva_lista" {
		t.Fatalf("CreateFolder = %q, %v", p, err)
	}

	if got, err := MoveTrack(root, "a/dos.mp3", "mi_nueva_lista"); err != nil || got != "mi_nueva_lista/dos.mp3" {
		t.Fatalf("MoveTrack = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "dos.mp3")); !os.IsNotExist(err) {
		t.Fatal("el original debía desaparecer")
	}
	if _, err := MoveTrack(root, "a/uno.mp3", "b"); err == nil {
		t.Fatal("no debe sobrescribir")
	}
	if got, err := MoveTrack(root, "a/uno.mp3", "a"); err != nil || got != "a/uno.mp3" {
		t.Fatalf("mover a la misma carpeta = %q, %v", got, err)
	}
	if got, err := MoveTrack(root, "b/uno.mp3", ""); err != nil || got != "uno.mp3" {
		t.Fatalf("mover a la raíz = %q, %v", got, err)
	}
	for _, c := range [][2]string{
		{"../x.mp3", "a"}, {"a/nota.txt", "b"}, {"a/uno.mp3", "../afuera"}, {"nada.mp3", "a"}, {"a/uno.mp3", "noexiste"},
	} {
		if _, err := MoveTrack(root, c[0], c[1]); err == nil {
			t.Fatalf("MoveTrack(%q,%q) debía fallar", c[0], c[1])
		}
	}
}

func TestReadChunkAndResolveFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "x.mp3"), "0123456789")
	write(t, filepath.Join(root, "a", "nota.txt"), "no")
	got, err := ReadChunk(root, "a/x.mp3", 3, 4)
	if err != nil || string(got) != "3456" {
		t.Fatalf("chunk = %q, %v", got, err)
	}
	if got, err := ReadChunk(root, "a/x.mp3", 8, 100); err != nil || string(got) != "89" {
		t.Fatalf("final = %q, %v", got, err)
	}
	if got, err := ReadChunk(root, "a/x.mp3", 50, 4); err != nil || len(got) != 0 {
		t.Fatalf("pasado el final = %q, %v", got, err)
	}
	for _, c := range []struct {
		rel string
		off int64
		n   int
	}{{"../x.mp3", 0, 1}, {"a/nota.txt", 0, 1}, {"a/x.mp3", -1, 1}, {"a/x.mp3", 0, 0}, {"a/x.mp3", 0, MaxChunk + 1}} {
		if _, err := ReadChunk(root, c.rel, c.off, c.n); err == nil {
			t.Fatalf("ReadChunk(%+v) debía fallar", c)
		}
	}
}
