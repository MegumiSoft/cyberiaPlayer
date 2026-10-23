package pioneer

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// Lectura de un export.pdb existente para AÑADIR a una memoria que ya tiene biblioteca sin tocar lo que hay.
// Las filas existentes se conservan byte a byte (solo se renumera su posición dentro de la página, que el
// escritor recalcula); las filas nuevas se anexan con identificadores nuevos. Port de la lectura de rbxport
// (GPL-2.0-or-later), crates/rbl-pdb/src/lib.rs.

const (
	tTracks    = 0
	tArtists   = 2
	tAlbums    = 3
	tKeys      = 5
	tPlaylists = 7
	tEntries   = 8
	tProperty  = 19
)

// libState es el contenido de las tablas, como filas ya codificadas, en el orden en que están en el archivo.
type libState struct {
	order []uint32
	rows  map[uint32][][]byte
}

func newFreshState(deviceName string) *libState {
	s := &libState{rows: map[uint32][][]byte{}}
	for t := uint32(0); t <= 19; t++ {
		s.order = append(s.order, t)
	}
	for i, n := range colorNames {
		s.rows[6] = append(s.rows[6], colorRow(uint16(i+1), n))
	}
	s.rows[16] = copyRows(referenceColumns)
	s.rows[17] = copyRows(referenceHistoryPlaylists)
	s.rows[18] = copyRows(referenceHistoryEntries)
	s.rows[tProperty] = [][]byte{propertyRow(0, time.Now().Format("2006-01-02"), deviceName)}
	return s
}

func copyRows(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, r := range in {
		out[i] = append([]byte(nil), r...)
	}
	return out
}

func rd2(b []byte, at int) int {
	if at < 0 || at+2 > len(b) {
		return 0
	}
	return int(binary.LittleEndian.Uint16(b[at:]))
}

func rd4(b []byte, at int) uint32 {
	if at < 0 || at+4 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint32(b[at:])
}

func rowIndexLen(page []byte) int {
	packed := int(page[0x18]) | int(page[0x19])<<8 | int(page[0x1a])<<16
	offsets := packed & 0x1fff
	legacy := rd2(page, 0x22)
	if offsets == 0xff && legacy > offsets && legacy != 0x1fff {
		return legacy
	}
	return offsets
}

// readLibrary interpreta un export.pdb. Las filas borradas (bit de presencia apagado) se descartan.
func readLibrary(b []byte) (*libState, error) {
	if len(b) < 28+16 {
		return nil, errors.New("export.pdb demasiado pequeño")
	}
	ps := int(rd4(b, 4))
	if ps < 512 || ps > 65536 || ps&(ps-1) != 0 {
		return nil, errors.New("export.pdb no tiene un tamaño de página válido")
	}
	n := int(rd4(b, 8))
	if n < 1 || n > 64 {
		return nil, errors.New("export.pdb con un número de tablas inesperado")
	}
	s := &libState{rows: map[uint32][][]byte{}}
	for i := 0; i < n; i++ {
		at := 28 + i*16
		if at+16 > len(b) {
			return nil, errors.New("export.pdb truncado")
		}
		typ, first, last := rd4(b, at), rd4(b, at+8), rd4(b, at+12)
		s.order = append(s.order, typ)
		s.rows[typ] = append(s.rows[typ], tableRows(b, ps, first, last)...)
	}
	return s, nil
}

func tableRows(b []byte, ps int, first, last uint32) [][]byte {
	var out [][]byte
	idx := first
	for guard := 0; guard < 100000; guard++ {
		off := int(idx) * ps
		if off < 0 || off+ps > len(b) {
			break
		}
		p := b[off : off+ps]
		if p[0x1b]&0x40 == 0 {
			if num := rowIndexLen(p); num > 0 {
				used := rd2(p, 0x1e)
				type ent struct {
					off     int
					present bool
				}
				var ents []ent
				groups := (num-1)/16 + 1
				for g := 0; g < groups; g++ {
					base := ps - g*rowGroupLen
					present := rd2(p, base-4)
					in := 16
					if g == groups-1 {
						in = (num-1)%16 + 1
					}
					for r := 0; r < in; r++ {
						ents = append(ents, ent{rd2(p, base-(6+2*r)), present&(1<<r) != 0})
					}
				}
				var offs []int
				for _, e := range ents {
					offs = append(offs, e.off)
				}
				sort.Ints(offs)
				for _, e := range ents {
					if !e.present {
						continue
					}
					end := used
					for _, o := range offs {
						if o > e.off {
							end = o
							break
						}
					}
					if end > ps-pageHeaderLen {
						end = ps - pageHeaderLen
					}
					start := pageHeaderLen + e.off
					if end <= e.off || start+(end-e.off) > len(p) {
						continue
					}
					out = append(out, append([]byte(nil), p[start:start+(end-e.off)]...))
				}
			}
		}
		next := rd4(p, 0x0c)
		if idx == last || next == idx || next == 0 {
			break
		}
		idx = next
	}
	return out
}

