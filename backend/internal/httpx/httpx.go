package httpx

import (
	"encoding/json"
	"io"
	"net/http"
)

func WriteOK(w http.ResponseWriter, status int, data any) {
	write(w, status, map[string]any{"ok": true, "data": data})
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{
		"ok": false,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func WriteForbidden(w http.ResponseWriter, userID, name string) {
	write(w, http.StatusForbidden, map[string]any{
		"ok":          false,
		"userId":      userID,
		"displayName": name,
		"error": map[string]string{
			"code":    "FORBIDDEN",
			"message": "這個 LINE 帳號還沒加入",
		},
	})
}

func Decode(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
