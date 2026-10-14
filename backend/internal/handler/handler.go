package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
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

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"jingfeng/backend/internal/config"
	"jingfeng/backend/internal/httpx"
	"jingfeng/backend/internal/line"
	"jingfeng/backend/internal/parse"
	"jingfeng/backend/internal/stamp"
	"jingfeng/backend/internal/store"
	"jingfeng/backend/internal/threads"
	"jingfeng/backend/internal/webui"
	"jingfeng/backend/internal/week"

	_ "image/gif"
	_ "image/png"
)

type App struct {
	Store          *store.Store
	Config         config.Config
	Client         *http.Client
	mu             sync.Mutex
	tokenCache     map[string]cachedProfile
	previewMu      sync.Mutex
	previewFlights map[string]*previewCall
}

type previewCall struct {
	wg   sync.WaitGroup
	card store.ThreadCard
	err  error
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
	mux.Handle("GET /api/tasks/{id}/thread", a.withUser(a.threadPreview))
	mux.Handle("GET /api/tasks/{id}/thread-image", a.withUser(a.threadImage))
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
			httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "月份不正確")
			return
		}
		offset = n
	}
	if offset > 0 {
		offset = 0
	}
	if offset < -36 {
		offset = -36
	}
	start, end, label := week.Range(time.Now(), offset)
	tasks, err := a.Store.WeekTasks(r.Context(), start, end, user.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	stats, err := a.Store.WeekReplyStats(r.Context(), start, end)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	type person struct {
		Name      string `json:"name"`
		UserID    string `json:"userId"`
		Total     int    `json:"total"`
		Submitted int    `json:"submitted"`
	}
	people := make([]person, 0, len(stats)+1)
	mine := 0
	seenMe := false
	for _, stat := range stats {
		if stat.UserID == user.ID {
			mine = stat.Submitted
			seenMe = true
			people = append([]person{{
				Name:      user.Name,
				UserID:    user.ID,
				Total:     len(tasks),
				Submitted: stat.Submitted,
			}}, people...)
			continue
		}
		name := stat.Name
		if name == "" {
			name = stat.UserID
		}
		people = append(people, person{
			Name:      name,
			UserID:    stat.UserID,
			Total:     len(tasks),
			Submitted: stat.Submitted,
		})
	}
	if !seenMe {
		name := user.Name
		if name == "" {
			name = user.ID
		}
		people = append([]person{{
			Name:      name,
			UserID:    user.ID,
			Total:     len(tasks),
			Submitted: 0,
		}}, people...)
	}
	httpx.WriteOK(w, http.StatusOK, map[string]any{
		"week":      offset,
		"label":     label,
		"total":     len(tasks),
		"submitted": mine,
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
	task, err := a.Store.SetTaskProduct(r.Context(), id, req.ProductID, user.ID, time.Now())
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
	task, err := a.Store.MarkCopied(r.Context(), id, user.ID, user.Name, reply, time.Now())
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
	a.removeProofFiles(id)
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
	account := strings.TrimSpace(r.FormValue("account"))
	if account != "" && (len(account) != 1 || account[0] < '1' || account[0] > '9') {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "請選擇第幾個分身")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "請選擇截圖")
		return
	}
	defer file.Close()
	stamped, err := stamp.Apply(file, code, account)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "這張圖沒辦法讀取，請換 jpg 或 png")
		return
	}
	if err := os.MkdirAll(a.Config.UploadDir, 0o755); err != nil {
		writeStoreErr(w, err)
		return
	}
	path, err := a.proofPath(id, user.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "這個帳號無法儲存截圖")
		return
	}
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
	task, err := a.Store.MarkSubmitted(r.Context(), id, user.ID, user.Name, time.Now())
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
	path, err := a.proofPath(id, user.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_INPUT", "這個帳號無法讀取截圖")
		return
	}
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

func (a *App) threadPreview(w http.ResponseWriter, r *http.Request, _ User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	raw, err := a.Store.TaskThreadsURL(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "找不到這則任務")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "DB", "讀不到任務")
		return
	}
	card, err := a.ensurePreview(r.Context(), raw)
	if err != nil {
		log.Printf("thread preview: %v", err)
		httpx.WriteError(w, http.StatusBadGateway, "THREADS", "暫時讀不到這則貼文")
		return
	}
	httpx.WriteOK(w, http.StatusOK, map[string]any{
		"author":   card.Author,
		"body":     card.Body,
		"hasImage": card.HasImage,
	})
}

