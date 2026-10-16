// Package auth is the trusted bearer-token boundary. Configuration contains only hashes.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

type Role string

const (
	Viewer   Role = "viewer"
	Proposer Role = "proposer"
	Operator Role = "operator"
)

type Principal struct {
	Subject string
	Role    Role
}
type Credential struct {
	Subject string `json:"subject"`
	Role    Role   `json:"role"`
	SHA256  string `json:"sha256"`
}
type entry struct {
	principal Principal
	digest    [32]byte
}
type Authenticator struct{ entries []entry }
type contextKey struct{}

var ErrCredentials = errors.New("invalid bearer credentials or hash configuration")

func New(config string) (*Authenticator, error) {
	if len(config) > 32768 {
		return nil, ErrCredentials
	}
	var credentials []Credential
	decoder := json.NewDecoder(strings.NewReader(config))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil || len(credentials) == 0 || len(credentials) > 64 {
		return nil, ErrCredentials
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrCredentials
	}
	a := &Authenticator{}
	seen := map[string]bool{}
	for _, c := range credentials {
		digest, err := hex.DecodeString(c.SHA256)
		if err != nil || len(digest) != 32 || seen[strings.ToLower(c.SHA256)] || c.Subject == "" || strings.TrimSpace(c.Subject) != c.Subject || strings.ContainsFunc(c.Subject, unicode.IsControl) || len(c.Subject) > 128 || (c.Role != Viewer && c.Role != Proposer && c.Role != Operator) {
			return nil, ErrCredentials
		}
		seen[strings.ToLower(c.SHA256)] = true
		e := entry{principal: Principal{c.Subject, c.Role}}
		copy(e.digest[:], digest)
		a.entries = append(a.entries, e)
	}
	return a, nil
}

// Authenticate installs identity only after verification; no public identity setter exists.
func (a *Authenticator) Authenticate(ctx context.Context, token string) (context.Context, error) {
	if len(token) < 32 || len(token) > 512 {
		return ctx, ErrCredentials
	}
	digest := sha256.Sum256([]byte(token))
	var found *Principal
	for _, e := range a.entries {
		if subtle.ConstantTimeCompare(digest[:], e.digest[:]) == 1 {
			p := e.principal
			found = &p
		}
	}
	if found == nil {
		return ctx, ErrCredentials
	}
	return context.WithValue(ctx, contextKey{}, *found), nil
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	return p, ok
}
