package pioneer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Cue es un punto marcado: Slot 0-7 son los hot cues A-H, -1 es un memory cue.
type Cue struct {
	Slot    int
	TimeMs  int
	Name    string
	R, G, B uint8
}

// Track es una canción a exportar, con lo que la app sabe de ella.
type Track struct {
	Src      string // ruta absoluta del audio
	Title    string
	Artist   string
	Album    string
	Key      string  // "Am", "F#", … o ""
	BPM      float64 // 0 si no hay
	Duration float64 // segundos; 0 si no se analizó
	// Rejilla: HasGrid indica que BPM y GridOffset (segundos hasta el primer pulso) son válidos.
	HasGrid    bool
	GridOffset float64
	Cues       []Cue
	Wave       []byte  // 3 bytes por cuadro (graves, medios, agudos), 0-255; nil si no se analizó
	WaveFPS    float64 // cuadros por segundo de Wave
}

// Playlist es una lista de canciones (índices en Options.Tracks).
type Playlist struct {
	Name       string
	Tracks     []int
	ExistingID uint32 // si no es 0, se añaden a esa playlist que ya existe en la memoria (en vez de buscar por nombre)
}

// Options configura una exportación.
type Options struct {
	Dest       string // raíz de la memoria USB
	DeviceName string
	Tracks     []Track
	Playlists  []Playlist
	Progress   func(done, total int, msg string)
	// Waveform: "blue", "rgb" o "3band" (color de la onda en el reproductor); vacío = no tocar DEVSETTING.DAT.
	Waveform string
	// KeyAlphanumeric: el reproductor muestra la tonalidad como 8A (Camelot) en vez de Am.
	KeyAlphanumeric bool
}

// Report resume lo que se hizo.
type Report struct {
	Root     string   `json:"root"`
	Tracks   int      `json:"tracks"`   // canciones nuevas escritas
	Reused   int      `json:"reused"`   // canciones elegidas que ya estaban en la memoria
	Extended int      `json:"extended"` // playlists existentes a las que se añadieron canciones
	Merged   bool     `json:"merged"`   // se añadió a una biblioteca que ya existía
	Playlist int      `json:"playlists"`
	Analyzed int      `json:"analyzed"`
	Bytes    int64    `json:"bytes"`
	Skipped  []string `json:"skipped"`
	Warnings []string `json:"warnings"`
}

// SupportedExt indica si los CDJ reproducen el formato (Opus/Ogg no).
func SupportedExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".m4a", ".aac", ".flac", ".wav", ".aif", ".aiff":
		return true
	}
	return false
}

const (
	dirNameMax  = 48
	fileNameMax = 120
)

func fatSafe(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case strings.ContainsRune(`/\:*?"<>|`, r), r < 0x20:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), ". ")
	if strings.TrimSpace(out) == "" {
		return "Unknown"
	}
	return out
}

func dirName(name string) string {
	s := fatSafe(name)
	if utf8.RuneCountInString(s) <= dirNameMax {
		return s
	}
	cut := string([]rune(s)[:dirNameMax])
	cut = strings.TrimRight(cut, ". ")
	if cut == "" {
		return "Unknown"
	}
	return cut
}

func fileName(name string) string {
	s := fatSafe(name)
	if len(s) <= fileNameMax {
		return s
	}
	ext := filepath.Ext(s)
	stem := strings.TrimSuffix(s, ext)
	if ext == "" || len(ext) >= fileNameMax {
		stem, ext = s, ""
	}
	end := fileNameMax - len(ext)
	if end > len(stem) {
		end = len(stem)
	}
	for end > 0 && !utf8.RuneStart(stem[end]) {
		end--
	}
	short := strings.TrimRight(stem[:end], ". ")
	if short == "" {
		short = "Unknown"
	}
	return short + ext
}

type namedID struct {
	id   uint32
	name string
}

type interner struct {
	ids   map[string]uint32
	max   uint32
	added []namedID
}

func newInterner() *interner { return &interner{ids: map[string]uint32{}} }

// id devuelve el identificador de un nombre (0 para el vacío); si es nuevo le asigna el siguiente libre.
func (in *interner) id(name string) uint32 {
	if name == "" {
		return 0
	}
	if v, ok := in.ids[name]; ok {
		return v
	}
	in.max++
	in.ids[name] = in.max
	in.added = append(in.added, namedID{in.max, name})
	return in.max
}

