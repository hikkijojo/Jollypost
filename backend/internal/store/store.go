package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"jingfeng/backend/internal/parse"
	"jingfeng/backend/internal/week"

	"github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schemaSQL string

//go:embed catalog.json
var catalogJSON []byte

type InputError struct {
	Msg string
}

func (e InputError) Error() string { return e.Msg }

type Store struct {
	db *sql.DB
}

type Product struct {
	ID           int64    `json:"id"`
	CategoryID   int64    `json:"categoryId"`
	CategoryName string   `json:"categoryName"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Lines        []string `json:"lines"`
}

type Category struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Lines    []string  `json:"lines"`
	Products []Product `json:"products"`
}

type Task struct {
	ID             int64     `json:"id"`
	Code           string    `json:"code"`
	CodeYear       int       `json:"codeYear"`
	CodeDate       time.Time `json:"codeDate"`
	URL            string    `json:"url"`
	ProductText    string    `json:"productText"`
	ProductID      *int64    `json:"productId"`
	ReplyText      string    `json:"replyText"`
	Status         string    `json:"status"`
	CreatedBy      string    `json:"createdBy"`
	CreatedByName  string    `json:"createdByName"`
	CreatedAt      time.Time `json:"createdAt"`
	HasProof       bool      `json:"hasProof"`
	NeedsReproof   bool      `json:"needsReproof"`
	Unmatched      bool      `json:"unmatched"`
	Duplicate      bool      `json:"duplicate"`
	ProofsCleared  bool      `json:"proofsCleared,omitempty"`
}

type UpdateCodeResult struct {
	Task          Task
	ClearedProofs []ProofRef
}

type ReplyStat struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Submitted int    `json:"submitted"`
}

type ProofRef struct {
	TaskID int64
	UserID string
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping() error { return s.db.Ping() }

func (s *Store) EnsureSchema(ctx context.Context) error {
	for _, part := range strings.Split(schemaSQL, ";") {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return s.migrate(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	var columns int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'products' AND COLUMN_NAME = 'category_id'`).Scan(&columns); err != nil {
		return err
	}
	if columns == 0 {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE products ADD COLUMN category_id BIGINT NULL"); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE products ADD KEY idx_products_category (category_id)"); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS product_lines"); err != nil {
		return err
	}
	var fks int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'products' AND CONSTRAINT_NAME = 'fk_products_category'`).Scan(&fks); err != nil {
		return err
	}
	if fks == 0 {
		if _, err := s.db.ExecContext(ctx, `
			ALTER TABLE products
			ADD CONSTRAINT fk_products_category
			FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE SET NULL`); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS task_replies (
		  task_id BIGINT NOT NULL,
		  user_id VARCHAR(64) NOT NULL,
		  user_name VARCHAR(128) NOT NULL DEFAULT '',
		  reply_text VARCHAR(500) NOT NULL DEFAULT '',
		  status VARCHAR(16) NOT NULL DEFAULT 'pending',
		  updated_at DATETIME NOT NULL,
		  PRIMARY KEY (task_id, user_id),
		  CONSTRAINT fk_task_replies_task FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return err
	}
	var proofUser int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'proofs' AND COLUMN_NAME = 'user_id'`).Scan(&proofUser); err != nil {
		return err
	}
	if proofUser == 0 {
		if _, err := s.db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS proofs_user (
			  task_id BIGINT NOT NULL,
			  user_id VARCHAR(64) NOT NULL,
			  created_at DATETIME NOT NULL,
			  PRIMARY KEY (task_id, user_id),
			  CONSTRAINT fk_proofs_user FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT IGNORE INTO proofs_user (task_id, user_id, created_at)
			SELECT p.task_id, t.created_by, p.created_at
			FROM proofs p
			JOIN tasks t ON t.id = p.task_id`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "RENAME TABLE proofs TO proofs_old, proofs_user TO proofs"); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS proofs_old"); err != nil {
		return err
	}
	var split int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_meta WHERE k = 'reply_split'").Scan(&split); err != nil {
		return err
	}
	if split == 0 {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO task_replies (task_id, user_id, user_name, reply_text, status, updated_at)
			SELECT t.id, t.created_by, t.created_by_name, t.reply_text,
			       CASE WHEN p.task_id IS NULL THEN t.status ELSE 'submitted' END,
			       t.updated_at
			FROM tasks t
			LEFT JOIN proofs p ON p.task_id = t.id AND p.user_id = t.created_by
			WHERE t.status <> 'pending' OR t.reply_text <> '' OR p.task_id IS NOT NULL
			ON DUPLICATE KEY UPDATE task_id = task_replies.task_id`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "INSERT INTO app_meta (k, v) VALUES ('reply_split', '1')"); err != nil {
			return err
		}
	}
	var codeDateCol int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'tasks' AND COLUMN_NAME = 'code_date'`).Scan(&codeDateCol); err != nil {
		return err
	}
	if codeDateCol == 0 {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE tasks ADD COLUMN code_date DATE NULL"); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE tasks SET code_date = STR_TO_DATE(
			  CONCAT(
			    code_year, '-',
			    LPAD(SUBSTRING_INDEX(code, '/', 1), 2, '0'), '-',
			    LPAD(SUBSTRING_INDEX(SUBSTRING_INDEX(code, '-', 1), '/', -1), 2, '0')
			  ),
			  '%Y-%m-%d'
			)
			WHERE code REGEXP '^[0-9]{1,2}/[0-9]{1,2}-[0-9]+$'`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE tasks SET code_date = DATE(created_at) WHERE code_date IS NULL"); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE tasks MODIFY COLUMN code_date DATE NOT NULL"); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE tasks ADD KEY idx_tasks_code_date (code_date)"); err != nil {
			return err
		}
	}
	var needsReproofCol int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_replies' AND COLUMN_NAME = 'needs_reproof'`).Scan(&needsReproofCol); err != nil {
		return err
	}
	if needsReproofCol == 0 {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE task_replies ADD COLUMN needs_reproof TINYINT(1) NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Seed(ctx context.Context, now time.Time) error {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_meta WHERE k = 'catalog_sjf'").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var catalog struct {
		Categories []struct {
			Name     string   `json:"name"`
			Lines    []string `json:"lines"`
			Products []string `json:"products"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM products"); err != nil {
		return err
	}
	for i, category := range catalog.Categories {
		res, err := tx.ExecContext(ctx, "INSERT INTO categories (name, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?)", category.Name, i+1, now, now)
		if err != nil {
			return err
		}
		categoryID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for j, line := range category.Lines {
			if _, err := tx.ExecContext(ctx, "INSERT INTO category_lines (category_id, body, sort_order) VALUES (?, ?, ?)", categoryID, line, j+1); err != nil {
				return err
			}
		}
		for j, name := range category.Products {
			if _, err := tx.ExecContext(ctx, "INSERT INTO products (category_id, name, enabled, sort_order, created_at, updated_at) VALUES (?, ?, 1, ?, ?, ?)", categoryID, name, j+1, now, now); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO app_meta (k, v) VALUES ('catalog_sjf', '1')"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListProducts(ctx context.Context, enabledOnly bool) ([]Product, error) {
	q := `
		SELECT p.id, p.name, p.enabled, IFNULL(p.category_id, 0), IFNULL(c.name, '')
		FROM products p
		LEFT JOIN categories c ON c.id = p.category_id`
	if enabledOnly {
		q += " WHERE p.enabled = 1"
	}
	q += " ORDER BY c.sort_order, c.id, p.sort_order, p.name, p.id"
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &enabled, &p.CategoryID, &p.CategoryName); err != nil {
			return nil, err
		}
		p.Enabled = enabled == 1
		p.Lines = []string{}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if products == nil {
		products = []Product{}
	}
	lines, err := s.linesByCategory(ctx)
	if err != nil {
		return nil, err
	}
	for i := range products {
		products[i].Lines = append([]string{}, lines[products[i].CategoryID]...)
	}
	return products, nil
}

func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name FROM categories ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []Category
	index := map[int64]int{}
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.Name); err != nil {
			return nil, err
		}
		category.Lines = []string{}
		category.Products = []Product{}
		index[category.ID] = len(categories)
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lines, err := s.linesByCategory(ctx)
	if err != nil {
		return nil, err
	}
	for id, bodies := range lines {
		if i, ok := index[id]; ok {
			categories[i].Lines = bodies
		}
	}
	products, err := s.ListProducts(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, product := range products {
		product.Lines = nil
		if i, ok := index[product.CategoryID]; ok {
			categories[i].Products = append(categories[i].Products, product)
		}
	}
	if categories == nil {
		categories = []Category{}
	}
	return categories, nil
}

func (s *Store) linesByCategory(ctx context.Context) (map[int64][]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT category_id, body FROM category_lines ORDER BY category_id, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := map[int64][]string{}
	for rows.Next() {
		var id int64
		var body string
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		lines[id] = append(lines[id], body)
	}
	return lines, rows.Err()
}

func (s *Store) SaveCategory(ctx context.Context, category Category, now time.Time) (Category, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE categories SET name = ?, updated_at = ? WHERE id = ?", category.Name, now, category.ID)
	if err != nil {
		return Category{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Category{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM category_lines WHERE category_id = ?", category.ID); err != nil {
		return Category{}, err
	}
	for i, line := range category.Lines {
		if _, err := tx.ExecContext(ctx, "INSERT INTO category_lines (category_id, body, sort_order) VALUES (?, ?, ?)", category.ID, line, i+1); err != nil {
			return Category{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Category{}, err
	}
	return category, nil
}

func (s *Store) SaveProduct(ctx context.Context, p Product, now time.Time) (Product, error) {
	if p.CategoryID <= 0 {
		return Product{}, InputError{Msg: "請選擇類型"}
	}
	var categoryName string
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM categories WHERE id = ?", p.CategoryID).Scan(&categoryName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Product{}, InputError{Msg: "找不到這個類型"}
		}
		return Product{}, err
	}
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	if p.ID == 0 {
		var sort int
		if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sort_order), 0) + 1 FROM products WHERE category_id = ?", p.CategoryID).Scan(&sort); err != nil {
			return Product{}, err
		}
		res, err := s.db.ExecContext(ctx, "INSERT INTO products (category_id, name, enabled, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)", p.CategoryID, p.Name, enabled, sort, now, now)
		if err != nil {
			return Product{}, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return Product{}, err
		}
		p.ID = id
	} else {
		res, err := s.db.ExecContext(ctx, "UPDATE products SET category_id = ?, name = ?, enabled = ?, updated_at = ? WHERE id = ?", p.CategoryID, p.Name, enabled, now, p.ID)
		if err != nil {
			return Product{}, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return Product{}, sql.ErrNoRows
		}
	}
	p.CategoryName = categoryName
	p.Enabled = enabled == 1
	return p, nil
}

func (s *Store) DeleteProduct(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM products WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Import(ctx context.Context, items []parse.Item, userID, name string, now time.Time) ([]Task, error) {
	products, err := s.ListProducts(ctx, true)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	today := now.Format("2006-01-02")
	start, end, _ := week.Range(now, 0)
	maxToday := 0
	used := map[string]struct{}{}
	type ready struct {
		item     parse.Item
		code     string
		codeYear int
		index    int
	}
	batch := make([]ready, 0, len(items))
	for i, item := range items {
		if item.Code == "" {
			batch = append(batch, ready{item: item, index: i})
			continue
		}
		var month, day, n int
		if _, err := fmt.Sscanf(item.Code, "%d/%d-%d", &month, &day, &n); err != nil {
			return nil, InputError{Msg: fmt.Sprintf("第 %d 則的編號不正確", i+1)}
		}
		key := fmt.Sprintf("%d:%s", now.Year(), item.Code)
		if _, ok := used[key]; ok {
			return nil, InputError{Msg: fmt.Sprintf("第 %d 則的編號 %s 重複了", i+1, item.Code)}
		}
		var existing int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE code_year = ? AND code = ?", now.Year(), item.Code).Scan(&existing)
		if err == nil {
			return nil, InputError{Msg: fmt.Sprintf("第 %d 則的編號 %s 已經用過", i+1, item.Code)}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		used[key] = struct{}{}
		if month == int(now.Month()) && day == now.Day() && n > maxToday {
			maxToday = n
		}
		batch = append(batch, ready{item: item, code: item.Code, codeYear: now.Year(), index: i})
	}
	if maxToday > 0 {
		if err := bumpAtLeast(ctx, tx, today, maxToday); err != nil {
			return nil, err
		}
	}

	out := make([]Task, 0, len(batch))
	seenURL := map[string]bool{}
	for _, item := range batch {
		code := item.code
		codeYear := item.codeYear
		if code == "" {
			n, err := alloc(ctx, tx, today)
			if err != nil {
				return nil, err
			}
			code = fmt.Sprintf("%d/%d-%d", int(now.Month()), now.Day(), n)
			codeYear = now.Year()
			key := fmt.Sprintf("%d:%s", codeYear, code)
			if _, ok := used[key]; ok {
				return nil, InputError{Msg: fmt.Sprintf("編號 %s 已經用過", code)}
			}
			used[key] = struct{}{}
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE threads_url = ? AND created_at >= ? AND created_at < ?", item.item.URL, start, end).Scan(&count); err != nil {
			return nil, err
		}
		duplicate := count > 0 || seenURL[item.item.URL]
		seenURL[item.item.URL] = true

		productText := trimRunes(strings.TrimSpace(item.item.Product), 128)
		var productID *int64
		if matched, ok := matchProduct(products, productText); ok {
			productText = matched.Name
			id := matched.ID
			productID = &id
		}
		month, day, _, ok := parseCodeParts(code)
		if !ok {
			return nil, InputError{Msg: fmt.Sprintf("編號 %s 不正確", code)}
		}
		stamp := time.Date(codeYear, time.Month(month), day, 0, 0, 0, 0, now.Location())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (code, code_year, code_date, threads_url, product_text, product_id, reply_text, status, created_by, created_by_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, '', 'pending', ?, ?, ?, ?)`,
			code, codeYear, stamp, item.item.URL, productText, productID, userID, name, now, now)
		if err != nil {
			if isDup(err) {
				return nil, InputError{Msg: fmt.Sprintf("編號 %s 已經用過", code)}
			}
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		out = append(out, Task{
			ID:            id,
			Code:          code,
			CodeYear:      codeYear,
			CodeDate:      stamp,
			URL:           item.item.URL,
			ProductText:   productText,
			ProductID:     productID,
			Status:        "pending",
			CreatedBy:     userID,
			CreatedByName: name,
			CreatedAt:     now,
			Unmatched:     productText != "" && productID == nil,
			Duplicate:     duplicate,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) WeekTasks(ctx context.Context, start, end time.Time, userID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.code, t.code_year, t.code_date, t.threads_url, t.product_text, t.product_id,
		       COALESCE(r.reply_text, ''), COALESCE(r.status, 'pending'),
		       t.created_by, t.created_by_name, t.created_at, IF(p.task_id IS NULL, 0, 1),
		       COALESCE(r.needs_reproof, 0)
		FROM tasks t
		LEFT JOIN task_replies r ON r.task_id = t.id AND r.user_id = ?
		LEFT JOIN proofs p ON p.task_id = t.id AND p.user_id = ?
		WHERE t.code_date >= ? AND t.code_date < ?`, userID, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if tasks == nil {
		tasks = []Task{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortTasksByCodeDate(tasks)
	return tasks, nil
}

// sortTasksByCodeDate 依編號日期排，同一天再依流水號。
func sortTasksByCodeDate(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		di := tasks[i].CodeDate
		dj := tasks[j].CodeDate
		if di.IsZero() || dj.IsZero() {
			ai, okI := codeSortKey(tasks[i].CodeYear, tasks[i].Code)
			aj, okJ := codeSortKey(tasks[j].CodeYear, tasks[j].Code)
			if okI != okJ {
				return okI
			}
			if okI && ai != aj {
				return ai < aj
			}
			if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
				return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
			}
			return tasks[i].ID < tasks[j].ID
		}
		if !di.Equal(dj) {
			return di.Before(dj)
		}
		_, _, ni, okI := parseCodeParts(tasks[i].Code)
		_, _, nj, okJ := parseCodeParts(tasks[j].Code)
		if okI && okJ && ni != nj {
			return ni < nj
		}
		return tasks[i].ID < tasks[j].ID
	})
}

func codeSortKey(year int, code string) (int, bool) {
	month, day, n, ok := parseCodeParts(code)
	if !ok {
		return 0, false
	}
	if year < 1 {
		year = 0
	}
	return year*1_000_000 + month*10_000 + day*1_000 + n, true
}

func parseCodeParts(code string) (month, day, n int, ok bool) {
	if _, err := fmt.Sscanf(code, "%d/%d-%d", &month, &day, &n); err != nil {
		return 0, 0, 0, false
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || n < 1 || n > 999 {
		return 0, 0, 0, false
	}
	return month, day, n, true
}

func (s *Store) WeekReplyStats(ctx context.Context, start, end time.Time) ([]ReplyStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.user_id, MAX(r.user_name), CAST(SUM(CASE WHEN r.status = 'submitted' THEN 1 ELSE 0 END) AS SIGNED)
		FROM task_replies r
		JOIN tasks t ON t.id = r.task_id
		WHERE t.code_date >= ? AND t.code_date < ?
		GROUP BY r.user_id
		ORDER BY r.user_id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := []ReplyStat{}
	for rows.Next() {
		var stat ReplyStat
		if err := rows.Scan(&stat.UserID, &stat.Name, &stat.Submitted); err != nil {
			return nil, err
		}
		if stat.Name == "" {
			stat.Name = stat.UserID
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (s *Store) ListProofs(ctx context.Context) ([]ProofRef, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT task_id, user_id FROM proofs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []ProofRef
	for rows.Next() {
		var ref ProofRef
		if err := rows.Scan(&ref.TaskID, &ref.UserID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if refs == nil {
		refs = []ProofRef{}
	}
	return refs, rows.Err()
}

func (s *Store) SetTaskProduct(ctx context.Context, taskID, productID int64, userID string, now time.Time) (Task, error) {
	var name string
	var enabled int
	err := s.db.QueryRowContext(ctx, "SELECT name, enabled FROM products WHERE id = ?", productID).Scan(&name, &enabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && enabled != 1) {
		return Task{}, InputError{Msg: "找不到這個產品"}
	}
	if err != nil {
		return Task{}, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE tasks SET product_id = ?, product_text = ?, updated_at = ? WHERE id = ?", productID, name, now, taskID)
	if err != nil {
		return Task{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Task{}, sql.ErrNoRows
	}
	return s.taskByID(ctx, taskID, userID)
}

func (s *Store) MarkCopied(ctx context.Context, taskID int64, userID, userName, reply string, now time.Time) (Task, error) {
	if err := s.requireTask(ctx, taskID); err != nil {
		return Task{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO task_replies (task_id, user_id, user_name, reply_text, status, updated_at)
		VALUES (?, ?, ?, ?, 'copied', ?) AS new
		ON DUPLICATE KEY UPDATE
		  user_name = new.user_name,
		  reply_text = new.reply_text,
		  status = CASE WHEN task_replies.status = 'submitted' THEN task_replies.status ELSE 'copied' END,
		  updated_at = new.updated_at`, taskID, userID, userName, reply, now); err != nil {
		return Task{}, err
	}
	return s.taskByID(ctx, taskID, userID)
}

func (s *Store) MarkSubmitted(ctx context.Context, taskID int64, userID, userName string, now time.Time) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if err := s.requireTaskTx(ctx, tx, taskID); err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_replies (task_id, user_id, user_name, reply_text, status, needs_reproof, updated_at)
		VALUES (?, ?, ?, '', 'submitted', 0, ?) AS new
		ON DUPLICATE KEY UPDATE
		  user_name = new.user_name,
		  status = 'submitted',
		  needs_reproof = 0,
		  updated_at = new.updated_at`, taskID, userID, userName, now); err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO proofs (task_id, user_id, created_at) VALUES (?, ?, ?) AS new
		ON DUPLICATE KEY UPDATE created_at = new.created_at`, taskID, userID, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.taskByID(ctx, taskID, userID)
}

func (s *Store) TaskCode(ctx context.Context, id int64) (string, error) {
	var code string
	err := s.db.QueryRowContext(ctx, "SELECT code FROM tasks WHERE id = ?", id).Scan(&code)
	return code, err
}

func (s *Store) TaskCreatedBy(ctx context.Context, id int64) (string, error) {
	var createdBy string
	err := s.db.QueryRowContext(ctx, "SELECT created_by FROM tasks WHERE id = ?", id).Scan(&createdBy)
	return createdBy, err
}

func (s *Store) UpdateTaskCode(ctx context.Context, taskID int64, year int, code string, viewerID string, now time.Time) (UpdateCodeResult, error) {
	display, err := parse.NormalizeCode(code, year)
	if err != nil {
		var input parse.InputError
		if errors.As(err, &input) {
			return UpdateCodeResult{}, InputError{Msg: input.Msg}
		}
		return UpdateCodeResult{}, InputError{Msg: "編號不正確"}
	}
	month, day, serial, ok := parseCodeParts(display)
	if !ok {
		return UpdateCodeResult{}, InputError{Msg: "編號不正確"}
	}
	stamp := time.Date(year, time.Month(month), day, 0, 0, 0, 0, now.Location())
	if stamp.Year() != year || int(stamp.Month()) != month || stamp.Day() != day {
		return UpdateCodeResult{}, InputError{Msg: "編號裡的日期不存在"}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateCodeResult{}, err
	}
	defer tx.Rollback()

	var curCode string
	var curYear int
	err = tx.QueryRowContext(ctx, "SELECT code, code_year FROM tasks WHERE id = ? FOR UPDATE", taskID).Scan(&curCode, &curYear)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateCodeResult{}, sql.ErrNoRows
	}
	if err != nil {
		return UpdateCodeResult{}, err
	}
	if curCode == display && curYear == year {
		task, err := s.taskByID(ctx, taskID, viewerID)
		if err != nil {
			return UpdateCodeResult{}, err
		}
		return UpdateCodeResult{Task: task}, nil
	}

	var other int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE code_year = ? AND code = ? AND id <> ?", year, display, taskID).Scan(&other)
	if err == nil {
		return UpdateCodeResult{}, InputError{Msg: fmt.Sprintf("編號 %d年 %s 已經有任務在用", year, display)}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return UpdateCodeResult{}, err
	}

	proofRows, err := tx.QueryContext(ctx, "SELECT task_id, user_id FROM proofs WHERE task_id = ?", taskID)
	if err != nil {
		return UpdateCodeResult{}, err
	}
	cleared := []ProofRef{}
	for proofRows.Next() {
		var ref ProofRef
		if err := proofRows.Scan(&ref.TaskID, &ref.UserID); err != nil {
			proofRows.Close()
			return UpdateCodeResult{}, err
		}
		cleared = append(cleared, ref)
	}
	if err := proofRows.Err(); err != nil {
		proofRows.Close()
		return UpdateCodeResult{}, err
	}
	proofRows.Close()

	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks SET code = ?, code_year = ?, code_date = ?, updated_at = ?
		WHERE id = ?`, display, year, stamp, now, taskID); err != nil {
		if isDup(err) {
			return UpdateCodeResult{}, InputError{Msg: fmt.Sprintf("編號 %d年 %s 已經有任務在用", year, display)}
		}
		return UpdateCodeResult{}, err
	}

	if len(cleared) > 0 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM proofs WHERE task_id = ?", taskID); err != nil {
			return UpdateCodeResult{}, err
		}
		for _, ref := range cleared {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_replies (task_id, user_id, user_name, reply_text, status, needs_reproof, updated_at)
				VALUES (?, ?, '', '', 'pending', 1, ?) AS new
				ON DUPLICATE KEY UPDATE
				  needs_reproof = 1,
				  status = CASE WHEN task_replies.reply_text <> '' THEN 'copied' ELSE 'pending' END,
				  updated_at = new.updated_at`, taskID, ref.UserID, now); err != nil {
				return UpdateCodeResult{}, err
			}
		}
	}

	today := now.Format("2006-01-02")
	if stamp.Format("2006-01-02") == today {
		if err := bumpAtLeast(ctx, tx, today, serial); err != nil {
			return UpdateCodeResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return UpdateCodeResult{}, err
	}
	task, err := s.taskByID(ctx, taskID, viewerID)
	if err != nil {
		return UpdateCodeResult{}, err
	}
	task.ProofsCleared = len(cleared) > 0
	return UpdateCodeResult{Task: task, ClearedProofs: cleared}, nil
}

