package resolver

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/xaw624/multiparty/internal/pool"
)

// origen es la plataforma y el id de la canción del link pegado.
type origen struct {
	plataforma string // spotify, youtube, apple, deezer
	id         string
}

var idValido = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// detectar reconoce links de canción de las plataformas soportadas.
func detectar(u *url.URL) (origen, error) {
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	q := u.Query()

	var o origen
	switch host {
	case "open.spotify.com":
		if strings.HasPrefix(segs[0], "intl-") {
			segs = segs[1:]
		}
		if len(segs) >= 2 && segs[0] == "track" {
			o = origen{"spotify", segs[1]}
		} else if len(segs) >= 1 && esColeccion(segs[0]) {
			return o, pool.ErrNoEsCancion
		}

	case "youtube.com", "music.youtube.com":
		switch {
		case q.Get("v") != "":
			o = origen{"youtube", q.Get("v")}
		case len(segs) >= 2 && segs[0] == "shorts":
			o = origen{"youtube", segs[1]}
		case esColeccion(segs[0]) || strings.HasPrefix(segs[0], "@"):
			return o, pool.ErrNoEsCancion
		}

	case "youtu.be":
		o = origen{"youtube", segs[0]}

	case "music.apple.com", "itunes.apple.com":
		switch {
		case q.Get("i") != "":
			o = origen{"apple", q.Get("i")}
		case len(segs) >= 3 && segs[1] == "song":
			o = origen{"apple", segs[len(segs)-1]}
		case len(segs) >= 2 && esColeccion(segs[1]):
			return o, pool.ErrNoEsCancion
		}

	case "deezer.com":
		for i := 0; i+1 < len(segs); i++ {
			if segs[i] == "track" {
				o = origen{"deezer", segs[i+1]}
				break
			}
			if esColeccion(segs[i]) {
				return o, pool.ErrNoEsCancion
			}
		}

	default:
		return o, pool.ErrNoSoportada
	}

	if o.plataforma == "" {
		return o, pool.ErrNoEncontrada
	}
	if !idValido.MatchString(o.id) {
		return o, pool.ErrEnlaceInvalido
	}
	return o, nil
}

func esColeccion(seg string) bool {
	switch seg {
	case "album", "playlist", "artist", "show", "episode", "channel", "c", "user", "browse":
		return true
	}
	return false
}

// Identificar reconoce un link de canción y devuelve su plataforma
// ("spotify", "youtube", "apple" o "deezer") y su id, sin consultar nada.
func Identificar(enlace string) (plataforma, id string, err error) {
	u, err := url.Parse(strings.TrimSpace(enlace))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", pool.ErrEnlaceInvalido
	}
	o, err := detectar(u)
	return o.plataforma, o.id, err
}
