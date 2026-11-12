// Paquete youtube es un cliente delgado de la YouTube Data API v3 con lo que
// necesita Multiparty: canal propio, playlists, sus elementos, búsqueda y
// duración de videos.
//
// Costo en unidades de cuota (10 000 al día por proyecto):
// listar = 1, insertar o borrar = 50, buscar = 100.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

// Scope permite leer y modificar las playlists del usuario.
const Scope = "https://www.googleapis.com/auth/youtube"

const urlAPI = "https://www.googleapis.com/youtube/v3"

var (
	// ErrCuota: se agotó la cuota diaria del proyecto.
	ErrCuota = errors.New("cuota diaria de YouTube agotada")
	// ErrReconectar: el token se revocó o caducó; hay que volver a conectar.
	ErrReconectar = errors.New("hay que volver a conectar YouTube")
	// ErrSinCanal: la cuenta de Google no tiene canal de YouTube.
	ErrSinCanal = errors.New("la cuenta no tiene canal de YouTube")
	// ErrNoEncontrado: la playlist o el elemento ya no existe.
	ErrNoEncontrado = errors.New("no encontrado en YouTube")
)

// Client llama a la API con un http.Client autenticado (oauth2.NewClient).
type Client struct {
	http *http.Client
	base string
}

// New crea un cliente. httpClient debe agregar el token OAuth.
func New(httpClient *http.Client) *Client {
	return &Client{http: httpClient, base: urlAPI}
}

// NewConBase es como New pero contra otra URL base (pruebas).
func NewConBase(httpClient *http.Client, base string) *Client {
	return &Client{http: httpClient, base: strings.TrimRight(base, "/")}
}

// Video es un resultado de búsqueda o un elemento de playlist.
type Video struct {
	ID     string
	Titulo string
	Canal  string
}

// Playlist es una playlist del usuario.
type Playlist struct {
	ID       string
	Titulo   string
	Cantidad int
}

type errorAPI struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Errors  []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
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
	req, err := http.NewRequestWithContext(ctx, metodo, c.base+ruta+"?"+q.Encode(), body)
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
		return fmt.Errorf("youtube: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e errorAPI
		json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		for _, r := range e.Error.Errors {
			switch r.Reason {
			case "quotaExceeded", "dailyLimitExceeded", "rateLimitExceeded":
				return ErrCuota
			case "youtubeSignupRequired", "channelNotFound":
				return ErrSinCanal
			case "playlistNotFound", "playlistItemNotFound", "videoNotFound":
				return ErrNoEncontrado
			}
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return ErrReconectar
		case http.StatusNotFound:
			return ErrNoEncontrado
		}
		return fmt.Errorf("youtube: HTTP %d: %s", resp.StatusCode, e.Error.Message)
	}
	if destino == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(destino); err != nil {
		return fmt.Errorf("youtube: respuesta inválida: %w", err)
	}
	return nil
}

// CanalPropio devuelve el id y el nombre del canal del usuario (1 unidad).
func (c *Client) CanalPropio(ctx context.Context) (id, nombre string, err error) {
	var r struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		} `json:"items"`
	}
	q := url.Values{"part": {"snippet"}, "mine": {"true"}}
	if err := c.llamar(ctx, http.MethodGet, "/channels", q, nil, &r); err != nil {
		return "", "", err
	}
	if len(r.Items) == 0 {
		return "", "", ErrSinCanal
	}
	return r.Items[0].ID, r.Items[0].Snippet.Title, nil
}

// CrearPlaylist crea una playlist privada y devuelve su id (50 unidades).
func (c *Client) CrearPlaylist(ctx context.Context, titulo, descripcion string) (string, error) {
	cuerpo := map[string]any{
		"snippet": map[string]string{"title": titulo, "description": descripcion},
		"status":  map[string]string{"privacyStatus": "private"},
	}
	var r struct {
		ID string `json:"id"`
	}
	q := url.Values{"part": {"snippet,status"}}
	if err := c.llamar(ctx, http.MethodPost, "/playlists", q, cuerpo, &r); err != nil {
		return "", err
	}
	return r.ID, nil
}

// AgregarItem agrega un video al final de una playlist y devuelve el id del
// elemento, que se usa para quitarlo después (50 unidades).
func (c *Client) AgregarItem(ctx context.Context, playlistID, videoID string) (string, error) {
	cuerpo := map[string]any{
		"snippet": map[string]any{
			"playlistId": playlistID,
			"resourceId": map[string]string{"kind": "youtube#video", "videoId": videoID},
		},
	}
	var r struct {
		ID string `json:"id"`
	}
	q := url.Values{"part": {"snippet"}}
	if err := c.llamar(ctx, http.MethodPost, "/playlistItems", q, cuerpo, &r); err != nil {
		return "", err
	}
	return r.ID, nil
}

