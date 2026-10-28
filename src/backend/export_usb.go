package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"musicdl/src/backend/library"
	"musicdl/src/backend/pioneer"
	"musicdl/src/backend/trackmeta"
)

// USBVolume es una unidad que podría ser la memoria USB.
type USBVolume struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	HasPi bool   `json:"hasPioneer"` // ya tiene una biblioteca Pioneer
}

// USBPlaylist es una playlist que ya existe en la memoria.
type USBPlaylist struct {
	ID     uint32 `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent"`
	Folder bool   `json:"folder"`
	Count  int    `json:"count"`
}

// USBInfo describe lo que hay en la carpeta/unidad elegida como destino.
type USBInfo struct {
	Exists     bool          `json:"exists"`
	HasLibrary bool          `json:"hasLibrary"` // ya hay una biblioteca Pioneer: se le añade lo nuevo
	HasOneLib  bool          `json:"hasOneLibrary"`
	Total      uint64        `json:"total"`
	Free       uint64        `json:"free"`
	Playlists  []USBPlaylist `json:"playlists"`
	ReadError  string        `json:"readError"`
}

// USBJob es "estas canciones (rutas relativas de la biblioteca) van a esta playlist de la memoria": una playlist
// nueva con ese nombre o, si ExistingID no es 0, una que ya existe en la memoria.
type USBJob struct {
	Name       string   `json:"name"`
	ExistingID uint32   `json:"existingId"`
	Tracks     []string `json:"tracks"`
}

// USBRequest es lo que pide la interfaz: destino y qué mandar adónde.
type USBRequest struct {
	Dest       string   `json:"dest"`
	DeviceName string   `json:"deviceName"`
	Jobs       []USBJob `json:"jobs"`
	Waveform   string   `json:"waveform"` // "blue", "rgb" o "3band"
}

// USBPreviewTrack / USBPreviewList / USBPreview son el resumen que se muestra antes de exportar.
type USBPreviewTrack struct {
	Title string `json:"title"`
	Copy  bool   `json:"copy"` // el archivo no está en la memoria y se copiará
	Size  int64  `json:"size"`
}
type USBPreviewList struct {
	Name     string            `json:"name"`
	Existing bool              `json:"existing"` // ya existe en la memoria
	Tracks   []USBPreviewTrack `json:"tracks"`   // las que se añaden a esta playlist
	InList   int               `json:"inList"`   // las que ya estaban en ella
}
type USBPreview struct {
	Lists       []USBPreviewList `json:"lists"`
	CopyTracks  int              `json:"copyTracks"`
	CopyBytes   int64            `json:"copyBytes"`
	Unsupported int              `json:"unsupported"`
	Free        uint64           `json:"free"`
}

// USBStatus es el progreso de la exportación.
type USBStatus struct {
	Running bool            `json:"running"`
	Done    int             `json:"done"`
	Total   int             `json:"total"`
	Message string          `json:"message"`
	Report  *pioneer.Report `json:"report,omitempty"`
	Error   string          `json:"error"`
}

type usbState struct {
	mu     sync.Mutex
	status USBStatus
	cancel context.CancelFunc
}

var usb usbState

func (a *App) emitUSB() {
	if a.wails != nil {
		usb.mu.Lock()
		s := usb.status
		usb.mu.Unlock()
		wruntime.EventsEmit(a.wails, "usb", s)
	}
}

