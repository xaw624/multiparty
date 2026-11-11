# Multiparty

Playlist compartida entre plataformas. Un grupo de amigos aporta canciones a un pool común, y cada persona tiene una **ventana de 10 canciones** en su propia app (Spotify, YouTube Music o cualquier otra) que avanza a medida que escucha.

- Sin suscripciones extra: cada quien usa la app que ya tiene.
- Funciona con cualquier plataforma: playlist automática donde hay API y links de song.link donde no la hay.
- Orden intercalado: una canción de cada quien, por turnos.

> **Estado:** esqueleto listo (fase F0). Lee el [plan del proyecto](docs/PLAN.md).

## Desarrollo

Requisitos: Go 1.26 o superior. No necesita CGO: SQLite está escrito en Go puro.

```bash
cp .env.example .env   # opcional; ya hay valores por defecto
make run               # http://localhost:8080
make test
make build-arm64       # binario para el servidor de Oracle (ARM64)
```

Stack: Go (`net/http`, `html/template`), HTMX y SQLite (`modernc.org/sqlite`). Las migraciones están en `internal/db/migrations` y se aplican al arrancar.
