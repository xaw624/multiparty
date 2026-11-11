package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/xaw624/multiparty/internal/pool"
)

// datos son los metadatos de una canción en una fuente.
type datos struct {
	titulo    string
	artista   string
	isrc      string
	portada   string
	duracionS int
	id        string // id en la plataforma de la fuente
	url       string // link a la canción en esa plataforma
}

func (r *Resolver) pedir(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; multiparty)")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusNotFound, http.StatusBadRequest:
		err = pool.ErrNoEncontrada
	case http.StatusTooManyRequests:
		err = pool.ErrLimite
	default:
		err = fmt.Errorf("%s: estado HTTP %d", req.URL.Host, resp.StatusCode)
	}
	resp.Body.Close()
	return nil, err
}

func (r *Resolver) json(ctx context.Context, u string, dst any) error {
	resp, err := r.pedir(ctx, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(dst); err != nil {
		return fmt.Errorf("respuesta inválida de %s: %w", resp.Request.URL.Host, err)
	}
	return nil
}

// ---- Deezer: API pública sin clave ----

type deezerTrack struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Link     string `json:"link"`
	Duration int    `json:"duration"`
	ISRC     string `json:"isrc"`
	Artist   struct {
		Name string `json:"name"`
	} `json:"artist"`
	Album struct {
		CoverMedium string `json:"cover_medium"`
	} `json:"album"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func (t deezerTrack) datos() datos {
	id := strconv.FormatInt(t.ID, 10)
	return datos{
		titulo: t.Title, artista: t.Artist.Name, isrc: t.ISRC, portada: t.Album.CoverMedium,
		duracionS: t.Duration, id: id, url: "https://www.deezer.com/track/" + id,
	}
}

func (r *Resolver) deezerTrack(ctx context.Context, id string) (datos, error) {
	var t deezerTrack
	if err := r.json(ctx, r.base.deezer+"/track/"+url.PathEscape(id), &t); err != nil {
		return datos{}, err
	}
	if t.Error != nil || t.ID == 0 {
		return datos{}, pool.ErrNoEncontrada // Deezer responde 200 con {"error": …}
	}
	return t.datos(), nil
}

func (r *Resolver) deezerBuscar(ctx context.Context, consulta string) ([]datos, error) {
	var res struct {
		Data []deezerTrack `json:"data"`
	}
	q := url.Values{"q": {consulta}, "limit": {"10"}}
	if err := r.json(ctx, r.base.deezer+"/search?"+q.Encode(), &res); err != nil {
		return nil, err
	}
	out := make([]datos, 0, len(res.Data))
	for _, t := range res.Data {
		out = append(out, t.datos())
	}
	return out, nil
}

// ---- iTunes / Apple Music: Search API sin clave ----

type itunesRespuesta struct {
	Results []struct {
		Kind            string `json:"kind"`
		TrackID         int64  `json:"trackId"`
		TrackName       string `json:"trackName"`
		ArtistName      string `json:"artistName"`
		TrackViewURL    string `json:"trackViewUrl"`
		ArtworkURL100   string `json:"artworkUrl100"`
		TrackTimeMillis int    `json:"trackTimeMillis"`
	} `json:"results"`
}

func (res itunesRespuesta) canciones() []datos {
	var out []datos
	for _, t := range res.Results {
		if t.Kind != "song" {
			continue
		}
		out = append(out, datos{
			titulo:    t.TrackName,
			artista:   t.ArtistName,
			portada:   strings.Replace(t.ArtworkURL100, "100x100", "300x300", 1),
			duracionS: (t.TrackTimeMillis + 500) / 1000,
			id:        strconv.FormatInt(t.TrackID, 10),
			url:       sinParametro(t.TrackViewURL, "uo"),
		})
	}
	return out
}

func (r *Resolver) itunesLookup(ctx context.Context, id string) (datos, error) {
	var res itunesRespuesta
	q := url.Values{"id": {id}, "country": {r.pais}}
	if err := r.json(ctx, r.base.itunes+"/lookup?"+q.Encode(), &res); err != nil {
		return datos{}, err
	}
	if c := res.canciones(); len(c) > 0 {
		return c[0], nil
	}
	return datos{}, pool.ErrNoEncontrada
}

func (r *Resolver) itunesBuscar(ctx context.Context, termino string) ([]datos, error) {
	var res itunesRespuesta
	q := url.Values{"term": {termino}, "entity": {"song"}, "limit": {"10"}, "country": {r.pais}}
	if err := r.json(ctx, r.base.itunes+"/search?"+q.Encode(), &res); err != nil {
		return nil, err
	}
	return res.canciones(), nil
}

// ---- YouTube: oEmbed sin clave (no gasta cuota de la Data API) ----

func (r *Resolver) youtube(ctx context.Context, id string) (datos, error) {
	var o struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	q := url.Values{"url": {"https://www.youtube.com/watch?v=" + id}, "format": {"json"}}
	if err := r.json(ctx, r.base.youtube+"?"+q.Encode(), &o); err != nil {
		// oEmbed responde 401/403 para videos privados o sin inserción.
		return datos{}, err
	}
	titulo, artista := limpiarYouTube(o.Title, o.AuthorName)
	return datos{titulo: titulo, artista: artista, portada: o.ThumbnailURL, id: id}, nil
}

// ---- Spotify: etiquetas de vista previa de la página pública ----

var metaSpotify = regexp.MustCompile(`<meta (?:property|name)="(og:title|og:image|music:duration|music:musician_description)" content="([^"]*)"`)

func (r *Resolver) spotify(ctx context.Context, id string) (datos, error) {
	resp, err := r.pedir(ctx, r.base.spotify+"/track/"+url.PathEscape(id))
	if err != nil {
		return datos{}, err
	}
	defer resp.Body.Close()
	cuerpo, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return datos{}, err
	}
	d := datos{id: id}
	for _, m := range metaSpotify.FindAllSubmatch(cuerpo, -1) {
		v := html.UnescapeString(string(m[2]))
		switch string(m[1]) {
		case "og:title":
			d.titulo = v
		case "og:image":
			d.portada = v
		case "music:duration":
			d.duracionS, _ = strconv.Atoi(v)
		case "music:musician_description":
			if d.artista == "" { // la primera es el artista principal
				d.artista = v
			}
		}
	}
	if d.titulo == "" {
		return datos{}, pool.ErrNoEncontrada
	}
	return d, nil
}

func sinParametro(enlace, param string) string {
	u, err := url.Parse(enlace)
	if err != nil {
		return enlace
	}
	q := u.Query()
	q.Del(param)
	u.RawQuery = q.Encode()
	return u.String()
}
