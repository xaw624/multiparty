# Multiparty — Plan del proyecto

> Estado: **borrador para revisión** · Última actualización: 2026-11-11

## 1. Objetivo

Que un grupo de amigos comparta música **sin importar qué plataforma usa cada quien** y **sin pagar ninguna suscripción extra**. Todos aportan canciones a un pool común y cada persona las escucha en su propia app.

Principios:

1. **Cero costo para los usuarios.** Cada quien usa la app de música que ya tiene, con plan gratuito o de pago.
2. **Cualquier plataforma.** Si no hay API, la experiencia baja de nivel pero nadie queda fuera.
3. **La base de datos propia es la fuente de verdad.** Las playlists en Spotify, YouTube Music, etc. son solo espejos.
4. **Monetización solo con anuncios pequeños** (banner inferior). Ver las restricciones en la sección 7.

## 2. Conceptos

| Concepto | Qué es |
|---|---|
| **Grupo** | Conjunto de amigos que comparten un pool. Se entra con un link o código de invitación. |
| **Pool** | Todas las canciones aportadas por el grupo. |
| **Fila personal** | Las canciones pendientes que aportó cada persona, en orden. |
| **Orden común** | El orden oficial del pool, armado intercalando las filas (una de cada quien). |
| **Ventana** | Las **10 canciones** que cada persona tiene disponibles en este momento. Hay **una ventana por persona**, cada una con su propio cursor sobre el orden común. |
| **Liberar** | Marcar una canción como escuchada: sale de la ventana y entra la siguiente. |
| **Plataforma de escucha** | La plataforma donde vive la ventana de cada persona. Es una sola, aunque haya conectado varias para aportar. |

```
Filas:   A: A1 A2 A3 …     B: B1 B2 …     C: C1 …
Orden:   A1 B1 C1 A2 B2 A3 …            (turnos; quien no tiene pendientes se salta)

Ventana de A:  [A1 … ]  cursor 0   →  libera A1  →  [B1 … ]  cursor 1
Ventana de B:  [A1 … ]  cursor 0       (cada quien avanza a su ritmo)
```

## 3. Reglas del orden

1. El orden común se arma por **turnos rotativos** entre los miembros que tienen canciones pendientes.
2. Una **canción suelta** (pegar link o buscar) pasa al **frente de la fila** de quien la agrega, así que sale en su **siguiente turno**.
3. *(Propuesta, por confirmar)* Las canciones **importadas** desde una playlist van al **final** de la fila de quien las importa. Así, importar 30 canciones no entierra las que la persona ya tenía pendientes.
4. Las canciones nuevas solo se insertan **después de la ventana más avanzada**. Lo que ya está dentro de alguna ventana ya se escribió en una plataforma y no se mueve.
5. **Fin del pool:** *(pendiente)* volver a empezar, reordenar o detenerse.
6. **Borrar:** solo quien aportó la canción, y solo si no ha entrado en ninguna ventana.
7. **Sin duplicados:** una canción no se puede aportar dos veces al mismo grupo.
8. **Escuchar fuera de orden:** se puede marcar cualquier canción de la propia ventana; el cursor solo avanza cuando no quedan huecos antes.

### Implementación en F1

- **Sin login:** cada persona escribe su nombre y queda identificada por una cookie firmada en su navegador. El login con Spotify o Google llega en F2/F3.
- **Grupos privados:** se entra con un link de invitación que contiene un código aleatorio.
- **Plataforma de los links:** cada miembro elige en qué plataforma se abren sus links. Si la canción no tiene link en esa plataforma, se usa la página de song.link.

## 4. Niveles por plataforma

