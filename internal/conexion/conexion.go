// Paquete conexion arma clientes autenticados de YouTube y Spotify a partir
// de los tokens OAuth guardados (cifrados) de cada usuario, y guarda los
// tokens renovados. Lo usan la web y las tareas de fondo.
package conexion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/xaw624/multiparty/internal/cifrado"
	"github.com/xaw624/multiparty/internal/pool"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/youtube"
)

// ErrNoConectado: el usuario no conectó la plataforma, o hay que reconectar.
var ErrNoConectado = errors.New("cuenta no conectada")

// Plataformas con OAuth.
const (
	YouTube = "youtube"
	Spotify = "spotify"
)

// Nombre es el nombre visible de cada plataforma.
var Nombre = map[string]string{YouTube: "YouTube", Spotify: "Spotify"}

// Opciones configura las conexiones. Una plataforma sin OAuth queda deshabilitada.
type Opciones struct {
	Google  *oauth2.Config
	Spotify *oauth2.Config
	Caja    *cifrado.Caja
	// Bases reemplazan las URL de las API (pruebas), por plataforma.
	Bases map[string]string
}

// Conexiones guarda la configuración OAuth de cada plataforma.
type Conexiones struct {
	store *pool.Store
	caja  *cifrado.Caja
	confs map[string]*oauth2.Config
	bases map[string]string
	log   *slog.Logger
}

// New crea las conexiones. Si hay alguna plataforma, hace falta una Caja.
func New(store *pool.Store, log *slog.Logger, op Opciones) (*Conexiones, error) {
	c := &Conexiones{store: store, caja: op.Caja, confs: map[string]*oauth2.Config{}, bases: op.Bases, log: log}
	if op.Google != nil {
		c.confs[YouTube] = op.Google
	}
	if op.Spotify != nil {
		c.confs[Spotify] = op.Spotify
	}
	if len(c.confs) > 0 && c.caja == nil {
		return nil, errors.New("hace falta una Caja para cifrar los tokens OAuth")
	}
	if c.bases == nil {
		c.bases = map[string]string{}
	}
	return c, nil
}

// Habilitada dice si la plataforma está configurada.
func (c *Conexiones) Habilitada(p string) bool { return c != nil && c.confs[p] != nil }

// Alguna dice si hay al menos una plataforma configurada.
func (c *Conexiones) Alguna() bool { return c != nil && len(c.confs) > 0 }

// OAuth devuelve la configuración OAuth de la plataforma (nil si no hay).
func (c *Conexiones) OAuth(p string) *oauth2.Config { return c.confs[p] }

// Caja cifra y descifra los tokens.
func (c *Conexiones) Caja() *cifrado.Caja { return c.caja }

func httpCliente(ctx context.Context, ts oauth2.TokenSource) *http.Client {
	hc := oauth2.NewClient(ctx, ts)
	hc.Timeout = 20 * time.Second
	return hc
}

// YouTubeConToken arma un cliente con un token recién obtenido.
func (c *Conexiones) YouTubeConToken(ctx context.Context, tok *oauth2.Token) *youtube.Client {
	return c.youtube(httpCliente(ctx, c.confs[YouTube].TokenSource(ctx, tok)))
}

// SpotifyConToken arma un cliente con un token recién obtenido.
func (c *Conexiones) SpotifyConToken(ctx context.Context, tok *oauth2.Token) *spotify.Client {
	return c.spotify(httpCliente(ctx, c.confs[Spotify].TokenSource(ctx, tok)))
}

func (c *Conexiones) youtube(hc *http.Client) *youtube.Client {
	if b := c.bases[YouTube]; b != "" {
		return youtube.NewConBase(hc, b)
	}
	return youtube.New(hc)
}

func (c *Conexiones) spotify(hc *http.Client) *spotify.Client {
	if b := c.bases[Spotify]; b != "" {
		return spotify.NewConBase(hc, b)
	}
	return spotify.New(hc)
}

// YouTube arma un cliente con los tokens guardados del usuario. Hay que
// llamar a la función devuelta al terminar: guarda el token si se renovó.
func (c *Conexiones) YouTube(ctx context.Context, uid int64) (*youtube.Client, func(), error) {
	hc, guardar, err := c.autenticado(ctx, uid, YouTube)
	if err != nil {
		return nil, nil, err
	}
	return c.youtube(hc), guardar, nil
}

// Spotify es como YouTube, para Spotify.
func (c *Conexiones) Spotify(ctx context.Context, uid int64) (*spotify.Client, func(), error) {
	hc, guardar, err := c.autenticado(ctx, uid, Spotify)
	if err != nil {
		return nil, nil, err
	}
	return c.spotify(hc), guardar, nil
}

func (c *Conexiones) autenticado(ctx context.Context, uid int64, p string) (*http.Client, func(), error) {
	conf := c.confs[p]
	if conf == nil {
		return nil, nil, ErrNoConectado
	}
	cuenta, err := c.store.CuentaDe(ctx, uid, p)
	if errors.Is(err, pool.ErrNoEncontrado) || (err == nil && len(cuenta.RefreshCifrado) == 0) {
		return nil, nil, ErrNoConectado
	}
	if err != nil {
		return nil, nil, err
	}
	refresh, err := c.caja.Abrir(cuenta.RefreshCifrado)
	if err != nil {
		return nil, nil, fmt.Errorf("descifrar refresh token: %w", err)
	}
	tok := &oauth2.Token{RefreshToken: string(refresh), Expiry: cuenta.Expira, TokenType: "Bearer"}
	if access, err := c.caja.Abrir(cuenta.AccessCifrado); err == nil {
		tok.AccessToken = string(access)
	}
	// Sin cancelación: el token puede renovarse después de que termine ctx.
	base := context.WithoutCancel(ctx)
	ts := conf.TokenSource(base, tok)
	guardar := func() {
		nuevo, err := ts.Token()
		if err != nil || nuevo.AccessToken == tok.AccessToken {
			return
		}
		var refreshNuevo []byte
		if nuevo.RefreshToken != "" && nuevo.RefreshToken != tok.RefreshToken {
			refreshNuevo = c.caja.Sellar([]byte(nuevo.RefreshToken)) // Spotify puede rotarlo
		}
		if err := c.store.ActualizarTokens(base, uid, p,
			c.caja.Sellar([]byte(nuevo.AccessToken)), refreshNuevo, nuevo.Expiry); err != nil {
			c.log.Warn("guardar token renovado", "plataforma", p, "err", err)
		}
	}
	return httpCliente(ctx, ts), guardar, nil
}