func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) requireTask(ctx context.Context, id int64) error {
	return s.requireTaskTx(ctx, s.db, id)
}

func (s *Store) requireTaskTx(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) error {
	var found int64
	err := q.QueryRowContext(ctx, "SELECT id FROM tasks WHERE id = ?", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) taskByID(ctx context.Context, id int64, userID string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.code, t.code_year, t.code_date, t.threads_url, t.product_text, t.product_id,
		       COALESCE(r.reply_text, ''), COALESCE(r.status, 'pending'),
		       t.created_by, t.created_by_name, t.created_at, IF(p.task_id IS NULL, 0, 1),
		       COALESCE(r.needs_reproof, 0)
		FROM tasks t
		LEFT JOIN task_replies r ON r.task_id = t.id AND r.user_id = ?
		LEFT JOIN proofs p ON p.task_id = t.id AND p.user_id = ?
		WHERE t.id = ?`, userID, userID, id)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, sql.ErrNoRows
	}
	return task, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var productID sql.NullInt64
	var has int
	var needs int
	err := row.Scan(
		&task.ID, &task.Code, &task.CodeYear, &task.CodeDate, &task.URL, &task.ProductText, &productID,
		&task.ReplyText, &task.Status, &task.CreatedBy, &task.CreatedByName, &task.CreatedAt, &has, &needs,
	)
	if err != nil {
		return Task{}, err
	}
	if productID.Valid {
		id := productID.Int64
		task.ProductID = &id
	}
	task.HasProof = has == 1
	task.NeedsReproof = needs == 1
	task.Unmatched = task.ProductText != "" && task.ProductID == nil
	return task, nil
}

func matchProduct(products []Product, text string) (Product, bool) {
	key := parse.NormalizeName(text)
	if key == "" {
		return Product{}, false
	}
	var exact Product
	foundExact := false
	var loose Product
	looseLen := -1
	for _, product := range products {
		pk := parse.NormalizeName(product.Name)
		if pk == "" {
			continue
		}
		if pk == key {
			exact = product
			foundExact = true
			break
		}
		if strings.Contains(pk, key) || strings.Contains(key, pk) {
			if len([]rune(pk)) > looseLen {
				loose = product
				looseLen = len([]rune(pk))
			}
		}
	}
	if foundExact {
		return exact, true
	}
	if looseLen >= 0 {
		return loose, true
	}
	return Product{}, false
}

func bumpAtLeast(ctx context.Context, tx *sql.Tx, day string, atLeast int) error {
	n, ok, err := seqNo(ctx, tx, day)
	if err != nil {
		return err
	}
	if !ok {
		_, err = tx.ExecContext(ctx, "INSERT INTO daily_seq (day, last_no) VALUES (?, ?)", day, atLeast)
		if isDup(err) {
			_, err = tx.ExecContext(ctx, "UPDATE daily_seq SET last_no = GREATEST(last_no, ?) WHERE day = ?", atLeast, day)
		}
		return err
	}
	if n < atLeast {
		_, err = tx.ExecContext(ctx, "UPDATE daily_seq SET last_no = ? WHERE day = ?", atLeast, day)
	}
	return err
}

func alloc(ctx context.Context, tx *sql.Tx, day string) (int, error) {
	n, ok, err := seqNo(ctx, tx, day)
	if err != nil {
		return 0, err
	}
	if !ok {
		_, err = tx.ExecContext(ctx, "INSERT INTO daily_seq (day, last_no) VALUES (?, 1)", day)
		if err == nil {
			return 1, nil
		}
		if !isDup(err) {
			return 0, err
		}
		n, ok, err = seqNo(ctx, tx, day)
		if err != nil || !ok {
			return 0, err
		}
	}
	next := n + 1
	if _, err := tx.ExecContext(ctx, "UPDATE daily_seq SET last_no = ? WHERE day = ?", next, day); err != nil {
		return 0, err
	}
	return next, nil
}

func seqNo(ctx context.Context, tx *sql.Tx, day string) (int, bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT last_no FROM daily_seq WHERE day = ? FOR UPDATE", day).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

type ThreadCard struct {
	Author   string
	Body     string
	HasImage bool
}

func (s *Store) TaskThreadsURL(ctx context.Context, id int64) (string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT threads_url FROM tasks WHERE id = ?", id).Scan(&raw)
	return raw, err
}

func (s *Store) GetThreadPreview(ctx context.Context, hash string) (ThreadCard, time.Time, bool, error) {
	var card ThreadCard
	var has int
	var at time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT author, body, has_image, fetched_at FROM thread_previews WHERE url_hash = ?`, hash).
		Scan(&card.Author, &card.Body, &has, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ThreadCard{}, time.Time{}, false, nil
	}
	if err != nil {
		return ThreadCard{}, time.Time{}, false, err
	}
	card.HasImage = has == 1
	return card, at, true, nil
}