func (a *App) threadImage(w http.ResponseWriter, r *http.Request, _ User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	raw, err := a.Store.TaskThreadsURL(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "找不到這則貼文圖")
		return
	}
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	card, _, found, err := a.Store.GetThreadPreview(r.Context(), hash)
	if err != nil || !found || !card.HasImage {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "還沒有貼文圖")
		return
	}
	path, err := a.previewPath(hash)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "還沒有貼文圖")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "還沒有貼文圖")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = io.Copy(w, f)
}

func (a *App) ensurePreview(ctx context.Context, raw string) (store.ThreadCard, error) {
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	card, fetched, found, err := a.Store.GetThreadPreview(ctx, hash)
	if err != nil {
		return store.ThreadCard{}, err
	}
	if found && (card.Body != "" || time.Since(fetched) < 15*time.Minute) {
		return card, nil
	}
	return a.fetchPreview(ctx, hash, raw)
}

func (a *App) fetchPreview(ctx context.Context, hash, raw string) (store.ThreadCard, error) {
	a.previewMu.Lock()
	if a.previewFlights == nil {
		a.previewFlights = map[string]*previewCall{}
	}
	if call, exists := a.previewFlights[hash]; exists {
		a.previewMu.Unlock()
		call.wg.Wait()
		return call.card, call.err
	}
	call := &previewCall{}
	call.wg.Add(1)
	a.previewFlights[hash] = call
	a.previewMu.Unlock()
	defer func() {
		a.previewMu.Lock()
		delete(a.previewFlights, hash)
		a.previewMu.Unlock()
		call.wg.Done()
	}()

	preview, err := threads.Fetch(ctx, raw)
	if err != nil {
		log.Printf("thread fetch: %v", err)
		_ = a.Store.SaveThreadPreview(ctx, hash, "", "", false, time.Now())
		return store.ThreadCard{}, nil
	}
	hasImage := false
	if len(preview.Image) > 0 {
		if jpg, err := toJPEG(preview.Image); err == nil {
			if path, err := a.previewPath(hash); err == nil {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil && os.WriteFile(path, jpg, 0o644) == nil {
					hasImage = true
				}
			}
		}
	}
	card := store.ThreadCard{Author: preview.Author, Body: preview.Body, HasImage: hasImage}
	if err := a.Store.SaveThreadPreview(ctx, hash, card.Author, card.Body, hasImage, time.Now()); err != nil {
		call.err = err
		call.card = card
		return card, err
	}
	call.card = card
	return card, nil
}

func (a *App) previewPath(hash string) (string, error) {
	if len(hash) != 64 {
		return "", fmt.Errorf("bad hash")
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("bad hash")
		}
	}
	return filepath.Join(a.Config.UploadDir, "previews", hash+".jpg"), nil
}

func toJPEG(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		img, err = webp.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
	}
	bounds := img.Bounds()
	edge := bounds.Dx()
	if bounds.Dy() > edge {
		edge = bounds.Dy()
	}
	if edge > 480 && edge > 0 {
		scale := 480.0 / float64(edge)
		dst := image.NewRGBA(image.Rect(0, 0, int(float64(bounds.Dx())*scale), int(float64(bounds.Dy())*scale)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *App) proofPath(taskID int64, userID string) (string, error) {
	if !safeUserID(userID) {
		return "", fmt.Errorf("unsafe user id")
	}
	return filepath.Join(a.Config.UploadDir, fmt.Sprintf("%d_%s.jpg", taskID, userID)), nil
}

func (a *App) removeProofFiles(taskID int64) {
	dir := a.Config.UploadDir
	_ = os.Remove(filepath.Join(dir, fmt.Sprintf("%d.jpg", taskID)))
	matches, _ := filepath.Glob(filepath.Join(dir, fmt.Sprintf("%d_*.jpg", taskID)))
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

func (a *App) MigrateProofFiles(ctx context.Context) error {
	refs, err := a.Store.ListProofs(ctx)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		next, err := a.proofPath(ref.TaskID, ref.UserID)
		if err != nil {
			continue
		}
		if _, err := os.Stat(next); err == nil {
			continue
		}
		old := filepath.Join(a.Config.UploadDir, fmt.Sprintf("%d.jpg", ref.TaskID))
		if _, err := os.Stat(old); err != nil {
			continue
		}
		if err := os.Rename(old, next); err != nil {
			return err
		}
	}
	return nil
}

func safeUserID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
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
		httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "LINE 登入已失效，請重新登入")
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
