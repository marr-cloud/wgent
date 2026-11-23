# wgent — diseño

Fecha: 2026-11-23 · Estado: aprobado

Decisiones del usuario: README y mensajes en inglés; publicar en GitHub (`marr-cloud/wgent`); al terminar, borrar el prototipo (tareas `vpn-on`/`vpn-off` y scripts `vpn*`).

## Intención

`wgent` es un wrapper de WireGuard para Windows pensado para **agentes de IA** (y para Mauricio desde la terminal). Hoy existe un prototipo en PowerShell (`~/.local/bin/vpn*.ps1` + tareas programadas `vpn-on`/`vpn-off`) que solo sirve para un túnel fijo (`a-ado-cloud`). El objetivo es un binario único, configurable y con un README bien hecho.

Por qué: que un agente pueda **encender/apagar un túnel sin que el humano intervenga** (sin UAC) y **saber con certeza el estado**: encendido, apagado o con problemas.

## Objetivos

- Túnel por defecto definido en `~/.config/wgent/config.toml`; los comandos lo usan si no se pasa nombre.
- `wgent list`: lista los túneles disponibles para copiar el nombre.
- `wgent up [nombre]` / `wgent down [nombre]` / `wgent status [nombre]`.
- Estado con contexto real: servicio del túnel, última respuesta del servidor (handshake), bytes rx/tx, y un veredicto (`up`, `down`, `degraded`, `error`).
- `--json` en todos los comandos de lectura y acción, con salida estable y documentada para agentes.
- Sin UAC tras una instalación única con admin.
- `wgent tray`: icono de bandeja opcional (verde/gris/ámbar/rojo), clic alterna el túnel por defecto, menú con todos los túneles.
- README completo + sección "Para agentes de IA" (contrato JSON, códigos de salida, ejemplos).

## No-objetivos (v1)

- Importar, crear o editar archivos `.conf`. Los túneles se gestionan en la app oficial de WireGuard; `wgent` solo usa los que ya están ahí.
- Linux/macOS.
- Reemplazar la GUI de WireGuard.
- Alias, perfiles por red, reconexión automática.

## Anti-objetivos (sería un fracaso aunque funcione)

- Que cualquier proceso o usuario de la máquina pueda usar el servicio elevado para algo más que encender/apagar túneles ya importados. **El servicio no acepta rutas de archivo ni comandos arbitrarios.**
- Que `status` diga "up" cuando el túnel no pasa tráfico de verdad (el agente confiaría en algo falso).
- Salida pensada solo para humanos que el agente tenga que interpretar a ojo.

## Restricciones

- Windows 11, WireGuard para Windows instalado en `C:\Program Files\WireGuard`.
- Configuraciones en `C:\Program Files\WireGuard\Data\Configurations\*.conf.dpapi` (solo legibles por SYSTEM/admins).
- Encender/apagar túneles y leer sus estadísticas requiere privilegios de SYSTEM/admin.
- Go 1.27, binario único sin dependencias externas en runtime.
- Repo con tests → TDD (regla global del usuario).

## Enfoques considerados

| Enfoque | Resultado |
|---|---|
| **Go + servicio de Windows propio + named pipe** | **Elegido.** Binario único, `x/sys/windows/svc` maduro para servicios, `go-winio` para named pipes con ACL. (Revisión: las estadísticas salen de `wg.exe dump`, ver Operaciones.) |
| Rust + servicio propio | Descartado: sin bindings oficiales; habría que parsear la salida de `wg.exe`. |
| C# .NET 10 | Descartado: binario pesado, Native AOT con WinForms problemático, mismo parseo de `wg.exe`. |
| Mantener tareas programadas | Descartado: una tarea no recibe argumentos, no escala a N túneles. |

## Arquitectura

Un solo ejecutable `wgent.exe` con dos roles:

1. **Servicio** (`wgent service run`, lo arranca el SCM como LocalSystem, nombre `wgent`): escucha en un named pipe `\\.\pipe\wgent`. Ejecuta las órdenes privilegiadas.
2. **Cliente** (todos los demás subcomandos): corre como el usuario normal, habla con el servicio por el pipe.

### Seguridad del pipe

- El DACL del pipe permite acceso solo a SYSTEM, Administradores y **el SID del usuario que instaló el servicio** (guardado en la configuración del servicio en el registro al instalar). SDDL: `D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;<SID>)`; el usuario tiene lectura/escritura pero no `FILE_CREATE_PIPE_INSTANCE`, así que no puede crear instancias de servidor del pipe.
- El cliente verifica que el proceso servidor del pipe corre como LocalSystem (`S-1-5-18`, vía `GetNamedPipeServerProcessId` y el token del proceso). Si no, rechaza la conexión (exit 3, "pipe server is not the wgent service"): evita que otro proceso ocupe el pipe y suplante el estado.
- Protocolo cerrado: mensajes JSON con `op ∈ {list, status, up, down}` y `tunnel` (string). El nombre se valida contra `^[A-Za-z0-9_=+.-]{1,32}$` (regla de nombres de WireGuard) y debe existir en el directorio de configuraciones de WireGuard. Nada de rutas.

### Operaciones del servicio

- `list`: lee el directorio de configuraciones (`*.conf.dpapi`, `*.conf`) → nombres + si están activos.
- `up`: si ya existe el servicio `WireGuardTunnel$<n>` y está parado, lo inicia; si no existe, ejecuta `wireguard.exe /installtunnelservice <ruta>` (CLI estable y documentada de WireGuard). Espera hasta que el servicio esté `Running` o venza un timeout (15 s).
- `down`: `wireguard.exe /uninstalltunnelservice <n>`; espera a que el servicio desaparezca.
- `status`: estado del servicio del túnel + estadísticas vía `wg.exe show <n> dump` (formato tabulado para máquinas: último handshake por peer, rx/tx). Revisión 2026-11-23: se descartó el paquete `driver` de wireguard-windows porque requiere `wireguard.dll`, que la instalación oficial no deja en System32 (va embebido en `wireguard.exe`).

