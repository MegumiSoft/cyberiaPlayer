package pioneer

import (
	"encoding/binary"
	"math"
	"unicode/utf16"
)

// Archivos de análisis ANLZ (.DAT, .EXT, .2EX). Port de rbxport (GPL-2.0-or-later):
// crates/rbl-anlz/src/{encode,write,cues}.rs.

// Beat es un pulso de la rejilla.
type Beat struct {
	Number    uint16 // 1..4, 1 = tiempo fuerte
	TempoX100 uint16
	TimeMs    uint32
}

// BandColumn es una columna de la waveform analizada (cada valor 0-255).
type BandColumn struct{ Low, Mid, High, Peak uint8 }

// ExportCue es un cue tal como lo guarda el dispositivo.
type ExportCue struct {
	Kind      uint8 // 0 memory, 1-3 A-C, 5-17 D-P
	TimeMs    uint32
	ColorCode uint8 // índice de la paleta del dispositivo (0 = color por defecto del slot)
	Comment   string
}

func (c ExportCue) slot() uint32 {
	if c.Kind >= 5 {
		return uint32(c.Kind - 1)
	}
	return uint32(c.Kind)
}

type section struct {
	tag     string
	header  []byte
	payload []byte
}

func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func be16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }

var defaultHeaderExtra = []byte{0, 0, 0, 1, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}

func render(sections []section) []byte {
	const frame = 12
	lenHeader := frame + len(defaultHeaderExtra)
	body := 0
	for _, s := range sections {
		body += frame + len(s.header) + len(s.payload)
	}
	out := []byte("PMAI")
	out = append(out, be32(uint32(lenHeader))...)
	out = append(out, be32(uint32(lenHeader+body))...)
	out = append(out, defaultHeaderExtra...)
	for _, s := range sections {
		out = append(out, s.tag...)
		out = append(out, be32(uint32(frame+len(s.header)))...)
		out = append(out, be32(uint32(frame+len(s.header)+len(s.payload)))...)
		out = append(out, s.header...)
		out = append(out, s.payload...)
	}
	return out
}

func ppthSection(path string) section {
	var text []byte
	for _, u := range utf16.Encode([]rune(path)) {
		text = append(text, byte(u>>8), byte(u))
	}
	text = append(text, 0, 0)
	return section{"PPTH", be32(uint32(len(text))), text}
}

func vbrZero() section { return section{"PVBR", be32(0), make([]byte, 401*4)} }

func gridSection(beats []Beat) section {
	h := append(be32(0), be32(0x00080000)...)
	h = append(h, be32(uint32(len(beats)))...)
	p := make([]byte, 0, len(beats)*8)
	for _, b := range beats {
		p = append(p, be16(b.Number)...)
		p = append(p, be16(b.TempoX100)...)
		p = append(p, be32(b.TimeMs)...)
	}
	return section{"PQTZ", h, p}
}

func previewSection(tag string, data []byte) section {
	return section{tag, append(be32(uint32(len(data))), be32(0x00010000)...), data}
}

func scrollSection(tag string, stride int, data []byte) section {
	h := append(be32(uint32(stride)), be32(uint32(len(data)/stride))...)
	switch tag {
	case "PWV6":
	case "PWV5":
		h = append(h, be32(0x00960305)...)
	case "PWV4":
		h = append(h, be32(0)...)
	default:
		h = append(h, be32(0x00960000)...)
	}
	return section{tag, h, data}
}

// ---- waveforms ----

const (
	bandFullScale = 44
	pwavCeiling   = 25
	pwv2Ceiling   = 15
	detailCeiling = 31
	pwavFloor     = 2
	pwv2Floor     = 1
	pwv2Max       = 15
)

func band(v uint8) uint32 { return uint32(v) * bandFullScale / 255 }

func resample(cols []BandColumn, n int) []BandColumn {
	out := make([]BandColumn, n)
	if len(cols) == 0 {
		return out
	}
	for b := 0; b < n; b++ {
		start := b * len(cols) / n
		end := (b + 1) * len(cols) / n
		if end < start+1 {
			end = start + 1
		}
		if end > len(cols) {
			end = len(cols)
		}
		var m BandColumn
		for _, c := range cols[start:end] {
			m.Low, m.Mid, m.High, m.Peak = max8(m.Low, c.Low), max8(m.Mid, c.Mid), max8(m.High, c.High), max8(m.Peak, c.Peak)
		}
		out[b] = m
	}
	return out
}

func max8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

