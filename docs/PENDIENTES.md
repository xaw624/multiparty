# Lo que falta hacer (y quién)

Estado al 2026-11-11: F0–F4 construidas y probadas con YouTube y Spotify **simulados**. Lo que sigue necesita tus cuentas, tu servidor o tu decisión.

## 1. Probar en tu máquina con cuentas reales

- [ ] **Go en el PATH.** Agrega `export PATH="$HOME/.local/go/bin:$PATH"` a tu `~/.bashrc`.
- [ ] **Secretos locales.** Copia `.env.example` a `.env` y genera dos claves distintas con `openssl rand -base64 32`:
  - `MULTIPARTY_SECRET` (firma las sesiones)
  - `MULTIPARTY_CLAVE_TOKENS` (cifra los tokens de YouTube y Spotify)
- [ ] **Google Cloud** ([GOOGLE.md](GOOGLE.md)), unos 15 min:
  - [ ] Crear el proyecto y habilitar **YouTube Data API v3**.
  - [ ] Pantalla de consentimiento: externo, scope `youtube`, y **pasarla a "En producción"** (si queda en "Testing", los permisos caducan cada 7 días).
  - [ ] Cliente OAuth **Aplicación web** con URI `http://127.0.0.1:8080/cuentas/youtube/callback`.
  - [ ] Copiar `GOOGLE_CLIENT_ID` y `GOOGLE_CLIENT_SECRET` al `.env`.
- [ ] **Spotify** ([SPOTIFY.md](SPOTIFY.md)), unos 10 min. **Lo debe hacer una cuenta Premium** (por ejemplo, la de tu amigo):
  - [ ] Crear la app en developer.spotify.com con URI `http://127.0.0.1:8080/cuentas/spotify/callback` y marcar **Web API**.
  - [ ] En **User Management**, agregar el correo de Spotify de cada persona (máximo 5).
  - [ ] Copiar `SPOTIFY_CLIENT_ID` y `SPOTIFY_CLIENT_SECRET` al `.env`.
- [ ] **Arrancar y probar**:
  ```bash
  set -a; . ./.env; set +a; make run
  ```
  Abre **`http://127.0.0.1:8080`** (no `localhost`) y prueba en este orden:
  - [ ] Crear un grupo y pegar links de Spotify, YouTube y Apple Music.
  - [ ] **Conectar YouTube** → crear la playlist → revisar que aparezca en YouTube Music.
  - [ ] «Ya la escuché» → la canción sale de la playlist y entra la siguiente.
  - [ ] Importar de una playlist de YouTube.
  - [ ] **Conectar Spotify** → crear la playlist → escuchar una canción de la ventana en Spotify → en 3–5 minutos debe avanzar sola.
  - [ ] Si alguna canción queda «No está en…», resolverla en «canciones por revisar».

> Avísame qué falla: lo único que no se pudo probar sin tus credenciales son las respuestas reales de Google y Spotify.

## 2. Publicarlo para tu amigo ([DESPLIEGUE.md](DESPLIEGUE.md))

Tu amigo no puede usar tu `127.0.0.1`; para que entre necesitas el servidor.

- [ ] Elegir el subdominio (por ejemplo `multiparty.tu-dominio.com`) y apuntarlo a la IP de la VM de Oracle.
- [ ] Abrir los puertos 80 y 443 (Security List de la VCN **y** firewall de la VM).
- [ ] `make build-arm64` (Ampere) o `make build-amd64` (E2.1.Micro) y copiar el binario.
- [ ] Instalar el servicio y el timer de respaldos, y llenar `/etc/multiparty/env` con **claves nuevas** para producción.
- [ ] Configurar Caddy o tu nginx actual.
- [ ] Agregar los URI de producción en Google Cloud y en Spotify:
  - `https://<tu-dominio>/cuentas/youtube/callback`
  - `https://<tu-dominio>/cuentas/spotify/callback`
- [ ] Comprobar que `curl https://<tu-dominio>/healthz` responde `ok`.
- [ ] Mandarle a tu amigo el link de invitación del grupo.

## 3. Decisiones tuyas

- [ ] **Subir a GitHub** los commits locales (`git push`); por ahora solo están en tu máquina.
- [ ] **Fin del pool:** cuando alguien escucha todo, ¿se vuelve a empezar, se revuelve o se detiene? Hoy se detiene y muestra «No tienes canciones por escuchar».
- [ ] **Importadas al final de la fila:** está implementado así; confirma que te parece bien.
- [ ] **Licencia del repo:** hoy no tiene ninguna, así que todos los derechos quedan reservados.
- [ ] **Nombre y dominio** definitivos.

## 4. Para tener en cuenta

| Tema | Detalle |
|---|---|
| Aviso de Google | Sin verificar, Google muestra «app no verificada»: se continúa con **Avanzado → Ir a Multiparty**. Es normal para uso privado. |
| Límite de Spotify | Máximo 5 personas en modo desarrollo, agregadas a mano. |
| Cuota de YouTube | 10 000 unidades al día para todo el grupo: unas 50 canciones nuevas al día. Si se agota, «Sincronizar» continúa al día siguiente. |
| Links pegados de Spotify | Sus datos salen de la página pública de Spotify; si Spotify la cambia, pegar links de Spotify puede fallar. Importar desde Spotify usa la API y no se ve afectado. |
| Respaldos | Se guardan en la misma VM; baja copias de vez en cuando (ver DESPLIEGUE.md). |
| Clave de tokens | Si pierdes `MULTIPARTY_CLAVE_TOKENS`, todos tendrán que reconectar sus cuentas. |
