# atm-km-api

Backend de KM (préstamo del auto por kilómetro + registro de combustible).
Go + Gin, PostgreSQL, monolito modular con módulos hexagonales:
`identity`, `settings`, `trips`, `fuel`, `home`, `share`.

La web es `atm-km-web` (React); en desarrollo su Vite reenvía `/api` a
`localhost:3081`.

## Puesta en marcha (local)

1. Base de datos (una sola vez, como superusuario):
   `scripts/provision-db.sql` crea el rol `km_app`, la base `km` y el esquema `km`.
   Para las pruebas de integración, `scripts/provision-test-db.sql` crea `km_test`.
2. `cp .env.example .env` y completa los valores (nunca se sube el `.env`).
3. Migraciones: `scripts/dev.sh migrate up` (también `down` y `status`).
4. Crea un usuario (ver abajo).
5. Servidor: `scripts/dev.sh` → `http://localhost:3081/api/v1`.

## Usuarios

No hay registro ni recuperación de contraseña: las cuentas se gestionan desde
el servidor con la CLI. La contraseña nunca va como argumento (quedaría en el
historial): se escribe sin eco o se pasa por stdin.

```sh
scripts/dev.sh user create --username 12345678 --name "Joel"   # pide la contraseña
echo 'una-clave-larga' | scripts/dev.sh user create --username joel --name "Joel"
scripts/dev.sh user password  --username joel     # cambia la clave y cierra sus sesiones
scripts/dev.sh user deactivate --username joel    # bloquea el acceso (y su enlace compartido)
scripts/dev.sh user activate   --username joel
```

En producción, lo mismo dentro del contenedor: `docker exec -it km-api /app/api user create ...`.

Reglas: usuario de 3 a 40 caracteres (minúsculas, dígitos, `.`, `-`, `_`; un DNI
sirve); contraseña de 8 a 128 caracteres, guardada con Argon2id.

## Sesión

- `POST /auth/login` devuelve un access token (JWT, 1 hora) y un refresh token
  (90 días, se renueva en cada uso: la sesión solo vence tras 90 días sin abrir la app).
- Los navegadores envían `X-Session-Transport: cookie` y reciben el refresh en
  una cookie HttpOnly (`km_refresh`); el access token vive solo en memoria.
- Cada refresh rota el token. Reusar uno ya rotado (pasados 30 s de gracia)
  revoca la sesión entera.

## Endpoints (`/api/v1`)

| Método | Ruta | |
|---|---|---|
| GET | `/health`, `/health/ready` | estado |
| POST | `/auth/login`, `/auth/refresh`, `/auth/logout` | sesión |
| GET | `/me` | usuario actual |
| GET | `/home` | auto prestado, pendiente, últimos 30 días, últimos viajes |
| GET / PUT / POST | `/settings`, `/settings/rates`, `/settings/profile`, `/settings/share-token` | tarifas, nombres, rotar enlace |
| GET / POST | `/trips`, `/trips/open` | listar (paginado, `?status=`) / prestar |
| GET / DELETE | `/trips/:id` | detalle / borrar (solo cancelados) |
| POST | `/trips/:id/return`, `/cancel`, `/pay`, `/unpay`; `/trips/pay` | recibir, cancelar, cobrar |
| PUT / DELETE | `/trips/:id/photos/:leg` (`start`, `end`) | foto del odómetro |
| GET | `/photos/:id?exp&sig` | foto (URL firmada, sin sesión) |
| GET / POST / PUT / DELETE | `/fuel-logs`, `/fuel-logs/:id` | registros de combustible (PUT corrige uno) |
| PUT / DELETE / GET | `/fuel-logs/:id/photo` | foto opcional del comprobante (GET con URL firmada) |
| GET | `/fuel/summary?months=` | resumen mensual |
| GET | `/share/:token` | página pública de quien pide el auto |

Errores: `{ "error": { "code", "message", "details"? } }`; en validación, `details` lista `{ field, code }`.

## Fotos

Se guardan en disco (`PHOTOS_DIR`) y se sirven con URLs firmadas (HMAC, ~12 h).
Se borran solas tras `PHOTO_RETENTION_DAYS` (90 por defecto): la limpieza corre
un minuto después de arrancar y luego cada 6 horas, y también elimina archivos
huérfanos. Manual: `scripts/dev.sh cleanup`.

## Pruebas y calidad

```sh
scripts/test.sh          # unitarias + e2e contra km_test (TEST_DATABASE_URL)
scripts/test.sh -race
golangci-lint run
```

`internal/archtest` verifica que los módulos solo se usen entre sí por su
paquete raíz y que el dominio no dependa de infraestructura.

SQL: las consultas viven en `adapters/postgres/queries.sql` de cada módulo y se
generan con sqlc (`scripts/sqlc-only.sh`); las migraciones (goose) están en
`db/migrations` y van embebidas en el binario.

## Despliegue

`Dockerfile` + `deploy/docker-compose.yml` (puerto `127.0.0.1:3151`, volumen
`km-photos` en `/app/data/photos`). El servicio `migrate` del compose
aplica las migraciones antes de arrancar la API.
