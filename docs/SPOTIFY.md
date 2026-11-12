# Configurar Spotify

Esto habilita la fase F3: conectar Spotify, tener tu ventana como playlist que **avanza sola** cuando escuchas, e importar canciones de tus playlists.

## Requisitos (reglas de Spotify desde 2026)

- La cuenta que crea la app en el panel de desarrolladores **debe tener Spotify Premium**. Los demás usuarios no lo necesitan.
- La app queda en **modo desarrollo**: **máximo 5 personas**, y cada una se agrega a mano en el panel. Para un grupo privado alcanza.
- El modo para más usuarios solo se concede a empresas con más de 250 000 usuarios activos al mes; no aplica a este proyecto.

## 1. Crear la app

1. Entra a <https://developer.spotify.com/dashboard> con la cuenta Premium.
2. **Create app**:
   - Nombre: `Multiparty`. Descripción: la que quieras.
   - **Redirect URIs** (Spotify ya **no acepta `localhost`**; usa la IP):
     - En tu máquina: `http://127.0.0.1:8080/cuentas/spotify/callback`
     - En el servidor: `https://tu-dominio/cuentas/spotify/callback`
   - APIs: marca **Web API**.
3. En **Settings** copia el **Client ID** y el **Client secret**.

## 2. Autorizar a cada persona

En la app, **User Management → Add new user**: nombre y el **correo de su cuenta de Spotify**. Hasta 5 personas, incluida la dueña de la app.

Si alguien no está en la lista, al conectar verá: «Tu cuenta de Spotify no está autorizada en la app».

## 3. Variables de entorno

```bash
SPOTIFY_CLIENT_ID=…
SPOTIFY_CLIENT_SECRET=…
# Debe coincidir con el Redirect URI (sin /cuentas/…). Nada de localhost.
MULTIPARTY_BASE_URL=http://127.0.0.1:8080
# La misma clave que para YouTube (si ya la tienes, no la cambies).
MULTIPARTY_CLAVE_TOKENS=$(openssl rand -base64 32)
```

En tu máquina, abre la app en `http://127.0.0.1:8080` (no en `localhost`): la sesión es por dirección y el regreso desde Spotify llega a `127.0.0.1`.

## Qué hace la app con tu cuenta

| Permiso | Para qué |
|---|---|
| `playlist-modify-private` / `playlist-modify-public` | Crear **una** playlist privada `Multiparty · <grupo>` y mantenerla igual a tu ventana |
| `playlist-read-private` | Listar tus playlists solo cuando abres «Importar» |
| `user-read-recently-played` | Ver qué escuchaste para marcarlo como escuchado y avanzar la ventana sola |

Cada pocos minutos la app revisa lo que escuchaste en Spotify. Si una canción de tu ventana aparece, la marca como escuchada y actualiza tu playlist. También puedes usar «Ya la escuché».

Al pulsar **Desconectar Spotify** se borran los tokens. Spotify no tiene API para revocar el permiso: para quitarlo por completo, ve a <https://www.spotify.com/account/apps/>.