| Nivel | Plataformas | Ventana | Liberar | Aportar |
|---|---|---|---|---|
| **1. Automático** | Spotify | Playlist en su app que se actualiza sola | Automático: lee lo que escuchó recientemente | Buscar, pegar un link, importar |
| **2. Espejo con botón** | YouTube Music | Playlist en su app | Botón en la web (la API no da acceso al historial) | Pegar un link, importar |
| **3. Solo links** | Amazon Music, Deezer, Tidal, SoundCloud, Apple Music y cualquier otra | Lista de 10 en la web; cada canción es un link que abre su app | Botón en la web | Pegar un link de cualquier plataforma |

- El nivel 3 funciona **sin cuentas ni claves**: solo con fuentes públicas y la página de song.link. Por eso es la base del MVP.
- **Links aceptados en F1:** Spotify, YouTube, YouTube Music, Apple Music y Deezer. Para abrir, cualquier plataforma (vía song.link).
- **Apple Music** podría subir al nivel 2, pero requiere una cuenta Apple Developer (99 USD al año), que pagaría quien desarrolla la app, no los usuarios.
- **YouTube:** la política exige que las escrituras en la cuenta del usuario sean "claramente iniciadas por el usuario" (III.C.2) y que el usuario consienta antes de ejecutarlas (III.E.3.4). Por eso, en YouTube la ventana avanza **con el botón** y no con un temporizador.

## 5. Emparejamiento entre plataformas

Para identificar una canción se intenta, en este orden:

1. **ISRC.** Spotify lo entrega en `external_ids`. Se anunció su eliminación en febrero de 2026, pero se revirtió en marzo.
2. **Resolvedor propio** (reemplaza a la API de Odesli, retirada en julio de 2026):
   - Datos del link pegado: página pública de Spotify (etiquetas `og:`), oEmbed de YouTube, iTunes o Deezer.
   - Busca la misma canción en **Deezer** (que da el ISRC) y en **Apple Music** por artista, título y duración (±5 s).
   - Siempre agrega la página de **song.link**, que abre la canción en cualquier plataforma.
   - Si falla una fuente secundaria, la canción se agrega igual con menos links.
3. **Búsqueda.**
   - Spotify: `isrc:XXXX`, o artista y título.
   - YouTube: preferir canales "Artista - Topic" y comparar la duración (±3 s).
4. **Revisión manual** si la coincidencia no es confiable.

**Resolución perezosa:** una canción solo se empareja con la plataforma de alguien cuando le faltan unas **5 posiciones** para entrar en su ventana. Esto reparte la cuota y da tiempo para la revisión manual.

## 6. Restricciones de las APIs (verificadas en noviembre de 2026)

### Spotify (modo desarrollo)
- **Máximo 5 usuarios** autenticados por app. Cada uno se agrega a mano a la lista de permitidos.
- El **dueño de la app debe tener Premium**. Los usuarios no lo necesitan.
- La búsqueda devuelve como máximo 10 resultados. Se eliminaron endpoints por lote como `GET /tracks`.
- El **modo extendido solo se concede a organizaciones registradas con 250 000 usuarios activos al mes o más**. Para un desarrollador individual no está disponible.
- El modo desarrollo está pensado para proyectos personales **no comerciales**.

### YouTube Data API v3
- Cuota: **10 000 unidades al día** por proyecto.

| Operación | Unidades |
|---|---|
| `playlistItems.list`, `videos.list` | 1 |
| `playlistItems.insert`, `update`, `delete`, `playlists.insert` | 50 |
| `search.list` | 100 (solo para canciones aportadas desde otra plataforma) |

- Cada rotación de ventana cuesta 1 borrado + 1 inserción = **100 unidades**, así que caben unas **100 rotaciones al día** en total.
- **OAuth:** en modo "Testing" los refresh tokens caducan a los **7 días**. Hay que publicar la app en "Production". Sin verificar, el límite es de 100 usuarios con pantalla de advertencia; para un uso público hay que verificar el scope sensible `youtube`, lo cual es gratuito.
- **Datos:** los metadatos obtenidos de YouTube se refrescan o borran cada **30 días**. Si un usuario revoca el acceso, sus datos se borran en **7 días** o menos.

