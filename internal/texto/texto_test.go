package texto

import "testing"

func TestLimpiarYouTube(t *testing.T) {
	casos := []struct{ titulo, canal, quiereT, quiereA string }{
		{"Never Gonna Give You Up", "Rick Astley - Topic", "Never Gonna Give You Up", "Rick Astley"},
		{"Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)", "Rick Astley", "Never Gonna Give You Up", "Rick Astley"},
		{"Bad Bunny – Tití Me Preguntó (Video Oficial)", "Bad Bunny", "Tití Me Preguntó", "Bad Bunny"},
		{"Despacito (feat. Daddy Yankee)", "LuisFonsiVEVO", "Despacito (feat. Daddy Yankee)", "LuisFonsi"},
	}
	for _, c := range casos {
		tt, a := LimpiarYouTube(c.titulo, c.canal)
		if tt != c.quiereT || a != c.quiereA {
			t.Errorf("LimpiarYouTube(%q, %q) = %q, %q; quiere %q, %q", c.titulo, c.canal, tt, a, c.quiereT, c.quiereA)
		}
	}
}

func TestNormalizar(t *testing.T) {
	casos := map[string]string{
		"Never Gonna Give You Up (2022 Remaster)": "never gonna give you up",
		"Tití Me Preguntó":                        "titi me pregunto",
		"Here Comes the Sun - Remastered 2009":    "here comes the sun",
		"  Hello,   World!  ":                     "hello world",
	}
	for in, quiere := range casos {
		if got := Normalizar(in); got != quiere {
			t.Errorf("Normalizar(%q) = %q, quiere %q", in, got, quiere)
		}
	}
}