type placed struct {
	t       Track
	id      uint32
	audio   string // /Contents/…
	dir     string // /PIONEER/USBANLZ/Pxxx/HASH
	name    string
	hasAnlz bool
	size    int64
	probe   probeInfo
	row     []byte
	reused  bool // ya estaba en la memoria: no se vuelve a escribir
}

// Export escribe la biblioteca Pioneer en opts.Dest. Si la memoria ya tiene un export.pdb se AÑADE a lo que hay
// (canciones y playlists existentes intactas); si no, se crea la biblioteca desde cero.
// contentsDir es la carpeta de la memoria donde va el audio de la canción (con barra final).
func contentsDir(t Track) string {
	art, alb := t.Artist, t.Album
	if art == "" {
		art = "UnknownArtist"
	}
	if alb == "" {
		alb = "UnknownAlbum"
	}
	return "/Contents/" + dirName(art) + "/" + dirName(alb) + "/"
}

// TargetPath es la ruta (sin colisiones) donde quedaría el audio dentro de la memoria; sirve para saber si ya está.
func TargetPath(t Track) string { return contentsDir(t) + fileName(filepath.Base(t.Src)) }

func Export(ctx context.Context, opts Options) (*Report, error) {
	rep := &Report{}
	st, err := os.Stat(opts.Dest)
	if err != nil || !st.IsDir() {
		return nil, errors.New("la memoria USB no está disponible")
	}
	root := "PIONEER"
	hasLib := func(r string) bool {
		_, e1 := os.Stat(filepath.Join(opts.Dest, r, "rekordbox", "export.pdb"))
		_, e2 := os.Stat(filepath.Join(opts.Dest, r, "rekordbox", "exportLibrary.db"))
		_, e3 := os.Stat(filepath.Join(opts.Dest, r, "DEVSETTING.DAT"))
		return e1 == nil || e2 == nil || e3 == nil
	}
	switch p, d := hasLib("PIONEER"), hasLib(".PIONEER"); {
	case p && d:
		return nil, errors.New("la memoria tiene PIONEER y .PIONEER a la vez; borra uno antes de exportar")
	case d:
		root = ".PIONEER"
	}
	rep.Root = root
	dbDir := filepath.Join(opts.Dest, root, "rekordbox")
	pdbPath := filepath.Join(dbDir, "export.pdb")

	// 0. Biblioteca existente: se lee entera antes de tocar nada; si no se entiende, no se modifica la memoria.
	var state *libState
	if old, err := os.ReadFile(pdbPath); err == nil {
		state, err = readLibrary(old)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer la biblioteca que ya tiene la memoria (%v); no se modificó nada", err)
		}
		rep.Merged = true
	} else {
		state = newFreshState(opts.DeviceName)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "exportLibrary.db")); err == nil {
		rep.Warnings = append(rep.Warnings, "La memoria tiene también la base OneLibrary de rekordbox (exportLibrary.db), que esta app no puede actualizar. Los equipos que usan la biblioteca clásica (CDJ-2000NXS2, CDJ-3000, XDJ-1000MK2…) verán lo nuevo; un equipo que lea solo OneLibrary no lo verá hasta que sincronices con rekordbox.")
	}

	artists, albums, keys := newInterner(), newInterner(), newInterner()
	for _, r := range state.rows[tArtists] {
		id, n := artistOf(r)
		artists.seed(id, n)
	}
	for _, r := range state.rows[tAlbums] {
		id, n := albumOf(r)
		albums.seed(id, n)
	}
	for _, r := range state.rows[tKeys] {
		id, n := keyOf(r)
		keys.seed(id, n)
	}
	// Ids ocupados aunque su nombre sea vacío.
	artists.max = max(artists.max, maxOf(state.rows[tArtists], func(r []byte) uint32 { id, _ := artistOf(r); return id }))
	albums.max = max(albums.max, maxOf(state.rows[tAlbums], func(r []byte) uint32 { id, _ := albumOf(r); return id }))
	keys.max = max(keys.max, maxOf(state.rows[tKeys], func(r []byte) uint32 { id, _ := keyOf(r); return id }))
	nextTrack := maxOf(state.rows[tTracks], trackIDOf) + 1

	taken := map[string]bool{}
	existing := map[string]uint32{} // ruta (minúsculas) → id de una canción que ya está en la memoria
	dirTaken := map[string]bool{}
	for _, r := range state.rows[tTracks] {
		p := lowerKey(trackStr(r, slotFilePath))
		if p != "" {
			taken[p] = true
			existing[p] = trackIDOf(r)
		}
		if a := trackStr(r, slotAnalyzePath); a != "" {
			dirTaken[a[:strings.LastIndex(a, "/")+0]] = true
		}
	}
	diskDirTaken := func(dir string) bool {
		if dirTaken[dir] {
			return true
		}
		_, err := os.Stat(filepath.Join(opts.Dest, filepath.FromSlash(dir)))
		return err == nil
	}

	// 1. Qué canciones entran.
	used := map[int]bool{}
	for _, pl := range opts.Playlists {
		for _, i := range pl.Tracks {
			if i >= 0 && i < len(opts.Tracks) {
				used[i] = true
			}
		}
	}
	var order []int
	for i := range used {
		order = append(order, i)
	}
	sort.Ints(order)

	var items []*placed
	exportID := map[int]uint32{}
	for _, i := range order {
		t := opts.Tracks[i]
		if !SupportedExt(t.Src) {
			rep.Skipped = append(rep.Skipped, filepath.Base(t.Src)+": formato no compatible con los CDJ (usa MP3, M4A, FLAC o WAV)")
			continue
		}
		fi, err := os.Stat(t.Src)
		if err != nil {
			rep.Skipped = append(rep.Skipped, filepath.Base(t.Src)+": no se encuentra el archivo")
			continue
		}
		p := &placed{t: t, size: fi.Size(), id: nextTrack}
		art, alb := t.Artist, t.Album
		if art == "" {
			art = "UnknownArtist"
		}
		if alb == "" {
			alb = "UnknownAlbum"
		}
		p.name = fileName(filepath.Base(t.Src))
		base := contentsDir(t)
		p.audio = base + p.name
		// Misma ruta y mismo tamaño que un archivo ya presente: es la misma canción, se reutiliza.
		if id, ok := existing[strings.ToLower(p.audio)]; ok {
			if di, err := os.Stat(filepath.Join(opts.Dest, filepath.FromSlash(p.audio))); err == nil && di.Size() == fi.Size() {
				p.reused, p.id = true, id
				exportID[i] = id
				items = append(items, p)
				rep.Reused++
				continue
			}
		}
		for n := 0; taken[strings.ToLower(p.audio)]; n++ {
			ext := filepath.Ext(p.name)
			suffix := fmt.Sprintf("-%d", p.id)
			if n > 0 {
				suffix = fmt.Sprintf("-%d-%d", p.id, n)
			}
			p.name = fileName(strings.TrimSuffix(filepath.Base(t.Src), filepath.Ext(t.Src)) + suffix + ext)
			p.audio = base + p.name
		}
		if len(p.audio) > 250 {
			rep.Skipped = append(rep.Skipped, filepath.Base(t.Src)+": ruta demasiado larga")
			continue
		}
		taken[strings.ToLower(p.audio)] = true
		// La carpeta de análisis sale del hash de la ruta final; si choca con otra, se renombra.
		for n := 0; diskDirTaken(analysisDir(p.audio, root)); n++ {
			ext := filepath.Ext(p.name)
			p.name = fileName(strings.TrimSuffix(filepath.Base(t.Src), ext) + fmt.Sprintf("-%d-h%d", p.id, n) + ext)
			p.audio = base + p.name
			taken[strings.ToLower(p.audio)] = true
		}
		p.dir = analysisDir(p.audio, root)
		p.probe = probe(t.Src, fi.Size(), t.Duration)
		if len(t.Wave) >= 3*50 && t.WaveFPS > 0 && t.Duration > 0 {
			p.hasAnlz = true
		}
		analyze := ""
		if p.hasAnlz {
			analyze = p.dir + "/ANLZ0000.DAT"
		}
		p.row = trackRow(trackInput{
			ID:          p.id,
			ArtistID:    artists.id(t.Artist),
			AlbumID:     albums.id(t.Album),
			KeyID:       keys.id(t.Key),
			TempoX100:   uint32(math.Round(t.BPM * 100)),
			DurationSec: uint16(math.Min(math.Round(t.Duration), 0xffff)),
			Bitrate:     p.probe.Bitrate,
			SampleRate:  p.probe.SampleRate,
			FileSize:    uint32(math.Min(float64(p.size), 0xffffffff)),
			SampleDepth: p.probe.BitDepth,
			Title:       t.Title,
			Filename:    p.name,
			FilePath:    p.audio,
			AnalyzePath: analyze,
			DateAdded:   time.Now().Format("2006-01-02"),
			AutoLoadHot: len(t.Cues) > 0,
		})
		if len(p.row) > maxRowLen() {
			rep.Skipped = append(rep.Skipped, filepath.Base(t.Src)+": el título o la ruta son demasiado largos")
			delete(taken, strings.ToLower(p.audio))
			continue
		}
		dirTaken[p.dir] = true
		nextTrack++
		exportID[i] = p.id
		items = append(items, p)
	}
	if len(items) == 0 {
		return rep, errors.New("no hay canciones compatibles para exportar")
	}

	// 2. Archivos de análisis (en memoria) y copia del audio. Lo que ya estaba en la memoria no se toca.
	for _, d := range []string{"Contents", filepath.Join(root, "USBANLZ"), filepath.Join(root, "Artwork"), filepath.Join(root, "rekordbox")} {
		if err := os.MkdirAll(filepath.Join(opts.Dest, d), 0o755); err != nil {
			return rep, fmt.Errorf("no se pudo escribir en la memoria: %w", err)
		}
	}
	total := len(items)
	for n, p := range items {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if opts.Progress != nil {
			opts.Progress(n, total, p.name)
		}
		if p.reused {
			continue
		}
		dst := filepath.Join(opts.Dest, filepath.FromSlash(p.audio))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return rep, err
		}
		copied, err := copyFile(ctx, p.t.Src, dst)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", p.name, err)
		}
		rep.Bytes += copied
		if p.hasAnlz {
			files := AuthorAnalysis(p.audio, buildBeats(p.t), buildColumns(p.t), exportCues(p.t))
			adir := filepath.Join(opts.Dest, filepath.FromSlash(p.dir))
			if err := os.MkdirAll(adir, 0o755); err != nil {
				return rep, err
			}
			for name, data := range map[string][]byte{"ANLZ0000.DAT": files.DAT, "ANLZ0000.EXT": files.EXT, "ANLZ0000.2EX": files.TwoEX} {
				if err := writeAtomic(filepath.Join(adir, name), data); err != nil {
					return rep, err
				}
			}
		}
	}
	if opts.Progress != nil {
		opts.Progress(total, total, "Escribiendo la base de datos…")
	}

	// 3. Base de datos: al final, para que una exportación interrumpida no deje una base que apunte a archivos que faltan.
	for _, p := range items {
		if p.reused {
			continue
		}
		state.rows[tTracks] = append(state.rows[tTracks], p.row)
		rep.Tracks++
		if p.hasAnlz {
			rep.Analyzed++
		}
	}
	for _, a := range artists.added {
		state.rows[tArtists] = append(state.rows[tArtists], artistRow(a.id, a.name))
	}
	for _, a := range albums.added {
		state.rows[tAlbums] = append(state.rows[tAlbums], albumRow(a.id, 0, a.name))
	}
	for _, a := range keys.added {
		state.rows[tKeys] = append(state.rows[tKeys], keyRow(a.id, a.name))
	}
	mergePlaylists(state, opts.Playlists, exportID, rep)

	// La fila "property" cuenta las canciones de la memoria.
	if props := state.rows[tProperty]; len(props) > 0 {
		last := props[len(props)-1]
		if len(last) >= 8 {
			binary.LittleEndian.PutUint32(last[4:], uint32(len(state.rows[tTracks])))
		}
	}
	pdb := buildPDB(state)
	if rep.Merged {
		// Copia de seguridad de la base anterior (una sola, la última).
		if old, err := os.ReadFile(pdbPath); err == nil {
			_ = writeAtomic(pdbPath+".bak", old)
		}
	}
	if err := writeAtomic(pdbPath, pdb); err != nil {
		return rep, err
	}
	if opts.Waveform != "" {
		if err := writeDevSetting(filepath.Join(opts.Dest, root), opts.Waveform, opts.KeyAlphanumeric); err != nil {
			rep.Warnings = append(rep.Warnings, "No se pudo ajustar el color de la onda / formato de tonalidad: "+err.Error())
		}
	}
	if _, err := os.Stat(filepath.Join(dbDir, "exportExt.pdb")); err != nil {
		if err := writeAtomic(filepath.Join(dbDir, "exportExt.pdb"), buildExtPDB()); err != nil {
			return rep, err
		}
	}
	if len(rep.Skipped) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d canciones no se exportaron.", len(rep.Skipped)))
	}
	if rep.Analyzed < rep.Tracks {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d canciones nuevas no estaban analizadas en la app: se copiaron sin forma de onda ni rejilla (el CDJ las analizará al cargarlas).", rep.Tracks-rep.Analyzed))
	}
	return rep, nil
}

