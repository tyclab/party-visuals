package owner

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Lock struct{ f *os.File }

func Acquire(root string) (*Lock, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	if err := protect(root); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, "owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lock(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("controller ownership unavailable (another owner or lock error): %w", err)
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() {
	if l != nil && l.f != nil {
		unlock(l.f)
		l.f.Close()
		l.f = nil
	}
}

type Endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}
type Server struct {
	HTTP     *http.Server
	endpoint string
	listener net.Listener
}

func Listen(root string, status func() any, command func(string) error) (*Server, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		l.Close()
		return nil, err
	}
	ep := Endpoint{URL: "http://" + l.Addr().String(), Token: hex.EncodeToString(b)}
	file := filepath.Join(root, "endpoint.json")
	raw, _ := json.Marshal(ep)
	if err = lifecycle.AtomicWrite(file, raw); err != nil {
		l.Close()
		return nil, err
	}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != l.Addr().String() || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+ep.Token)) != 1 {
			http.Error(w, "local authorization required", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(status())
			return
		}
		if r.Method != "POST" || r.URL.Path != "/command" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var req struct {
			Command string `json:"command"`
		}
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil {
			http.Error(w, "invalid command", 400)
			return
		}
		if err := command(req.Command); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		json.NewEncoder(w).Encode(status())
	})
	s := &Server{HTTP: &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}, endpoint: file, listener: l}
	go s.HTTP.Serve(l)
	return s, nil
}
func (s *Server) Close() { s.HTTP.Close(); os.Remove(s.endpoint) }
func Call(root, command string) (json.RawMessage, error) {
	b, err := os.ReadFile(filepath.Join(root, "endpoint.json"))
	if err != nil {
		return nil, errors.New("controller is not running; launch party-visuals app")
	}
	var ep Endpoint
	if json.Unmarshal(b, &ep) != nil {
		return nil, errors.New("invalid controller endpoint")
	}
	u, parseErr := url.Parse(ep.URL)
	if parseErr != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(ep.Token) != 64 {
		return nil, errors.New("invalid local controller endpoint")
	}
	method, path, body := "GET", "/status", ""
	if command != "" {
		method, path = "POST", "/command"
		raw, _ := json.Marshal(map[string]string{"command": command})
		body = string(raw)
	}
	r, err := http.NewRequest(method, ep.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+ep.Token)
	c := http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("controller redirect refused") }}
	res, err := c.Do(r)
	if err != nil {
		return nil, fmt.Errorf("controller unavailable: %w", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil {
		return nil, err
	}
	if len(out) > 65536 {
		return nil, errors.New("controller response too large")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("controller: %s", strings.TrimSpace(string(out)))
	}
	return out, nil
}
