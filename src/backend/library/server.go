package library

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Server sirve los archivos de la biblioteca solo a esta app: escucha en 127.0.0.1, exige un token
// aleatorio en la ruta, valida la cabecera Host (contra DNS rebinding) y nunca sale de la carpeta raíz.
// Soporta Range (para poder avanzar/retroceder) y CORS (para que el analizador de audio pueda leer
// las muestras: sin CORS el navegador devuelve silencio al visualizador).
type Server struct {
	root  func() string
	token string
	ln    net.Listener
	srv   *http.Server
}

// StartServer abre el servidor en un puerto libre. root se evalúa en cada petición, así que cambiar la
// carpeta de música en Ajustes se aplica sin reiniciar.
func StartServer(root func() string) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, err
	}
	s := &Server{root: root, token: hex.EncodeToString(b), ln: ln}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// BaseURL es la dirección base; los archivos están en BaseURL + "/f/" + ruta.
func (s *Server) BaseURL() string {
	return "http://" + s.ln.Addr().String() + "/" + s.token
}

func (s *Server) Close() error { return s.srv.Close() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Range")
	h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
		return
	}
	if host, _, err := net.SplitHostPort(r.Host); err != nil || host != "127.0.0.1" {
		http.Error(w, "host no permitido", http.StatusForbidden)
		return
	}
	prefix := "/" + s.token + "/f/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, prefix) // el servidor ya decodificó los %XX
	full, err := s.resolve(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := ContentType(full)
	if ct == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(full), st.ModTime(), f)
}

var errOutside = errors.New("ruta fuera de la biblioteca")

// resolve convierte una ruta relativa de la URL en una ruta real dentro de la raíz, rechazando
// "..", rutas absolutas, archivos ocultos y enlaces simbólicos que apunten fuera.
func (s *Server) resolve(rel string) (string, error) { return resolveIn(s.root(), rel) }

func resolveIn(root, rel string) (string, error) {
	if root == "" || rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) {
		return "", errOutside
	}
	clean := path.Clean("/" + rel)[1:]
	if clean == "" || clean != rel || strings.HasPrefix(clean, "..") {
		return "", errOutside
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." || ignorable(part) {
			return "", errOutside
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	back, err := filepath.Rel(realRoot, full)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) || filepath.IsAbs(back) {
		return "", errOutside
	}
	return full, nil
}

// FileURL arma la URL de un archivo (ruta relativa con "/") a partir de la base del servidor.
func FileURL(base, rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return base + "/f/" + strings.Join(parts, "/")
}