// QuitarItem quita un elemento de una playlist (50 unidades). Si ya no
// existe (alguien lo quitó a mano), no es error.
func (c *Client) QuitarItem(ctx context.Context, itemID string) error {
	err := c.llamar(ctx, http.MethodDelete, "/playlistItems", url.Values{"id": {itemID}}, nil, nil)
	if errors.Is(err, ErrNoEncontrado) {
		return nil
	}
	return err
}

// MisPlaylists lista las playlists del usuario (1 unidad por cada 50).
func (c *Client) MisPlaylists(ctx context.Context) ([]Playlist, error) {
	var out []Playlist
	q := url.Values{"part": {"snippet,contentDetails"}, "mine": {"true"}, "maxResults": {"50"}}
	for pagina := 0; pagina < 4; pagina++ {
		var r struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ID      string `json:"id"`
				Snippet struct {
					Title string `json:"title"`
				} `json:"snippet"`
				ContentDetails struct {
					ItemCount int `json:"itemCount"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		if err := c.llamar(ctx, http.MethodGet, "/playlists", q, nil, &r); err != nil {
			return nil, err
		}
		for _, it := range r.Items {
			out = append(out, Playlist{ID: it.ID, Titulo: it.Snippet.Title, Cantidad: it.ContentDetails.ItemCount})
		}
		if r.NextPageToken == "" {
			break
		}
		q.Set("pageToken", r.NextPageToken)
	}
	return out, nil
}

// ItemsDePlaylist lista hasta max videos de una playlist (1 unidad por cada
// 50). Omite los videos borrados o privados.
func (c *Client) ItemsDePlaylist(ctx context.Context, playlistID string, max int) ([]Video, error) {
	var out []Video
	q := url.Values{"part": {"snippet"}, "playlistId": {playlistID}, "maxResults": {"50"}}
	for len(out) < max {
		var r struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				Snippet struct {
					Title                  string `json:"title"`
					VideoOwnerChannelTitle string `json:"videoOwnerChannelTitle"`
					ResourceID             struct {
						VideoID string `json:"videoId"`
					} `json:"resourceId"`
				} `json:"snippet"`
			} `json:"items"`
		}
		if err := c.llamar(ctx, http.MethodGet, "/playlistItems", q, nil, &r); err != nil {
			return nil, err
		}
		for _, it := range r.Items {
			s := it.Snippet
			if s.VideoOwnerChannelTitle == "" || s.ResourceID.VideoID == "" {
				continue // "Deleted video" / "Private video"
			}
			out = append(out, Video{ID: s.ResourceID.VideoID, Titulo: s.Title, Canal: s.VideoOwnerChannelTitle})
			if len(out) == max {
				break
			}
		}
		if r.NextPageToken == "" {
			break
		}
		q.Set("pageToken", r.NextPageToken)
	}
	return out, nil
}

// Buscar busca videos de música (100 unidades).
func (c *Client) Buscar(ctx context.Context, consulta string, max int) ([]Video, error) {
	var r struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
			Snippet struct {
				Title        string `json:"title"`
				ChannelTitle string `json:"channelTitle"`
			} `json:"snippet"`
		} `json:"items"`
	}
	q := url.Values{
		"part": {"snippet"}, "type": {"video"}, "videoCategoryId": {"10"},
		"maxResults": {strconv.Itoa(max)}, "q": {consulta},
	}
	if err := c.llamar(ctx, http.MethodGet, "/search", q, nil, &r); err != nil {
		return nil, err
	}
	out := make([]Video, 0, len(r.Items))
	for _, it := range r.Items {
		out = append(out, Video{
			ID: it.ID.VideoID,
			// La búsqueda devuelve títulos con entidades HTML (&amp;, &#39;).
			Titulo: html.UnescapeString(it.Snippet.Title),
			Canal:  html.UnescapeString(it.Snippet.ChannelTitle),
		})
	}
	return out, nil
}

// Duraciones devuelve la duración en segundos de hasta 50 videos (1 unidad).
func (c *Client) Duraciones(ctx context.Context, ids []string) (map[string]int, error) {
	out := map[string]int{}
	if len(ids) == 0 {
		return out, nil
	}
	var r struct {
		Items []struct {
			ID             string `json:"id"`
			ContentDetails struct {
				Duration string `json:"duration"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	q := url.Values{"part": {"contentDetails"}, "id": {strings.Join(ids, ",")}}
	if err := c.llamar(ctx, http.MethodGet, "/videos", q, nil, &r); err != nil {
		return nil, err
	}
	for _, it := range r.Items {
		if s, ok := segundosISO8601(it.ContentDetails.Duration); ok {
			out[it.ID] = s
		}
	}
	return out, nil
}

var duracionISO = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`)

// segundosISO8601 convierte "PT3M33S" en 213.
func segundosISO8601(d string) (int, bool) {
	m := duracionISO.FindStringSubmatch(d)
	if m == nil || d == "PT" {
		return 0, false
	}
	total := 0
	for i, mult := range []int{3600, 60, 1} {
		if m[i+1] != "" {
			n, _ := strconv.Atoi(m[i+1])
			total += n * mult
		}
	}
	return total, true
}
