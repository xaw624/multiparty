-- F1: lugar de cada aporte en la fila personal, plataforma preferida para
-- abrir links y búsqueda de mapeos por id de plataforma.

-- Menor = más al frente. Una canción suelta toma (mínimo - 1); una importada,
-- (máximo + 1).
ALTER TABLE aportes ADD COLUMN clave_fila INTEGER NOT NULL DEFAULT 0;

-- Clave de PlataformasLinks; NULL = página de song.link.
ALTER TABLE miembros ADD COLUMN plataforma_links TEXT;

CREATE INDEX mapeos_track ON mapeos (plataforma, track_id);
