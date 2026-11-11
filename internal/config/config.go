// Paquete config lee la configuración desde variables de entorno.
package config

import "os"

// Config reúne la configuración del servidor.
type Config struct {
	// Addr es la dirección donde escucha el servidor HTTP.
	Addr string
	// DatabasePath es la ruta del archivo SQLite.
	DatabasePath string
}

// Load lee la configuración del entorno y aplica valores por defecto.
func Load() Config {
	return Config{
		Addr:         env("MULTIPARTY_ADDR", ":8080"),
		DatabasePath: env("MULTIPARTY_DB", "data/multiparty.db"),
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