### Veredicto de estado

| Estado | Condición |
|---|---|
| `down` | No existe el servicio del túnel o está parado. |
| `up` | Servicio en `Running` y algún peer con handshake hace < 180 s. |
| `degraded` | Servicio en `Running` pero sin handshake o el último > 180 s. Incluye `reason`. |
| `error` | El servicio del túnel falló, o no se pudieron leer estadísticas. Incluye `reason`. |

Nota: sin tráfico ni `PersistentKeepalive`, un túnel sano puede no hacer handshake durante un rato; el `reason` lo dice ("sin handshake reciente; puede ser inactividad si no hay keepalive") y el README lo explica.

## CLI

```
wgent list                     [--json]
wgent status [tunel]           [--json]
wgent up     [tunel]           [--json]
wgent down   [tunel]           [--json]
wgent toggle [tunel]           [--json]
wgent config                   # muestra ruta y contenido efectivo
wgent config set-default <tunel>
wgent tray   [--autostart on|off]
wgent service install|uninstall   # requiere admin (sudo wgent service install)
wgent version
```

- Sin nombre → `default_tunnel` del config. Si no hay default y no se pasa nombre → error con sugerencia (`wgent list`, `wgent config set-default`).
- `up`/`down` son idempotentes: si ya está en el estado pedido, éxito sin hacer nada (`"changed": false`).
- `up X` no toca otros túneles activos.

### Salida JSON (contrato)

```json
{
  "tunnel": "a-ado-cloud",
  "state": "up",
  "reason": "",
  "changed": true,
  "service": "running",
  "peers": [{"endpoint": "1.2.3.4:51820", "last_handshake": "2026-11-23T15:40:01Z", "handshake_age_s": 12, "rx_bytes": 1234, "tx_bytes": 5678}]
}
```

Errores en JSON: `{"error": {"code": "service_not_installed", "message": "...", "hint": "sudo wgent service install"}}`.

### Códigos de salida

| Código | Significado |
|---|---|
| 0 | OK (en `status`: el comando funcionó, sea cual sea el estado) |
| 1 | Error genérico |
| 2 | Uso incorrecto / falta túnel por defecto |
| 3 | Servicio `wgent` no instalado o no responde |
| 4 | Túnel no encontrado |
| 5 | Timeout esperando el cambio de estado |

## Configuración

`%USERPROFILE%\.config\wgent\config.toml` (respeta `XDG_CONFIG_HOME` si existe):

```toml
default_tunnel = "a-ado-cloud"

[tray]
poll_seconds = 2
```

Si no existe, se usa sin default. `wgent config set-default` la crea.

## Bandeja

- `wgent tray`: una instancia (mutex), icono verde/gris/ámbar (cambiando)/rojo (`degraded`/`error`), tooltip con estado y túnel, notificación al cambiar.
- Clic izquierdo: `toggle` del túnel por defecto. Menú: lista de túneles con check en los activos, "Salir".
- `--autostart on|off`: crea/quita la entrada en `HKCU\...\Run`.
- El binario es de consola (no `-H windowsgui`, para que la CLI siga funcionando). `wgent tray` se relanza a sí mismo desacoplado y sin ventana (`DETACHED_PROCESS`/`CREATE_NO_WINDOW`) y devuelve el control a la terminal.
- Si la app oficial de WireGuard también está abierta, ambas muestran el mismo estado (las dos leen los servicios `WireGuardTunnel$*`); no hay conflicto.

## Distribución

- `go install github.com/marr-cloud/wgent/cmd/wgent@latest` o binario de GitHub Releases (goreleaser, windows/amd64 y arm64).
- `wgent service install` copia el ejecutable a `%ProgramFiles%\wgent\wgent.exe` y registra el servicio apuntando ahí (un servicio SYSTEM no debe ejecutarse desde una ruta escribible por el usuario). Cliente y servicio intercambian `protocol_version`; si no coinciden, el cliente sugiere `sudo wgent service install` para actualizar.

## Tests

- Unitarios: parseo de config, resolución de túnel (arg/default/error), validación de nombres, veredicto de estado (tabla con handshakes y estados de servicio), serialización JSON, protocolo del pipe con un servidor falso.
- Interfaces para SCM, `wireguard.exe` y driver → fakes en tests.
- Manual/integración (documentado en README): up/down/status contra un túnel real.

## Migración del prototipo

Al terminar, con confirmación del usuario: borrar tareas `vpn-on`/`vpn-off`, `~/.local/bin/vpn*.ps1`, `vpn.cmd`, y el acceso directo de inicio si se creó.

## Decisiones tomadas por Claude (revisables)

- Nombre `wgent`, módulo `github.com/marr-cloud/wgent`, repo en `~/workspace/go/wgent`.
- Servicio propio + named pipe en vez de tareas programadas.
- Estados y umbral de 180 s.
- `up X` no apaga otros túneles.
- No importar/editar `.conf` en v1.
- README y mensajes en inglés (repo público en GitHub); este diseño en español.

## A criterio del implementador

- Librería CLI (stdlib `flag` o `cobra`), librería de bandeja (`fyne.io/systray` o similar), named pipes (`github.com/Microsoft/go-winio`), parseo TOML (`BurntSushi/toml` o `pelletier/go-toml/v2`).
- Layout interno de paquetes.
