package pioneer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// DEVSETTING.DAT: los ajustes de pantalla que lee el reproductor (color de la onda, formato de la tonalidad…).
// Formato (140 bytes) tomado de rbxport, crates/rbl-devices/src/settings.rs (GPL-2.0-or-later), comprobado allí
// byte a byte contra un archivo real de rekordbox 7.2.8:
//
//	0x00 u32 LE 0x60 · 0x04 [32] "PIONEER DJ" · 0x24 [32] "rekordbox" · 0x44 [32] versión · 0x64 u32 LE 32
//	0x68 [32] cuerpo · 0x88 u16 LE CRC-16/XMODEM del cuerpo · 0x8a u16 0
//
// En el cuerpo: byte 9 tipo de onda general (1 mitad, 2 completa), 10 color de la onda (1 azul, 3 RGB, 4 3 bandas),
// 12 formato de la tonalidad (1 clásico "Am", 2 alfanumérico "8A"), 13 posición actual (1 centro, 2 izquierda).
const (
	devFileLen  = 140
	devBodyAt   = 4 + 0x60 + 4
	devBodyLen  = 32
	devColorAt  = 10
	devKeyAt    = 12
	WaveBlue    = "blue"
	WaveRGB     = "rgb"
	Wave3Band   = "3band"
	devDefaultV = "7.2.11"
)

var devDefaultBody = [devBodyLen]byte{0x78, 0x56, 0x34, 0x12, 0x01, 0, 0, 0, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01}

func crc16xmodem(b []byte) uint16 {
	var crc uint16
	for _, x := range b {
		crc ^= uint16(x) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 == 0 {
				crc <<= 1
			} else {
				crc = crc<<1 ^ 0x1021
			}
		}
	}
	return crc
}

func waveCode(w string) (byte, bool) {
	switch w {
	case WaveBlue:
		return 1, true
	case WaveRGB:
		return 3, true
	case Wave3Band:
		return 4, true
	}
	return 0, false
}

func buildDevSetting(body [devBodyLen]byte, version []byte) []byte {
	out := make([]byte, 0, devFileLen)
	out = append(out, 0x60, 0, 0, 0)
	field := func(b []byte) {
		f := make([]byte, 32)
		copy(f, b[:min(len(b), 31)])
		out = append(out, f...)
	}
	field([]byte("PIONEER DJ"))
	field([]byte("rekordbox"))
	field(version)
	out = append(out, 0x20, 0, 0, 0)
	out = append(out, body[:]...)
	c := crc16xmodem(body[:])
	return append(out, byte(c), byte(c>>8), 0, 0)
}

// writeDevSetting crea DEVSETTING.DAT, o cambia solo el color de la onda y el formato de la tonalidad si ya existe.
// Si el archivo existente no se entiende no se toca (devuelve error).
func writeDevSetting(root, waveform string, alphanumeric bool) error {
	code, ok := waveCode(waveform)
	if !ok {
		return nil
	}
	path := filepath.Join(root, "DEVSETTING.DAT")
	key := byte(1)
	if alphanumeric {
		key = 2
	}
	old, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		body := devDefaultBody
		body[devColorAt], body[devKeyAt] = code, key
		return writeAtomic(path, buildDevSetting(body, []byte(devDefaultV)))
	}
	if len(old) != devFileLen || !bytes.Equal(old[:4], []byte{0x60, 0, 0, 0}) || !bytes.Equal(old[4+0x60:devBodyAt], []byte{0x20, 0, 0, 0}) {
		return errors.New("DEVSETTING.DAT de la memoria no tiene el formato esperado; no se modificó")
	}
	var body [devBodyLen]byte
	copy(body[:], old[devBodyAt:devBodyAt+devBodyLen])
	if want := crc16xmodem(body[:]); old[devBodyAt+devBodyLen] != byte(want) || old[devBodyAt+devBodyLen+1] != byte(want>>8) {
		return errors.New("DEVSETTING.DAT de la memoria tiene un checksum distinto al esperado; no se modificó")
	}
	body[devColorAt], body[devKeyAt] = code, key
	ver := bytes.TrimRight(old[0x44:0x64], "\x00")
	return writeAtomic(path, buildDevSetting(body, ver))
}