// mergePlaylists añade las playlists pedidas: si ya hay una en la raíz con el mismo nombre se le agregan solo las
// canciones que le faltan; si no, se crea. Lo demás del árbol y las entradas existentes quedan intactos.
func mergePlaylists(state *libState, lists []Playlist, exportID map[int]uint32, rep *Report) {
	nodes := map[uint32]plNode{}
	byName := map[string]plNode{}
	var maxPl, maxSort uint32
	rootCount := false
	for _, r := range state.rows[tPlaylists] {
		n := nodeOf(r)
		nodes[n.id] = n
		if n.id > maxPl {
			maxPl = n.id
		}
		if n.parent == 0 {
			if !rootCount || n.sort >= maxSort {
				maxSort, rootCount = n.sort, true
			}
			byName[strings.ToLower(n.name)] = n
		}
	}
	inList := map[uint32]map[uint32]bool{}
	maxIdx := map[uint32]uint32{}
	for _, r := range state.rows[tEntries] {
		idx, tr, pl := rd4(r, 0), rd4(r, 4), rd4(r, 8)
		if inList[pl] == nil {
			inList[pl] = map[uint32]bool{}
		}
		inList[pl][tr] = true
		if idx > maxIdx[pl] {
			maxIdx[pl] = idx
		}
	}
	for _, pl := range lists {
		var ids []uint32
		seen := map[uint32]bool{}
		for _, i := range pl.Tracks {
			if id, ok := exportID[i]; ok && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		name := pl.Name
		target, exists := byName[strings.ToLower(name)]
		if pl.ExistingID != 0 {
			if n, ok := nodes[pl.ExistingID]; ok && !n.folder {
				target, exists = n, true
				name = n.name
			}
		}
		if exists && target.folder {
			name += " (Cyberia)"
			target, exists = byName[strings.ToLower(name)]
			if exists && target.folder {
				continue
			}
		}
		if name == "" {
			continue
		}
		var id uint32
		if exists {
			id = target.id
			added := 0
			for _, tr := range ids {
				if inList[id][tr] {
					continue
				}
				maxIdx[id]++
				state.rows[tEntries] = append(state.rows[tEntries], playlistEntryRow(maxIdx[id], tr, id))
				added++
			}
			if added > 0 {
				rep.Extended++
			}
			continue
		}
		maxPl++
		id = maxPl
		so := uint32(0)
		if rootCount {
			maxSort++
			so = maxSort
		} else {
			rootCount = true
		}
		state.rows[tPlaylists] = append(state.rows[tPlaylists], playlistRow(id, 0, so, false, name))
		byName[strings.ToLower(name)] = plNode{id: id, name: name}
		for k, tr := range ids {
			state.rows[tEntries] = append(state.rows[tEntries], playlistEntryRow(uint32(k+1), tr, id))
		}
		maxIdx[id] = uint32(len(ids))
		rep.Playlist++
	}
}

func buildPDB(s *libState) []byte {
	f := &fileBuilder{}
	num := func(row []byte, slot uint16) { setIndexShift(row, slot) }
	for _, typ := range s.order {
		var n func([]byte, uint16)
		switch typ {
		case tTracks, tArtists, tAlbums, tProperty:
			n = num
		}
		f.addTable(typ, s.rows[typ], n)
	}
	return f.finish()
}

// buildExtPDB es el exportExt.pdb sin etiquetas (My Tags): nueve tablas, la 7 con la fila maestra.
func buildExtPDB() []byte {
	f := &fileBuilder{writeOrder: []uint32{7, 3}}
	master := make([]byte, 60)
	copy(master, []byte{0x00, 0x07, 0x00, 0x00})
	master[0x1c] = 0x03
	copy(master[0x1d:], []byte{0x22, 0x23, 0x24, 0x25, 0x26})
	for i := 0x22; i < 0x27; i++ {
		master[i] = 0x03
	}
	for typ := uint32(0); typ <= 8; typ++ {
		if typ == 7 {
			f.addTable(typ, [][]byte{master}, nil)
		} else {
			f.addTable(typ, nil, nil)
		}
	}
	return f.finish()
}

// buildBeats genera los pulsos de la rejilla con el tiempo fuerte en el primer pulso.
func buildBeats(t Track) []Beat {
	if !t.HasGrid || t.BPM <= 0 || t.Duration <= 0 {
		return nil
	}
	period := 60 / t.BPM
	tempo := uint16(math.Min(math.Round(t.BPM*100), 0xffff))
	// El desfase de la rejilla es un pulso 1 (puede estar lejos del inicio): se retrocede hasta el primer pulso
	// que cae dentro de la canción, conservando qué pulso es el 1.
	k := int(math.Ceil(-t.GridOffset/period - 1e-9))
	var out []Beat
	for ; len(out) < 1<<18; k++ {
		sec := t.GridOffset + float64(k)*period
		if sec > t.Duration {
			break
		}
		out = append(out, Beat{Number: uint16(((k%4)+4)%4) + 1, TempoX100: tempo, TimeMs: uint32(math.Round(math.Max(sec, 0) * 1000))})
	}
	return out
}

// buildColumns pasa la waveform de la app (graves/medios/agudos por cuadro) a 150 columnas por segundo.
func buildColumns(t Track) []BandColumn {
	frames := len(t.Wave) / 3
	if frames == 0 || t.WaveFPS <= 0 || t.Duration <= 0 {
		return nil
	}
	n := int(t.Duration * 150)
	cols := make([]BandColumn, n)
	for i := range cols {
		a := int(float64(i) / 150 * t.WaveFPS)
		b := int(float64(i+1) / 150 * t.WaveFPS)
		if b <= a {
			b = a + 1
		}
		var c BandColumn
		for f := a; f < b && f < frames; f++ {
			c.Low, c.Mid, c.High = max8(c.Low, t.Wave[f*3]), max8(c.Mid, t.Wave[f*3+1]), max8(c.High, t.Wave[f*3+2])
		}
		c.Peak = max8(c.Low, max8(c.Mid, c.High))
		cols[i] = c
	}
	return cols
}

func exportCues(t Track) []ExportCue {
	var out []ExportCue
	var hot []Cue
	for _, c := range t.Cues {
		if c.Slot >= 0 && c.Slot < 8 {
			hot = append(hot, c)
		} else if c.Slot < 0 {
			out = append(out, ExportCue{Kind: 0, TimeMs: uint32(max0(c.TimeMs)), ColorCode: nearestPaletteCode(int(c.R), int(c.G), int(c.B)), Comment: c.Name})
		}
	}
	sort.SliceStable(hot, func(i, j int) bool { return hot[i].Slot < hot[j].Slot })
	var res []ExportCue
	for _, c := range hot {
		kind := uint8(c.Slot + 1)
		if c.Slot >= 3 {
			kind = uint8(c.Slot + 2)
		}
		res = append(res, ExportCue{Kind: kind, TimeMs: uint32(max0(c.TimeMs)), ColorCode: nearestPaletteCode(int(c.R), int(c.G), int(c.B)), Comment: c.Name})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TimeMs < out[j].TimeMs })
	return append(res, out...)
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func copyFile(ctx context.Context, src, dst string) (int64, error) {
	if fi, err := os.Stat(dst); err == nil {
		if si, e2 := os.Stat(src); e2 == nil && si.Size() == fi.Size() {
			return 0, nil // ya está copiado
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 1<<20)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			os.Remove(tmp)
			return n, err
		}
		k, rerr := in.Read(buf)
		if k > 0 {
			if _, werr := out.Write(buf[:k]); werr != nil {
				out.Close()
				os.Remove(tmp)
				return n, werr
			}
			n += int64(k)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return n, rerr
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return n, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return n, err
	}
	return n, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
