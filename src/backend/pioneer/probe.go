package pioneer

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// probeInfo son los datos técnicos que la base de datos del CDJ guarda de cada archivo.
type probeInfo struct {
	SampleRate uint32
	Bitrate    uint32 // kbps
	BitDepth   uint16
}

// probe lee lo que puede de la cabecera del archivo y, si no, usa valores razonables.
func probe(path string, size int64, duration float64) probeInfo {
	info := probeInfo{SampleRate: 44100}
	f, err := os.Open(path)
	if err != nil {
		return info
	}
	defer f.Close()
	head := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		probeMP3(head, &info)
	case ".wav":
		probeWAV(head, &info)
	case ".flac":
		if len(head) >= 22 && string(head[:4]) == "fLaC" {
			// STREAMINFO: tasa de muestreo (20 bits) y profundidad (5 bits) en los bytes 18-21.
			v := uint32(head[18])<<12 | uint32(head[19])<<4 | uint32(head[20])>>4
			if v > 0 {
				info.SampleRate = v
			}
			info.BitDepth = uint16(((head[20]&1)<<4 | head[21]>>4)) + 1
		}
	}
	if info.BitDepth == 0 && (strings.HasSuffix(strings.ToLower(path), ".wav") || strings.HasSuffix(strings.ToLower(path), ".flac")) {
		info.BitDepth = 16
	}
	if info.Bitrate == 0 && duration > 0 {
		info.Bitrate = uint32(float64(size) * 8 / duration / 1000)
	}
	return info
}

var mpegRates = [4][3]uint32{{11025, 12000, 8000}, {0, 0, 0}, {22050, 24000, 16000}, {44100, 48000, 32000}}
var mp3BitratesV1L3 = [16]uint32{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var mp3BitratesV2L3 = [16]uint32{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}

func probeMP3(b []byte, info *probeInfo) {
	i := 0
	if len(b) > 10 && string(b[:3]) == "ID3" {
		i = 10 + (int(b[6]&0x7f)<<21 | int(b[7]&0x7f)<<14 | int(b[8]&0x7f)<<7 | int(b[9]&0x7f))
	}
	for ; i+4 <= len(b); i++ {
		if b[i] != 0xff || b[i+1]&0xe0 != 0xe0 {
			continue
		}
		ver := (b[i+1] >> 3) & 3
		layer := (b[i+1] >> 1) & 3
		br := (b[i+2] >> 4) & 0xf
		sr := (b[i+2] >> 2) & 3
		if ver == 1 || layer != 1 || sr == 3 || br == 0 || br == 15 {
			continue
		}
		info.SampleRate = mpegRates[ver][sr]
		if ver == 3 {
			info.Bitrate = mp3BitratesV1L3[br]
		} else {
			info.Bitrate = mp3BitratesV2L3[br]
		}
		return
	}
}

func probeWAV(b []byte, info *probeInfo) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return
	}
	for i := 12; i+8 <= len(b); {
		id := string(b[i : i+4])
		sz := int(binary.LittleEndian.Uint32(b[i+4:]))
		if id == "fmt " && i+8+16 <= len(b) {
			info.SampleRate = binary.LittleEndian.Uint32(b[i+12:])
			info.Bitrate = binary.LittleEndian.Uint32(b[i+16:]) * 8 / 1000
			info.BitDepth = binary.LittleEndian.Uint16(b[i+22:])
			return
		}
		i += 8 + sz + sz&1
	}
}
