-- Стартовые данные: Вьетнам, Нячанг и Дананг, дерево категорий, настройки.
--
-- Категории — гипотеза из «Каталог и категории», с решениями из открытых вопросов:
--   - нет «Туров и экскурсий» (Q-13) и «Дома» (Q-12);
--   - нет медицины; массаж только релакс и спа, косметология без инъекций (Q-10);
--   - не заведены, пока Q-10 открыт: татуаж, психологи, визовые услуги и визаран.
-- Районы городов не заведены: список согласовать отдельно.

-- +goose Up

INSERT INTO country (code, name, currency, timezone, is_active)
VALUES ('VN', 'Вьетнам', 'VND', 'Asia/Ho_Chi_Minh', true);

INSERT INTO city (country_id, slug, name, sort_order, is_active)
SELECT id, v.slug, v.name, v.sort_order, true
FROM country, (VALUES ('nha-trang', 'Нячанг', 1), ('da-nang', 'Дананг', 2)) AS v(slug, name, sort_order)
WHERE country.code = 'VN';

INSERT INTO category (slug, name, emoji, sort_order) VALUES
    ('beauty',   'Красота',            '💅', 10),
    ('food',     'Еда на заказ',       '🍰', 20),
    ('transport','Транспорт',          '🛵', 30),
    ('docs',     'Документы и право',  '📄', 40),
    ('kids',     'Дети',               '🧸', 50),
    ('sport',    'Спорт и йога',       '🧘', 60),
    ('photo',    'Фото и видео',       '📸', 70);

INSERT INTO category (parent_id, slug, name, sort_order)
SELECT p.id, v.slug, v.name, v.sort_order
FROM (VALUES
    ('beauty',    'manicure',     'Маникюр и педикюр',            1),
    ('beauty',    'brows-lashes', 'Ресницы и брови',              2),
    ('beauty',    'hair',         'Парикмахеры и колористы',      3),
    ('beauty',    'makeup',       'Визаж',                        4),
    ('beauty',    'cosmetology',  'Косметология без инъекций',    5),
    ('beauty',    'massage',      'Массаж: релакс и спа',         6),
    ('beauty',    'depilation',   'Депиляция',                    7),
    ('food',      'cakes',        'Торты и десерты',              1),
    ('food',      'home-food',    'Домашняя еда',                 2),
    ('food',      'dumplings',    'Пельмени и заготовки',         3),
    ('transport', 'transfers',    'Трансферы',                    1),
    ('transport', 'rental',       'Аренда байков и авто',         2),
    ('docs',      'lawyers',      'Юристы',                       1),
    ('docs',      'translators',  'Переводчики',                  2),
    ('docs',      'accounting',   'Бухгалтерия',                  3),
    ('kids',      'nannies',      'Няни',                         1),
    ('kids',      'tutors',       'Репетиторы',                   2),
    ('kids',      'kids-classes', 'Кружки и занятия',             3),
    ('sport',     'trainers',     'Тренеры',                      1),
    ('sport',     'yoga',         'Йога',                         2),
    ('photo',     'photographers','Фотографы',                    1),
    ('photo',     'videographers','Видеографы',                   2)
) AS v(parent_slug, slug, name, sort_order)
JOIN category p ON p.slug = v.parent_slug;

-- Все стартовые категории включены во Вьетнаме.
INSERT INTO category_country (category_id, country_id)
SELECT c.id, co.id FROM category c, country co WHERE co.code = 'VN';

INSERT INTO app_setting (key, value, description) VALUES
    ('listing_limit',        '3',  'Максимум активных объявлений на исполнителя (FR-P-08)'),
    ('actualize_after_days', '60', 'Через сколько дней спрашивать «Объявление актуально?» (FR-P-07)'),
    ('actualize_grace_days', '7',  'Сколько ждать ответа до паузы (FR-P-07)'),
    ('follow_up_after_days', '3',  'Через сколько дней без ответа напомнить оператору (FR-M-05)'),
    ('listings_per_page',    '5',  'Карточек на экран в боте (FR-B-03)');

-- +goose Down
DELETE FROM app_setting;
DELETE FROM category_country;
DELETE FROM category WHERE parent_id IS NOT NULL;
DELETE FROM category;
DELETE FROM city;
DELETE FROM country;
