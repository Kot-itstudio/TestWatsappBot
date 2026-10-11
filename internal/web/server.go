package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/user/wabot/internal/calls"
	"github.com/user/wabot/internal/commands"
	"github.com/user/wabot/internal/config"
	"github.com/user/wabot/internal/wa"
)

type Server struct {
	cfg     *config.Config
	sock    *wa.Client
	router  *commands.Router
	calls   *calls.Manager
	tmpl    *template.Template
	mu      sync.Mutex
	events  []string
}

func NewServer(cfg *config.Config, sock *wa.Client, router *commands.Router, cm *calls.Manager) (*Server, error) {
	s := &Server{cfg: cfg, sock: sock, router: router, calls: cm}
	tmpl, err := template.New("index").Funcs(template.FuncMap{
		"seq": func(n int) []int { return make([]int, n) },
	}).Parse(indexHTML)
	if err != nil {
		return nil, fmt.Errorf("не удалось разобрать шаблон: %w", err)
	}
	s.tmpl = tmpl
	return s, nil
}

func (s *Server) AddEvent(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, text)
	if len(s.events) > 200 {
		s.events = s.events[len(s.events)-200:]
	}
}

func (s *Server) Serve(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/command", s.handleCommand)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/logout", s.handleLogout)
	server := &http.Server{Addr: addr, Handler: mux}
	fmt.Printf("[web] панель управления: http://%s\n", addr)
	return server.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]interface{}{
		"Prefix": s.cfg.Prefix,
		"State":  s.sock.State().String(),
	}
	if err := s.tmpl.Execute(w, data); err != nil {
		fmt.Printf("[web] ошибка шаблона: %v\n", err)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := calls.Status{State: "disabled"}
	if s.calls != nil {
		st = s.calls.Status()
	}
	resp := map[string]interface{}{
		"state": s.sock.State().String(),
		"jid":   s.sock.JID(),
		"qr":    s.sock.QR(),
		"call":  st,
	}
	writeJSON(w, resp)
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		http.Error(w, "empty command", http.StatusBadRequest)
		return
	}
	s.AddEvent("> " + text)
	jid := s.sock.JID()
	if jid == "" {
		jid = "self"
	}
	s.router.HandleCommand(jid, text)
	writeJSON(w, map[string]string{"ok": "true"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	s.mu.Lock()
	events := s.events
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	s.mu.Unlock()
	writeJSON(w, map[string]interface{}{"events": events})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.sock.Logout(); err != nil {
		fmt.Printf("[web] ошибка выхода: %v\n", err)
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Printf("[web] ошибка json: %v\n", err)
	}
}