func (s *Store) SaveThreadPreview(ctx context.Context, hash, author, body string, hasImage bool, at time.Time) error {
	flag := 0
	if hasImage {
		flag = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO thread_previews (url_hash, author, body, has_image, fetched_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		  author = VALUES(author), body = VALUES(body), has_image = VALUES(has_image), fetched_at = VALUES(fetched_at)`,
		hash, trimRunes(author, 120), trimRunes(body, 500), flag, at)
	return err
}

type Member struct {
	UserID      string    `json:"userId"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *Store) SaveProfile(ctx context.Context, userID, name string, now time.Time) error {
	name = trimRunes(strings.TrimSpace(name), 128)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO profiles (user_id, display_name, seen_at) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
		  display_name = IF(VALUES(display_name) = '', display_name, VALUES(display_name)),
		  seen_at = VALUES(seen_at)`, userID, name, now)
	return err
}

func (s *Store) ProfileNames(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, "SELECT user_id, display_name FROM profiles WHERE user_id IN ("+marks+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func (s *Store) IsMember(ctx context.Context, userID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM members WHERE user_id = ?", userID).Scan(&n)
	return n > 0, err
}

func (s *Store) ListMembers(ctx context.Context) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, display_name, created_at FROM members ORDER BY created_at, user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var item Member
		if err := rows.Scan(&item.UserID, &item.DisplayName, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if out == nil {
		out = []Member{}
	}
	return out, rows.Err()
}

func (s *Store) AddMember(ctx context.Context, userID, name string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO members (user_id, display_name, created_at) VALUES (?, ?, ?)`,
		userID, trimRunes(strings.TrimSpace(name), 128), now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return InputError{Msg: "已經在名單裡"}
	}
	return nil
}

func (s *Store) DeleteMember(ctx context.Context, userID string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM members WHERE user_id = ?", userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) TouchMemberName(ctx context.Context, userID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE members SET display_name = ? WHERE user_id = ? AND display_name <> ?`,
		trimRunes(name, 128), userID, name)
	return err
}

func (s *Store) ImportMembersOnce(ctx context.Context, ids []string, now time.Time) error {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_meta WHERE k = 'members_imported'").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO members (user_id, display_name, created_at) VALUES (?, '', ?)`, id, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO app_meta (k, v) VALUES ('members_imported', '1')"); err != nil {
		return err
	}
	return tx.Commit()
}

func isDup(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
