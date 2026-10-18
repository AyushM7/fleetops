package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"testing/fstest"
)

func fixtureMigrations(t *testing.T, files map[string]string) []migration {
	t.Helper()
	source := fstest.MapFS{}
	for name, sql := range files {
		source["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	}
	migrations, err := loadMigrations(source)
	if err != nil {
		t.Fatal(err)
	}
	return migrations
}

func TestMigrationCatalogOrderingAndChecksums(t *testing.T) {
	migrations := fixtureMigrations(t, map[string]string{"10_second.sql": "SELECT 2;", "2_first.sql": "SELECT 1;"})
	if len(migrations) != 2 || migrations[0].version != 2 || migrations[1].version != 10 {
		t.Fatal("versions not sorted numerically")
	}
	digest := sha256.Sum256([]byte("SELECT 1;"))
	if migrations[0].checksum != hex.EncodeToString(digest[:]) {
		t.Fatal("checksum is not SHA-256 of exact SQL bytes")
	}
	embedded, err := loadMigrations(migrationFiles)
	if err != nil || len(embedded) != 5 || embedded[0].version != 1 || embedded[1].version != 2 || embedded[2].version != 3 || embedded[3].version != 4 || embedded[4].version != 5 {
		t.Fatalf("embedded foundation: %v", err)
	}
}

func TestInvalidMigrationCatalog(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  error
	}{
		{name: "duplicate version", files: map[string]string{"1_one.sql": "SELECT 1", "001_other.sql": "SELECT 2"}, want: ErrDuplicateVersion},
		{name: "zero", files: map[string]string{"0_zero.sql": "SELECT 1"}, want: ErrInvalidMigration},
		{name: "bad filename", files: map[string]string{"first.sql": "SELECT 1"}, want: ErrInvalidMigration},
		{name: "overflow", files: map[string]string{"99999999999999999999_big.sql": "SELECT 1"}, want: ErrInvalidMigration},
		{name: "empty SQL", files: map[string]string{"1_empty.sql": " \n"}, want: ErrInvalidMigration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fstest.MapFS{}
			for name, sql := range tc.files {
				source["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
			}
			if _, err := loadMigrations(source); !errors.Is(err, tc.want) {
				t.Fatalf("catalog: %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMigrationHistoryValidation(t *testing.T) {
	base := fixtureMigrations(t, map[string]string{"2_first.sql": "SELECT 1"})
	if err := validateHistory(base, map[int64]migration{2: base[0]}); err != nil {
		t.Fatal(err)
	}
	changed := fixtureMigrations(t, map[string]string{"2_first.sql": "SELECT 1;"})
	if err := validateHistory(changed, map[int64]migration{2: base[0]}); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatal(err)
	}
	if err := validateHistory(nil, map[int64]migration{2: base[0]}); !errors.Is(err, ErrMigrationHistory) {
		t.Fatal(err)
	}
	lower := fixtureMigrations(t, map[string]string{"1_earlier.sql": "SELECT 0", "2_first.sql": "SELECT 1"})
	if err := validateHistory(lower, map[int64]migration{2: base[0]}); !errors.Is(err, ErrMigrationOrder) {
		t.Fatal(err)
	}
}
