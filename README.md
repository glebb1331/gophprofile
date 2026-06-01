# GophProfile — Avatar Service (Sprint 1 MVP)

Микросервис управления аватарками пользователей. Сервис принимает изображения,
сохраняет их в S3-совместимом хранилище, генерирует миниатюры в фоне через
очередь сообщений и отдаёт оригинал/миниатюры по REST API.

## Возможности (этап 1)

- REST API на Go + chi
- PostgreSQL для метаданных, миграции `golang-migrate`
- MinIO/S3 для бинарного хранения
- RabbitMQ для асинхронной обработки и удаления
- Worker, который декодирует изображение, создаёт миниатюры 100×100 и 300×300,
  выгружает их в S3 и обновляет статусы в БД (с идемпотентностью)
- Health-эндпоинт со статусами PostgreSQL, RabbitMQ и MinIO
- Веб-интерфейс: форма загрузки и галерея пользователя
- Multi-stage Dockerfile + docker-compose со всеми зависимостями
- Юнит-тесты с покрытием >50% по ключевым пакетам

## Структура проекта

```
gophprofile/
├── cmd/
│   ├── server/         # HTTP сервер
│   └── worker/         # фоновый worker
├── internal/
│   ├── api/            # (зарезервировано)
│   ├── broker/rabbitmq # клиент брокера + publisher
│   ├── config/         # конфигурация из ENV
│   ├── domain/         # доменные модели и события
│   ├── handlers/       # HTTP-обработчики и роутер
│   ├── imageproc/      # декодирование/изменение размера
│   ├── repository/
│   │   ├── postgres    # пул соединений, репозиторий, миграции
│   │   └── s3          # клиент MinIO/S3
│   ├── services/       # доменная логика загрузки/чтения/удаления
│   └── worker/         # обработчик событий из очереди
├── migrations/         # SQL-миграции (golang-migrate)
├── docker/Dockerfile   # multi-stage build
├── docker-compose.yml  # локальное окружение
├── web/static          # frontend (форма + галерея)
└── tests/              # (зарезервировано под integration)
```

## Запуск через Docker Compose

```bash
docker compose up --build
```

Поднимет: PostgreSQL, MinIO (`:9000`, консоль `:9001`), RabbitMQ (`:5672`,
панель `:15672`), сервер (`:8080`) и worker. Сервер автоматически применяет
миграции при старте.

Открыть форму загрузки: http://localhost:8080/
Галерея пользователя: http://localhost:8080/web/gallery/{user_id}

## Локальный запуск без Docker

1. Поднять PostgreSQL, MinIO, RabbitMQ (например, через `docker compose up postgres minio rabbitmq`).
2. Скопировать `.env.example` → `.env` и при необходимости поправить значения.
3. Применить миграции произойдёт автоматически при запуске сервера.
4. Запустить сервисы:
   ```bash
   go run ./cmd/server
   go run ./cmd/worker
   ```

## REST API

| Метод  | Путь                                   | Описание                                  |
|--------|----------------------------------------|-------------------------------------------|
| POST   | `/api/v1/avatars`                      | Загрузка аватарки (`multipart/form-data`) |
| GET    | `/api/v1/avatars/{id}`                 | Бинарные данные аватарки (`?size=100x100`)|
| GET    | `/api/v1/avatars/{id}/metadata`        | Метаданные                                |
| DELETE | `/api/v1/avatars/{id}`                 | Удаление (soft delete + асинхр. S3)       |
| GET    | `/api/v1/users/{user_id}/avatar`       | Текущий аватар пользователя               |
| GET    | `/api/v1/users/{user_id}/avatars`      | Список загруженных аватарок               |
| DELETE | `/api/v1/users/{user_id}/avatar`       | Удалить текущий аватар                    |
| GET    | `/health`                              | Healthcheck с компонентами                |
| GET    | `/`                                    | Веб-форма загрузки                        |
| GET    | `/web/gallery/{user_id}`               | Веб-галерея пользователя                  |

Заголовок `X-User-ID` обязателен для всех мутирующих операций.

### Пример загрузки

```bash
curl -X POST http://localhost:8080/api/v1/avatars \
  -H "X-User-ID: gopher@example.com" \
  -F "file=@avatar.png"
```

Ответ `201 Created`:
```json
{
  "id": "5a3e9fa1-...",
  "user_id": "gopher@example.com",
  "url": "/api/v1/avatars/5a3e9fa1-...",
  "status": "pending",
  "created_at": "2026-05-27T12:34:56Z"
}
```

### Получение миниатюры

```bash
curl -o thumb.jpg "http://localhost:8080/api/v1/avatars/<id>?size=100x100"
```

## Очередь сообщений

Используется RabbitMQ topic-exchange `avatars.exchange`. Очереди:
- `avatars.upload` (routing-key `avatar.uploaded`) — обработка загруженной аватарки.
- `avatars.delete` (routing-key `avatar.deleted`) — асинхронное удаление файлов.

Все события включают `event_id` (UUID). Worker сохраняет успешно обработанные
ID в таблицу `processed_events`, чтобы повторная доставка не выполняла
повторных операций. Ошибки приводят к экспоненциальному ретраю с шапкой 30
секунд, после исчерпания попыток сообщение ack-ается как `nack`/`requeue=false`.

## Конфигурация (ENV)

Полный список — в `.env.example`. Основные:

| Переменная                    | По умолчанию                                                    |
|-------------------------------|------------------------------------------------------------------|
| `HTTP_ADDR`                   | `:8080`                                                          |
| `POSTGRES_DSN`                | `postgres://avatars:avatars@localhost:5432/avatars?sslmode=disable` |
| `POSTGRES_MIGRATIONS_PATH`    | `file://migrations`                                              |
| `S3_ENDPOINT`                 | `localhost:9000`                                                 |
| `S3_BUCKET`                   | `avatars`                                                        |
| `RABBITMQ_URL`                | `amqp://guest:guest@localhost:5672/`                             |
| `AVATAR_MAX_UPLOAD_BYTES`     | `10485760` (10 МБ)                                               |
| `STATIC_DIR`                  | `web/static`                                                     |

## Тесты

```bash
go test ./... -cover
```

Покрытие основных пакетов:
- `config`     — ~95%
- `imageproc`  — ~85%
- `handlers`   — ~62%
- `services`   — ~60%
- `worker`     — ~52%

## Дальнейшие шаги

Sprint 2/3