func maxU(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func bandHeight(c BandColumn, ceiling uint32) uint32 {
	loudest := maxU(band(c.Low), maxU(band(c.Mid), band(c.High)))
	h := loudest * ceiling / bandFullScale
	if h > ceiling {
		h = ceiling
	}
	return h
}

func detailHeight(c BandColumn) uint32 {
	const full = 255 * 255
	p := uint32(c.Peak)
	h := (detailCeiling*p*p + full/2) / full
	if h > detailCeiling {
		h = detailCeiling
	}
	return h
}

func monoByte(c BandColumn, height uint32) byte {
	total := uint32(c.Low) + uint32(c.Mid) + uint32(c.High)
	var white uint32
	if total > 0 {
		white = (uint32(c.High)*7 + total/2) / total
	}
	if white > 7 {
		white = 7
	}
	if height > detailCeiling {
		height = detailCeiling
	}
	return byte(white<<5 | height)
}

func devicePreviewByte(b byte) byte {
	h := b & 0x1f
	if h < pwavFloor {
		h = pwavFloor
	}
	return b&0xe0 | h
}

func deviceTinyByte(b byte) byte {
	h := b & 0x1f
	if h < pwv2Floor {
		h = pwv2Floor
	}
	if h > pwv2Max {
		h = pwv2Max
	}
	return h
}

func pwav(cols []BandColumn) []byte {
	var out []byte
	for _, c := range resample(cols, 400) {
		out = append(out, devicePreviewByte(monoByte(c, bandHeight(c, pwavCeiling))))
	}
	return out
}

func pwv2(cols []BandColumn) []byte {
	var out []byte
	for _, c := range resample(cols, 100) {
		out = append(out, deviceTinyByte(byte(bandHeight(c, pwv2Ceiling))))
	}
	return out
}

func pwv3(cols []BandColumn) []byte {
	out := make([]byte, 0, len(cols))
	for _, c := range cols {
		out = append(out, monoByte(c, detailHeight(c)))
	}
	return out
}

func pwv4(cols []BandColumn) []byte {
	weigh := func(v uint8, num, den uint32) byte {
		r := uint32(v>>1) * num / den
		if r > 127 {
			r = 127
		}
		return byte(r)
	}
	var out []byte
	for _, c := range resample(cols, 1200) {
		out = append(out, c.Peak>>1, 0, 0, c.Low>>1, weigh(c.Mid, 4, 5), weigh(c.High, 13, 20))
	}
	return out
}

func pwv5Colour(c BandColumn) (uint16, uint16, uint16) {
	low, mid, high := 2*uint32(c.Low), 2*uint32(c.Mid), 3*uint32(c.High)
	strongest := maxU(low, maxU(mid, high))
	if strongest == 0 {
		return 0, 0, 0
	}
	share := func(v, full uint32) uint16 { return uint16((2*full*v + strongest) / (2 * strongest)) }
	return share(low, 7), share(mid, 5), share(high, 7)
}

func pwv5(cols []BandColumn) []byte {
	out := make([]byte, 0, len(cols)*2)
	for _, c := range cols {
		r, g, b := pwv5Colour(c)
		w := r<<13 | g<<10 | b<<7 | uint16(detailHeight(c))<<2
		out = append(out, be16(w)...)
	}
	return out
}

func pwv6(cols []BandColumn) []byte {
	var out []byte
	for _, c := range resample(cols, 1200) {
		out = append(out, byte(band(c.Low)), byte(band(c.Mid)), byte(band(c.High)))
	}
	return out
}

func pwv7(cols []BandColumn) []byte {
	out := make([]byte, 0, len(cols)*3)
	for _, c := range cols {
		out = append(out, c.Low>>1, c.Mid>>1, c.High>>1)
	}
	return out
}

// ---- cues ----

func exportColour(code uint8, slot uint32) [3]byte {
	def := 0
	switch slot {
	case 1, 9:
		def = 43
	case 2, 10:
		def = 8
	case 3, 11:
		def = 23
	case 4, 12:
		def = 60
	}
	i := int(code)
	if code == 0 {
		i = def
	}
	if i >= len(devicePalette) {
		return [3]byte{}
	}
	return devicePalette[i]
}

// cueSections: .DAT lleva memory y A-C; .EXT añade D-P y las listas extendidas completas.
func cueSections(cues []ExportCue, extended bool) []section {
	var out []section
	for _, modern := range []bool{false, true} {
		if modern && !extended {
			continue
		}
		for _, hot := range []bool{true, false} {
			var list []ExportCue
			for _, c := range cues {
				if (c.Kind != 0) != hot {
					continue
				}
				if modern || (extended && hot && c.slot() > 3) || (!extended && c.slot() <= 3) {
					list = append(list, c)
				}
			}
			count := len(list)
			hdrLen := 12
			if modern {
				hdrLen = 8
			}
			header := make([]byte, hdrLen)
			if hot {
				binary.BigEndian.PutUint32(header[0:], 1)
			}
			if modern {
				binary.BigEndian.PutUint16(header[4:], uint16(count))
			} else {
				binary.BigEndian.PutUint16(header[6:], uint16(count))
				last := uint32(0xffffffff)
				if !hot && count > 0 {
					last = uint32(count - 1)
				}
				binary.BigEndian.PutUint32(header[8:], last)
			}
			var payload []byte
			for index, cue := range list {
				var comment []byte
				if cue.Comment != "" {
					for _, u := range append(utf16.Encode([]rune(cue.Comment)), 0) {
						comment = append(comment, byte(u>>8), byte(u))
					}
				}
				length := 56
				if modern {
					length = 88 + len(comment)
				}
				e := make([]byte, length)
				tag := "PCPT"
				base := 28
				if modern {
					tag, base = "PCP2", 16
				}
				copy(e, tag)
				binary.BigEndian.PutUint32(e[4:], uint32(base))
				binary.BigEndian.PutUint32(e[8:], uint32(length))
				binary.BigEndian.PutUint32(e[12:], cue.slot())
				e[base] = 1
				copy(e[base+1:], []byte{0, 3, 232})
				binary.BigEndian.PutUint32(e[base+4:], cue.TimeMs)
				binary.BigEndian.PutUint32(e[base+8:], 0xffffffff)
				if modern {
					e[29] = 1
					binary.BigEndian.PutUint32(e[40:], uint32(len(comment)))
					copy(e[44:], comment)
					e[44+len(comment)] = cue.ColorCode
					rgb := exportColour(cue.ColorCode, cue.slot())
					copy(e[45+len(comment):], rgb[:])
				} else {
					binary.BigEndian.PutUint32(e[20:], 0x10000)
					prev, next := uint16(0xffff), uint16(0xffff)
					if !hot && index > 0 {
						prev = uint16(index - 1)
					}
					if !hot && index+1 != count {
						next = uint16(index + 1)
					}
					binary.BigEndian.PutUint16(e[24:], prev)
					binary.BigEndian.PutUint16(e[26:], next)
				}
				payload = append(payload, e...)
			}
			tag := "PCOB"
			if modern {
				tag = "PCO2"
			}
			out = append(out, section{tag, header, payload})
		}
	}
	return out
}

// nearestPaletteCode devuelve el código de la paleta del dispositivo más cercano a un color RGB.
func nearestPaletteCode(r, g, b int) uint8 {
	best, bestD := 1, math.MaxInt32
	for i := 1; i < 63; i++ {
		p := devicePalette[i]
		dr, dg, db := int(p[0])-r, int(p[1])-g, int(p[2])-b
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	return uint8(best)
}

// AnalysisFiles son los tres archivos que rekordbox guarda junto a cada canción.
type AnalysisFiles struct{ DAT, EXT, TwoEX []byte }

// AuthorAnalysis crea los archivos de análisis de una canción. ppth es la ruta del audio en la memoria.
func AuthorAnalysis(ppth string, beats []Beat, cols []BandColumn, cues []ExportCue) AnalysisFiles {
	dat := []section{ppthSection(ppth), vbrZero(), gridSection(beats),
		previewSection("PWAV", pwav(cols)), previewSection("PWV2", pwv2(cols))}
	dat = append(dat, cueSections(cues, false)...)

	ext := []section{ppthSection(ppth), scrollSection("PWV3", 1, pwv3(cols))}
	ext = append(ext, cueSections(cues, true)...)
	ext = append(ext, scrollSection("PWV5", 2, pwv5(cols)), scrollSection("PWV4", 6, pwv4(cols)))

	two := []section{ppthSection(ppth), scrollSection("PWV7", 3, pwv7(cols)), scrollSection("PWV6", 3, pwv6(cols))}
	return AnalysisFiles{render(dat), render(ext), render(two)}
}

// analysisDir calcula la carpeta de análisis a partir de la ruta FINAL del audio en la memoria
// (firmware CDJ-3000 3.20): hash UTF-16 de la ruta y "shard".
func analysisDir(audio, root string) string {
	var h uint32
	for _, c := range utf16.Encode([]rune(audio)) {
		if c == 0 {
			break
		}
		h = h*0x34f5501d + uint32(c)*0x93b6
	}
	hash := h % 0x00030d43
	var shard uint32
	for i, bit := range []uint{0, 2, 6, 7, 9, 13, 16} {
		shard |= ((hash >> bit) & 1) << uint(i)
	}
	return "/" + root + "/USBANLZ/P" + hex3(shard) + "/" + hex8(hash)
}

const hexDigits = "0123456789ABCDEF"

func hexN(v uint32, n int) string {
	b := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		b[i] = hexDigits[v&0xf]
		v >>= 4
	}
	return string(b)
}
func hex3(v uint32) string { return hexN(v, 3) }
func hex8(v uint32) string { return hexN(v, 8) }
