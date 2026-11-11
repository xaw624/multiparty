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

## 4. Niveles por plataforma

| Nivel | Plataformas | Ventana | Liberar | Aportar |
|---|---|---|---|---|
| **1. Automático** | Spotify | Playlist en su app que se actualiza sola | Automático: lee lo que escuchó recientemente | Buscar, pegar un link, importar |
| **2. Espejo con botón** | YouTube Music | Playlist en su app | Botón en la web (la API no da acceso al historial) | Pegar un link, importar |
| **3. Solo links** | Amazon Music, Deezer, Tidal, SoundCloud, Apple Music y cualquier otra | Lista de 10 en la web; cada canción es un link que abre su app | Botón en la web | Pegar un link de cualquier plataforma |

- El nivel 3 funciona **sin ninguna API de plataforma**: solo con Odesli/song.link. Por eso es la base del MVP.
- **Apple Music** podría subir al nivel 2, pero requiere una cuenta Apple Developer (99 USD al año), que pagaría quien desarrolla la app, no los usuarios.
- **YouTube:** la política exige que las escrituras en la cuenta del usuario sean "claramente iniciadas por el usuario" (III.C.2) y que el usuario consienta antes de ejecutarlas (III.E.3.4). Por eso, en YouTube la ventana avanza **con el botón** y no con un temporizador.

## 5. Emparejamiento entre plataformas

Para identificar una canción se intenta, en este orden:

1. **ISRC.** Spotify lo entrega en `external_ids`. Se anunció su eliminación en febrero de 2026, pero se revirtió en marzo.
2. **Odesli (song.link).** Convierte un link de cualquier plataforma en los de las demás **sin gastar cuota de YouTube**.
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
| `search.list` | 100 (se evita: se usa Odesli) |

- Cada rotación de ventana cuesta 1 borrado + 1 inserción = **100 unidades**, así que caben unas **100 rotaciones al día** en total.
- **OAuth:** en modo "Testing" los refresh tokens caducan a los **7 días**. Hay que publicar la app en "Production". Sin verificar, el límite es de 100 usuarios con pantalla de advertencia; para un uso público hay que verificar el scope sensible `youtube`, lo cual es gratuito.
- **Datos:** los metadatos obtenidos de YouTube se refrescan o borran cada **30 días**. Si un usuario revoca el acceso, sus datos se borran en **7 días** o menos.

### Odesli / song.link
- Sin clave: unas **10 peticiones por minuto**. Con clave el límite es mayor; se solicita por correo.
- Para **uso comercial** (con anuncios) hay que revisar sus términos y pedir una clave antes de lanzar.

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

**Decisión pendiente:** con qué modo se empieza. Recomendación: **empezar como grupo privado sin anuncios**, validar que el concepto se usa y, si crece, lanzar la versión pública con Spotify en nivel 3.

Requisitos para mostrar anuncios: política de privacidad, aviso de consentimiento de cookies (obligatorio para usuarios de la UE y el Reino Unido), un dominio propio y suficiente contenido para que AdSense apruebe el sitio.

## 8. Arquitectura

```
 Navegador (web/PWA)                      Servidor (Oracle Cloud Free Tier)
┌──────────────────────┐   HTTPS   ┌─────────────────────────────────────────┐
│ Pool · Mi ventana    │◄────────►│  App web (rutas + plantillas)             │
│ Pegar link · Importar│           │  ├─ Servicio de orden (turnos, cursores) │
│ Botón "ya la escuché"│           │  ├─ Emparejador (ISRC → Odesli → búsqueda)│
│ Banner inferior      │           │  └─ Adaptadores: spotify · youtube · links│
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
                 confianza: isrc|odesli|busqueda|manual, verificado_en)
                 PK (cancion_id, plataforma)
aportes         (id, grupo_id, cancion_id, usuario_id, origen: suelta|importada,
                 estado: pendiente|ordenado|eliminado, agregado_en)
orden           (grupo_id, posicion, aporte_id)        -- orden común materializado
espejos         (miembro_id, plataforma, playlist_id)
ventana_items   (miembro_id, posicion, item_id_plataforma, estado: activa|escuchada)
revision        (cancion_id, plataforma, candidatos_json, estado, resuelto_por)
```

## 10. Stack

