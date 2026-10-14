package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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
	ID            int64     `json:"id"`
	Code          string    `json:"code"`
	URL           string    `json:"url"`
	ProductText   string    `json:"productText"`
	ProductID     *int64    `json:"productId"`
	ReplyText     string    `json:"replyText"`
	Status        string    `json:"status"`
	CreatedBy     string    `json:"createdBy"`
	CreatedByName string    `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
	HasProof      bool      `json:"hasProof"`
	Unmatched     bool      `json:"unmatched"`
	Duplicate     bool      `json:"duplicate"`
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
		res, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (code, code_year, threads_url, product_text, product_id, reply_text, status, created_by, created_by_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, '', 'pending', ?, ?, ?, ?)`,
			code, codeYear, item.item.URL, productText, productID, userID, name, now, now)
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

func (s *Store) WeekTasks(ctx context.Context, start, end time.Time) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.code, t.threads_url, t.product_text, t.product_id, t.reply_text, t.status,
		       t.created_by, t.created_by_name, t.created_at, IF(p.task_id IS NULL, 0, 1)
		FROM tasks t
		LEFT JOIN proofs p ON p.task_id = t.id
		WHERE t.created_at >= ? AND t.created_at < ?
		ORDER BY t.created_at, t.id`, start, end)
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
	return tasks, rows.Err()
}

func (s *Store) SetTaskProduct(ctx context.Context, taskID, productID int64, now time.Time) (Task, error) {
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
	return s.taskByID(ctx, taskID)
}

func (s *Store) MarkCopied(ctx context.Context, taskID int64, reply string, now time.Time) (Task, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET reply_text = ?, updated_at = ?,
		    status = CASE WHEN status = 'submitted' THEN status ELSE 'copied' END
		WHERE id = ?`, reply, now, taskID)
	if err != nil {
		return Task{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Task{}, sql.ErrNoRows
	}
	return s.taskByID(ctx, taskID)
}

func (s *Store) MarkSubmitted(ctx context.Context, taskID int64, now time.Time) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE tasks SET status = 'submitted', updated_at = ? WHERE id = ?", now, taskID)
	if err != nil {
		return Task{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Task{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO proofs (task_id, created_at) VALUES (?, ?) AS new
		ON DUPLICATE KEY UPDATE created_at = new.created_at`, taskID, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.taskByID(ctx, taskID)
}

func (s *Store) TaskCode(ctx context.Context, id int64) (string, error) {
	var code string
	err := s.db.QueryRowContext(ctx, "SELECT code FROM tasks WHERE id = ?", id).Scan(&code)
	return code, err
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

func (s *Store) taskByID(ctx context.Context, id int64) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.code, t.threads_url, t.product_text, t.product_id, t.reply_text, t.status,
		       t.created_by, t.created_by_name, t.created_at, IF(p.task_id IS NULL, 0, 1)
		FROM tasks t
		LEFT JOIN proofs p ON p.task_id = t.id
		WHERE t.id = ?`, id)
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
	err := row.Scan(&task.ID, &task.Code, &task.URL, &task.ProductText, &productID, &task.ReplyText, &task.Status, &task.CreatedBy, &task.CreatedByName, &task.CreatedAt, &has)
	if err != nil {
		return Task{}, err
	}
	if productID.Valid {
		id := productID.Int64
		task.ProductID = &id
	}
	task.HasProof = has == 1
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
