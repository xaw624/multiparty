// Paquete config lee la configuración desde variables de entorno.
package config

import (
	"crypto/rand"
	"os"
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
}

// Load lee la configuración del entorno y aplica valores por defecto.
func Load() Config {
	c := Config{
		Addr:         env("MULTIPARTY_ADDR", ":8080"),
		DatabasePath: env("MULTIPARTY_DB", "data/multiparty.db"),
		Secreto:      []byte(env("MULTIPARTY_SECRET", "")),
		Pais:         env("MULTIPARTY_PAIS", "MX"),
	}
	if len(c.Secreto) == 0 {
		c.Secreto = make([]byte, 32)
		rand.Read(c.Secreto)
		c.SecretoGenerado = true
	}
	return c
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
