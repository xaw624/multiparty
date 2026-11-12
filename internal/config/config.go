// Paquete config lee la configuración desde variables de entorno.
package config

import (
	"crypto/rand"
	"errors"
	"os"
	"strings"
)

// Config reúne la configuración del servidor.
type Config struct {
	// Addr es la dirección donde escucha el servidor HTTP.
	Addr string
	// DatabasePath es la ruta del archivo SQLite.
	DatabasePath string
	// Secreto firma las cookies de sesión.
	Secreto []byte
	// SecretoGenerado indica que no se configuró MULTIPARTY_SECRET y se usa
	// uno aleatorio: las sesiones se pierden al reiniciar.
	SecretoGenerado bool
	// Pais es el país (ISO 3166-1) de la tienda de Apple Music para buscar.
	Pais string

	// BaseURL es la URL pública del sitio (para los redirect de OAuth).
	BaseURL string
	// GoogleClientID y GoogleClientSecret habilitan YouTube Music (F2).
	GoogleClientID     string
	GoogleClientSecret string
	// ClaveTokens (base64, 32 bytes) cifra los tokens OAuth guardados.
	ClaveTokens string
}

// GoogleHabilitado dice si se configuró el acceso a YouTube.
func (c Config) GoogleHabilitado() bool { return c.GoogleClientID != "" }

// Load lee la configuración del entorno y aplica valores por defecto.
func Load() (Config, error) {
	c := Config{
		Addr:               env("MULTIPARTY_ADDR", ":8080"),
		DatabasePath:       env("MULTIPARTY_DB", "data/multiparty.db"),
		Secreto:            []byte(env("MULTIPARTY_SECRET", "")),
		Pais:               env("MULTIPARTY_PAIS", "MX"),
		BaseURL:            strings.TrimRight(env("MULTIPARTY_BASE_URL", "http://localhost:8080"), "/"),
		GoogleClientID:     env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
		ClaveTokens:        env("MULTIPARTY_CLAVE_TOKENS", ""),
	}
	if len(c.Secreto) == 0 {
		c.Secreto = make([]byte, 32)
		rand.Read(c.Secreto)
		c.SecretoGenerado = true
	}
	if c.GoogleHabilitado() {
		if c.GoogleClientSecret == "" {
			return c, errors.New("GOOGLE_CLIENT_ID está definido pero falta GOOGLE_CLIENT_SECRET")
		}
		if c.ClaveTokens == "" {
			return c, errors.New("para usar Google hay que definir MULTIPARTY_CLAVE_TOKENS (genera una con: openssl rand -base64 32)")
		}
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
