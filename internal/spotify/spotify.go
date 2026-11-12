// Paquete spotify es un cliente delgado de la Spotify Web API con lo que
// necesita Multiparty, ya adaptado a los cambios de febrero de 2026 para
// apps en modo desarrollo: /me/playlists, /playlists/{id}/items y búsqueda
// de máximo 10 resultados.
package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Scopes que pide Multiparty.
var Scopes = []string{
	"playlist-modify-private",
	"playlist-modify-public",
	"playlist-read-private",
	"user-read-recently-played",
}

const urlAPI = "https://api.spotify.com/v1"

var (
	// ErrReconectar: el token se revocó o caducó.
	ErrReconectar = errors.New("hay que volver a conectar Spotify")
	// ErrNoAutorizado: la cuenta no está en la lista de usuarios de la app
	// (modo desarrollo: máximo 5, agregados a mano en el panel de Spotify).
	ErrNoAutorizado = errors.New("la cuenta de Spotify no está autorizada en la app")
	// ErrLimite: demasiadas peticiones; hay que esperar.
	ErrLimite = errors.New("límite de peticiones de Spotify")
	// ErrNoEncontrado: la playlist o la canción ya no existe.
	ErrNoEncontrado = errors.New("no encontrado en Spotify")
)

// MaxBusqueda es el límite de resultados de búsqueda en modo desarrollo.
const MaxBusqueda = 10

// Client llama a la API con un http.Client autenticado.
type Client struct {
	http *http.Client
	base string
}

// New crea un cliente. httpClient debe agregar el token OAuth.
func New(httpClient *http.Client) *Client { return &Client{http: httpClient, base: urlAPI} }

// NewConBase es como New pero contra otra URL base (pruebas).
func NewConBase(httpClient *http.Client, base string) *Client {
	return &Client{http: httpClient, base: strings.TrimRight(base, "/")}
}

// Track es una canción de Spotify.
type Track struct {
	ID        string
	URI       string
	Titulo    string
	Artista   string // el primero de la lista
	DuracionS int
	ISRC      string
	Portada   string
}

// URL es el link público de la canción.
func (t Track) URL() string { return "https://open.spotify.com/track/" + t.ID }

// Playlist es una playlist del usuario.
type Playlist struct {
	ID       string
	Titulo   string
	Cantidad int
}

// Escucha es una canción escuchada recientemente.
type Escucha struct {
	TrackID string
	Cuando  time.Time
}

type trackJSON struct {
	ID         string `json:"id"`
	URI        string `json:"uri"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	IsLocal    bool   `json:"is_local"`
	DurationMS int    `json:"duration_ms"`
	Artists    []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Images []struct {
			URL   string `json:"url"`
			Width int    `json:"width"`
		} `json:"images"`
	} `json:"album"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
}

func (t trackJSON) track() (Track, bool) {
	if t.ID == "" || t.IsLocal || (t.Type != "" && t.Type != "track") {
		return Track{}, false
	}
	out := Track{
		ID: t.ID, URI: t.URI, Titulo: t.Name,
		DuracionS: (t.DurationMS + 500) / 1000, ISRC: t.ExternalIDs.ISRC,
	}
	if out.URI == "" {
		out.URI = "spotify:track:" + t.ID
	}
	if len(t.Artists) > 0 {
		out.Artista = t.Artists[0].Name
	}
	// La imagen mediana (~300 px) si existe; si no, la primera.
	for _, img := range t.Album.Images {
		if out.Portada == "" || (img.Width >= 200 && img.Width <= 400) {
			out.Portada = img.URL
		}
	}
	return out, true
}

func (c *Client) llamar(ctx context.Context, metodo, ruta string, q url.Values, cuerpo, destino any) error {
	var body io.Reader
	if cuerpo != nil {
		b, err := json.Marshal(cuerpo)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	u := c.base + ruta
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, metodo, u, body)
	if err != nil {
		return err
	}
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return fmt.Errorf("%w: %s", ErrReconectar, re.ErrorCode)
		}
		return fmt.Errorf("spotify: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return ErrReconectar
		case http.StatusForbidden:
			if strings.Contains(strings.ToLower(e.Error.Message), "regist") {
				return ErrNoAutorizado
			}
			return fmt.Errorf("spotify: prohibido: %s", e.Error.Message)
		case http.StatusNotFound:
			return ErrNoEncontrado
		case http.StatusTooManyRequests:
			return ErrLimite
		}
		return fmt.Errorf("spotify: HTTP %d: %s", resp.StatusCode, e.Error.Message)
	}
	if destino == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(destino); err != nil {
		return fmt.Errorf("spotify: respuesta inválida: %w", err)
	}
	return nil
}

// Yo devuelve el id y el nombre visible del usuario.
func (c *Client) Yo(ctx context.Context) (id, nombre string, err error) {
	var r struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	if err := c.llamar(ctx, http.MethodGet, "/me", nil, nil, &r); err != nil {
		return "", "", err
	}
	if r.DisplayName == "" {
		r.DisplayName = r.ID
	}
	return r.ID, r.DisplayName, nil
}

