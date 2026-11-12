# Despliegue en Oracle Cloud (Free Tier)

Multiparty es **un solo binario** sin dependencias: SQLite va incluido y no necesita Docker. Detrás va un proxy con HTTPS (Caddy o el nginx que ya tengas).

## 0. Qué necesitas

- La VM de Oracle con SSH.
- Un subdominio apuntando a la IP de la VM, por ejemplo `multiparty.tu-dominio.com`.
- Los puertos 80 y 443 abiertos: en la **Security List** de la VCN **y** en el firewall de la VM (en Oracle Linux, `firewalld`; en Ubuntu, las reglas de `iptables` que trae la imagen de Oracle).

## 1. Compilar

En tu máquina, según la forma de tu VM:

```bash
make build-arm64   # Ampere A1 (VM.Standard.A1.Flex)
```

```bash
make build-amd64   # VM.Standard.E2.1.Micro
```

El binario queda en `bin/`. Si no sabes cuál es, en la VM: `uname -m` (`aarch64` = arm64, `x86_64` = amd64).

## 2. Instalar en la VM

```bash
scp bin/multiparty-linux-arm64 deploy/*.service deploy/*.timer deploy/env.example usuario@tu-vm:/tmp/
```

En la VM:

```bash
sudo install -m 755 /tmp/multiparty-linux-arm64 /usr/local/bin/multiparty
sudo useradd --system --home /var/lib/multiparty --shell /usr/sbin/nologin multiparty
sudo install -d -m 750 /etc/multiparty
sudo install -m 600 /tmp/env.example /etc/multiparty/env
sudo nano /etc/multiparty/env       # completa los valores (ver abajo)
sudo cp /tmp/multiparty.service /tmp/multiparty-respaldo.service /tmp/multiparty-respaldo.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now multiparty multiparty-respaldo.timer
sudo systemctl status multiparty
journalctl -u multiparty -f         # registros
```

### `/etc/multiparty/env`

| Variable | Valor |
|---|---|
| `MULTIPARTY_SECRET` | `openssl rand -base64 32` (firma las sesiones) |
| `MULTIPARTY_CLAVE_TOKENS` | `openssl rand -base64 32` (cifra los tokens; **guárdala**: si se pierde, todos reconectan) |
| `MULTIPARTY_BASE_URL` | `https://multiparty.tu-dominio.com` |
| `GOOGLE_CLIENT_ID` / `_SECRET` | Ver [GOOGLE.md](GOOGLE.md) |
| `SPOTIFY_CLIENT_ID` / `_SECRET` | Ver [SPOTIFY.md](SPOTIFY.md) |

Agrega en Google Cloud y en Spotify los URI de redirección de producción:

- `https://multiparty.tu-dominio.com/cuentas/youtube/callback`
- `https://multiparty.tu-dominio.com/cuentas/spotify/callback`

## 3. Proxy con HTTPS

**Caddy** (obtiene el certificado solo): agrega el bloque de [`deploy/Caddyfile`](../deploy/Caddyfile) a `/etc/caddy/Caddyfile` y ejecuta `sudo systemctl reload caddy`.

**nginx existente:** usa [`deploy/nginx.conf`](../deploy/nginx.conf) como base. Debe pasar `Host` y `X-Forwarded-Proto`: la app los usa para las cookies seguras y el link de invitación.

Comprueba: `curl https://multiparty.tu-dominio.com/healthz` → `ok`.

## 4. Respaldos

El timer copia la base cada día a las 4:00 en `/var/lib/multiparty/respaldos/` y conserva 14 días. Copia manual:

```bash
sudo -u multiparty env MULTIPARTY_DB=/var/lib/multiparty/multiparty.db \
  multiparty respaldo /var/lib/multiparty/respaldos/manual.db
```

Para tener copias fuera de la VM, bájalas de vez en cuando:

```bash
scp usuario@tu-vm:/var/lib/multiparty/respaldos/*.db ./respaldos/
```

**Restaurar:** detén el servicio, reemplaza `/var/lib/multiparty/multiparty.db` por la copia (y borra los archivos `-wal` y `-shm` si existen) y vuelve a arrancar.

## 5. Actualizar

```bash
make build-arm64
scp bin/multiparty-linux-arm64 usuario@tu-vm:/tmp/
ssh usuario@tu-vm 'sudo install -m 755 /tmp/multiparty-linux-arm64 /usr/local/bin/multiparty && sudo systemctl restart multiparty'
```

Las migraciones de la base se aplican solas al arrancar.

## Instalar en el celular

La web se puede instalar como app: en Chrome (Android), menú **⋮ → Agregar a la pantalla principal**; en Safari (iPhone), **Compartir → Agregar a inicio**.