// USBVolumes lista las unidades montadas que podrían ser una memoria USB.
func (a *App) USBVolumes() []USBVolume {
	var out []USBVolume
	switch runtime.GOOS {
	case "windows":
		for c := 'D'; c <= 'Z'; c++ {
			p := string(c) + `:\`
			if _, err := os.Stat(p); err == nil {
				out = append(out, USBVolume{Name: string(c) + ":", Path: p})
			}
		}
	case "darwin":
		ents, _ := os.ReadDir("/Volumes")
		for _, e := range ents {
			p := filepath.Join("/Volumes", e.Name())
			if strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "Macintosh HD") || strings.HasPrefix(e.Name(), "com.apple") {
				continue
			}
			if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir() {
				out = append(out, USBVolume{Name: e.Name(), Path: p})
			}
		}
	default:
		for _, base := range []string{"/media", "/run/media", "/mnt"} {
			ents, _ := os.ReadDir(base)
			for _, e := range ents {
				p := filepath.Join(base, e.Name())
				if e.IsDir() {
					out = append(out, USBVolume{Name: e.Name(), Path: p})
				}
			}
		}
	}
	for i := range out {
		out[i].Total, out[i].Free = diskSpace(out[i].Path)
		for _, r := range []string{"PIONEER", ".PIONEER"} {
			if _, err := os.Stat(filepath.Join(out[i].Path, r, "rekordbox")); err == nil {
				out[i].HasPi = true
			}
		}
	}
	return out
}

// USBInspect mira el destino antes de exportar.
func (a *App) USBInspect(dest string) USBInfo {
	var info USBInfo
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return info
	}
	info.Exists = true
	for _, r := range []string{"PIONEER", ".PIONEER"} {
		if _, err := os.Stat(filepath.Join(dest, r, "rekordbox", "export.pdb")); err == nil {
			info.HasLibrary = true
		}
		if _, err := os.Stat(filepath.Join(dest, r, "rekordbox", "exportLibrary.db")); err == nil {
			info.HasOneLib = true
		}
	}
	info.Total, info.Free = diskSpace(dest)
	if lib, err := pioneer.Inspect(dest); err != nil {
		info.ReadError = err.Error()
	} else if lib != nil {
		for _, p := range lib.Playlists {
			info.Playlists = append(info.Playlists, USBPlaylist{ID: p.ID, Name: p.Name, Parent: p.Parent, Folder: p.Folder, Count: p.Count})
		}
	}
	return info
}

// USBState devuelve el estado actual (por si la interfaz se reconecta).
func (a *App) USBState() USBStatus {
	usb.mu.Lock()
	defer usb.mu.Unlock()
	return usb.status
}

// CancelUSBExport detiene la exportación en curso.
func (a *App) CancelUSBExport() {
	usb.mu.Lock()
	defer usb.mu.Unlock()
	if usb.cancel != nil {
		usb.cancel()
	}
}

var noteMajor = []string{"C", "Db", "D", "Eb", "E", "F", "F#", "G", "Ab", "A", "Bb", "B"}

// Nombres como los escribe rekordbox (Dbm y Abm, no C#m ni G#m): el reproductor los convierte a 8A/… según DEVSETTING.
// Rueda Camelot por nota (índice = clase de nota, 0 = Do): 8A = La menor, 8B = Do mayor.
var camelotMinor = []int{5, 12, 7, 2, 9, 4, 11, 6, 1, 8, 3, 10}
var camelotMajor = []int{8, 3, 10, 5, 12, 7, 2, 9, 4, 11, 6, 1}

var noteMinor = []string{"Cm", "Dbm", "Dm", "Ebm", "Em", "Fm", "F#m", "Gm", "Abm", "Am", "Bbm", "Bm"}

// cueRGB son los colores de los cues de la app (misma paleta que src/frontend/src/player/trackmeta.ts).
var cueRGB = [][3]uint8{{0x18, 0xe0, 0xff}, {0x3a, 0x78, 0xff}, {0x96, 0x46, 0xff}, {0xff, 0x2f, 0xb0}, {0xff, 0x7a, 0x45}, {0xff, 0xe6, 0x00}, {0x3d, 0xff, 0x9a}, {0xff, 0xff, 0xff}}

func splitArtistTitle(title string) (artist, name string) {
	if i := strings.Index(title, " - "); i > 0 && i < len(title)-3 {
		return strings.TrimSpace(title[:i]), strings.TrimSpace(title[i+3:])
	}
	return "", title
}

// resolveJobs convierte lo que pidió la interfaz en lo que necesita el exportador. Los trabajos que apuntan a la misma
// playlist se juntan; las rutas que ya no existen en la biblioteca se ignoran.
func (a *App) resolveJobs(jobs []USBJob, withWave bool) ([]pioneer.Track, []pioneer.Playlist, error) {
	root := a.settings.Get().OutDir
	pls, err := library.Scan(root)
	if err != nil {
		return nil, nil, err
	}
	type src struct {
		t  library.Track
		pl string
	}
	byPath := map[string]src{}
	for _, pl := range pls {
		for _, t := range pl.Tracks {
			byPath[t.Path] = src{t, pl.Name}
		}
	}
	metas := a.meta.All()
	index := map[string]int{}
	var tracks []pioneer.Track
	var lists []pioneer.Playlist
	at := map[string]int{}
	for _, j := range jobs {
		name := strings.TrimSpace(j.Name)
		if name == "" && j.ExistingID == 0 {
			continue
		}
		key := strings.ToLower(name)
		if j.ExistingID != 0 {
			key = "#" + strconv.FormatUint(uint64(j.ExistingID), 10)
		}
		li, ok := at[key]
		if !ok {
			li = len(lists)
			at[key] = li
			lists = append(lists, pioneer.Playlist{Name: name, ExistingID: j.ExistingID})
		}
		for _, p := range j.Tracks {
			s, ok := byPath[p]
			if !ok {
				continue
			}
			i, ok := index[p]
			if !ok {
				i = len(tracks)
				index[p] = i
				tracks = append(tracks, a.usbTrack(root, s.pl, s.t, metas[p], withWave))
			}
			if !slices.Contains(lists[li].Tracks, i) {
				lists[li].Tracks = append(lists[li].Tracks, i)
			}
		}
	}
	return tracks, lists, nil
}

// USBPreview calcula qué pasaría al exportar: qué canciones se añaden a cada playlist, cuáles hay que copiar y cuánto pesan.
func (a *App) USBPreview(req USBRequest) (USBPreview, error) {
	var out USBPreview
	tracks, lists, err := a.resolveJobs(req.Jobs, false)
	if err != nil {
		return out, err
	}
	lib, _ := pioneer.Inspect(req.Dest)
	byID := map[uint32]pioneer.PlaylistInfo{}
	byName := map[string]pioneer.PlaylistInfo{}
	if lib != nil {
		for _, p := range lib.Playlists {
			byID[p.ID] = p
			if p.Parent == "" && !p.Folder {
				byName[strings.ToLower(p.Name)] = p
			}
		}
	}
	_, out.Free = diskSpace(req.Dest)
	counted := map[int]bool{}
	for _, l := range lists {
		pv := USBPreviewList{Name: l.Name}
		members := map[string]bool{}
		var ex pioneer.PlaylistInfo
		found := false
		if l.ExistingID != 0 {
			ex, found = byID[l.ExistingID]
		} else {
			ex, found = byName[strings.ToLower(l.Name)]
		}
		if found {
			pv.Existing, pv.Name = true, ex.Name
			for _, p := range ex.Paths {
				members[p] = true
			}
		}
		for _, i := range l.Tracks {
			t := tracks[i]
			if !pioneer.SupportedExt(t.Src) {
				if !counted[i] {
					out.Unsupported++
					counted[i] = true
				}
				continue
			}
			target := strings.ToLower(pioneer.TargetPath(t))
			size := int64(0)
			if fi, err := os.Stat(t.Src); err == nil {
				size = fi.Size()
			}
			onUSB := false
			if lib != nil && lib.Tracks[target] {
				if di, err := os.Stat(filepath.Join(req.Dest, filepath.FromSlash(pioneer.TargetPath(t)))); err == nil && di.Size() == size {
					onUSB = true
				}
			}
			if members[target] {
				pv.InList++
				continue
			}
			pv.Tracks = append(pv.Tracks, USBPreviewTrack{Title: strings.TrimSuffix(filepath.Base(t.Src), filepath.Ext(t.Src)), Copy: !onUSB, Size: size})
			if !onUSB && !counted[i] {
				counted[i] = true
				out.CopyTracks++
				out.CopyBytes += size
			}
		}
		out.Lists = append(out.Lists, pv)
	}
	return out, nil
}

func (a *App) usbTrack(root, album string, t library.Track, m trackmeta.Meta, withWave bool) pioneer.Track {
	artist, title := splitArtistTitle(t.Title)
	pt := pioneer.Track{Src: filepath.Join(root, filepath.FromSlash(t.Path)), Title: title, Artist: artist, Album: album}
	if album == library.RootName {
		pt.Album = ""
	}
	an := m.Analysis
	valid := an != nil && an.Version == trackmeta.AnalysisVersion && an.Size == t.Size
	if valid {
		pt.Duration = an.Duration
		pt.BPM = an.BPM
		pt.HasGrid = an.BPM > 0
		pt.GridOffset = an.BeatOffset
		if an.HasKey && an.KeyPC >= 0 && an.KeyPC < 12 {
			camelot := a.settings.Get().KeyNotation == "camelot"
			switch {
			case camelot && an.KeyMinor:
				pt.Key = strconv.Itoa(camelotMinor[an.KeyPC]) + "A"
			case camelot:
				pt.Key = strconv.Itoa(camelotMajor[an.KeyPC]) + "B"
			case an.KeyMinor:
				pt.Key = noteMinor[an.KeyPC]
			default:
				pt.Key = noteMajor[an.KeyPC]
			}
		}
		if !withWave {
			// sin onda: solo se necesita el nombre y la ruta
		} else if w := a.meta.LoadWave(t.Path, t.Size); len(w) >= 3 {
			pt.Wave = w
			pt.WaveFPS = 22050.0 / 220.0
		}
	}
	if m.GridSet && m.GridBPM > 0 && valid {
		pt.BPM, pt.GridOffset, pt.HasGrid = m.GridBPM, m.GridOffset, true
	}
	for _, c := range m.Cues {
		col := cueRGB[0]
		if c.Color >= 0 && c.Color < len(cueRGB) {
			col = cueRGB[c.Color]
		}
		slot := c.Slot
		if slot < 0 || slot > 7 {
			slot = -1
		}
		pt.Cues = append(pt.Cues, pioneer.Cue{Slot: slot, TimeMs: c.TimeMs, Name: c.Name, R: col[0], G: col[1], B: col[2]})
	}
	return pt
}

// StartUSBExport empieza a escribir la memoria USB en segundo plano; el avance llega por el evento "usb".
func (a *App) StartUSBExport(req USBRequest) error {
	usb.mu.Lock()
	if usb.status.Running {
		usb.mu.Unlock()
		return errors.New("ya hay una exportación en curso")
	}
	usb.mu.Unlock()
	if req.Dest == "" {
		return errors.New("elige la memoria USB")
	}
	if len(req.Jobs) == 0 {
		return errors.New("elige al menos una playlist o canción")
	}
	tracks, lists, err := a.resolveJobs(req.Jobs, true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	usb.mu.Lock()
	usb.cancel = cancel
	usb.status = USBStatus{Running: true, Message: "Preparando…"}
	usb.mu.Unlock()
	a.emitUSB()
	go func() {
		defer cancel()
		rep, err := pioneer.Export(ctx, pioneer.Options{
			Dest: req.Dest, DeviceName: req.DeviceName, Tracks: tracks, Playlists: lists,
			Waveform: req.Waveform, KeyAlphanumeric: a.settings.Get().KeyNotation == "camelot",
			Progress: func(done, total int, msg string) {
				usb.mu.Lock()
				usb.status.Done, usb.status.Total, usb.status.Message = done, total, msg
				usb.mu.Unlock()
				a.emitUSB()
			},
		})
		usb.mu.Lock()
		st := usb.status
		st.Running = false
		st.Report = rep
		if err != nil {
			st.Error = err.Error()
			if errors.Is(err, context.Canceled) {
				st.Error = "Exportación cancelada."
			}
		} else {
			st.Done, st.Message = st.Total, "Listo"
		}
		usb.status = st
		usb.mu.Unlock()
		a.emitUSB()
	}()
	return nil
}
