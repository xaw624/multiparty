# Multiparty

Playlist compartida entre plataformas. Un grupo de amigos aporta canciones a un pool común, y cada persona tiene una **ventana de 10 canciones** en su propia app (Spotify, YouTube Music o cualquier otra) que avanza a medida que escucha.

- Sin suscripciones extra: cada quien usa la app que ya tiene.
- Funciona con cualquier plataforma: playlist automática donde hay API y links de song.link donde no la hay.
- Se aportan canciones pegando links de Spotify, YouTube, YouTube Music, Apple Music o Deezer. Los datos se obtienen de fuentes públicas sin clave (sin Odesli, cuya API se retiró en 2026).
- Orden intercalado: una canción de cada quien, por turnos.

> **Estado:** fase F1 lista: grupos, pegar links, turnos y ventana de 10 en la web. Lee el [plan del proyecto](docs/PLAN.md).

## Desarrollo

Requisitos: Go 1.26 o superior. No necesita CGO: SQLite está escrito en Go puro.

```bash
cp .env.example .env              # opcional; hay valores por defecto
set -a; . ./.env; set +a          # el servidor lee variables de entorno, no el archivo
make run                          # http://localhost:8080
make test
make build-arm64       # binario para el servidor de Oracle (ARM64)
```

Stack: Go (`net/http`, `html/template`), HTMX y SQLite (`modernc.org/sqlite`). Las migraciones están en `internal/db/migrations` y se aplican al arrancar.

En producción define `MULTIPARTY_SECRET` (ver `.env.example`); sin ella las sesiones se pierden en cada reinicio.

| Paquete | Qué hace |
|---|---|
| `internal/pool` | Dominio: grupos, aportes, orden por turnos, ventanas |
| `internal/resolver` | Convierte un link en datos de la canción y links a otras plataformas |
| `internal/web` | Rutas, sesiones, plantillas y estilos |
| `internal/db` | Conexión SQLite y migraciones |