### Odesli / song.link
- ⚠️ **La API pública sin clave se retiró el 31 de julio de 2026**: ahora responde `401 PUBLIC_API_ACCESS_DEPRECATED` (comprobado el 2026-11-11). No está claro si todavía se conceden claves.
- La **página** de song.link sigue funcionando sin API: `https://song.link/<link original>` redirige a la página de la canción. Sirve como link universal del nivel 3.

### Fuentes gratuitas sin clave (comprobadas el 2026-11-11)

| Fuente | Qué devuelve |
|---|---|
| oEmbed de YouTube | Título del video y canal |
| oEmbed de Spotify | Título de la canción y portada (sin artista) |
| API de búsqueda/lookup de iTunes | Título, artista, duración, portada, link de Apple Music |
| API pública de Deezer | Título, artista, duración, **ISRC**; búsqueda por texto y por ISRC |

## 7. Monetización y su conflicto con Spotify

Plan: **solo banners pequeños en la parte inferior**, por ejemplo con Google AdSense.

| Fuente | ¿Se permiten anuncios? |
|---|---|
| Política de Spotify | Sí para apps que **no reproducen** música, como los gestores de playlists, que es nuestro caso. **Pero** el modo desarrollo es no comercial y está limitado a 5 usuarios, y el modo extendido no está disponible para individuos. |
| Política de YouTube | Sí, siempre que la página tenga contenido propio con valor independiente de los datos de YouTube (III.G.1.d). Además, hay que avisar que la app muestra anuncios de terceros (III.A.2.f). |

**Consecuencia:** el nivel 1 de Spotify y los anuncios no pueden convivir en una versión pública. Hay dos modos posibles:

| Modo | Spotify | YouTube Music | Resto | Anuncios |
|---|---|---|---|---|
| **Grupo privado** (como máximo 5 usuarios de Spotify) | Nivel 1 | Nivel 2 | Nivel 3 | **No** |
| **Público** | **Nivel 3** (solo links) | Nivel 2 | Nivel 3 | Sí |

**Decisión (2026-11-11): se empieza como grupo privado, sin anuncios.** Primero se valida que el concepto se usa; si crece, se lanza la versión pública con Spotify en nivel 3 (fase F5).

Requisitos para mostrar anuncios: política de privacidad, aviso de consentimiento de cookies (obligatorio para usuarios de la UE y el Reino Unido), un dominio propio y suficiente contenido para que AdSense apruebe el sitio.

## 8. Arquitectura

```
 Navegador (web/PWA)                      Servidor (Oracle Cloud Free Tier)
┌──────────────────────┐   HTTPS   ┌─────────────────────────────────────────┐
│ Pool · Mi ventana    │◄────────►│  App web (rutas + plantillas)             │
│ Pegar link · Importar│           │  ├─ Servicio de orden (turnos, cursores) │
│ Botón "ya la escuché"│           │  ├─ Resolvedor (Spotify·YT·iTunes·Deezer)│
│ (banner solo en F5)  │           │  └─ Adaptadores: spotify · youtube · links│
└──────────────────────┘           │  Tareas en segundo plano                  │
                                   │  ├─ Detectar escuchas (Spotify, ~5 min)  │
                                   │  ├─ Resolver los próximos 5 de cada cola │
                                   │  ├─ Renovar tokens                       │
                                   │  └─ Refrescar datos de YouTube (30 días) │
                                   │  SQLite (archivo, con respaldo diario)   │
                                   └─────────────────────────────────────────┘
```

Cada plataforma es un **adaptador** con la misma interfaz: `resolver(cancion)`, `crear_playlist()`, `agregar(item)`, `quitar(item)` y `escuchadas_recientes()`, esta última opcional. Agregar Apple Music u otra plataforma después solo requiere un adaptador nuevo.

## 9. Modelo de datos (borrador)

