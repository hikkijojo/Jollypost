package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"jingfeng/backend/internal/config"
	"jingfeng/backend/internal/httpx"
	"jingfeng/backend/internal/line"
	"jingfeng/backend/internal/parse"
	"jingfeng/backend/internal/stamp"
	"jingfeng/backend/internal/store"
	"jingfeng/backend/internal/webui"
	"jingfeng/backend/internal/week"
)

type App struct {
	Store     *store.Store
	Config    config.Config
	Client    *http.Client
	mu        sync.Mutex
	tokenCache map[string]cachedProfile
}

type User struct {
	ID    string
	Name  string
	Admin bool
}

type cachedProfile struct {
	ID   string
	Name string
	Exp  time.Time
}

func (a *App) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /{$}", a.index)
	mux.Handle("GET /", a.files())
	mux.Handle("GET /api/me", a.withUser(a.me))
	mux.Handle("GET /api/products", a.withUser(a.products))
	mux.Handle("GET /api/categories", a.withAdmin(a.categories))
	mux.Handle("PUT /api/categories/{id}", a.withAdmin(a.updateCategory))
	mux.Handle("POST /api/products", a.withAdmin(a.createProduct))
	mux.Handle("PUT /api/products/{id}", a.withAdmin(a.updateProduct))
	mux.Handle("DELETE /api/products/{id}", a.withAdmin(a.deleteProduct))
	mux.Handle("POST /api/tasks/import", a.withUser(a.importTasks))
	mux.Handle("GET /api/tasks", a.withUser(a.weekTasks))
	mux.Handle("PATCH /api/tasks/{id}", a.withUser(a.patchTask))
	mux.Handle("DELETE /api/tasks/{id}", a.withUser(a.deleteTask))
	mux.Handle("POST /api/tasks/{id}/copy", a.withUser(a.copyTask))
	mux.Handle("POST /api/tasks/{id}/proof", a.withUser(a.uploadProof))
	mux.Handle("GET /api/tasks/{id}/proof", a.withUser(a.getProof))
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Ping(); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "DB", "資料庫還沒準備好")
		return
	}
	httpx.WriteOK(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	b, err := webui.Files.ReadFile("index.html")
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	html := strings.ReplaceAll(string(b), "__LIFF_ID__", a.Config.LiffID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(html))
}

func (a *App) files() http.Handler {
	sub, err := fs.Sub(webui.Files, ".")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "files missing", http.StatusInternalServerError)
		})
	}
	next := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (a *App) me(w http.ResponseWriter, r *http.Request, user User) {
	httpx.WriteOK(w, http.StatusOK, map[string]any{
		"userId":      user.ID,
		"displayName": user.Name,
		"admin":       user.Admin,
	})
}

func (a *App) products(w http.ResponseWriter, r *http.Request, user User) {
	all := r.URL.Query().Get("all") == "1" && user.Admin
	items, err := a.Store.ListProducts(r.Context(), !all)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, items)
}

func (a *App) categories(w http.ResponseWriter, r *http.Request, user User) {
	items, err := a.Store.ListCategories(r.Context())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, items)
}

func (a *App) updateCategory(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name  string   `json:"name"`
		Lines []string `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "請求格式錯誤")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 128 {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "類型名稱要有，而且最多 128 字")
		return
	}
	lines, err := cleanLines(req.Lines)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", err.Error())
		return
	}
	saved, err := a.Store.SaveCategory(r.Context(), store.Category{ID: id, Name: name, Lines: lines}, time.Now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, saved)
}

func (a *App) createProduct(w http.ResponseWriter, r *http.Request, user User) {
	a.saveProduct(w, r, 0)
}

func (a *App) updateProduct(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a.saveProduct(w, r, id)
}

func (a *App) saveProduct(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name       string `json:"name"`
		Enabled    bool   `json:"enabled"`
		CategoryID int64  `json:"categoryId"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "請求格式錯誤")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 128 {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "產品名稱要有，而且最多 128 字")
		return
	}
	saved, err := a.Store.SaveProduct(r.Context(), store.Product{
		ID: id, Name: name, Enabled: req.Enabled, CategoryID: req.CategoryID,
	}, time.Now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, saved)
}

func (a *App) deleteProduct(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.DeleteProduct(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (a *App) importTasks(w http.ResponseWriter, r *http.Request, user User) {
	var req struct {
		Text string `json:"text"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "請求格式錯誤")
		return
	}
	if len(req.Text) > 100000 {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "貼上的內容太長")
		return
	}
	now := time.Now()
	items, err := parse.Parse(req.Text, now.Year())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	tasks, err := a.Store.Import(r.Context(), items, user.ID, user.Name, now)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (a *App) weekTasks(w http.ResponseWriter, r *http.Request, user User) {
	offset := 0
	if raw := r.URL.Query().Get("week"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "週次不正確")
			return
		}
		offset = n
	}
	if offset > 52 {
		offset = 52
	}
	if offset < -52 {
		offset = -52
	}
	start, end, label := week.Range(time.Now(), offset)
	tasks, err := a.Store.WeekTasks(r.Context(), start, end)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	submitted := 0
	type person struct {
		Name      string `json:"name"`
		UserID    string `json:"userId"`
		Total     int    `json:"total"`
		Submitted int    `json:"submitted"`
	}
	order := []string{}
	byID := map[string]*person{}
	for _, task := range tasks {
		if task.Status == "submitted" {
			submitted++
		}
		item, ok := byID[task.CreatedBy]
		if !ok {
			name := task.CreatedByName
			if name == "" {
				name = task.CreatedBy
			}
			item = &person{Name: name, UserID: task.CreatedBy}
			byID[task.CreatedBy] = item
			order = append(order, task.CreatedBy)
		}
		item.Total++
		if task.Status == "submitted" {
			item.Submitted++
		}
	}
	people := make([]person, 0, len(order))
	for _, id := range order {
		people = append(people, *byID[id])
	}
	httpx.WriteOK(w, http.StatusOK, map[string]any{
		"week":      offset,
		"label":     label,
		"total":     len(tasks),
		"submitted": submitted,
		"byUser":    people,
		"tasks":     tasks,
	})
}

func (a *App) patchTask(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ProductID int64 `json:"productId"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "請求格式錯誤")
		return
	}
	if req.ProductID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "請選擇產品")
		return
	}
	task, err := a.Store.SetTaskProduct(r.Context(), id, req.ProductID, time.Now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, task)
}