// strAt lee una cadena DeviceSQL dentro de una fila.
func strAt(row []byte, off int) string {
	if off <= 0 || off >= len(row) {
		return ""
	}
	switch f := row[off]; {
	case f == 0x40:
		l := rd2(row, off+1)
		if l >= 4 && off+l <= len(row) {
			return string(row[off+4 : off+l])
		}
	case f == 0x90:
		l := rd2(row, off+1)
		if l >= 4 && off+l <= len(row) {
			var u []uint16
			for i := off + 4; i+1 < off+l; i += 2 {
				v := binary.LittleEndian.Uint16(row[i:])
				if v == 0 {
					break
				}
				u = append(u, v)
			}
			return string(utf16.Decode(u))
		}
	case f%2 == 1:
		l := int(f-1)/2 - 1
		if l >= 0 && off+1+l <= len(row) {
			return string(row[off+1 : off+1+l])
		}
	}
	return ""
}

func trackIDOf(row []byte) uint32 { return rd4(row, 0x48) }
func trackStr(row []byte, slot int) string {
	return strAt(row, rd2(row, trackFixedLen+slot*2))
}

func artistOf(row []byte) (uint32, string) {
	ofs := int(row[min(9, len(row)-1)])
	if rd2(row, 0) == 0x64 {
		ofs = rd2(row, 0x0a)
	}
	return rd4(row, 4), strAt(row, ofs)
}

func albumOf(row []byte) (uint32, string) {
	ofs := 0
	if len(row) > 0x15 {
		ofs = int(row[0x15])
	}
	return rd4(row, 0x0c), strAt(row, ofs)
}

func keyOf(row []byte) (uint32, string) { return rd4(row, 0), strAt(row, 8) }

type plNode struct {
	id, parent, sort uint32
	folder           bool
	name             string
}

func nodeOf(row []byte) plNode {
	return plNode{id: rd4(row, 12), parent: rd4(row, 0), sort: rd4(row, 8), folder: rd4(row, 16) != 0, name: strAt(row, playlistNameAt)}
}

const playlistNameAt = 20

// seed rellena un interner con los nombres que ya hay (id → nombre) conservando los identificadores.
func (in *interner) seed(id uint32, name string) {
	if name == "" {
		return
	}
	if _, ok := in.ids[name]; !ok {
		in.ids[name] = id
	}
	if id > in.max {
		in.max = id
	}
}

func maxOf(rows [][]byte, get func([]byte) uint32) uint32 {
	var m uint32
	for _, r := range rows {
		if v := get(r); v > m {
			m = v
		}
	}
	return m
}

func lowerKey(s string) string { return strings.ToLower(s) }

// PlaylistInfo es una playlist (o carpeta) que ya existe en una memoria.
type PlaylistInfo struct {
	ID     uint32   `json:"id"`
	Name   string   `json:"name"`
	Folder bool     `json:"folder"`
	Parent string   `json:"parent"` // nombre de la carpeta que la contiene ("" si está en la raíz)
	Count  int      `json:"count"`
	Paths  []string `json:"-"` // rutas (minúsculas) de sus canciones
}

// LibraryInfo es lo que ya tiene una memoria Pioneer.
type LibraryInfo struct {
	Playlists []PlaylistInfo
	Tracks    map[string]bool // rutas (minúsculas) de las canciones que ya están
}

// Inspect lee la biblioteca clásica de la memoria (export.pdb). Si no hay, devuelve nil sin error.
func Inspect(dest string) (*LibraryInfo, error) {
	var raw []byte
	for _, r := range []string{"PIONEER", ".PIONEER"} {
		if b, err := os.ReadFile(filepath.Join(dest, r, "rekordbox", "export.pdb")); err == nil {
			raw = b
			break
		}
	}
	if raw == nil {
		return nil, nil
	}
	st, err := readLibrary(raw)
	if err != nil {
		return nil, err
	}
	info := &LibraryInfo{Tracks: map[string]bool{}}
	pathOf := map[uint32]string{}
	for _, r := range st.rows[tTracks] {
		if p := lowerKey(trackStr(r, slotFilePath)); p != "" {
			pathOf[trackIDOf(r)] = p
			info.Tracks[p] = true
		}
	}
	members := map[uint32][]string{}
	for _, r := range st.rows[tEntries] {
		if p, ok := pathOf[rd4(r, 4)]; ok {
			members[rd4(r, 8)] = append(members[rd4(r, 8)], p)
		}
	}
	nodes := map[uint32]plNode{}
	for _, r := range st.rows[tPlaylists] {
		n := nodeOf(r)
		nodes[n.id] = n
	}
	var ids []uint32
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := nodes[ids[i]], nodes[ids[j]]
		if a.parent != b.parent {
			return a.parent < b.parent
		}
		return a.sort < b.sort
	})
	for _, id := range ids {
		n := nodes[id]
		pi := PlaylistInfo{ID: n.id, Name: n.name, Folder: n.folder, Paths: members[n.id], Count: len(members[n.id])}
		if par, ok := nodes[n.parent]; ok && n.parent != 0 {
			pi.Parent = par.name
		}
		info.Playlists = append(info.Playlists, pi)
	}
	return info, nil
}
