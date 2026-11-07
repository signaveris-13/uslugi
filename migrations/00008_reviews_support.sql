-- +goose Up
-- Отзывы клиентов (FR-B-07) и обращения в поддержку. Отзыв и ответ мастера
-- публикуются только после модерации. Оставить отзыв может тот, кто писал мастеру
-- через Uslugi (событие contact_clicked) — проверяет приложение.

CREATE TABLE review (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    listing_id        bigint NOT NULL REFERENCES listing(id) ON DELETE CASCADE,
    author_user_id    bigint NOT NULL REFERENCES users(id),
    rating            smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    tags              text[] NOT NULL DEFAULT '{}',
    text              text CHECK (char_length(text) <= 1000),
    show_name         boolean NOT NULL DEFAULT true,
    status            text NOT NULL DEFAULT 'moderation'
                      CHECK (status IN ('moderation', 'published', 'rejected')),
    rejection_reason  text,
    reply_text        text CHECK (char_length(reply_text) <= 1000),
    reply_status      text CHECK (reply_status IN ('moderation', 'published', 'rejected')),
    reply_rejection   text,
    reply_at          timestamptz,
    disputed_at       timestamptz,   -- мастер оспорил отзыв
    dispute_reason    text,
    reviewed_by       bigint REFERENCES team_member(id),
    reviewed_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (listing_id, author_user_id)
);
CREATE INDEX review_listing_idx ON review (listing_id, status);
CREATE INDEX review_queue_idx ON review (created_at) WHERE status = 'moderation' OR reply_status = 'moderation';

CREATE TABLE support_ticket (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users(id),
    topic       text NOT NULL CHECK (topic IN ('listing', 'reviews', 'provider', 'bug', 'idea', 'other')),
    listing_id  bigint REFERENCES listing(id) ON DELETE SET NULL,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'answered', 'closed')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_ticket_user_idx ON support_ticket (user_id, created_at DESC);
CREATE INDEX support_ticket_status_idx ON support_ticket (status, updated_at);

CREATE TABLE support_message (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ticket_id       bigint NOT NULL REFERENCES support_ticket(id) ON DELETE CASCADE,
    from_team       boolean NOT NULL DEFAULT false,
    team_member_id  bigint REFERENCES team_member(id),
    text            text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 2000),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX support_message_ticket_idx ON support_message (ticket_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS support_message;
DROP TABLE IF EXISTS support_ticket;
DROP TABLE IF EXISTS review;
