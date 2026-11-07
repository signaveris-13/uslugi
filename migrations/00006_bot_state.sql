-- Состояние диалога с ботом: какой шаг формы ждёт ответа и какое объявление заполняется.

-- +goose Up
CREATE TABLE bot_state (
    user_id            bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    step               text,
    listing_id         bigint REFERENCES listing(id) ON DELETE SET NULL,
    field              text,             -- уточнение шага: например, какой контакт вводим
    return_to_preview  boolean NOT NULL DEFAULT false,  -- шаг открыт из «Исправить»: после него — превью
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- Тексты бота по умолчанию; админка будет их менять (§6.4).
INSERT INTO bot_text (key, value) VALUES
    ('terms', 'Правила Uslugi коротко:
• Uslugi — площадка, а не сторона сделки: о цене и условиях договариваетесь с клиентом напрямую.
• Нельзя размещать запрещённые законом услуги, медицину, интим-услуги и мошенничество.
• Мы храним данные объявления и контакты, чтобы показывать их клиентам. Объявление и свои данные можно удалить в любой момент.
• Объявления проходят модерацию.');

-- +goose Down
DELETE FROM bot_text WHERE key = 'terms';
DROP TABLE IF EXISTS bot_state;
