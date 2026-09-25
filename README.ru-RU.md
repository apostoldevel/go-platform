[![en](https://img.shields.io/badge/lang-en-green.svg)](README.md)

Go-платформа
-

**Go-слой** для **Apostol CRM**[^crm].

Описание
-

**Go-платформа** — библиотека и набор готовых пакетов для написания **внепроцессных API-модулей** на Go для фреймворка [Апостол (C++20)](https://github.com/apostoldevel/libapostol). Модуль на её основе — HTTP-сервер, который отвечает на `/api/v2/*`, сам регистрируется в [Gateway API](https://github.com/apostoldevel/module-GatewayAPI) по WebSocket и вызывает функции `api.*` [db-platform](https://github.com/apostoldevel/db-platform) в PostgreSQL. Модуль тонкий и без состояния: авторизация, правила доступа, workflow и бизнес-логика остаются в базе данных — ровно там, где их оставляет внутрипроцессный [AppServer](https://github.com/apostoldevel/module-AppServer) для `/api/v1/*`.

В репозитории две части:

* **Библиотека** (`lib/`, корневой пакет `platform`) — контракт модуля и хост, собирающий пакеты в один обработчик; клиент шлюза; один запрос = одна транзакция базы; трансляция параметров списка в `api.sql()`; `application/problem+json` (RFC 9457); локальная проверка JWT.
* **Платформенные пакеты** — по одному Go-пакету на SQL-модуль db-platform (`admin`, `workflow`, `registry`, `log`, …), каждый отдаёт функции `api.*` своего модуля как ресурсы `/api/v2`. Проект добавляет рядом свои пакеты — по одному на сущность своей конфигурации.

Основные характеристики:

* **Go-дерево повторяет SQL-дерево.** Пакет лежит в папке своего SQL-модуля или сущности (`workflow/`, `entity/object/`, `entity/object/document/<x>/`) и называется как он. Общий код — в `lib/` (`go` — ключевое слово и не может быть последним элементом пути импорта).
* **Один бинарник на проект.** Импорты его `main` — манифест сборки, как `create.psql` для SQL: что не импортировано, того в процессе нет. Один `platform.New`, один сокет к шлюзу, один пул соединений.
* **Базе доверяется всё, кроме подписи.** JWT проверяется в Go до любого маршрута — база доверяет коду сессии, который ей дали, — и дальше каждый вызов `api.*` запроса идёт под `api.authorize` внутри одной транзакции.
* **Каждый ответ процесса — JSON**: строка, список `{items, total, limit, offset}` или `problem+json` — включая `404`, `405` (с `Allow`) и строку запроса, которую хост не смог разобрать (`400`). Ни один текстовый ответ самого `net/http` до клиента не доходит.
* **Версии связаны константой.** `platform.DBPlatform` — версия db-platform, на которой в последний раз прогонялись интеграционные тесты; прогон сверяет её с файлом `VERSION` базы.

### Место в Апостоле

```
клиент ── /api/v1/*  ──► worker: AppServer   ──► PostgreSQL (api.*)
       ── /oauth2/*  ──► worker: AuthServer                                    ┌─ этот репозиторий ─┐
       ── /api/v2/*  ──► worker: GatewayAPI ── HTTP ──► модуль на ipv4:port ──►│ platform.New        │──► PostgreSQL (api.*)
       ── /gateway/* ──► worker: GatewayAPI ◄── WebSocket ── модуль ───────────│ lib/gatewayclient   │
                                                                               └─────────────────────┘
```

Запросы идут **шлюз → модуль** по HTTP; канал управления — **модуль → шлюз** по WebSocket. Модулю не нужно ничего из конфигурации шлюза: он подключается, говорит, какие префиксы обслуживает и на каком адресе слушает, и попадает в ротацию в момент принятия регистрации.

Структура
-

```
module.go              пакет platform: Module { Name; Prefixes; Routes(mux) }, New(cfg, modules…), Prefixes(modules…)
version.go             DBPlatform — версия db-platform, на которой прогонялись интеграционные тесты
lib/
  auth/jwt/            проверка HS256/384/512 секретами OAuth2-провайдеров; Keyring, Claims
  gateway/frame/       кадр плоскости управления {t,u,a,p,c,m}: CALL, CALLRESULT, CALLERROR; предел 64 KiB
  gatewayclient/       подключение, /register, heartbeat, /status, /unregister; /ping, /drain, /reload; переподключение
  pgtx/                один запрос = одна транзакция: api.authorize → SAVEPOINT → api.* → api.log_request → COMMIT; отказ тоже журналируется
  problem/             application/problem+json с каталогом ошибок базы
  query/               ?filter[…]&sort=&fields=&page[limit]= → jsonb search/orderby/fields для api.sql()
  rest/                форма ресурса: список, строка + ETag, create/update/delete, Idempotency-Key, If-Match
admin/ api/ current/ error/ kladr/ log/ notification/ observer/ registry/ resource/ verification/ workflow/
entity/object/         платформенные пакеты, по одному на SQL-модуль db-platform (таблица ниже)
cmd/gatewaystub/       автономная заглушка плоскости управления шлюза для локального запуска
internal/gatewaystub/  та же заглушка как тестовый двойник; internal/resttest — обвязка интеграционных тестов
```

Контракт модуля
-

GoAPI-пакет — это `platform.Module`:

```go
type Module interface {
    Name() string            // SQL-модуль или сущность, которую он повторяет: "workflow", "client"
    Prefixes() []string      // что уходит в /register шлюза: "/api/v2/clients"
    Routes(mux *http.ServeMux)  // "GET /api/v2/clients/{id}" … на общем mux
}
```

`platform.New(cfg, modules…)` собирает модули в один `http.Handler`, который обслуживает процесс. До любого маршрута он проверяет bearer-токен ключами `cfg.Keys`, устанавливает сессию (`code` = `sub` из JWT, агент — из `User-Agent`, хост — по `X-Forwarded-For` и адресу пира, как велит `cfg.TrustedProxies`), возвращает `X-Request-Id` без изменений, считает запрос в полёте и разбирает строку запроса; два модуля с одним префиксом отвергаются на старте, а не в `/register`. Каждый 401 хоста — `problem+json` типа `unauthorized` с кодом каталога ошибок в `code`: `ERR-401-001` — bearer нет или токен не проверяется, `ERR-401-008` — проверенный токен истёк; заголовок — сообщение каталога, когда задан `cfg.Catalogue`, иначе `"Unauthorized"`. Всё, на чём `Verify` может споткнуться до того, как подпись сошлась (формат, неизвестная аудитория, алгоритм, подпись, издатель), — **один** ответ, и код, и `detail`: аудитория проверяется раньше подписи, и различие позволило бы неподписанным токеном перебирать client id; точная причина — в `cfg.Logger` на уровне debug. Сессию, которой база не знает, `pgtx` отвергает так же, `ERR-401-001`. Каждый 401 процесса — и хоста, и `pgtx`, с любым кодом — несёт `WWW-Authenticate` (RFC 6750 §3): голый `Bearer`, когда запрос не предъявил bearer вовсе (§3.1: без кода ошибки), иначе `Bearer error="invalid_token"`, с `detail` в `error_description`, если он — печатный ASCII без `"` и `\` (локализованный текст каталога остаётся только в теле). Ставит его одно место — `problem.(*Problem).Write`; голый случай помечает `(*Problem).NoCredentials()`. `platform.Prefixes(modules…)` — объединение префиксов в порядке регистрации, без префиксов, вложенных в другой префикс того же процесса: шлюз маршрутизирует по самому длинному префиксу и отвергает пересечение.

Внутри обработчика `platform.SessionOf(r)` — сессия, а `rest.Doer` (в продакшене `pgtx.Runner`) выполняет транзакцию:

```go
func (m *module) get(w http.ResponseWriter, r *http.Request) {
    id, err := rest.IDOf(r)                       // {id} — UUID, иначе 400 до базы
    if err != nil { rest.Fail(w, r, m.log, err); return }
    var row json.RawMessage
    err = m.doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil),
        func(ctx context.Context, tx pgx.Tx) error {
            return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_client($1) t", id).Scan(&row)
        })
    …
}
```

Большинство пакетов обработчиков не пишут вовсе: `rest.Resource` называет функции `api.get_<x>` / `api.list_<x>` / `api.count_<x>` ресурса, `rest.Writable` добавляет `api.set_<x>` / `api.delete_<x>` с типизированным телом, а `Routes` регистрирует глаголы (пять, или четыре, если у ресурса нет `api.delete_<x>` — тогда `DELETE` даёт `405`).

Путь запроса
-

Один HTTP-запрос — одна транзакция базы (`lib/pgtx`):

```
BEGIN
  SELECT * FROM api.authorize($session, $agent, $host)   -- или api.authorize_local, если база его имеет
  SAVEPOINT request                                      -- если база журналирует
  … вызовы api.* обработчика …
  SELECT api.log_request(…, status)                      -- если база его имеет
COMMIT
```

Отказ журналируется так же: работа обработчика отменяется `ROLLBACK TO SAVEPOINT`, ошибка объясняется, затем `api.log_request(…, 4xx)` выполняется под контекстом сессии, выставленным до точки сохранения (частичный откат его не трогает), и транзакция фиксируется с одной строкой аудита — `db.api_log` показывает, кому в чём отказано, как `api.run` показывает это для v1. Сессия, которую отвергла база, журналируется без сессии — ответила ли `api.authorize_local` `false` (`401`) или подняла ошибку (IP-таблица, заблокированный пользователь, истёкший пароль: статус кода каталога, в свежей транзакции — поднятая ошибка прервала транзакцию запроса); токен, который отверг хост, до базы не доходит. Строка аудита пишется под собственным контекстом: клиент, не дождавшийся ответа, строку с собой не уносит. `pgtx.Request.LogID` — записанная строка.

Контекст сессии в db-platform живёт в транзакции, а не в соединении: под пулом следующий запрос иначе унаследовал бы чужую сессию, поэтому ничто из запроса не выполняется вне его транзакции. При ошибке транзакция откатывается, а сообщение объясняется каталогом базы (`api.parse_message`) на отдельном соединении, в своей транзакции, и возвращается как `problem+json`: прерванная транзакция не может выполнить запрос, объясняющий её собственную ошибку. `Runner.Detect` один раз на старте проверяет, что из `api.authorize_local`, `api.log_request` и `api.parse_message` база предлагает.

### Соглашения `/api/v2`

| | |
|---|---|
| Ресурсы | существительные во множественном числе, по одному на семейство `api.*`: `/api/v2/users`, `/api/v2/users/{id}`, `/api/v2/users/{id}/groups` |
| Список | `GET /api/v2/<xs>?filter[state]=enabled&filter[created][gte]=…&filter[state][in]=a,b&sort=-created,name&fields=id,name&page[limit]=50&page[offset]=100` → `{items, total, limit, offset}`; `filter` и `sort` становятся jsonb `search`/`orderby` для `api.sql()` — язык операторов остаётся в базе, Go только переименовывает; `fields` применяется к строкам в Go |
| Строка | `GET …/{id}` → строка со слабым `ETag`; `If-None-Match` → `304`; строка `null` — `404` |
| Создание | `POST /api/v2/<xs>` → `201`, `Location`, `ETag`; `Idempotency-Key` повторяет ответ на то же тело |
| Изменение | `PATCH …/{id}` с `If-Match` (`428` без него, `412` при устаревшем) |
| Удаление | `DELETE …/{id}` → `204` |
| Действия | `POST …/{id}/actions/<verb>` для того, что не является изменением полей (методы workflow, `copy`, `clone`, …) |
| Тела | JSON-объекты, неизвестные ключи отвергаются (`400`), как это делает `CheckJsonbKeys` в базе |
| Ошибки | `application/problem+json`: `{type: "urn:apostol:error:ERR-400-032", title, status, detail, instance, request_id, code}`; код и текст — из каталога ошибок базы; ограничение, которое отвергла база, — `400` (`409` для дубликата ключа и для внешнего ключа, отвергшего `DELETE` — на ресурс ещё ссылаются) |
| Заголовки | `Authorization: Bearer <access token>` на входе, `X-Request-Id` на входе и на выходе без изменений |

### Что есть в v1 и чего нет в v2

`/ping`, `/time`, `/authenticate`, `/authorize`, `/su`, `/run`, `/sign/in`, `/sign/up`, `/sign/out` и `/observer/subscribe` не являются ресурсами `/api/v2` намеренно: проверка живости — собственная точка модуля; время — заголовок `Date`; вход, выход и подмена пользователя — дело OAuth2-сервера (`/oauth2/token`, `/oauth2/revoke`), модуль получает готовый JWT; `/authorize` — первый шаг каждой транзакции; `/run` мультиплексировал пути v1; подписки делаются через WebSocket API, а не по HTTP.

Плоскость управления
-

`lib/gatewayclient` — сторона модуля в плоскости управления [Gateway API](https://github.com/apostoldevel/module-GatewayAPI) (протокол — его README, раздел *Control plane*):

* **Рукопожатие** — `GET {GATEWAY_URL}/{module}/{instance}` с `Authorization: Bearer <токен client_credentials>`; `Config.Token` возвращает токен и вызывается при каждом (пере)подключении, так что может обновлять его.
* **`/register`** — module, instance, version, build, `address` (IPv4-литерал `host:port` плоскости данных — пустой `Config.Address` берёт локальный адрес управляющего сокета плюс `ListenPort`), префиксы и ёмкость. В ответе — `heartbeat_interval`.
* **`/heartbeat`** каждый интервал с числом запросов в полёте (`Config.InFlight`); **`/status`** при `ready` / `draining` / `overloaded` (`SetOverloaded`); **`/unregister`** перед закрытием.
* **Команды** — на `/ping` отвечает числом запросов в полёте; `/drain` запускает дренаж; `/reload` вызывает `Config.OnReload`, и изменённые адрес, префиксы или ёмкость заставляют клиента перерегистрироваться.
* **Дренаж** (`Client.Drain`, ответ вызывающего кода на `SIGTERM`) — `/status draining` → ожидание запросов в полёте (`DrainDeadline`, по умолчанию 30 с) → `/unregister` → закрытие `1000`. Выйти раньше — и запросы в полёте станут `502` для клиента.
* **Переподключение** — после потери сокета или неудачного соединения с `DefaultReconnect` (1 → 30 с, ±20 %); после отвергнутой регистрации или отвергнутого рукопожатия — через 30 с; закрытие `1001` (заменён более новой регистрацией с тем же именем) завершает `Run` ошибкой `ErrReplaced` — модуль не должен переподключаться; кадр больше 64 KiB закрывает сокет с `1009`.

```go
gw, err := gatewayclient.New(gatewayclient.Config{
    URL: cfg.GatewayURL, Module: "example-api", Instance: cfg.Instance, Version: version, Build: build,
    Address: cfg.AdvertiseAddr, Prefixes: platform.Prefixes(mods...), Capacity: cfg.Capacity,
    Token:    token,                                   // client_credentials из /oauth2/token
    InFlight: func() int { return int(inFlight.Load()) },
    Logger:   log,
})
go gw.Run(ctx)                                         // переподключается, пока ctx жив
…
<-ctx.Done()                                           // SIGTERM
gw.Drain(context.Background(), "sigterm")             // затем остановить HTTP-сервер
```

Платформенные пакеты
-

По одному пакету на SQL-модуль db-platform, в порядке его `create.psql`; ресурсы — функции `api.*` модуля в форме `/api/v2`.

| Пакет | SQL-модуль | Ресурсы |
|-------|------------|---------|
| `admin` | `admin` | `users` (+`profile`, `iptable`, членства `groups`/`areas`/`interfaces`, `memberships`, `actions/{action}`), `groups`, `areas` (+`actions/{action}`, `actions/clear`), `area-types`, `interfaces`, `sessions`, `locales` |
| `resource` | `resource` | `resources` |
| `error` | `error` | `errors`, `errors/by-code/{code}` |
| `registry` | `registry` | `registry`, `registry/keys` (+`{id}/path`, `enum`), `registry/values` (+`read`, типизированный `PUT`), `registry/tree` |
| `log` | `log` | `event-log`, `me/event-log` |
| `api` | `api` | `api-log` |
| `current` | `session`, `current` | `me` (`GET` — область, интерфейс, локаль, операционная дата и пользователь сессии одним объектом; `PATCH` — `set_session_*`) |
| `workflow` | `workflow` | `entities`, `types`, `classes`, `states`, `state-types`, `actions`, `methods`, `transitions`, `events`, `event-types`, `priorities` |
| `kladr` | `kladr` | `kladr`, `kladr/{id}/history`, `kladr/string` |
| `entity/object` | `entity/object` | `objects`, `objects/{id}/methods`, `objects/{id}/actions/{action}`, `objects/{id}/methods/{method}`, `objects/{id}/access` (`GET` — записи, `PUT` — одно право, `api.chmodo`; `access/decode[?userid=]` — биты одного пользователя), `objects/{id}/files` (`GET` список с `total`, `POST` — JSON-массив файлов, `api.set_object_files_json`, `DELETE` очищает; `files/{file}` — `GET` с байтами, `DELETE`), `search`. Остальное из платформенного диспетчера `rest.object` (класс, тип, история состояний и методов, группы, связи, данные, адреса, геолокация) и `rest.document`/`rest.reference` потребителя не имеют и формы здесь нет |
| `notification` | `notification` | `notifications`, `notifications/since`, `notifications/changed` |
| `verification` | `verification` | `verification/codes`, `verification/codes/confirm` |
| `observer` | `observer` | `observer/publishers`, `observer/listeners` (только своя сессия, только чтение) |

Пакеты проекта следуют тому же правилу в его собственном модуле: `entity/object/document/<x>/` для сущности `<x>` его конфигурации, в `main` — после платформенных.

Конфигурация
-

Библиотека сама окружение не читает; это делает процесс и передаёт значения дальше. Что нужно процессу:

| | |
|---|---|
| `platform.Config.Keys` | секреты OAuth2-провайдеров, чьи токены процесс принимает (`jwt.Keyring`: аудитория → секрет и алгоритм), те же ключи, которыми подписывает `AuthServer` |
| `platform.Config.TrustedProxies` | прокси, чьему `X-Forwarded-For` верят (`platform.ParseTrustedProxies("10.0.0.0/8, 172.20.0.1")`). Nil: верят только пиру — клиент есть **последний** элемент, дописанный пиром (шлюз шлёт ровно один); со списком клиент — самый правый адрес не из списка, а пир вне списка — сам клиент (nginx `real_ip_recursive`, Express `trust proxy`); пустой список не верит никому. Элемент, который не адрес, останавливает обход на последнем прочитанном адресе (прокси, который его написал, или пир) — никогда на «адреса нет»: NULL-хост для IP-таблиц базы значит «без ограничений» |
| `pgtx.NewPool(ctx, dsn)` | база данных, от API-роли db-platform (не суперпользователь: вызов, который работает от суперпользователя и падает от API-роли, — отсутствующий grant) |
| `gatewayclient.Config` | `URL` (`ws://gateway:port/gateway`), `Module`, `Instance`, `Address` или `ListenPort`, `Capacity`, `Token` |

Узлы слушают только внутреннюю сеть; между шлюзом и модулем — HTTP без TLS; cookies до модуля не доходят — доходят `Authorization: Bearer` и `X-Request-Id`.

Установка
-

Go 1.26 или новее. Репозиторий подключается как git-субмодуль проекта (`go/platform`) — так же, как db-platform подключается в `db/sql/platform`:

```bash
git submodule add https://github.com/apostoldevel/go-platform.git platform
```

```go
// go.mod проекта
require github.com/apostoldevel/go-platform v0.0.0
replace github.com/apostoldevel/go-platform => ./platform
```

Бинарник проекта перечисляет свои модули и запускает хост, пул и клиента шлюза:

```go
mods := []platform.Module{
    admin.New(admin.Config{Doer: runner, Logger: log}),
    workflow.New(workflow.Config{Doer: runner, Logger: log}),
    …
    client.New(client.Config{Doer: runner, Logger: log}),   // собственный пакет проекта
}
handler, err := platform.New(platform.Config{Keys: keys, InFlight: &inFlight}, mods...)
```

**База данных.** Версия db-platform, названная в `platform.DBPlatform`, с модулем `gateway` (`api.authorize_local`, `api.log_request`) для пути «сессия на транзакцию»; без него библиотека откатывается на `api.authorize` и ничего не журналирует.

**Шлюз.** [Gateway API](https://github.com/apostoldevel/module-GatewayAPI) в worker-процессе Апостола, с сетью модуля в его `allowed_cidr`. Без шлюза модуль всё равно отвечает на `/api/v2/*` по своему адресу; плоскость управления для локального запуска играет `cmd/gatewaystub`:

```bash
go run ./cmd/gatewaystub -addr 127.0.0.1:4978 -secret stub-secret -audience gateway-stub
```

Тесты
-

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1 -race
```

Интеграционные тесты идут на живой базе db-platform от API-роли, а сессию добывают входом администратора:

```bash
GO_TEST_PG_DSN=postgres://apibot:…@localhost:5432/db \
GO_TEST_ADMIN_DSN=postgres://admin:…@localhost:5432/db \
GO_TEST_DB_PLATFORM_VERSION=$(cat path/to/db-platform/VERSION) \
  go test -tags integration -run Integration ./... -count=1
```

`VERSION`, отличная от `platform.DBPlatform`, — красный тест: константа поднимается в том коммите, который перепрогоняет интеграционные тесты, и никогда отдельно.

[^crm]: **Apostol CRM** — шаблонный проект, построенный на фреймворках [A-POST-OL](https://github.com/apostoldevel/libapostol) (C++20) и [PostgreSQL Framework for Backend Development](https://github.com/apostoldevel/db-platform).
