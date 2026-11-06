-- Модель данных MVP (T-10): сущности из «Модели данных» в Notion, кроме Review (v1).
-- Плюс таблицы, которых нет в §9, но которые требуют другие разделы:
-- district (FR-B-03), listing_revision (FR-P-06), favorite (FR-B-06),
-- bot_text (§6.4), audit_log (§12), team_member (роли админки), app_setting.
--
-- Решение от 06.11: объявление заводит только сам исполнитель, поэтому
-- provider.user_id обязателен. Краулер пишет только в lead.
--
-- Перечисления — text + CHECK: новое значение добавляется миграцией без ALTER TYPE.

-- +goose Up

-- География ----------------------------------------------------------------

CREATE TABLE country (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code        text NOT NULL UNIQUE CHECK (code ~ '^[A-Z]{2}$'),  -- ISO 3166-1
    name        text NOT NULL,
    currency    text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),     -- ISO 4217
    timezone    text NOT NULL,
    is_active   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE city (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    country_id  bigint NOT NULL REFERENCES country(id),
    slug        text NOT NULL,
    name        text NOT NULL,
    sort_order  int NOT NULL DEFAULT 0,
    is_active   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (country_id, slug)
);

CREATE TABLE district (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    city_id     bigint NOT NULL REFERENCES city(id),
    slug        text NOT NULL,
    name        text NOT NULL,
    sort_order  int NOT NULL DEFAULT 0,
    is_active   boolean NOT NULL DEFAULT true,
    UNIQUE (city_id, slug)
);

-- Категории: двухуровневое дерево, объявление ссылается на подкатегории ----

CREATE TABLE category (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    parent_id   bigint REFERENCES category(id),
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    emoji       text,
    sort_order  int NOT NULL DEFAULT 0,
    is_active   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX category_parent_idx ON category (parent_id);

-- В каких странах включена категория.
CREATE TABLE category_country (
    category_id bigint NOT NULL REFERENCES category(id) ON DELETE CASCADE,
    country_id  bigint NOT NULL REFERENCES country(id) ON DELETE CASCADE,
    PRIMARY KEY (category_id, country_id)
);

-- Команда Uslugi: доступ в админку, модерация, аутрич ----------------------

CREATE TABLE team_member (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    telegram_id  bigint NOT NULL UNIQUE,
    name         text NOT NULL,
    role         text NOT NULL CHECK (role IN ('admin', 'moderator', 'operator')),
    is_active    boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Пользователи бота и исполнители ------------------------------------------

CREATE TABLE users (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    telegram_id        bigint NOT NULL UNIQUE,
    username           citext,
    first_name         text,
    language_code      text,
    country_id         bigint REFERENCES country(id),
    city_id            bigint REFERENCES city(id),
    start_source       text,            -- метка из deep link src_<tag> при первом /start
    marketing_opt_out  boolean NOT NULL DEFAULT false,
    is_blocked         boolean NOT NULL DEFAULT false,
    blocked_reason     text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_seen_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX users_username_idx ON users (username);

-- Исполнитель всегда привязан к аккаунту Telegram: регистрируется только сам.
CREATE TABLE provider (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id            bigint NOT NULL UNIQUE REFERENCES users(id),
    display_name       text NOT NULL,
    terms_accepted_at  timestamptz,     -- FR-P-04: правила и политика
    listing_limit      int,             -- NULL = общий лимит из app_setting (FR-P-08)
    verified_at        timestamptz,     -- бейдж «Проверен» (v1)
    is_blocked         boolean NOT NULL DEFAULT false,
    blocked_reason     text,
    created_at         timestamptz NOT NULL DEFAULT now()
);

-- Объявления ---------------------------------------------------------------

CREATE TABLE listing (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_id         bigint NOT NULL REFERENCES provider(id),
    status              text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'moderation', 'published', 'rejected', 'paused', 'archived')),
    title               text CHECK (char_length(title) <= 60),
    description         text CHECK (char_length(description) <= 1000),
    price_type          text CHECK (price_type IN ('fixed', 'from', 'negotiable')),
    price_amount        numeric(14, 2) CHECK (price_amount >= 0),
    currency            text CHECK (currency ~ '^[A-Z]{3}$'),
    travels_to_client   boolean NOT NULL DEFAULT false,   -- выезд
    contact_telegram    citext,
    contact_whatsapp    text,
    contact_phone       text,
    contact_instagram   text,
    languages           text[] NOT NULL DEFAULT '{}',
    schedule            text,
    rejection_reason    text,
    version             int NOT NULL DEFAULT 1,
    draft_step          text,           -- шаг формы, на котором остановился исполнитель (FR-P-02)
    submitted_at        timestamptz,
    published_at        timestamptz,
    bumped_at           timestamptz,    -- поднятие (v1)
    actualized_at       timestamptz,    -- последнее «да, актуально» (FR-P-07)
    actualize_asked_at  timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    -- Цена обязательна, кроме «по договорённости».
    CHECK (price_type IS DISTINCT FROM 'negotiable' OR price_amount IS NULL),
    CHECK (price_type NOT IN ('fixed', 'from') OR price_amount IS NOT NULL)
);
CREATE INDEX listing_provider_idx ON listing (provider_id);
CREATE INDEX listing_status_idx ON listing (status);
-- Дедупликация на модерации: совпадение контактов с другими объявлениями (§7).
CREATE INDEX listing_contact_telegram_idx ON listing (contact_telegram);
CREATE INDEX listing_contact_phone_idx ON listing (contact_phone);

-- Одна основная подкатегория и до двух дополнительных; лимит проверяет приложение.
CREATE TABLE listing_category (
    listing_id   bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    category_id  bigint NOT NULL REFERENCES category(id),
    is_primary   boolean NOT NULL DEFAULT false,
    PRIMARY KEY (listing_id, category_id)
);
CREATE UNIQUE INDEX listing_category_one_primary ON listing_category (listing_id) WHERE is_primary;
CREATE INDEX listing_category_category_idx ON listing_category (category_id);

CREATE TABLE listing_city (
    listing_id  bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    city_id     bigint NOT NULL REFERENCES city(id),
    PRIMARY KEY (listing_id, city_id)
);
CREATE INDEX listing_city_city_idx ON listing_city (city_id);

CREATE TABLE listing_district (
    listing_id   bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    district_id  bigint NOT NULL REFERENCES district(id),
    PRIMARY KEY (listing_id, district_id)
);

CREATE TABLE listing_photo (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    listing_id        bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    position          smallint NOT NULL CHECK (position BETWEEN 0 AND 9),  -- до 10 фото
    telegram_file_id  text,             -- чтобы бот отправлял фото без повторной загрузки
    storage_key       text,             -- копия в объектном хранилище (веб-каталог)
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (listing_id, position)
);

-- FR-P-06: правка опубликованного объявления ждёт модерации здесь,
-- а клиенты пока видят текущую версию из listing.
CREATE TABLE listing_revision (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    listing_id        bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    data              jsonb NOT NULL,   -- полный снимок полей, категорий, городов и фото
    status            text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'approved', 'rejected')),
    rejection_reason  text,
    reviewed_by       bigint REFERENCES team_member(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    reviewed_at       timestamptz
);
CREATE UNIQUE INDEX listing_revision_one_pending ON listing_revision (listing_id) WHERE status = 'pending';

