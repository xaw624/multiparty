// Paquete pool contiene el dominio: grupos, aportes, el orden común por
// turnos y la ventana de cada persona.
package pool

import (
	"errors"
	"fmt"
)

// TamVentana es cuántas canciones tiene disponibles cada persona a la vez.
const TamVentana = 10

// Origen indica cómo entró un aporte. Define su lugar en la fila personal:
// una canción suelta va al frente y una importada, al final.
type Origen string

const (
	Suelta    Origen = "suelta"
	Importada Origen = "importada"
)

// Confianza indica cómo se obtuvo un enlace.
type Confianza string

const (
	Directo  Confianza = "directo"  // es el link que se pegó (o se deriva de él)
	ISRC     Confianza = "isrc"     // misma grabación por ISRC
	Busqueda Confianza = "busqueda" // por título, artista y duración
	Manual   Confianza = "manual"   // confirmado por una persona
)

// Enlace es la ubicación de una canción en una plataforma.
type Enlace struct {
	Plataforma string // spotify, youtube, youtubeMusic, appleMusic, deezer o songlink
	TrackID    string // id de la canción en esa plataforma; vacío si no aplica
	URL        string
	Confianza  Confianza // vacío = Busqueda
}

// CancionResuelta es lo que devuelve un resolvedor de links.
type CancionResuelta struct {
	ISRC       string
	Titulo     string
	Artista    string
	PortadaURL string
	Enlaces    []Enlace
}

// Errores que pueden devolver los resolvedores de links.
var (
	ErrEnlaceInvalido = errors.New("el texto no es un link válido")
	ErrNoSoportada    = errors.New("plataforma no soportada")
	ErrNoEncontrada   = errors.New("no se encontró la canción")
	ErrNoEsCancion    = errors.New("el link no es de una canción")
	ErrLimite         = errors.New("límite de peticiones alcanzado")
)

// Errores del almacenamiento.
var (
	ErrNoEncontrado = errors.New("no encontrado")
	ErrNoPermitido  = errors.New("acción no permitida")
)

// ErrDuplicada indica que la canción ya está en el pool del grupo.
type ErrDuplicada struct{ Por string }

func (e ErrDuplicada) Error() string {
	return fmt.Sprintf("esa canción ya está en el pool (la agregó %s)", e.Por)
}

// Plataforma es una opción para abrir los links de la ventana.
type Plataforma struct{ Clave, Nombre string }

// PlataformasLinks son las plataformas que se pueden elegir para abrir los
// links. La clave vacía usa la página de song.link, que deja elegir al abrir.
var PlataformasLinks = []Plataforma{
	{"", "song.link (elegir al abrir)"},
	{"spotify", "Spotify"},
	{"youtubeMusic", "YouTube Music"},
	{"appleMusic", "Apple Music"},
	{"amazonMusic", "Amazon Music"},
	{"deezer", "Deezer"},
	{"tidal", "Tidal"},
	{"soundcloud", "SoundCloud"},
}

// PlataformaValida dice si clave es una de PlataformasLinks.
func PlataformaValida(clave string) bool {
	for _, p := range PlataformasLinks {
		if p.Clave == clave {
			return true
		}
	}
	return false
}
