// Package pioneer escribe una memoria USB que reproducen los equipos Pioneer DJ (CDJ/XDJ):
// la base de datos DeviceSQL (export.pdb), los archivos de análisis ANLZ (rejilla, waveform, cues)
// y la copia del audio.
//
// El formato es ingeniería inversa de la comunidad. Este código es un port a Go de la lógica de
// rbxport (https://github.com/chrisle/rbxport, GPL-2.0-or-later, © sus autores), en concreto de
// crates/rbl-pdb, crates/rbl-anlz y crates/rbl-export, por lo que este paquete (y el programa que lo
// incluye) se distribuye bajo GPL-2.0-or-later. Ver LICENSE.
package pioneer

import (
	"encoding/binary"
	"sort"
	"unicode/utf16"
)

const (
	pageSize      = 4096
	pageHeaderLen = 0x28
	rowGroupLen   = 0x24
)

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// shortASCII: la longitud se "incrementa, duplica e incrementa otra vez".
func shortASCII(text string) []byte {
	m := (len(text)+1)*2 + 1
	if m > 0xff {
		m = 0xff
	}
	return append([]byte{byte(m)}, text...)
}

func u16le(v int) []byte {
	if v > 0xffff {
		v = 0xffff
	}
	return []byte{byte(v), byte(v >> 8)}
}

// longASCII: 0x40, u16 con la longitud (4 + texto), un byte de relleno y el texto sin terminador.
func longASCII(text string) []byte {
	out := []byte{0x40}
	out = append(out, u16le(4+len(text))...)
	out = append(out, 0)
	return append(out, text...)
}

