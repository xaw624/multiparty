-- Esquema inicial (ver docs/PLAN.md, sección 9).
-- Fechas en texto ISO-8601 UTC, como las devuelve datetime('now').

CREATE TABLE grupos (
    id                INTEGER PRIMARY KEY,
    nombre            TEXT NOT NULL,
    codigo_invitacion TEXT NOT NULL UNIQUE,
    creado_en         TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE usuarios (
    id        INTEGER PRIMARY KEY,
    nombre    TEXT NOT NULL,
    creado_en TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Pertenencia a un grupo. cursor = posición del orden común donde empieza
-- la ventana de esta persona; orden_turno = su lugar en la rotación.
CREATE TABLE miembros (
    id                 INTEGER PRIMARY KEY,
    grupo_id           INTEGER NOT NULL REFERENCES grupos(id) ON DELETE CASCADE,
    usuario_id         INTEGER NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE,
    plataforma_escucha TEXT NOT NULL DEFAULT 'links'
                       CHECK (plataforma_escucha IN ('spotify', 'youtube', 'links')),
    cursor             INTEGER NOT NULL DEFAULT 0,
    orden_turno        INTEGER NOT NULL,
    unido_en           TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (grupo_id, usuario_id),
    UNIQUE (grupo_id, orden_turno)
);

-- Cuentas de plataforma conectadas (OAuth). Los tokens se guardan cifrados.
CREATE TABLE cuentas (
    id                    INTEGER PRIMARY KEY,
    usuario_id            INTEGER NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE,
    plataforma            TEXT NOT NULL CHECK (plataforma IN ('spotify', 'youtube')),
    id_externo            TEXT NOT NULL,
    access_token_cifrado  BLOB,
    refresh_token_cifrado BLOB,
    expira_en             TEXT,
    scopes                TEXT,
    UNIQUE (plataforma, id_externo),
    UNIQUE (usuario_id, plataforma)
);

-- Una canción (grabación) única. isrc puede ser NULL; SQLite permite varios
-- NULL en una columna UNIQUE.
CREATE TABLE canciones (
    id          INTEGER PRIMARY KEY,
    isrc        TEXT UNIQUE,
    titulo      TEXT NOT NULL,
    artista     TEXT NOT NULL,
    duracion_ms INTEGER,
    portada_url TEXT,
    creada_en   TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Dónde está cada canción en cada plataforma (spotify, youtube, deezer,
-- amazon, songlink, ...). La plataforma es texto libre a propósito.
CREATE TABLE mapeos (
    cancion_id    INTEGER NOT NULL REFERENCES canciones(id) ON DELETE CASCADE,
    plataforma    TEXT NOT NULL,
    track_id      TEXT,
    url           TEXT,
    confianza     TEXT NOT NULL CHECK (confianza IN ('isrc', 'odesli', 'busqueda', 'manual')),
    verificado_en TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (cancion_id, plataforma)
);

-- Cada vez que alguien aporta una canción al pool de un grupo.
CREATE TABLE aportes (
    id          INTEGER PRIMARY KEY,
    grupo_id    INTEGER NOT NULL REFERENCES grupos(id) ON DELETE CASCADE,
    cancion_id  INTEGER NOT NULL REFERENCES canciones(id),
    usuario_id  INTEGER NOT NULL REFERENCES usuarios(id),
    origen      TEXT NOT NULL CHECK (origen IN ('suelta', 'importada')),
    estado      TEXT NOT NULL DEFAULT 'pendiente'
                CHECK (estado IN ('pendiente', 'ordenado', 'eliminado')),
    agregado_en TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX aportes_fila ON aportes (grupo_id, usuario_id, estado);

-- Orden común materializado del grupo.
CREATE TABLE orden (
    grupo_id  INTEGER NOT NULL REFERENCES grupos(id) ON DELETE CASCADE,
    posicion  INTEGER NOT NULL,
    aporte_id INTEGER NOT NULL UNIQUE REFERENCES aportes(id),
    PRIMARY KEY (grupo_id, posicion)
);

-- Playlist espejo de cada miembro en su plataforma de escucha.
CREATE TABLE espejos (
    miembro_id  INTEGER PRIMARY KEY REFERENCES miembros(id) ON DELETE CASCADE,
    plataforma  TEXT NOT NULL CHECK (plataforma IN ('spotify', 'youtube')),
    playlist_id TEXT NOT NULL
);

-- Canciones que hoy están (o estuvieron) en la ventana de cada miembro.
-- item_id_plataforma: id del elemento en la playlist espejo (NULL en nivel 3).
CREATE TABLE ventana_items (
    miembro_id         INTEGER NOT NULL REFERENCES miembros(id) ON DELETE CASCADE,
    posicion           INTEGER NOT NULL,
    item_id_plataforma TEXT,
    estado             TEXT NOT NULL DEFAULT 'activa' CHECK (estado IN ('activa', 'escuchada')),
    liberada_en        TEXT,
    PRIMARY KEY (miembro_id, posicion)
);

-- Coincidencias dudosas que alguien debe confirmar.
CREATE TABLE revision (
    id              INTEGER PRIMARY KEY,
    cancion_id      INTEGER NOT NULL REFERENCES canciones(id) ON DELETE CASCADE,
    plataforma      TEXT NOT NULL,
    candidatos_json TEXT NOT NULL,
    estado          TEXT NOT NULL DEFAULT 'abierta' CHECK (estado IN ('abierta', 'resuelta')),
    resuelto_por    INTEGER REFERENCES usuarios(id),
    creada_en       TEXT NOT NULL DEFAULT (datetime('now'))
);
