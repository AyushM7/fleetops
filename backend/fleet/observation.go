package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Callers hold mu. The digest is a concurrency precondition, not a credential.
func (s *Service) observationID() string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%t", s.runtimeID, s.revision, s.poisoned)))
	return hex.EncodeToString(digest[:])
}

func (s *Service) advanceRevision() error {
	if s.revision == ^uint64(0) {
		s.poisoned = true
		return ErrPoisoned
	}
	s.revision++
	return nil
}

func (s *Service) checkObservation(expected []string, required bool) error {
	if !required && len(expected) == 0 {
		return nil
	}
	if len(expected) != 1 || expected[0] == "" || expected[0] != s.observationID() {
		return ErrObservation
	}
	return nil
}
