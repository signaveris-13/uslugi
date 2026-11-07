-- Форма спрашивает тип цены раньше суммы, поэтому у черновика сумма может быть пустой.
-- Для объявлений, отправленных на модерацию и дальше, сумма по-прежнему обязательна.

-- +goose Up
ALTER TABLE listing DROP CONSTRAINT listing_check1;
ALTER TABLE listing ADD CONSTRAINT listing_price_amount_required
    CHECK (status IN ('draft', 'rejected') OR price_type NOT IN ('fixed', 'from') OR price_amount IS NOT NULL);

-- +goose Down
ALTER TABLE listing DROP CONSTRAINT listing_price_amount_required;
ALTER TABLE listing ADD CONSTRAINT listing_check1
    CHECK (price_type NOT IN ('fixed', 'from') OR price_amount IS NOT NULL);
