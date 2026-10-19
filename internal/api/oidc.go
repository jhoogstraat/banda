package api

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/jhoogstraat/banda/internal/store"
)

const (
	sessionCookie = "banda_session"
	flowCookie    = "banda_oidc" // state, PKCE verifier and next, only during a login
	sessionTTL    = 7 * 24 * time.Hour
)

// OIDCConfig configures single sign-on. SessionSecret may be empty.
type OIDCConfig struct {
	Issuer, ClientID, ClientSecret, PublicURL, SessionSecret string
}

// OIDC signs users in through any OIDC provider (authorization code flow
// with PKCE) and authenticates API calls by a signed session cookie.
type OIDC struct {
	store    *store.Store
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	secret   []byte
	secure   bool
}

func NewOIDC(ctx context.Context, st *store.Store, c OIDCConfig) (*OIDC, error) {
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	secret := []byte(c.SessionSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		rand.Read(secret)
		log.Print("SESSION_SECRET is empty: using a random one, sessions won't survive a restart")
	}
	return &OIDC{
		store:    st,
		verifier: p.Verifier(&oidc.Config{ClientID: c.ClientID}),
		secret:   secret,
		secure:   strings.HasPrefix(c.PublicURL, "https://"),
		oauth: oauth2.Config{
			ClientID:     c.ClientID,
			ClientSecret: c.ClientSecret,
			Endpoint:     p.Endpoint(),
			RedirectURL:  c.PublicURL + "/auth/callback",
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
	}, nil
}

// Mount adds the browser routes of the login flow.
func (o *OIDC) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", o.login)
	mux.HandleFunc("GET /auth/callback", o.callback)
	mux.HandleFunc("GET /auth/logout", o.logout)
}

func (o *OIDC) login(w http.ResponseWriter, r *http.Request) {
	state, verifier := rand.Text(), oauth2.GenerateVerifier()
	flow := url.Values{"state": {state}, "verifier": {verifier}, "next": {r.URL.Query().Get("next")}}
	http.SetCookie(w, o.cookie(flowCookie, flow.Encode(), 600))
	http.Redirect(w, r, o.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (o *OIDC) callback(w http.ResponseWriter, r *http.Request) {
	var flow url.Values
	c, err := r.Cookie(flowCookie)
	if err == nil {
		flow, err = url.ParseQuery(c.Value)
	}
	q := r.URL.Query()
	if err != nil || q.Get("state") == "" || q.Get("state") != flow.Get("state") {
		http.Error(w, "invalid login state, try again", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, o.cookie(flowCookie, "", -1))
	fail := func(err error) {
		log.Printf("login: %v", err)
		http.Error(w, "login failed", http.StatusUnauthorized)
	}
	tok, err := o.oauth.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(flow.Get("verifier")))
	if err != nil {
		fail(err)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := o.verifier.Verify(r.Context(), raw)
	if err != nil {
		fail(err)
		return
	}
	var claims struct {
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
		Email    string `json:"email"`
	}
	if err := idt.Claims(&claims); err != nil {
		fail(err)
		return
	}
	u := store.User{ID: idt.Subject, Name: cmp.Or(claims.Name, claims.Username, claims.Email, idt.Subject), Email: claims.Email}
	if err := o.store.UpsertUser(r.Context(), u); err != nil {
		fail(err)
		return
	}
	http.SetCookie(w, o.cookie(sessionCookie, o.sign(u.ID, time.Now().Add(sessionTTL)), int(sessionTTL.Seconds())))
	http.Redirect(w, r, localPath(flow.Get("next")), http.StatusFound)
}

// localPath returns next if it is a plain path on this site, else "/": no
// open redirect. Browsers read backslashes like slashes and drop control
// characters, so those are refused too.
func localPath(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.ContainsFunc(next, func(r rune) bool { return r == '\\' || r < ' ' || r == 0x7f }) {
		return "/"
	}
	return next
}

func (o *OIDC) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, o.cookie(sessionCookie, "", -1))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (o *OIDC) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: o.secure}
}

// sign returns base64url("id|expiry") + "." + base64url(HMAC-SHA256 of the first part).
func (o *OIDC) sign(id string, exp time.Time) string {
	msg := base64.RawURLEncoding.EncodeToString([]byte(id + "|" + strconv.FormatInt(exp.Unix(), 10)))
	return msg + "." + base64.RawURLEncoding.EncodeToString(o.mac(msg))
}

func (o *OIDC) mac(msg string) []byte {
	h := hmac.New(sha256.New, o.secret)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// verify returns the user id of an untampered, unexpired session value.
func (o *OIDC) verify(value string, now time.Time) (string, bool) {
	msg, sig, _ := strings.Cut(value, ".")
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, o.mac(msg)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(msg)
	i := strings.LastIndexByte(string(raw), '|')
	if err != nil || i < 0 {
		return "", false
	}
	exp, err := strconv.ParseInt(string(raw[i+1:]), 10, 64)
	return string(raw[:i]), err == nil && now.Unix() < exp
}

// Interceptor authenticates calls by session cookie. RunnerService calls
// carry their own token and pass through.
func (o *OIDC) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if strings.HasPrefix(spec.Procedure, "/banda.v1.RunnerService/") {
				return next(ctx, spec, stream)
			}
			if info, ok := connect.CallInfoForServerContext(ctx); ok {
				cookies, _ := http.ParseCookie(info.RequestHeader().Get("Cookie"))
				for _, c := range cookies {
					if id, ok := o.verify(c.Value, time.Now()); c.Name == sessionCookie && ok {
						return next(WithUser(ctx, id), spec, stream)
					}
				}
			}
			return connect.NewError(connect.CodeUnauthenticated, "sign in required")
		}
	}
}