func (a *App) copyTask(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ReplyText string `json:"replyText"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "請求格式錯誤")
		return
	}
	reply := strings.TrimSpace(req.ReplyText)
	if reply == "" || len([]rune(reply)) > 500 {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "回文不能是空的，而且最多 500 字")
		return
	}
	task, err := a.Store.MarkCopied(r.Context(), id, reply, time.Now())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, task)
}

func (a *App) deleteTask(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.DeleteTask(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	_ = os.Remove(a.proofPath(id))
	httpx.WriteOK(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (a *App) uploadProof(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	code, err := a.Store.TaskCode(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "圖片太大或格式不對，請控制在 20MB 以內")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "請選擇截圖")
		return
	}
	defer file.Close()
	stamped, err := stamp.Apply(file, code)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "這張圖沒辦法讀取，請換 jpg 或 png")
		return
	}
	if err := os.MkdirAll(a.Config.UploadDir, 0o755); err != nil {
		writeStoreErr(w, err)
		return
	}
	path := a.proofPath(id)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, stamped, 0o644); err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		writeStoreErr(w, err)
		return
	}
	task, err := a.Store.MarkSubmitted(r.Context(), id, time.Now())
	if err != nil {
		_ = os.Remove(path)
		writeStoreErr(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, task)
}

func (a *App) getProof(w http.ResponseWriter, r *http.Request, user User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	path := a.proofPath(id)
	f, err := os.Open(path)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "還沒有截圖")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

func (a *App) proofPath(id int64) string {
	return filepath.Join(a.Config.UploadDir, fmt.Sprintf("%d.jpg", id))
}

func (a *App) withUser(next func(http.ResponseWriter, *http.Request, User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := a.authorize(w, r, false)
		if !ok {
			return
		}
		next(w, r, user)
	})
}

func (a *App) withAdmin(next func(http.ResponseWriter, *http.Request, User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := a.authorize(w, r, true)
		if !ok {
			return
		}
		next(w, r, user)
	})
}

func (a *App) authorize(w http.ResponseWriter, r *http.Request, adminOnly bool) (User, bool) {
	if strings.TrimSpace(a.Config.LiffChannelID) == "" {
		httpx.WriteError(w, http.StatusInternalServerError, "LIFF_UNCONFIGURED", "尚未設定 LIFF")
		return User{}, false
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	token, found := strings.CutPrefix(raw, "Bearer ")
	token = strings.TrimSpace(token)
	if !found || token == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "請先用 LINE 登入")
		return User{}, false
	}
	profile, err := a.profile(r.Context(), token)
	if err != nil {
		log.Printf("liff verify: %v", err)
		httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "LINE 登入無效，請重新開啟")
		return User{}, false
	}
	lists := config.CurrentLists(a.Config.EnvFile)
	if _, ok := lists.Allowed[profile.UserID]; !ok {
		httpx.WriteForbidden(w, profile.UserID, profile.Name)
		return User{}, false
	}
	_, isAdmin := lists.Admin[profile.UserID]
	if adminOnly && !isAdmin {
		httpx.WriteError(w, http.StatusForbidden, "FORBIDDEN", "只有管理員可以改話術")
		return User{}, false
	}
	name := profile.Name
	if name == "" {
		name = profile.UserID
	}
	return User{ID: profile.UserID, Name: name, Admin: isAdmin}, true
}

func (a *App) profile(ctx context.Context, token string) (line.Profile, error) {
	now := time.Now()
	a.mu.Lock()
	if a.tokenCache == nil {
		a.tokenCache = map[string]cachedProfile{}
	}
	if len(a.tokenCache) > 500 {
		a.tokenCache = map[string]cachedProfile{}
	}
	if cached, ok := a.tokenCache[token]; ok && now.Before(cached.Exp) {
		a.mu.Unlock()
		return line.Profile{UserID: cached.ID, Name: cached.Name}, nil
	}
	a.mu.Unlock()
	profile, err := line.VerifyIDToken(ctx, a.Client, token, a.Config.LiffChannelID)
	if err != nil {
		return line.Profile{}, err
	}
	a.mu.Lock()
	a.tokenCache[token] = cachedProfile{ID: profile.UserID, Name: profile.Name, Exp: now.Add(5 * time.Minute)}
	a.mu.Unlock()
	return profile, nil
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "編號不正確")
		return 0, false
	}
	return id, true
}

func cleanLines(in []string) ([]string, error) {
	var lines []string
	for _, line := range in {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len([]rune(line)) > 300 {
			return nil, errors.New("每一句最多 300 字")
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, errors.New("至少要有一句話術")
	}
	if len(lines) > 20 {
		return nil, errors.New("最多二十句")
	}
	return lines, nil
}

func writeStoreErr(w http.ResponseWriter, err error) {
	var input parse.InputError
	var storeErr store.InputError
	switch {
	case errors.As(err, &input) || errors.As(err, &storeErr):
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", err.Error())
	case errors.Is(err, sql.ErrNoRows):
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "找不到這筆資料")
	default:
		log.Printf("store: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "處理失敗，請稍後再試")
	}
}