CREATE TABLE favorite (
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id  bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, listing_id)
);

-- Жалобы (FR-B-08) -----------------------------------------------------------

CREATE TABLE complaint (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    listing_id        bigint NOT NULL REFERENCES listing(id),
    reporter_user_id  bigint REFERENCES users(id),
    reason            text NOT NULL
                      CHECK (reason IN ('fraud', 'outdated', 'wrong_contacts', 'forbidden', 'other')),
    comment           text,
    status            text NOT NULL DEFAULT 'new'
                      CHECK (status IN ('new', 'resolved', 'dismissed')),
    moderator_id      bigint REFERENCES team_member(id),
    resolution        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    resolved_at       timestamptz
);
CREATE INDEX complaint_status_idx ON complaint (status);

-- Краулер ------------------------------------------------------------------

CREATE TABLE crawler_source (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    url          text NOT NULL UNIQUE,  -- t.me/<chat>
    title        text,
    city_id      bigint REFERENCES city(id),
    is_active    boolean NOT NULL DEFAULT true,
    last_run_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE crawler_run (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id    bigint REFERENCES crawler_source(id),
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    found        int NOT NULL DEFAULT 0,
    new_leads    int NOT NULL DEFAULT 0,
    duplicates   int NOT NULL DEFAULT 0,
    rejected     int NOT NULL DEFAULT 0,
    error        text
);

-- CRM: лиды и касания ------------------------------------------------------

CREATE TABLE lead (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source              text NOT NULL CHECK (source IN ('crawler', 'manual', 'instagram', 'referral')),
    crawler_source_id   bigint REFERENCES crawler_source(id),
    source_url          text,           -- ссылка на исходный пост
    source_text         text,           -- пример поста
    source_posted_at    timestamptz,
    name                text,
    category_id         bigint REFERENCES category(id),
    city_id             bigint REFERENCES city(id),
    telegram_username   citext,
    telegram_user_id    bigint,
    phone               text,
    whatsapp            text,
    instagram           text,
    summary             text,           -- краткое описание от LLM или оператора
    confidence          numeric(3, 2) CHECK (confidence BETWEEN 0 AND 1),
    status              text NOT NULL DEFAULT 'found'
                        CHECK (status IN (
                            'found', 'review', 'qualified', 'invited', 'replied', 'interested',
                            'listing_created', 'published',
                            'declined', 'do_not_contact', 'invalid'
                        )),
    assignee_id         bigint REFERENCES team_member(id),
    invite_token        text UNIQUE,    -- deep link ?start=inv_<token> (FR-P-10)
    invited_at          timestamptz,
    follow_up_at        timestamptz,    -- когда напомнить оператору (FR-M-05)
    provider_id         bigint REFERENCES provider(id),  -- ставится, когда исполнитель зарегистрировался
    listing_id          bigint REFERENCES listing(id),
    notes               text,
    tags                text[] NOT NULL DEFAULT '{}',
    mention_count       int NOT NULL DEFAULT 1,          -- повторные упоминания краулером
    last_mentioned_at   timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lead_status_idx ON lead (status);
CREATE INDEX lead_telegram_username_idx ON lead (telegram_username);
CREATE INDEX lead_telegram_user_id_idx ON lead (telegram_user_id);
CREATE INDEX lead_phone_idx ON lead (phone);
CREATE INDEX lead_assignee_idx ON lead (assignee_id);

-- Касания по лиду. В MVP оператор пишет из Telegram-клиента и отмечает касание
-- здесь (Q-07); позже сюда же ляжет переписка из инбокса через MTProto.
CREATE TABLE lead_message (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lead_id          bigint NOT NULL REFERENCES lead(id) ON DELETE CASCADE,
    direction        text NOT NULL CHECK (direction IN ('outgoing', 'incoming')),
    channel          text NOT NULL DEFAULT 'manual' CHECK (channel IN ('manual', 'mtproto')),
    text             text,
    attachments      jsonb NOT NULL DEFAULT '[]',
    author_id        bigint REFERENCES team_member(id),
    template_id      bigint,          -- FK ниже, после message_template
    is_llm_draft     boolean NOT NULL DEFAULT false,
    delivery_status  text CHECK (delivery_status IN ('sent', 'delivered', 'read', 'failed')),
    sent_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lead_message_lead_idx ON lead_message (lead_id, sent_at);

CREATE TABLE message_template (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind        text NOT NULL CHECK (kind IN ('invite', 'follow_up', 'faq')),
    name        text NOT NULL,
    body        text NOT NULL,          -- с переменными: {{name}}, {{service}}, {{city}}
    language    text NOT NULL DEFAULT 'ru',
    is_active   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE lead_message
    ADD CONSTRAINT lead_message_template_fk FOREIGN KEY (template_id) REFERENCES message_template(id);

-- Аналитика (§11) ------------------------------------------------------------

CREATE TABLE event (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type         text NOT NULL,         -- bot_start, contact_clicked, listing_viewed…
    user_id      bigint REFERENCES users(id),
    object_type  text,
    object_id    bigint,
    source       text,                  -- метка src_<tag>
    props        jsonb NOT NULL DEFAULT '{}',  -- например, канал связи для contact_clicked
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_type_created_idx ON event (type, created_at);
CREATE INDEX event_object_idx ON event (object_type, object_id);

-- Настройки без релиза ------------------------------------------------------

-- Тексты бота меняются из админки (§6.4).
CREATE TABLE bot_text (
    key         text PRIMARY KEY,
    value       text NOT NULL,
    updated_by  bigint REFERENCES team_member(id),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Настраиваемые числа: лимит объявлений, срок актуализации и т. п.
CREATE TABLE app_setting (
    key          text PRIMARY KEY,
    value        jsonb NOT NULL,
    description  text,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Аудит действий модераторов и операторов (§12).
CREATE TABLE audit_log (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id     bigint REFERENCES team_member(id),
    action       text NOT NULL,         -- listing.approve, lead.status_change…
    object_type  text NOT NULL,
    object_id    bigint,
    details      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_object_idx ON audit_log (object_type, object_id);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS app_setting;
DROP TABLE IF EXISTS bot_text;
DROP TABLE IF EXISTS event;
DROP TABLE IF EXISTS lead_message;
DROP TABLE IF EXISTS message_template;
DROP TABLE IF EXISTS lead;
DROP TABLE IF EXISTS crawler_run;
DROP TABLE IF EXISTS crawler_source;
DROP TABLE IF EXISTS complaint;
DROP TABLE IF EXISTS favorite;
DROP TABLE IF EXISTS listing_revision;
DROP TABLE IF EXISTS listing_photo;
DROP TABLE IF EXISTS listing_district;
DROP TABLE IF EXISTS listing_city;
DROP TABLE IF EXISTS listing_category;
DROP TABLE IF EXISTS listing;
DROP TABLE IF EXISTS provider;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS team_member;
DROP TABLE IF EXISTS category_country;
DROP TABLE IF EXISTS category;
DROP TABLE IF EXISTS district;
DROP TABLE IF EXISTS city;
DROP TABLE IF EXISTS country;
