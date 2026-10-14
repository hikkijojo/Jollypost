package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"jingfeng/backend/internal/config"
	"jingfeng/backend/internal/handler"
	"jingfeng/backend/internal/store"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	cfg := config.Load()
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		log.Fatal(err)
	}
	time.Local = loc

	var st *store.Store
	for i := 0; i < 30; i++ {
		st, err = store.Open(cfg.DSN())
		if err == nil {
			err = st.Ping()
		}
		if err == nil {
			break
		}
		log.Printf("waiting for database: %v", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.EnsureSchema(ctx); err != nil {
		log.Fatalf("schema: %v", err)
	}
	if err := st.Seed(ctx, time.Now()); err != nil {
		log.Fatalf("seed: %v", err)
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Fatalf("uploads: %v", err)
	}
	log.Println("database connected")

	app := &handler.App{
		Store:  st,
		Config: cfg,
		Client: &http.Client{Timeout: 12 * time.Second},
	}
	mux := http.NewServeMux()
	app.Routes(mux)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	log.Printf("listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