```
grupos          (id, nombre, codigo_invitacion, creado_en)
usuarios        (id, nombre, creado_en)
miembros        (grupo_id, usuario_id, plataforma_escucha, cursor, orden_turno)
cuentas         (id, usuario_id, plataforma, id_externo, access_token_cifrado,
                 refresh_token_cifrado, expira_en, scopes)
canciones       (id, isrc NULL UNIQUE, titulo, artista, duracion_ms, portada_url)
mapeos          (cancion_id, plataforma, track_id, url,
                 confianza: directo|isrc|busqueda|manual, verificado_en)
                 PK (cancion_id, plataforma)
aportes         (id, grupo_id, cancion_id, usuario_id, origen: suelta|importada,
                 estado: pendiente|ordenado|eliminado, agregado_en)
orden           (grupo_id, posicion, aporte_id)        -- orden común materializado
espejos         (miembro_id, plataforma, playlist_id)
ventana_items   (miembro_id, posicion, item_id_plataforma, estado: activa|escuchada)
revision        (cancion_id, plataforma, candidatos_json, estado, resuelto_por)
```

## 10. Stack

### Elegido (2026-11-11): Go + HTMX

| Capa | Elección | Por qué |
|---|---|---|
| Lenguaje | **Go 1.26+** | Un solo binario, muy poca memoria; compila para ARM (Oracle Ampere) sin esfuerzo |
| HTTP | **`net/http`** de la biblioteca estándar | Desde Go 1.22 tiene rutas con método y parámetros; no hace falta framework |
| Plantillas | **`html/template` + `embed`** | Sin paso de compilación y con escapado automático. Se puede migrar a `templ` si las vistas crecen |
| Interactividad | **HTMX 2** + CSS propio | Sin compilar el frontend; rápido en móvil |
| Base de datos | **SQLite** con `modernc.org/sqlite` | Go puro, sin CGO: compilación cruzada trivial |
| Migraciones | Archivos SQL embebidos con un ejecutor propio | Cero dependencias; se puede cambiar a `goose` si hace falta |
| Consultas | `database/sql` directo | Se puede adoptar `sqlc` si las consultas crecen |
| OAuth | **`golang.org/x/oauth2`** | Ya trae los endpoints de Spotify y Google |
| Clientes de APIs | `net/http` propio, delgado | Las APIs de Spotify cambiaron en 2026; un cliente propio se rompe menos |
| Tareas | Goroutines con `time.Ticker` | No requiere colas ni procesos externos |
| Cifrado de tokens | AES-GCM (`crypto/aes`), con la clave en una variable de entorno | |
| Configuración | Variables de entorno | |
| Pruebas | `go test` + `httptest` | |
| Despliegue | Binario + systemd + **Caddy** (HTTPS automático) en Oracle Cloud Free Tier | Costo cero |
| Móvil | **PWA** instalable | Sin tiendas de apps ni costos de publicación |

### Alternativas descartadas

| Stack | Por qué no |
|---|---|
| Python + FastAPI + HTMX | Más fácil al inicio, pero más memoria y más dependencias que mantener |
| TypeScript: SvelteKit o Next.js | Interfaz más rica, pero más piezas y más consumo de memoria de lo que el proyecto necesita |

## 11. Fases

| Fase | Entregable | APIs necesarias |
|---|---|---|
| **F0** ✅ | Repo, plan y esqueleto del proyecto | — |
| **F1. Núcleo universal** ✅ | Grupos con invitación, pegar link (resolvedor propio), orden intercalado, ventana de 10 por persona en la web con botón "ya la escuché" (nivel 3 para **todas** las plataformas) | Fuentes públicas sin clave |
| **F2. YouTube Music** ✅ | OAuth de Google, playlist espejo, rotación con botón, importar desde playlists (guía: [GOOGLE.md](GOOGLE.md)) | YouTube Data API |
| **F3. Spotify** ✅ | OAuth, playlist espejo, detección automática de escuchas, búsqueda e importación (guía: [SPOTIFY.md](SPOTIFY.md)) | Spotify Web API |
| **F4. Revisión manual** ✅ | Pantalla para elegir entre candidatos, pegar el link correcto o volver a buscar | — |
| **F5. Versión pública** *(si se decide)* | Anuncios, política de privacidad, consentimiento, verificación de Google, Spotify en nivel 3 | — |
| Después | Apple Music (nivel 2), reproductor web | MusicKit |

