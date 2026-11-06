-- Вход в админку через бота: браузер получает одноразовый токен,
-- команда подтверждает его в Telegram по ссылке ?start=login_<token>.

-- +goose Up
CREATE TABLE admin_login (
    token           text PRIMARY KEY,
    team_member_id  bigint REFERENCES team_member(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    confirmed_at    timestamptz,
    used_at         timestamptz           -- токен обменян на сессию, повторно не работает
);

CREATE TABLE admin_session (
    token_hash      text PRIMARY KEY,     -- sha256 от значения cookie; само значение не храним
    team_member_id  bigint NOT NULL REFERENCES team_member(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    last_seen_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_session_member_idx ON admin_session (team_member_id);

-- +goose Down
DROP TABLE IF EXISTS admin_session;
DROP TABLE IF EXISTS admin_login;