// longUTF16 es la forma 0x90 para texto no ASCII (UTF-16LE sin terminador).
func longUTF16(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := []byte{0x90}
	out = append(out, u16le(4+len(units)*2)...)
	out = append(out, 0)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// deviceSQLString elige la codificación que usaría rekordbox.
func deviceSQLString(text string) []byte {
	if isASCII(text) {
		if len(text) < 0x7e {
			return shortASCII(text)
		}
		return longASCII(text)
	}
	return longUTF16(text)
}

// pushString añade texto a una fila y devuelve el desplazamiento donde empieza.
// Las cadenas largas arrancan alineadas a 4 bytes.
func pushString(row *[]byte, text string) int {
	enc := deviceSQLString(text)
	if enc[0] == 0x40 || enc[0] == 0x90 {
		for len(*row)%4 != 0 {
			*row = append(*row, 0)
		}
	}
	at := len(*row)
	*row = append(*row, enc...)
	return at
}

// maxRowLen es la fila más larga que cabe en una página.
func maxRowLen() int { return pageSize - (pageHeaderLen + rowGroupLen + 8) }

type pageBuilder struct {
	pageType uint32
	heap     []byte
	offsets  []uint16
}

func (p *pageBuilder) freeSpace() int {
	groups := (len(p.offsets) + 15) / 16
	if groups < 1 {
		groups = 1
	}
	f := pageSize - pageHeaderLen - len(p.heap) - groups*rowGroupLen
	if f < 0 {
		return 0
	}
	return f
}

func (p *pageBuilder) fits(n int) bool { return p.freeSpace() >= n+8 }

func (p *pageBuilder) push(row []byte) {
	for len(p.heap)%4 != 0 {
		p.heap = append(p.heap, 0)
	}
	p.offsets = append(p.offsets, uint16(len(p.heap)))
	p.heap = append(p.heap, row...)
}

func (p *pageBuilder) finish() []byte {
	page := make([]byte, pageSize)
	n := len(p.offsets)
	lastMarked := n > 0xff
	binary.LittleEndian.PutUint32(page[0x08:], p.pageType)
	rows := uint32(n)
	packed := (rows & 0x1fff) | (rows&0x7ff)<<13
	page[0x18], page[0x19], page[0x1a] = byte(packed), byte(packed>>8), byte(packed>>16)
	page[0x1b] = 0x24
	used := (len(p.heap) + 3) / 4 * 4
	groups := (n + 15) / 16
	free := pageSize - pageHeaderLen - used - (2*n + 4*groups)
	if free < 0 {
		free = 0
	}
	binary.LittleEndian.PutUint16(page[0x1c:], uint16(free))
	binary.LittleEndian.PutUint16(page[0x1e:], uint16(used))
	if lastMarked {
		binary.LittleEndian.PutUint16(page[0x20:], 1)
		binary.LittleEndian.PutUint16(page[0x22:], uint16(n-1))
	} else {
		binary.LittleEndian.PutUint16(page[0x20:], uint16(n))
	}
	copy(page[pageHeaderLen:], p.heap)
	for g := 0; g < groups; g++ {
		base := pageSize - g*rowGroupLen
		inGroup := 16
		if g == groups-1 {
			inGroup = n - g*16
		}
		var present uint16
		for r := 0; r < inGroup; r++ {
			present |= 1 << r
			at := base - (6 + 2*r)
			binary.LittleEndian.PutUint16(page[at:], p.offsets[g*16+r])
		}
		second := present
		if lastMarked {
			second = 0
			if g == groups-1 {
				second = 1 << (inGroup - 1)
			}
		}
		binary.LittleEndian.PutUint16(page[base-4:], present)
		binary.LittleEndian.PutUint16(page[base-2:], second)
	}
	return page
}

func indexPage(idx, typ, next uint32, firstData uint32, hasFirst bool) []byte {
	page := make([]byte, pageSize)
	le := binary.LittleEndian
	le.PutUint32(page[0x04:], idx)
	le.PutUint32(page[0x08:], typ)
	le.PutUint32(page[0x0c:], next)
	le.PutUint32(page[0x10:], 1)
	page[0x1b] = 0x64
	le.PutUint16(page[0x20:], 0x1fff)
	le.PutUint16(page[0x22:], 0x1fff)
	le.PutUint16(page[0x24:], 0x03ec)
	b := pageHeaderLen
	le.PutUint32(page[b:], idx)
	fd := uint32(0x03ffffff)
	if hasFirst {
		fd = firstData
	}
	le.PutUint32(page[b+4:], fd)
	le.PutUint32(page[b+8:], 0x03ffffff)
	at := b + 18
	for i := 0; i < 1004; i++ {
		if at+4 > len(page) {
			break
		}
		copy(page[at:], []byte{0xff, 0x1f, 0xf8, 0xff})
		at += 4
	}
	if at+2 <= len(page) {
		copy(page[at:], []byte{0xff, 0x1f})
	}
	return page
}

type table struct {
	typ   uint32
	pages [][]byte
}

type fileBuilder struct {
	tables     []table
	writeOrder []uint32
}

// addTable agrega una tabla con filas ya codificadas; number (opcional) recibe la posición de la fila
// dentro de su página.
func (f *fileBuilder) addTable(typ uint32, rows [][]byte, number func(row []byte, slot uint16)) {
	var pages [][]byte
	cur := &pageBuilder{pageType: typ}
	for _, r := range rows {
		if !cur.fits(len(r)) {
			pages = append(pages, cur.finish())
			cur = &pageBuilder{pageType: typ}
		}
		row := append([]byte(nil), r...)
		if number != nil {
			number(row, uint16(len(cur.offsets)))
		}
		cur.push(row)
	}
	pages = append(pages, cur.finish())
	f.tables = append(f.tables, table{typ, pages})
}

func hasRows(p []byte) bool {
	return p[0x18] != 0 || p[0x19]&0x1f != 0 || p[0x22] != 0 || p[0x23] != 0
}

type entry struct{ typ, first, last, cand uint32 }

func (f *fileBuilder) finish() []byte {
	le := binary.LittleEndian
	out := make([]byte, pageSize)
	nextIndex := uint32(1)
	sequence := uint32(1)
	var entries []entry
	var pages [][]byte
	type held struct {
		at   int
		rest [][]byte
	}
	var hold []held
	for at, t := range f.tables {
		tp := t.pages
		any := false
		for _, p := range tp {
			if hasRows(p) {
				any = true
				break
			}
		}
		indexAt := nextIndex
		nextIndex++
		if any {
			firstData := nextIndex
			nextIndex++
			pages = append(pages, indexPage(indexAt, t.typ, firstData, firstData, true))
			first := tp[0]
			le.PutUint32(first[0x04:], firstData)
			pages = append(pages, first)
			hold = append(hold, held{at, tp[1:]})
			entries = append(entries, entry{t.typ, indexAt, firstData, 0xffffffff})
		} else {
			cand := nextIndex
			nextIndex++
			pages = append(pages, indexPage(indexAt, t.typ, cand, 0, false))
			pages = append(pages, make([]byte, pageSize))
			entries = append(entries, entry{t.typ, indexAt, indexAt, cand})
		}
	}
	order := func(typ uint32) [2]uint32 {
		for i, t := range f.writeOrder {
			if t == typ {
				return [2]uint32{0, uint32(i)}
			}
		}
		if len(f.writeOrder) == 0 && typ == 19 {
			return [2]uint32{0, 0}
		}
		return [2]uint32{1, typ}
	}
	sort.SliceStable(hold, func(i, j int) bool {
		a, b := order(entries[hold[i].at].typ), order(entries[hold[j].at].typ)
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		return a[1] < b[1]
	})
	for _, h := range hold {
		e := &entries[h.at]
		typ, firstData := e.typ, e.last
		lastAt := -1
		for i, p := range pages {
			if le.Uint32(p[0x04:]) == firstData && le.Uint32(p[0x08:]) == typ && p[0x1b]&0x40 == 0 {
				lastAt = i
				break
			}
		}
		lastIndex := firstData
		if lastAt >= 0 {
			sequence++
			le.PutUint32(pages[lastAt][0x10:], sequence)
		}
		for _, page := range h.rest {
			idx := nextIndex
			nextIndex++
			le.PutUint32(page[0x04:], idx)
			sequence++
			le.PutUint32(page[0x10:], sequence)
			if lastAt >= 0 {
				le.PutUint32(pages[lastAt][0x0c:], idx)
			}
			pages = append(pages, page)
			lastAt = len(pages) - 1
			lastIndex = idx
		}
		cand := nextIndex
		nextIndex++
		pages = append(pages, make([]byte, pageSize))
		if lastAt >= 0 {
			le.PutUint32(pages[lastAt][0x0c:], cand)
		}
		e.last = lastIndex
		e.cand = cand
	}
	for len(pages) > 0 && allZero(pages[len(pages)-1]) {
		pages = pages[:len(pages)-1]
	}
	le.PutUint32(out[0x04:], pageSize)
	le.PutUint32(out[0x08:], uint32(len(f.tables)))
	le.PutUint32(out[0x0c:], nextIndex)
	le.PutUint32(out[0x10:], 1)
	le.PutUint32(out[0x14:], sequence+1)
	for i, e := range entries {
		at := 28 + i*16
		le.PutUint32(out[at:], e.typ)
		le.PutUint32(out[at+4:], e.cand)
		le.PutUint32(out[at+8:], e.first)
		le.PutUint32(out[at+12:], e.last)
	}
	for _, p := range pages {
		out = append(out, p...)
	}
	return out
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// ---- filas ----

func put2(row []byte, at int, v uint16) { binary.LittleEndian.PutUint16(row[at:], v) }
func put4(row []byte, at int, v uint32) { binary.LittleEndian.PutUint32(row[at:], v) }

// setIndexShift escribe la posición de la fila en su página (×32) en los bytes 2-3.
func setIndexShift(row []byte, slot uint16) {
	if len(row) >= 4 {
		put2(row, 2, slot*32)
	}
}

const (
	trackFixedLen = 0x5e
	trackStrings  = 21

	slotHotCueAutoLoad = 7
	slotDateAdded      = 10
	slotAnalyzePath    = 14
	slotTitle          = 17
	slotFilename       = 19
	slotFilePath       = 20
)

// trackInput es todo lo que necesita una fila de canción.
type trackInput struct {
	ID          uint32
	ArtistID    uint32
	AlbumID     uint32
	GenreID     uint32
	KeyID       uint32
	LabelID     uint32
	TempoX100   uint32
	DurationSec uint16
	Year        uint16
	Bitrate     uint32
	SampleRate  uint32
	FileSize    uint32
	SampleDepth uint16
	Title       string
	Filename    string
	FilePath    string // relativa al volumen: /Contents/Artista/Álbum/archivo.mp3
	AnalyzePath string // /PIONEER/USBANLZ/P001/0000ABCD/ANLZ0000.DAT
	DateAdded   string // YYYY-MM-DD
	AutoLoadHot bool
}

func trackRow(in trackInput) []byte {
	strs := make([]string, trackStrings)
	if in.AutoLoadHot {
		strs[slotHotCueAutoLoad] = "ON"
	}
	strs[slotDateAdded] = in.DateAdded
	strs[slotAnalyzePath] = in.AnalyzePath
	strs[slotTitle] = in.Title
	strs[slotFilename] = in.Filename
	strs[slotFilePath] = in.FilePath

	row := make([]byte, trackFixedLen+trackStrings*2)
	put2(row, 0x00, 0x24)
	put4(row, 0x08, in.SampleRate)
	put4(row, 0x10, in.FileSize)
	put4(row, 0x20, in.KeyID)
	put4(row, 0x28, in.LabelID)
	put4(row, 0x30, in.Bitrate)
	put4(row, 0x38, in.TempoX100)
	put4(row, 0x3c, in.GenreID)
	put4(row, 0x40, in.AlbumID)
	put4(row, 0x44, in.ArtistID)
	put4(row, 0x48, in.ID)
	put2(row, 0x50, in.Year)
	put2(row, 0x52, in.SampleDepth)
	put2(row, 0x54, in.DurationSec)
	put2(row, 0x56, 0x29)
	put2(row, 0x5a, audioFileType(in.Filename))
	put2(row, 0x5c, 3)
	for slot, text := range strs {
		at := pushString(&row, text)
		put2(row, trackFixedLen+slot*2, uint16(at))
	}
	return row
}

func audioFileType(name string) uint16 {
	ext := ""
	for i := len(name) - 1; i >= 0 && name[i] != '/'; i-- {
		if name[i] == '.' {
			ext = lower(name[i+1:])
			break
		}
	}
	switch ext {
	case "mp3":
		return 1
	case "m4a", "mp4", "aac":
		return 4
	case "flac":
		return 5
	case "wav":
		return 11
	case "aif", "aiff":
		return 12
	}
	return 0
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

func namedRow(id uint32, name string) []byte {
	return append(le32(id), deviceSQLString(name)...)
}

func keyRow(id uint32, name string) []byte {
	r := append(le32(id), le32(id)...)
	return append(r, deviceSQLString(name)...)
}

func colorRow(id uint16, name string) []byte {
	row := make([]byte, 8)
	row[4] = byte(id)
	put2(row, 5, id)
	return append(row, deviceSQLString(name)...)
}

func artistRow(id uint32, name string) []byte {
	row := make([]byte, 10)
	put2(row, 0x00, 0x60)
	put4(row, 0x04, id)
	row[0x08] = 0x03
	at := pushString(&row, name)
	row[0x09] = byte(at)
	return row
}

const albumNameAt = 0x16

func albumRow(id, artistID uint32, name string) []byte {
	row := make([]byte, albumNameAt)
	put2(row, 0x00, 0x80)
	put4(row, 0x08, artistID)
	put4(row, 0x0c, id)
	row[0x14] = 0x03
	at := pushString(&row, name)
	row[0x15] = byte(at)
	return row
}

func playlistRow(id, parent, sort uint32, folder bool, name string) []byte {
	row := append([]byte(nil), le32(parent)...)
	row = append(row, le32(0)...)
	row = append(row, le32(sort)...)
	row = append(row, le32(id)...)
	f := uint32(0)
	if folder {
		f = 1
	}
	row = append(row, le32(f)...)
	return append(row, deviceSQLString(name)...)
}

func playlistEntryRow(index, trackID, playlistID uint32) []byte {
	row := append([]byte(nil), le32(index)...)
	row = append(row, le32(trackID)...)
	return append(row, le32(playlistID)...)
}

// propertyRow es la única fila de la tabla 19. La fecha debe ser YYYY-MM-DD.
func propertyRow(contents uint32, date, deviceName string) []byte {
	row := []byte{0x80, 0x02, 0x00, 0x00}
	row = append(row, le32(contents)...)
	row = append(row, 0x00, 0x00, 0x00, 0x00) // color de fondo 0 = por defecto
	row = append(row, shortASCII(date)...)
	row = append(row, 0x19, 0x1e)
	row = append(row, shortASCII("1000")...)
	row = append(row, deviceSQLString(deviceName)...)
	row = append(row, make([]byte, 8)...)
	for len(row)%4 != 0 {
		row = append(row, 0)
	}
	return row
}

var colorNames = []string{"Pink", "Red", "Orange", "Yellow", "Green", "Aqua", "Blue", "Purple"}
