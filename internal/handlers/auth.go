package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sonofnos/snaplink/internal/store"
)

const (
	sessionCookie = "snaplink_session"
	sessionTTL    = 30 * 24 * time.Hour
	authPerMinute = 10
)

// dummyHash lets Login spend the same bcrypt time for unknown emails as for
// known ones, so response timing doesn't reveal which emails are registered.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("snaplink-dummy-password"), bcrypt.DefaultCost)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (h *Handler) currentUser(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	u, err := h.db.GetSessionUser(r.Context(), hashToken(c.Value))
	if err != nil {
		return store.User{}, false
	}
	return u, true
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(sessionTTL)
	if err := h.db.CreateSession(r.Context(), hashToken(token), userID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// requireJSON blocks cross-site form posts: browsers can't send
// application/json cross-origin without a CORS preflight, which we never
// approve. Together with SameSite=Lax cookies this covers CSRF.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

func (h *Handler) readCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	if !requireJSON(w, r) {
		return credentials{}, false
	}
	ok, err := h.rdb.Allow(r.Context(), "auth:"+h.clientIP(r), authPerMinute, time.Minute)
	if err == nil && !ok {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again in a minute")
		return credentials{}, false
	}
	var c credentials
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return credentials{}, false
	}
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	return c, true
}

func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) {
	c, ok := h.readCredentials(w, r)
	if !ok {
		return
	}
	if addr, err := mail.ParseAddress(c.Email); err != nil || addr.Address != c.Email || len(c.Email) > 254 {
		writeError(w, http.StatusBadRequest, "enter a valid email address")
		return
	}
	// bcrypt only reads the first 72 bytes, so longer passwords are rejected
	// rather than silently truncated.
	if len(c.Password) < 8 || len(c.Password) > 72 {
		writeError(w, http.StatusBadRequest, "password must be 8-72 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	u, err := h.db.CreateUser(r.Context(), c.Email, string(hash))
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "an account with that email already exists")
		return
	}
	if err != nil {
		h.logger.Error("create user failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.startSession(w, r, u.ID); err != nil {
		h.logger.Error("start session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"email": u.Email})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	c, ok := h.readCredentials(w, r)
	if !ok {
		return
	}
	u, err := h.db.GetUserByEmail(r.Context(), c.Email)
	hash := []byte(u.PasswordHash)
	if err != nil {
		hash = dummyHash
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(c.Password)) != nil || err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err := h.startSession(w, r, u.ID); err != nil {
		h.logger.Error("start session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": u.Email})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = h.db.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	// Anonymous is a normal state for this probe, so it answers 200 with an
	// empty email rather than a 401 that the browser logs as a console error.
	u, _ := h.currentUser(r)
	writeJSON(w, http.StatusOK, map[string]string{"email": u.Email})
}
