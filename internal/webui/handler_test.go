package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUIAndAPIShareHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("frontend"), 0600); err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	handler := NewWithDirectory(api, dir)
	for _, test := range []struct {
		path   string
		status int
	}{{"/", 200}, {"/api/v1/health", 401}, {"/config.json", 404}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s = %d, want %d", test.path, response.Code, test.status)
		}
	}
}
func TestMissingAssetsDoNotServeWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("index.html", []byte("must not be served"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewWithDirectory(http.NotFoundHandler(), "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != 503 {
		t.Fatalf("status=%d", response.Code)
	}
}
