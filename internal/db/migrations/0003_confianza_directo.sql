-- Resolvedor propio (la API de Odesli se retiró): se agrega la confianza
-- 'directo' para el link que pegó la persona. SQLite no permite cambiar un
-- CHECK, así que se reconstruye la tabla. Nada referencia a mapeos.

CREATE TABLE mapeos_nueva (
    cancion_id    INTEGER NOT NULL REFERENCES canciones(id) ON DELETE CASCADE,
    plataforma    TEXT NOT NULL,
    track_id      TEXT,
    url           TEXT,
    confianza     TEXT NOT NULL
                  CHECK (confianza IN ('directo', 'isrc', 'busqueda', 'manual', 'odesli')),
    verificado_en TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (cancion_id, plataforma)
);

INSERT INTO mapeos_nueva (cancion_id, plataforma, track_id, url, confianza, verificado_en)
SELECT cancion_id, plataforma, track_id, url, confianza, verificado_en FROM mapeos;

DROP TABLE mapeos;
ALTER TABLE mapeos_nueva RENAME TO mapeos;
CREATE INDEX mapeos_track ON mapeos (plataforma, track_id);
