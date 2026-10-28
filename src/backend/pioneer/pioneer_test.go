package pioneer

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisDirMatchesRekordboxReference(t *testing.T) {
	got := analysisDir("/Contents/Hosanna, Westend/Drum Death - Extended Mix/20130688_drum_death_(extended_mix).mp3", "PIONEER")
	if got != "/PIONEER/USBANLZ/P002/0002583E" {
		t.Fatalf("got %s", got)
	}
}

func TestStrings(t *testing.T) {
	if b := shortASCII("Pink"); b[0] != 0x0b || string(b[1:]) != "Pink" {
		t.Fatalf("short %x", b)
	}
	if b := longUTF16("LÜRUM"); string(b[:4]) != "\x90\x0e\x00\x00" || len(b) != 14 {
		t.Fatalf("utf16 %x", b)
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	if b := deviceSQLString(string(long)); b[0] != 0x40 || binary.LittleEndian.Uint16(b[1:]) != 204 {
		t.Fatalf("long ascii %x", b[:4])
	}
}

func TestNames(t *testing.T) {
	if fatSafe("A/B:C*D?E") != "A_B_C_D_E" || fatSafe("x...") != "x" || fatSafe("") != "Unknown" {
		t.Fatal("fatSafe")
	}
	long := ""
	for i := 0; i < 60; i++ {
		long += "é"
	}
	if n := len([]rune(dirName(long))); n != 48 {
		t.Fatalf("dir %d", n)
	}
	if n := len(fileName(long + ".mp3")); n > 120 || filepath.Ext(fileName(long+".mp3")) != ".mp3" {
		t.Fatalf("file %d", n)
	}
}

func synthTrack(t *testing.T, dir, name string, dur float64) Track {
	p := filepath.Join(dir, name)
	// un "mp3" mínimo: cabecera de trama MPEG1 L3 128 kbps 44.1 kHz y relleno.
	data := append([]byte{0xff, 0xfb, 0x90, 0x00}, make([]byte, 5000)...)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fps := 22050.0 / 220
	frames := int(dur * fps)
	wave := make([]byte, frames*3)
	for i := 0; i < frames; i++ {
		beat := math.Mod(float64(i)/fps, 0.5)
		v := byte(255 * math.Exp(-beat*8))
		wave[i*3], wave[i*3+1], wave[i*3+2] = v, v/2, v/3
	}
	return Track{Src: p, Title: "Song " + name, Artist: "Artista Ñ", Album: "Álbum", Key: "Am", BPM: 120, Duration: dur,
		HasGrid: true, GridOffset: 0.37, Wave: wave, WaveFPS: fps,
		Cues: []Cue{{Slot: 0, TimeMs: 1000, Name: "intro", R: 255, G: 0, B: 0}, {Slot: 4, TimeMs: 5000, R: 0, G: 255, B: 0}, {Slot: -1, TimeMs: 9000, Name: "drop", R: 0, G: 0, B: 255}}}
}

func TestExportEndToEnd(t *testing.T) {
	src := t.TempDir()
	dest := os.Getenv("PIONEER_OUT")
	if dest == "" {
		dest = t.TempDir()
	}
	var tracks []Track
	for i := 0; i < 3; i++ {
		tracks = append(tracks, synthTrack(t, src, string(rune('a'+i))+".mp3", 20))
	}
	// uno sin analizar y uno no compatible
	un := synthTrack(t, src, "u.mp3", 0)
	un.Wave, un.HasGrid, un.Cues = nil, false, nil
	tracks = append(tracks, un)
	op := synthTrack(t, src, "x.opus", 10)
	tracks = append(tracks, op)
	rep, err := Export(context.Background(), Options{Dest: dest, DeviceName: "TEST", Waveform: WaveRGB, KeyAlphanumeric: true, Tracks: tracks,
		Playlists: []Playlist{{Name: "Uno", Tracks: []int{0, 1, 3}}, {Name: "Dos", Tracks: []int{2, 4}}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Merged || rep.Tracks != 4 || rep.Playlist != 2 || rep.Analyzed != 3 || len(rep.Skipped) != 1 {
		t.Fatalf("report %+v", rep)
	}
	pdb, err := os.ReadFile(filepath.Join(dest, "PIONEER", "rekordbox", "export.pdb"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pdb)%pageSize != 0 || binary.LittleEndian.Uint32(pdb[8:]) != 20 {
		t.Fatalf("pdb size %d tables %d", len(pdb), binary.LittleEndian.Uint32(pdb[8:]))
	}
	if d, err := os.ReadFile(filepath.Join(dest, "PIONEER", "DEVSETTING.DAT")); err != nil || len(d) != 140 || d[devBodyAt+devColorAt] != 3 || d[devBodyAt+devKeyAt] != 2 {
		t.Fatalf("DEVSETTING.DAT: %v", err)
	}
	// ANLZ: PMAI con longitud coherente
	dir := analysisDir("/Contents/Artista Ñ/Álbum/a.mp3", "PIONEER")
	for _, n := range []string{"ANLZ0000.DAT", "ANLZ0000.EXT", "ANLZ0000.2EX"} {
		b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(dir), n))
		if err != nil {
			t.Fatal(err)
		}
		if string(b[:4]) != "PMAI" || int(binary.BigEndian.Uint32(b[8:])) != len(b) {
			t.Fatalf("%s mal formado", n)
		}
	}
}

func TestBuildBeats(t *testing.T) {
	b := buildBeats(Track{HasGrid: true, BPM: 120, GridOffset: 0.25, Duration: 3})
	if len(b) != 6 || b[0].TimeMs != 250 || b[1].TimeMs != 750 || b[4].Number != 1 || b[0].TempoX100 != 12000 {
		t.Fatalf("%+v", b)
	}
	n := buildBeats(Track{HasGrid: true, BPM: 120, GridOffset: -0.2, Duration: 2})
	if n[0].TimeMs != 300 || n[0].Number != 2 {
		t.Fatalf("%+v", n[0])
	}
	// El pulso 1 está en 1.3 s (pulso 2.6): antes de él hay 2 pulsos enteros y el primero cae en 0.3 s como "3".
	m := buildBeats(Track{HasGrid: true, BPM: 120, GridOffset: 1.3, Duration: 4})
	if m[0].TimeMs != 300 || m[0].Number != 3 || m[2].TimeMs != 1300 || m[2].Number != 1 {
		t.Fatalf("%+v", m[:3])
	}
}

func TestManyTracksPaginates(t *testing.T) {
	var rows [][]byte
	for i := 0; i < 700; i++ {
		rows = append(rows, playlistEntryRow(uint32(i+1), uint32(i+1), 1))
	}
	f := &fileBuilder{}
	f.addTable(8, rows, nil)
	out := f.finish()
	if len(out)%pageSize != 0 {
		t.Fatal("tamaño")
	}
}

func TestExportMergesIntoExistingLibrary(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	if d := os.Getenv("PIONEER_OUT2"); d != "" {
		dest = d
	}
	var tracks []Track
	for i := 0; i < 4; i++ {
		tracks = append(tracks, synthTrack(t, src, string(rune('a'+i))+".mp3", 20))
		tracks[i].Title = "T" + string(rune('a'+i))
	}
	ctx := context.Background()
	if _, err := Export(ctx, Options{Dest: dest, DeviceName: "X", Tracks: tracks, Playlists: []Playlist{{Name: "Uno", Tracks: []int{0, 1}}}}); err != nil {
		t.Fatal(err)
	}
	pdbPath := filepath.Join(dest, "PIONEER", "rekordbox", "export.pdb")
	first, _ := os.ReadFile(pdbPath)
	st1, err := readLibrary(first)
	if err != nil || len(st1.rows[tTracks]) != 2 {
		t.Fatalf("lectura inicial: %v %d", err, len(st1.rows[tTracks]))
	}
	// Segunda corrida: playlist nueva con una canción nueva + la ya existente, y se amplía "Uno".
	rep, err := Export(ctx, Options{Dest: dest, Tracks: tracks, Playlists: []Playlist{{Name: "Dos", Tracks: []int{1, 2}}, {Name: "uno", Tracks: []int{0, 3}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Merged || rep.Tracks != 2 || rep.Reused != 2 || rep.Playlist != 1 || rep.Extended != 1 {
		t.Fatalf("report %+v", rep)
	}
	second, _ := os.ReadFile(pdbPath)
	st2, err := readLibrary(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.rows[tTracks]) != 4 || len(st2.rows[tPlaylists]) != 2 || len(st2.rows[tEntries]) != 2+2+1 {
		t.Fatalf("tracks=%d playlists=%d entries=%d", len(st2.rows[tTracks]), len(st2.rows[tPlaylists]), len(st2.rows[tEntries]))
	}
	// Las filas de las dos primeras canciones siguen igual (salvo su posición en la página, bytes 2-3).
	for i := 0; i < 2; i++ {
		a, b := append([]byte(nil), st1.rows[tTracks][i]...), append([]byte(nil), st2.rows[tTracks][i]...)
		a[2], a[3], b[2], b[3] = 0, 0, 0, 0
		if string(a) != string(b) {
			t.Fatalf("la fila %d cambió", i)
		}
	}
	n1, n2 := nodeOf(st2.rows[tPlaylists][0]), nodeOf(st2.rows[tPlaylists][1])
	if n1.name != "Uno" || n2.name != "Dos" || n2.sort != n1.sort+1 || n2.id != n1.id+1 {
		t.Fatalf("%+v %+v", n1, n2)
	}
	// Una tercera corrida idéntica no cambia nada.
	rep, err = Export(ctx, Options{Dest: dest, Tracks: tracks, Playlists: []Playlist{{Name: "Dos", Tracks: []int{1, 2}}}})
	if err != nil || rep.Tracks != 0 || rep.Reused != 2 || rep.Playlist != 0 || rep.Extended != 0 {
		t.Fatalf("tercera: %v %+v", err, rep)
	}
	third, _ := os.ReadFile(pdbPath)
	st3, _ := readLibrary(third)
	if len(st3.rows[tTracks]) != 4 || len(st3.rows[tEntries]) != 5 {
		t.Fatal("la tercera corrida cambió la biblioteca")
	}
	if _, err := os.Stat(pdbPath + ".bak"); err != nil {
		t.Fatal("falta la copia de seguridad")
	}
}

func TestReadLibraryMultiPageRoundTrip(t *testing.T) {
	s := newFreshState("D")
	for i := 0; i < 900; i++ {
		s.rows[tEntries] = append(s.rows[tEntries], playlistEntryRow(uint32(i+1), uint32(i%50+1), 1))
	}
	for i := 0; i < 700; i++ {
		s.rows[tTracks] = append(s.rows[tTracks], trackRow(trackInput{ID: uint32(i + 1), Title: "Canción ñ " + string(rune('A'+i%26)), Filename: "x.mp3", FilePath: "/Contents/a/b/x" + string(rune('a'+i%26)) + ".mp3"}))
	}
	back, err := readLibrary(buildPDB(s))
	if err != nil {
		t.Fatal(err)
	}
	if len(back.rows[tEntries]) != 900 || len(back.rows[tTracks]) != 700 || len(back.rows[16]) != 27 {
		t.Fatalf("filas: %d %d %d", len(back.rows[tEntries]), len(back.rows[tTracks]), len(back.rows[16]))
	}
	for i, r := range back.rows[tEntries] {
		if rd4(r, 0) != uint32(i+1) {
			t.Fatalf("entrada %d fuera de orden", i)
		}
	}
	if trackStr(back.rows[tTracks][699], slotTitle) != "Canción ñ "+string(rune('A'+699%26)) {
		t.Fatal("título")
	}
}

func TestInspectAndExistingTarget(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir()
	var tracks []Track
	for i := 0; i < 3; i++ {
		tracks = append(tracks, synthTrack(t, src, string(rune('a'+i))+".mp3", 20))
	}
	ctx := context.Background()
	if _, err := Export(ctx, Options{Dest: dest, Tracks: tracks, Playlists: []Playlist{{Name: "Uno", Tracks: []int{0}}, {Name: "Otra", Tracks: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	lib, err := Inspect(dest)
	if err != nil || lib == nil || len(lib.Playlists) != 2 {
		t.Fatalf("inspect: %v %+v", err, lib)
	}
	if !lib.Tracks[strings.ToLower(TargetPath(tracks[0]))] || lib.Tracks[strings.ToLower(TargetPath(tracks[2]))] {
		t.Fatal("rutas de canciones existentes mal detectadas")
	}
	var otra PlaylistInfo
	for _, p := range lib.Playlists {
		if p.Name == "Otra" {
			otra = p
		}
	}
	if otra.Count != 1 || len(otra.Paths) != 1 {
		t.Fatalf("otra: %+v", otra)
	}
	// Mandar la canción 2 (nueva) y la 0 (ya estaba en otra lista) a "Otra" por id, con un nombre distinto.
	rep, err := Export(ctx, Options{Dest: dest, Tracks: tracks, Playlists: []Playlist{{Name: "ignorado", ExistingID: otra.ID, Tracks: []int{2, 0}}}})
	if err != nil || rep.Playlist != 0 || rep.Extended != 1 || rep.Tracks != 1 || rep.Reused != 1 {
		t.Fatalf("export: %v %+v", err, rep)
	}
	lib, _ = Inspect(dest)
	for _, p := range lib.Playlists {
		if p.Name == "Otra" && p.Count != 3 {
			t.Fatalf("Otra debería tener 3, tiene %d", p.Count)
		}
		if p.Name == "ignorado" {
			t.Fatal("se creó una playlist nueva")
		}
	}
}

// Archivo real de rekordbox 7.2.8 (onda 3 bandas, tonalidad clásica), tomado de las pruebas de rbxport.
var realDevSetting = []byte{
	0x60, 0x00, 0x00, 0x00, 0x50, 0x49, 0x4f, 0x4e, 0x45, 0x45, 0x52, 0x20, 0x44, 0x4a, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x72, 0x65, 0x6b, 0x6f, 0x72, 0x64, 0x62, 0x6f, 0x78, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x37, 0x2e, 0x32, 0x2e, 0x38, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x78, 0x56, 0x34, 0x12, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x01, 0x04, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x8f, 0x1a, 0x00, 0x00,
}

func TestDevSetting(t *testing.T) {
	if crc16xmodem([]byte("123456789")) != 0x31c3 {
		t.Fatal("crc")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "DEVSETTING.DAT")
	// El mismo valor que ya tiene el archivo real no lo cambia (3 bandas, clásico).
	os.WriteFile(p, realDevSetting, 0o644)
	if err := writeDevSetting(dir, Wave3Band, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, realDevSetting) {
		t.Fatal("el archivo real cambió sin haber cambiado ajustes")
	}
	// RGB + alfanumérico: solo cambian los dos bytes y el checksum.
	if err := writeDevSetting(dir, WaveRGB, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	var diff []int
	for i := range got {
		if got[i] != realDevSetting[i] {
			diff = append(diff, i)
		}
	}
	if len(got) != 140 || fmt.Sprint(diff) != fmt.Sprint([]int{devBodyAt + devColorAt, devBodyAt + devKeyAt, 0x88, 0x89}) {
		t.Fatalf("diff %v", diff)
	}
	// Archivo nuevo = el real con los valores pedidos (cambia la versión).
	os.Remove(p)
	if err := writeDevSetting(dir, Wave3Band, false); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(p)
	if len(got) != 140 || got[devBodyAt+devColorAt] != 4 || got[devBodyAt+devKeyAt] != 1 {
		t.Fatal("archivo nuevo mal formado")
	}
	// Archivo dañado: no se toca.
	bad := append([]byte(nil), realDevSetting...)
	bad[devBodyAt+devColorAt] = 3
	os.WriteFile(p, bad, 0o644)
	if err := writeDevSetting(dir, WaveBlue, true); err == nil {
		t.Fatal("debió negarse")
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(after, bad) {
		t.Fatal("tocó un archivo que no entiende")
	}
}
