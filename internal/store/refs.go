package store

import "context"

type City struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

// Category — подкатегория с названием родителя: в формах они сгруппированы.
type Category struct {
	ID         int64  `db:"id"`
	Name       string `db:"name"`
	ParentName string `db:"parent_name"`
	Emoji      string `db:"emoji"`
}

func (s *Store) ListCities(ctx context.Context) ([]City, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT c.id, c.name FROM city c
		JOIN country co ON co.id = c.country_id
		WHERE c.is_active AND co.is_active
		ORDER BY co.name, c.sort_order`)
	return collectAll[City](rows)
}

// ListSubcategories — активные подкатегории в порядке дерева.
func (s *Store) ListSubcategories(ctx context.Context) ([]Category, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT s.id, s.name, p.name AS parent_name, coalesce(p.emoji, '') AS emoji
		FROM category s JOIN category p ON p.id = s.parent_id
		WHERE s.is_active AND p.is_active
		ORDER BY p.sort_order, s.sort_order`)
	return collectAll[Category](rows)
}
