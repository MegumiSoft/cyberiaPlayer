package match

import "testing"

func cands() []Candidate {
	return []Candidate{
		{Title: "Daft Punk - One More Time (Official Video)", Uploader: "Daft Punk", URL: "video", Duration: 322},
		{Title: "Daft Punk - One More Time (Live at Coachella)", Uploader: "Fan", URL: "live", Duration: 318},
		{Title: "One More Time", Uploader: "Daft Punk - Topic", URL: "topic", Duration: 320},
		{Title: "One More Time (Remix) - Daft Punk", Uploader: "DJ Mix", URL: "remix", Duration: 400},
		{Title: "One More Time Karaoke Version", Uploader: "Sing King", URL: "karaoke", Duration: 321},
		{Title: "Otra cosa totalmente distinta", Uploader: "Alguien", URL: "other", Duration: 320},
	}
}

func TestBestPrefersTopicUploadWithMatchingDuration(t *testing.T) {
	w := Want{Title: "One More Time", Artists: []string{"Daft Punk"}, Duration: 320}
	best, score, ok := Best(cands(), w)
	if !ok || best.URL != "topic" {
		t.Fatalf("best = %+v score=%.1f ok=%v", best, score, ok)
	}
}

func TestLiveAndKaraokeAreRejectedWhenBestExcluded(t *testing.T) {
	w := Want{Title: "One More Time", Artists: []string{"Daft Punk"}, Duration: 320}
	var rest []Candidate
	for _, c := range cands() {
		if c.URL != "topic" && c.URL != "video" {
			rest = append(rest, c)
		}
	}
	if best, score, ok := Best(rest, w); ok {
		t.Errorf("no debería aceptar nada, pero eligió %q (%.1f)", best.URL, score)
	}
}

func TestDurationFarOffIsRejected(t *testing.T) {
	w := Want{Title: "One More Time", Artists: []string{"Daft Punk"}, Duration: 320}
	if s := Score(Candidate{Title: "Daft Punk - One More Time", Uploader: "Daft Punk - Topic", Duration: 700}, w); s > -50 {
		t.Errorf("duración muy distinta debe rechazarse, score=%.1f", s)
	}
}

func TestUnknownDurationStillWorksButPenalized(t *testing.T) {
	w := Want{Title: "One More Time", Artists: []string{"Daft Punk"}, Duration: 320}
	with := Score(Candidate{Title: "One More Time", Uploader: "Daft Punk - Topic", Duration: 320}, w)
	without := Score(Candidate{Title: "One More Time", Uploader: "Daft Punk - Topic"}, w)
	if without >= with || without < MinScore {
		t.Errorf("with=%.1f without=%.1f", with, without)
	}
}

func TestWantedVersionIsNotPenalized(t *testing.T) {
	w := Want{Title: "Hotel California - Live", Artists: []string{"Eagles"}, Duration: 400}
	// "Live" está en el título buscado: no debe restar puntos.
	s := Score(Candidate{Title: "Eagles - Hotel California (Live)", Uploader: "Eagles", Duration: 401}, w)
	if s < MinScore {
		t.Errorf("score=%.1f", s)
	}
}

func TestTitleNoiseIsIgnored(t *testing.T) {
	w := Want{Title: "Bohemian Rhapsody - Remastered 2011", Artists: []string{"Queen"}, Duration: 355}
	s := Score(Candidate{Title: "Queen - Bohemian Rhapsody (Official Video Remastered)", Uploader: "Queen Official", Duration: 359}, w)
	if s < MinScore {
		t.Errorf("score=%.1f", s)
	}
	w2 := Want{Title: "Canción (feat. Otro)", Artists: []string{"Ella"}, Duration: 200}
	if s := Score(Candidate{Title: "Ella - Canción", Uploader: "Ella", Duration: 201}, w2); s < MinScore {
		t.Errorf("feat. debe ignorarse, score=%.1f", s)
	}
}

func TestAccentsAndCase(t *testing.T) {
	w := Want{Title: "Corazón Espinado", Artists: []string{"Maná"}, Duration: 270}
	if s := Score(Candidate{Title: "MANA - CORAZON ESPINADO", Uploader: "Mana", Duration: 271}, w); s < MinScore {
		t.Errorf("score=%.1f", s)
	}
}

func TestNonLatinTitles(t *testing.T) {
	w := Want{Title: "夜に駆ける", Artists: []string{"YOASOBI"}, Duration: 261}
	if s := Score(Candidate{Title: "YOASOBI「夜に駆ける」 Official Music Video", Uploader: "Ayase", Duration: 262}, w); s < MinScore {
		t.Errorf("score=%.1f", s)
	}
}

func TestBestSkipsEmptyURLAndEmptyInput(t *testing.T) {
	if _, _, ok := Best(nil, Want{Title: "x"}); ok {
		t.Error("sin candidatos no hay resultado")
	}
	if _, _, ok := Best([]Candidate{{Title: "One More Time", Uploader: "Daft Punk - Topic", Duration: 320}}, Want{Title: "One More Time", Duration: 320}); ok {
		t.Error("un candidato sin URL no puede elegirse")
	}
}

func TestQuery(t *testing.T) {
	if q := Query(Want{Title: "Canción", Artists: []string{"A", "B"}}); q != "A - Canción" {
		t.Errorf("q = %q", q)
	}
	if q := Query(Want{Title: " Solo "}); q != "Solo" {
		t.Errorf("q = %q", q)
	}
}
