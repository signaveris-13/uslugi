-- Решение от 06.11 по Q-10: татуаж, психологи и визовые услуги (включая визаран) разрешены.
-- «Спорт и йога» возвращается к исходному названию «Здоровье и спорт»: в нём снова психологи.

-- +goose Up
UPDATE category SET name = 'Здоровье и спорт' WHERE slug = 'sport';

INSERT INTO category (parent_id, slug, name, sort_order)
SELECT p.id, v.slug, v.name, v.sort_order
FROM (VALUES
    ('beauty',    'tattoo-pmu',   'Татуаж',             8),
    ('sport',     'psychologists','Психологи',          3),
    ('docs',      'visas',        'Визы и продление',   0),
    ('transport', 'visa-run',     'Визаран',            3)
) AS v(parent_slug, slug, name, sort_order)
JOIN category p ON p.slug = v.parent_slug;

INSERT INTO category_country (category_id, country_id)
SELECT c.id, co.id
FROM category c, country co
WHERE co.code = 'VN' AND c.slug IN ('tattoo-pmu', 'psychologists', 'visas', 'visa-run');

-- +goose Down
DELETE FROM category WHERE slug IN ('tattoo-pmu', 'psychologists', 'visas', 'visa-run');
UPDATE category SET name = 'Спорт и йога' WHERE slug = 'sport';
