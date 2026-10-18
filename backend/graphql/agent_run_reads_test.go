package graphql

import (
	"errors"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestAgentReadErrorsDistinguishAuthorizationFromOutages(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{fleet.ErrUnauthorized, "FORBIDDEN"}, {fleet.ErrPersistence, "UNAVAILABLE"}, {errors.New("private provider bearer secret"), "UNAVAILABLE"}} {
		got, ok := agentRunReadError(tc.err).(*gqlerror.Error)
		if !ok || got.Extensions["code"] != tc.code || strings.Contains(got.Message, "secret") || strings.Contains(got.Message, "bearer") {
			t.Fatal(got)
		}
	}
}
