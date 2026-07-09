package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"household-app/internal/models"
)

type contextKey string

const userContextKey contextKey = "currentUser"

const sessionCookieName = "session_token"
const sessionDuration = 30 * 24 * time.Hour

// RequireAuth schützt eine Route: ohne gültige Session wird auf /login
// umgeleitet. Bei gültiger Session wird der eingeloggte Nutzer in den
// Request-Context gelegt, damit Handler ihn per currentUser(r) abrufen können.
func (h *Handlers) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := h.userFromSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// currentUser liest den in RequireAuth hinterlegten Nutzer aus dem Context.
// Wird nur in geschützten Routen aufgerufen, dort ist er garantiert gesetzt.
func currentUser(r *http.Request) *models.User {
	u, _ := r.Context().Value(userContextKey).(*models.User)
	return u
}

func (h *Handlers) userFromSession(r *http.Request) (*models.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}

	var userID int64
	var expiresAt string
	err = h.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE id = ?`, cookie.Value).
		Scan(&userID, &expiresAt)
	if err != nil {
		return nil, false
	}

	expiry, err := time.Parse("2006-01-02 15:04:05", expiresAt)
	if err != nil || time.Now().After(expiry) {
		return nil, false
	}

	var u models.User
	err = h.db.QueryRow(`SELECT id, name, password_hash, role FROM users WHERE id = ?`, userID).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.Role)
	if err != nil {
		return nil, false
	}

	return &u, true
}

func (h *Handlers) createSession(w http.ResponseWriter, userID int64) error {
	token, err := generateToken()
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(sessionDuration)
	if _, err := h.db.Exec(
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expiresAt.UTC().Format("2006-01-02 15:04:05"),
	); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// LoginPage zeigt das Login-Formular.
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.userFromSession(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := struct{ ErrorMsg string }{ErrorMsg: r.URL.Query().Get("error")}
	if err := h.tmpl.ExecuteTemplate(w, "login.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Login prüft Name/Passwort und legt bei Erfolg eine Session an.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")

	var u models.User
	err := h.db.QueryRow(`SELECT id, name, password_hash, role FROM users WHERE name = ?`, name).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.Role)

	switch {
	case err == sql.ErrNoRows:
		redirectWithMessage(w, r, "/login", "error", "Name oder Passwort falsch.")
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		redirectWithMessage(w, r, "/login", "error", "Name oder Passwort falsch.")
		return
	}

	if err := h.createSession(w, u.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout löscht die Session und leitet zum Login zurück.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		h.db.Exec(`DELETE FROM sessions WHERE id = ?`, cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// RegisterPage zeigt das Registrierungsformular.
func (h *Handlers) RegisterPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.userFromSession(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := struct{ ErrorMsg string }{ErrorMsg: r.URL.Query().Get("error")}
	if err := h.tmpl.ExecuteTemplate(w, "register.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Register legt einen neuen Nutzer an – oder übernimmt einen bestehenden
// "Geister-Nutzer" (Name schon vorhanden, aber noch kein Passwort gesetzt,
// weil er bisher nur über das freie Namensfeld beim Buchen entstanden ist).
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if name == "" {
		redirectWithMessage(w, r, "/register", "error", "Name darf nicht leer sein.")
		return
	}
	if len(password) < 6 {
		redirectWithMessage(w, r, "/register", "error", "Passwort muss mindestens 6 Zeichen haben.")
		return
	}
	if password != passwordConfirm {
		redirectWithMessage(w, r, "/register", "error", "Passwörter stimmen nicht überein.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var existingID int64
	var existingHash string
	err = h.db.QueryRow(`SELECT id, password_hash FROM users WHERE name = ?`, name).Scan(&existingID, &existingHash)

	switch {
	case err == sql.ErrNoRows:
		res, err := h.db.Exec(`INSERT INTO users (name, password_hash, role) VALUES (?, ?, 'member')`, name, string(hash))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		id, err := res.LastInsertId()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.createSession(w, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if existingHash != "" {
		redirectWithMessage(w, r, "/register", "error", "Dieser Name ist bereits vergeben.")
		return
	}

	if _, err := h.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), existingID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.createSession(w, existingID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
