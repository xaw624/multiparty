package resolver

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	parentesis = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]`)
	// Paréntesis que son adornos del video, no parte del nombre de la canción.
	adorno = regexp.MustCompile(`(?i)\s*[\(\[][^\)\]]*\b(official|oficial|video|vídeo|audio|lyrics?|letra|visuali[sz]er|4k|hd|hq|mv|remaster(ed)?)\b[^\)\]]*[\)\]]`)
	// Sufijos tipo " - Remastered 2009" o " - Live".
	sufijoVersion = regexp.MustCompile(`(?i)\s+-\s+.*\b(remaster(ed)?|live|en vivo|version|versión|edit|mix)\b.*$`)
	sinAcentos    = strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
		"à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u", "ç", "c",
	)
)

// limpiarYouTube separa artista y título de un video de YouTube.
//
// Los canales "Artista - Topic" son pistas de YouTube Music con título limpio.
// En los demás, el formato habitual es "Artista - Canción (Official Video)".
func limpiarYouTube(titulo, canal string) (string, string) {
	if a, ok := strings.CutSuffix(canal, " - Topic"); ok {
		return strings.TrimSpace(titulo), a
	}
	artista := strings.TrimSuffix(canal, "VEVO")
	for _, sep := range []string{" - ", " – ", " — "} {
		if a, t, ok := strings.Cut(titulo, sep); ok {
			artista, titulo = a, t
			break
		}
	}
	titulo = adorno.ReplaceAllString(titulo, "")
	return strings.TrimSpace(titulo), strings.TrimSpace(artista)
}

// normalizar deja un texto comparable: minúsculas, sin acentos, sin
// paréntesis ni sufijos de versión, solo letras y números.
func normalizar(s string) string {
	s = sufijoVersion.ReplaceAllString(s, "")
	s = parentesis.ReplaceAllString(s, "")
	s = sinAcentos.Replace(strings.ToLower(s))
	var b strings.Builder
	espacio := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			espacio = false
		} else if !espacio && b.Len() > 0 {
			b.WriteByte(' ')
			espacio = true
		}
	}
	return strings.TrimSpace(b.String())
}

// mismoTitulo compara títulos ya normalizados por igualdad.
func mismoTitulo(a, b string) bool {
	na, nb := normalizar(a), normalizar(b)
	return na != "" && na == nb
}

// mismoArtista tolera colaboraciones: "Bad Bunny" coincide con "Bad Bunny & Drake".
func mismoArtista(a, b string) bool {
	na, nb := normalizar(a), normalizar(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb || strings.Contains(na, nb) || strings.Contains(nb, na)
}