### Recomendado: Python

| Capa | Elección | Por qué |
|---|---|---|
| Backend | **FastAPI** | Liviano y asíncrono, y coincide con el borrador original |
| Plantillas y UI | **Jinja2 + HTMX** + CSS simple (Pico.css) | Se renderiza en el servidor: sin compilar el frontend, es rápido en móvil y AdSense lo indexa bien |
| Base de datos | **SQLite** + SQLModel/SQLAlchemy + **Alembic** | Basta para miles de usuarios; respaldarla es copiar un archivo |
| OAuth | **Authlib** | Spotify y Google con el mismo código |
| Clientes HTTP | **httpx**, sin spotipy | Las APIs de Spotify cambiaron en 2026; un cliente propio y delgado se rompe menos |
| Tareas | **APScheduler** dentro del mismo proceso | No requiere Redis ni Celery para esta escala |
| Cifrado de tokens | `cryptography` (Fernet), con la clave en una variable de entorno | |
| Despliegue | **Docker Compose + Caddy** (HTTPS automático) en Oracle Cloud Free Tier | Costo cero |
| Pruebas | pytest + respx (para simular las APIs) | |
| Móvil | **PWA** instalable | Sin tiendas de apps ni costos de publicación |

### Alternativas

| Stack | A favor | En contra |
|---|---|---|
| **TypeScript: SvelteKit o Next.js + Drizzle + SQLite + Auth.js** | Un solo lenguaje; interfaz más rica; Auth.js ya trae los proveedores Spotify y Google | Más piezas que mantener; Node consume más memoria |
| **Go + templ + HTMX + SQLite** | Un solo binario; muy poca memoria | Menos librerías para OAuth y música; desarrollo más lento al principio |

Se recomienda **Python + FastAPI + HTMX**: es el camino más corto al MVP, el trabajo del worker (emparejar, reintentar, rotar) es natural en Python y el servidor gratuito alcanza de sobra.

## 11. Fases

| Fase | Entregable | APIs necesarias |
|---|---|---|
| **F0** | Repo, plan y esqueleto del proyecto | — |
| **F1. Núcleo universal** | Grupos con invitación, pegar link (Odesli), orden intercalado, ventana de 10 por persona en la web con botón "ya la escuché" (nivel 3 para **todas** las plataformas) | Solo Odesli |
| **F2. YouTube Music** | OAuth de Google, playlist espejo, rotación con botón, importar desde playlists | YouTube Data API |
| **F3. Spotify** | OAuth, playlist espejo, detección automática de escuchas, búsqueda e importación | Spotify Web API |
| **F4. Revisión manual** | Pantalla para elegir entre candidatos cuando la coincidencia no es confiable | — |
| **F5. Versión pública** *(si se decide)* | Anuncios, política de privacidad, consentimiento, verificación de Google, Spotify en nivel 3 | — |
| Después | Apple Music (nivel 2), reproductor web | MusicKit |

La F1 ya sirve por sí sola: funciona con cualquier plataforma sin conectar ninguna cuenta.

## 12. Decisiones pendientes

- [ ] ¿Se empieza como grupo privado (sin anuncios) o como versión pública?
- [ ] ¿Las canciones importadas van al final de la fila? (propuesta de la sección 3)
- [ ] ¿Qué pasa al llegar al final del pool?
- [ ] Nombre definitivo y dominio.
- [ ] Licencia del repositorio. Por ahora no tiene ninguna, así que todos los derechos quedan reservados.

## 13. Riesgos

| Riesgo | Mitigación |
|---|---|
| Spotify o YouTube cambian sus condiciones (ya pasó en 2024 y 2026) | Adaptadores aislados; el nivel 3 siempre funciona como respaldo |
| Emparejamientos erróneos en YouTube (versiones en vivo, remixes) | Canales "Topic", comparación de duración y revisión manual |
| Cuota de YouTube | Odesli en lugar de `search.list`; rotaciones de 100 unidades; resolución perezosa |
| Odesli limita las peticiones o cambia sus términos | Caché de mapeos; pedir una clave; búsqueda directa como respaldo |
| Google rechaza la verificación o AdSense no aprueba el sitio | Empezar como grupo privado; agregar contenido propio (estadísticas del grupo, historial) |
