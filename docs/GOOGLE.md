# Configurar Google Cloud para YouTube Music

Esto habilita la fase F2: conectar YouTube, crear tu playlist espejo e importar canciones. Sin esta configuración la app funciona igual, pero solo con links (nivel 3).

Toma unos 15 minutos y no cuesta nada.

## 1. Crear el proyecto y activar la API

1. Entra a <https://console.cloud.google.com/> con tu cuenta de Google.
2. Crea un proyecto nuevo, por ejemplo `multiparty`.
3. En **APIs y servicios → Biblioteca**, busca **YouTube Data API v3** y pulsa **Habilitar**.

## 2. Pantalla de consentimiento

En **APIs y servicios → Pantalla de consentimiento de OAuth** (o **Google Auth Platform**):

1. Tipo de usuario: **Externo**.
2. Nombre de la app: `Multiparty`. Correo de asistencia: el tuyo.
3. **Scopes:** agrega `https://www.googleapis.com/auth/youtube`.
4. **Usuarios de prueba:** agrega el correo de cada persona del grupo que usará YouTube.
5. **Estado de publicación:** cámbialo a **En producción**.

> ⚠️ **No te quedes en "Testing".** En modo de prueba, Google invalida los permisos cada 7 días y cada persona tendría que volver a conectar YouTube cada semana. En producción, sin verificar, Google muestra una advertencia de "app no verificada" (se continúa con **Avanzado → Ir a Multiparty**) y permite hasta 100 usuarios, de sobra para un grupo de amigos.

## 3. Credenciales OAuth

En **APIs y servicios → Credenciales → Crear credenciales → ID de cliente de OAuth**:

1. Tipo de aplicación: **Aplicación web**.
2. **URI de redireccionamiento autorizados:** agrega la URL de tu sitio seguida de `/cuentas/youtube/callback`:
   - En tu máquina: `http://localhost:8080/cuentas/youtube/callback`
   - En el servidor: `https://tu-dominio/cuentas/youtube/callback`
3. Copia el **ID de cliente** y el **Secreto del cliente**.

## 4. Variables de entorno

```bash
GOOGLE_CLIENT_ID=…apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=…
# Debe coincidir con el URI de redireccionamiento (sin /cuentas/…).
MULTIPARTY_BASE_URL=http://localhost:8080
# Cifra los tokens guardados. Genérala una vez y no la cambies:
# si la pierdes, cada persona tendrá que volver a conectar YouTube.
MULTIPARTY_CLAVE_TOKENS=$(openssl rand -base64 32)
```

Al arrancar, el servidor muestra `YouTube Music habilitado` y la URL de redirección que espera.

## Cuota

El proyecto tiene **10 000 unidades al día**, compartidas por todo el grupo.

| Acción | Unidades |
|---|---|
| Agregar o quitar una canción de la playlist | 50 |
| Buscar una canción en YouTube (si se aportó desde otra plataforma) | 100 + 1 |
| Crear la playlist | 50 |
| Listar playlists o sus canciones (importar) | 1 por cada 50 |

En la práctica: cada canción que pasa por tu playlist cuesta unas 100 unidades (entrar y salir), más 100 la primera vez si hay que buscarla. Si la cuota se agota, la app lo avisa y lo que falte se completa al día siguiente con **Sincronizar**.

## Qué hace la app con tu cuenta

- Lee el nombre de tu canal (para mostrar con qué cuenta estás conectado).
- Crea **una** playlist privada `Multiparty · <grupo>` y solo modifica esa playlist.
- Agrega y quita canciones **solo cuando tú lo pides**: al crear la playlist, al marcar «Ya la escuché» o con «Sincronizar».
- Lee tus playlists solo cuando abres «Importar».
- Al pulsar **Desconectar YouTube**, revoca el permiso en Google y borra los tokens. La playlist queda en tu cuenta. También puedes quitar el acceso en <https://myaccount.google.com/permissions>.
