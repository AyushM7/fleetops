package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestHashedAuthentication(t *testing.T) {
	token := strings.Repeat("test-only-randomness-not-a-secret", 2)
	hash := sha256.Sum256([]byte(token))
	raw, _ := json.Marshal([]Credential{{Subject: "alice", Role: Proposer, SHA256: hex.EncodeToString(hash[:])}})
	a, err := New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := a.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := FromContext(ctx)
	if !ok || p.Subject != "alice" || p.Role != Proposer {
		t.Fatal(p)
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("untrusted context")
	}
	for _, token := range []string{"", strings.Repeat("x", 64), strings.Repeat("x", 513)} {
		if _, err := a.Authenticate(ctx, token); err == nil {
			t.Fatal("accepted invalid token")
		}
	}
	for _, raw := range []string{"", "null", "[]", string(raw) + "{}", `[{"subject":"alice","role":"admin","sha256":"bad"}]`, strings.Replace(string(raw), "alice", " ", 1), strings.Replace(string(raw), "sha256", "token", 1), string(append(append([]byte{}, raw[:len(raw)-1]...), append([]byte(","), raw[1:]...)...))} {
		if _, err := New(raw); err == nil {
			t.Fatalf("accepted bad config")
		}
	}
}
