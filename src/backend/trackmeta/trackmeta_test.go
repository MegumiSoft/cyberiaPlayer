package trackmeta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripRenameDelete(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	m := Meta{
		Analysis: &Analysis{Size: 10, Version: AnalysisVersion, BPM: 128, BeatOffset: 0.37, HasKey: true, KeyPC: 9, KeyMinor: true},
		GridSet:  true, GridBPM: 127.9, GridOffset: 0.4,
		Cues: []Cue{{Slot: 0, TimeMs: 1000, Name: "drop", Color: 3}, {Slot: -1, TimeMs: 5000}},
	}
	if err := s.Set("a/x.mp3", m); err != nil {
		t.Fatal(err)
	}
	s2 := Open(dir)
	got := s2.All()["a/x.mp3"]
	if got.GridBPM != 127.9 || len(got.Cues) != 2 || got.Analysis.BPM != 128 {
		t.Fatalf("no sobrevivió: %+v", got)
	}
	if err := s2.Rename("a/x.mp3", "b/x.mp3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(dir).All()["b/x.mp3"]; !ok {
		t.Error("rename no persistió")
	}
	if err := s2.Delete("b/x.mp3"); err != nil || len(Open(dir).All()) != 0 {
		t.Error("delete falló")
	}
}

func TestValidate(t *testing.T) {
	bad := []Meta{
		{GridSet: true, GridBPM: 10, GridOffset: 0},
		{GridSet: true, GridBPM: 128, GridOffset: -1},
		{Cues: []Cue{{Slot: 8}}},
		{Cues: []Cue{{Slot: 1, TimeMs: -5}}},
		{Cues: []Cue{{Slot: 1}, {Slot: 1}}},
	}
	for i, m := range bad {
		if _, err := Validate(m); err == nil {
			t.Errorf("caso %d debía fallar", i)
		}
	}
	m, err := Validate(Meta{Analysis: &Analysis{Version: 1}, Cues: []Cue{{Slot: 0, Color: 99}}})
	if err != nil || m.Analysis != nil || m.Cues[0].Color != 0 {
		t.Errorf("análisis viejo debe descartarse y el color corregirse: %+v %v", m, err)
	}
}

func TestEmptyMetaRemovesEntry(t *testing.T) {
	s := Open(t.TempDir())
	_ = s.Set("x.mp3", Meta{Cues: []Cue{{Slot: 0}}})
	_ = s.Set("x.mp3", Meta{})
	if len(s.All()) != 0 {
		t.Error("una entrada vacía debe borrarse")
	}
}

func TestWaveAndCorruptFile(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if s.LoadWave("x.mp3", 5) != nil {
		t.Error("no debería haber onda")
	}
	if err := s.SaveWave("x.mp3", 5, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadWave("x.mp3", 5); len(got) != 3 {
		t.Errorf("%v", got)
	}
	if s.LoadWave("x.mp3", 6) != nil {
		t.Error("otro tamaño = otro archivo: la onda guardada ya no vale")
	}
	os.WriteFile(filepath.Join(dir, "trackmeta.json"), []byte("{no json"), 0o644)
	if len(Open(dir).All()) != 0 {
		t.Error("archivo corrupto debe leerse como vacío")
	}
}