// CrearPlaylist crea una playlist privada y devuelve su id.
func (c *Client) CrearPlaylist(ctx context.Context, nombre, descripcion string) (string, error) {
	var r struct {
		ID string `json:"id"`
	}
	cuerpo := map[string]any{"name": nombre, "public": false, "description": descripcion}
	if err := c.llamar(ctx, http.MethodPost, "/me/playlists", nil, cuerpo, &r); err != nil {
		return "", err
	}
	return r.ID, nil
}

// Reemplazar deja en la playlist exactamente esas canciones, en ese orden
// (máximo 100). Una lista vacía la vacía.
func (c *Client) Reemplazar(ctx context.Context, playlistID string, uris []string) error {
	if len(uris) > 100 {
		uris = uris[:100]
	}
	if uris == nil {
		uris = []string{}
	}
	return c.llamar(ctx, http.MethodPut, "/playlists/"+url.PathEscape(playlistID)+"/items",
		nil, map[string]any{"uris": uris}, nil)
}

// MisPlaylists lista las playlists del usuario (hasta 200).
func (c *Client) MisPlaylists(ctx context.Context) ([]Playlist, error) {
	var out []Playlist
	q := url.Values{"limit": {"50"}}
	for pagina := 0; pagina < 4; pagina++ {
		q.Set("offset", strconv.Itoa(pagina*50))
		var r struct {
			Next  *string `json:"next"`
			Items []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Items *struct {
					Total int `json:"total"`
				} `json:"items"`
				Tracks *struct { // nombre anterior a febrero de 2026
					Total int `json:"total"`
				} `json:"tracks"`
			} `json:"items"`
		}
		if err := c.llamar(ctx, http.MethodGet, "/me/playlists", q, nil, &r); err != nil {
			return nil, err
		}
		for _, p := range r.Items {
			pl := Playlist{ID: p.ID, Titulo: p.Name}
			if p.Items != nil {
				pl.Cantidad = p.Items.Total
			} else if p.Tracks != nil {
				pl.Cantidad = p.Tracks.Total
			}
			out = append(out, pl)
		}
		if r.Next == nil || *r.Next == "" {
			break
		}
	}
	return out, nil
}

// ItemsDePlaylist lista hasta max canciones de una playlist propia o
// colaborativa. Omite episodios y archivos locales.
func (c *Client) ItemsDePlaylist(ctx context.Context, playlistID string, max int) ([]Track, error) {
	var out []Track
	q := url.Values{"limit": {"50"}}
	for offset := 0; len(out) < max; offset += 50 {
		q.Set("offset", strconv.Itoa(offset))
		var r struct {
			Next  *string `json:"next"`
			Items []struct {
				Item  *trackJSON `json:"item"`
				Track *trackJSON `json:"track"` // nombre anterior a febrero de 2026
			} `json:"items"`
		}
		if err := c.llamar(ctx, http.MethodGet, "/playlists/"+url.PathEscape(playlistID)+"/items", q, nil, &r); err != nil {
			return nil, err
		}
		for _, it := range r.Items {
			tj := it.Item
			if tj == nil {
				tj = it.Track
			}
			if tj == nil {
				continue
			}
			if t, ok := tj.track(); ok {
				out = append(out, t)
				if len(out) == max {
					break
				}
			}
		}
		if r.Next == nil || *r.Next == "" || len(r.Items) == 0 {
			break
		}
	}
	return out, nil
}

// Buscar busca canciones (máximo MaxBusqueda resultados). Acepta filtros de
// Spotify como isrc:XXXX o track:"…" artist:"…".
func (c *Client) Buscar(ctx context.Context, consulta string, limite int) ([]Track, error) {
	limite = min(max(limite, 1), MaxBusqueda)
	var r struct {
		Tracks struct {
			Items []trackJSON `json:"items"`
		} `json:"tracks"`
	}
	q := url.Values{"q": {consulta}, "type": {"track"}, "limit": {strconv.Itoa(limite)}}
	if err := c.llamar(ctx, http.MethodGet, "/search", q, nil, &r); err != nil {
		return nil, err
	}
	var out []Track
	for _, tj := range r.Tracks.Items {
		if t, ok := tj.track(); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// Escuchadas devuelve las canciones escuchadas después de desde (máximo 50).
func (c *Client) Escuchadas(ctx context.Context, desde time.Time) ([]Escucha, error) {
	q := url.Values{"limit": {"50"}}
	if !desde.IsZero() {
		q.Set("after", strconv.FormatInt(desde.UnixMilli(), 10))
	}
	var r struct {
		Items []struct {
			Track    trackJSON `json:"track"`
			PlayedAt time.Time `json:"played_at"`
		} `json:"items"`
	}
	if err := c.llamar(ctx, http.MethodGet, "/me/player/recently-played", q, nil, &r); err != nil {
		return nil, err
	}
	out := make([]Escucha, 0, len(r.Items))
	for _, it := range r.Items {
		if it.Track.ID != "" {
			out = append(out, Escucha{TrackID: it.Track.ID, Cuando: it.PlayedAt})
		}
	}
	return out, nil
}
