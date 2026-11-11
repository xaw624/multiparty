// Paquete resolver convierte un link de canción en sus datos y en links a la
// misma canción en otras plataformas, usando solo fuentes gratuitas y sin
// clave:
//
//   - Spotify: etiquetas de vista previa (og:*) de la página pública.
//   - YouTube / YouTube Music: oEmbed.
//   - Apple Music: Search API de iTunes.
//   - Deezer: API pública (da el ISRC).
//
// Siempre agrega la página de song.link como link universal. Reemplaza a la
// API de Odesli, retirada en julio de 2026 (ver docs/PLAN.md, sección 6).
package resolver

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xaw624/multiparty/internal/pool"
)

// toleranciaDuracion es la diferencia máxima, en segundos, para considerar
// que dos versiones son la misma grabación.
const toleranciaDuracion = 5

type bases struct {
	deezer, itunes, youtube, spotify string
}

// Resolver resuelve links de canciones.
type Resolver struct {
	http *http.Client
	pais string
	base bases
}

// New crea un Resolver. pais es el código ISO de la tienda de Apple Music
// (por ejemplo "MX").
func New(pais string) *Resolver {
	return &Resolver{
		http: &http.Client{Timeout: 8 * time.Second},
		pais: strings.ToLower(pais),
		base: bases{
			deezer:  "https://api.deezer.com",
			itunes:  "https://itunes.apple.com",
			youtube: "https://www.youtube.com/oembed",
			spotify: "https://open.spotify.com",
		},
	}
}

// Resolver devuelve la canción del link. Errores: pool.ErrEnlaceInvalido,
// pool.ErrNoSoportada, pool.ErrNoEsCancion, pool.ErrNoEncontrada,
// pool.ErrLimite o un error de red. Si falla una fuente secundaria (Deezer o
// Apple), la canción se devuelve igual, con menos links.
func (r *Resolver) Resolver(ctx context.Context, enlace string) (pool.CancionResuelta, error) {
	u, err := url.Parse(strings.TrimSpace(enlace))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return pool.CancionResuelta{}, pool.ErrEnlaceInvalido
	}
	o, err := detectar(u)
	if err != nil {
		return pool.CancionResuelta{}, err
	}

	// 1. Datos de la plataforma de origen.
	var (
		d       datos
		enlaces []pool.Enlace
		directo = func(plataforma, id, u string) {
			enlaces = append(enlaces, pool.Enlace{Plataforma: plataforma, TrackID: id, URL: u, Confianza: pool.Directo})
		}
	)
	switch o.plataforma {
	case "spotify":
		d, err = r.spotify(ctx, o.id)
		directo("spotify", o.id, "https://open.spotify.com/track/"+o.id)
	case "youtube":
		d, err = r.youtube(ctx, o.id)
		directo("youtubeMusic", o.id, "https://music.youtube.com/watch?v="+o.id)
		directo("youtube", o.id, "https://www.youtube.com/watch?v="+o.id)
	case "apple":
		d, err = r.itunesLookup(ctx, o.id)
		directo("appleMusic", o.id, d.url)
	case "deezer":
		d, err = r.deezerTrack(ctx, o.id)
		directo("deezer", o.id, d.url)
	}
	if err != nil {
		return pool.CancionResuelta{}, err
	}
	canonico := enlaces[0].URL

	// 2. Buscar la misma canción en Deezer (ISRC) y Apple Music.
	if o.plataforma != "deezer" {
		if dz, ok := r.buscar(ctx, d, r.deezerBuscar); ok {
			enlaces = append(enlaces, pool.Enlace{Plataforma: "deezer", TrackID: dz.id, URL: dz.url, Confianza: pool.Busqueda})
			if d.isrc == "" {
				d.isrc = dz.isrc
			}
			if o.plataforma == "youtube" {
				// Los títulos de YouTube suelen traer adornos; los de Deezer, no.
				d.titulo, d.artista = dz.titulo, dz.artista
				if dz.portada != "" {
					d.portada = dz.portada
				}
			}
			if d.duracionS == 0 {
				d.duracionS = dz.duracionS
			}
		}
	}
	if o.plataforma != "apple" {
		if ap, ok := r.buscar(ctx, d, r.itunesBuscar); ok {
			enlaces = append(enlaces, pool.Enlace{Plataforma: "appleMusic", TrackID: ap.id, URL: ap.url, Confianza: pool.Busqueda})
		}
	}

	// 3. Página de song.link: abre la canción en cualquier plataforma.
	enlaces = append(enlaces, pool.Enlace{Plataforma: "songlink", URL: "https://song.link/" + canonico, Confianza: pool.Directo})

	return pool.CancionResuelta{
		ISRC:       d.isrc,
		Titulo:     d.titulo,
		Artista:    d.artista,
		PortadaURL: d.portada,
		Enlaces:    enlaces,
	}, nil
}

// buscar consulta una fuente por "artista título" y devuelve el primer
// resultado con el mismo título, el mismo artista y una duración parecida.
func (r *Resolver) buscar(ctx context.Context, d datos, fuente func(context.Context, string) ([]datos, error)) (datos, bool) {
	if d.artista == "" || d.titulo == "" {
		return datos{}, false
	}
	resultados, err := fuente(ctx, d.artista+" "+normalizar(d.titulo))
	if err != nil {
		return datos{}, false
	}
	for _, c := range resultados {
		if !mismoTitulo(d.titulo, c.titulo) || !mismoArtista(d.artista, c.artista) {
			continue
		}
		if d.duracionS > 0 && c.duracionS > 0 && abs(d.duracionS-c.duracionS) > toleranciaDuracion {
			continue
		}
		return c, true
	}
	return datos{}, false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
