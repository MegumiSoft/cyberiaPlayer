// Package match elige, entre los resultados de una búsqueda en YouTube, el que mejor corresponde a una
// canción conocida (título, artistas y duración). Prefiere no devolver nada antes que la canción equivocada.
package match

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

type Candidate struct {
	Title    string
	Uploader string
	URL      string
	Duration float64 // segundos; 0 si se desconoce
}

type Want struct {
	Title    string
	Artists  []string
	Duration float64 // segundos; 0 si se desconoce
}

// MinScore es el puntaje mínimo para aceptar un resultado.
const MinScore = 3.0

// versionPenalty es lo que resta cada palabra de "otra versión" (live, remix, karaoke...). Es lo bastante
// grande para que una versión alternativa quede bajo MinScore aunque su duración coincida por casualidad:
// es otra grabación, y es preferible un error visible a bajar la equivocada.
const versionPenalty = 6.0

// Palabras que indican otra versión de la canción. Penalizan salvo que el título buscado también las tenga.
var versionWords = []string{
	"live", "cover", "remix", "karaoke", "instrumental", "reaction", "slowed", "reverb",
	"sped", "nightcore", "8d", "acoustic", "mashup", "tutorial", "lesson", "fanmade",
	"playlist", "compilation", "hour", "hours", "full album",
}

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ß", "ss",
)

func normalize(s string) string {
	return accents.Replace(strings.ToLower(s))
}

func tokens(s string) []string {
	return strings.FieldsFunc(normalize(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

var noiseParen = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)

// Palabras de relleno que las plataformas añaden a los títulos y que no sirven para comparar.
var filler = map[string]bool{
	"feat": true, "ft": true, "featuring": true, "remastered": true, "remaster": true, "version": true,
	"official": true, "audio": true, "video": true, "lyrics": true, "lyric": true, "hd": true, "hq": true,
	"the": true, "a": true, "de": true, "la": true, "el": true, "and": true, "y": true, "with": true, "mono": true,
	"stereo": true, "single": true, "radio": true, "edit": true, "explicit": true, "topic": true,
}

// coreTitle devuelve las palabras que distinguen al título: sin paréntesis, sin sufijos " - Remastered..."
// y sin relleno.
func coreTitle(title string) []string {
	t := noiseParen.ReplaceAllString(title, " ")
	if i := strings.Index(t, " - "); i > 0 {
		t = t[:i]
	}
	var out []string
	for _, w := range tokens(t) {
		if !filler[w] {
			out = append(out, w)
		}
	}
	if len(out) == 0 { // título hecho solo de relleno ("Audio"): usar todo
		return tokens(title)
	}
	return out
}

func containsAll(haystack map[string]bool, words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !haystack[w] {
			return false
		}
	}
	return true
}

func set(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// Score puntúa un candidato. Un valor negativo muy bajo significa rechazo seguro.
func Score(c Candidate, w Want) float64 {
	candTitle := set(tokens(c.Title))
	candAll := set(append(tokens(c.Title), tokens(c.Uploader)...))
	wantTokens := set(tokens(w.Title))

	core := coreTitle(w.Title)
	if len(core) == 0 {
		return -100
	}
	hits := 0
	for _, word := range core {
		if candAll[word] {
			hits++
		}
	}
	ratio := float64(hits) / float64(len(core))
	if ratio < 0.7 {
		return -100 // no parece la misma canción
	}
	score := ratio * 2

	// Duración: es la señal más fiable para distinguir versiones.
	switch {
	case w.Duration > 0 && c.Duration > 0:
		d := math.Abs(w.Duration - c.Duration)
		switch {
		case d <= 3:
			score += 4
		case d <= 8:
			score += 2
		case d <= 15:
			score += 0
		default:
			return -100
		}
	case w.Duration > 0 && c.Duration == 0:
		score -= 1
	}

	// Artista presente en el título o en el nombre del canal.
	for _, a := range w.Artists {
		if containsAll(candAll, tokens(a)) {
			score += 2
			break
		}
	}

	// Versiones alternativas.
	joined := " " + strings.Join(tokens(c.Title), " ") + " "
	for _, vw := range versionWords {
		if strings.Contains(joined, " "+vw+" ") && !wantTokens[strings.Fields(vw)[0]] {
			score -= versionPenalty
		}
	}

	// Subidas oficiales o automáticas.
	if strings.HasSuffix(normalize(strings.TrimSpace(c.Uploader)), "- topic") {
		score += 3
	}
	if candTitle["official"] && candTitle["audio"] {
		score += 1.5
	} else if candTitle["audio"] || candTitle["official"] {
		score += 0.5
	}
	return score
}

// Best devuelve el mejor candidato aceptable. ok es false si ninguno llega al puntaje mínimo.
func Best(cands []Candidate, w Want) (best Candidate, score float64, ok bool) {
	score = math.Inf(-1)
	for _, c := range cands {
		if c.URL == "" {
			continue
		}
		if s := Score(c, w); s > score {
			best, score = c, s
		}
	}
	return best, score, score >= MinScore
}

// Query arma el texto de búsqueda para una canción.
func Query(w Want) string {
	q := w.Title
	if len(w.Artists) > 0 {
		q = w.Artists[0] + " - " + w.Title
	}
	return strings.TrimSpace(q)
}