La F1 ya sirve por sí sola: funciona con cualquier plataforma sin conectar ninguna cuenta.

### Implementación en F2

- **Login con Google** (OAuth con PKCE, acceso offline). Los tokens se guardan cifrados con AES-256-GCM (`MULTIPARTY_CLAVE_TOKENS`).
- **Playlist espejo:** una playlist privada `Multiparty · <grupo>` por persona. Se sincroniza **solo por acción del usuario** (crearla, «Ya la escuché», «Sincronizar»), como pide la política de YouTube.
- **Emparejamiento con YouTube** al sincronizar: búsqueda en la categoría Música, se prefieren canales «Artista - Topic» (pistas de YouTube Music) y se compara la duración (±5 s en pistas Topic, ±20 s en videos). Si no hay coincidencia, la canción queda en `revision` y no se vuelve a buscar.
- **Cuota agotada:** la sincronización se detiene y continúa donde quedó la siguiente vez.
- **Importar:** hasta 50 canciones por vez desde las playlists del usuario; entran al final de su fila. La lista «Me gusta» de YouTube Music no está disponible en la API.
- **Desconectar:** revoca el permiso en Google y borra los tokens; la playlist queda en la cuenta del usuario.

### Implementación en F3 y F4

- **Spotify:** login con PKCE; la playlist espejo se **reemplaza completa** con la ventana en una sola llamada. Emparejamiento por ISRC y, si no, por título, artista y duración (±5 s).
- **Detección de escuchas** (cada 3 min): lo escuchado en Spotify que esté en la ventana se marca solo y la playlist se actualiza. La primera vez solo se fija el punto de partida.
- **Tareas de fondo:** pre-emparejar la ventana y las 5 siguientes (máximo 3 búsquedas de YouTube y 10 de Spotify cada 15 min) y verificar cada 30 días los ids de YouTube (se borran los que ya no existen).
- **Revisión manual:** elegir entre los candidatos guardados, pegar el link correcto (queda con confianza `manual`) o volver a buscar.

## 12. Decisiones pendientes

- [x] ¿Grupo privado o versión pública? → **Grupo privado, sin anuncios.**
- [x] Stack → **Go + HTMX + SQLite.**
- [ ] ¿Las canciones importadas van al final de la fila? (propuesta de la sección 3)
- [ ] ¿Qué pasa al llegar al final del pool?
- [ ] Nombre definitivo y dominio.
- [ ] Licencia del repositorio. Por ahora no tiene ninguna, así que todos los derechos quedan reservados.

## 13. Riesgos

| Riesgo | Mitigación |
|---|---|
| Spotify o YouTube cambian sus condiciones (ya pasó en 2024 y 2026) | Adaptadores aislados; el nivel 3 siempre funciona como respaldo |
| Emparejamientos erróneos en YouTube (versiones en vivo, remixes) | Canales "Topic", comparación de duración y revisión manual |
| Cuota de YouTube | Rotaciones de 100 unidades; resolución perezosa; las canciones aportadas desde YouTube ya traen su id |
| Un servicio externo desaparece (ya pasó con la API de Odesli en julio de 2026) | Resolvedor propio con varias fuentes; la página de song.link como respaldo universal |
| Sin Odesli, emparejar con YouTube cuesta una búsqueda (100 unidades) | Unas 50 canciones nuevas al día en YouTube (búsqueda + rotación); suficiente para un grupo de amigos |
| Google rechaza la verificación o AdSense no aprueba el sitio | Empezar como grupo privado; agregar contenido propio (estadísticas del grupo, historial) |
