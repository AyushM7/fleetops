package visitor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
)

var errVisitorRetained = errors.New("visitor schema retained for reconciliation")

type visitorSchemaOwner struct {
	name string
	oid  uint32
}

type visitorArchive struct {
	Version    int               `json:"version"`
	Schema     string            `json:"schema"`
	SchemaOID  uint32            `json:"schemaOid"`
	TableNames []string          `json:"tableNames"`
	Proposals  []json.RawMessage `json:"proposals"`
	Audit      []json.RawMessage `json:"audit"`
	AgentRuns  []json.RawMessage `json:"agentRuns"`
	Migrations []json.RawMessage `json:"migrations"`
}

func retainVisitorSchema(schema visitorSchemaOwner, cause error) error {
	return fmt.Errorf("%w: %s: %w", errVisitorRetained, schema.name, cause)
}

func (f visitorFactory) confirmOwner(ctx context.Context, schema visitorSchemaOwner) error {
	suffix := strings.TrimPrefix(schema.name, "fleetops_visitor_")
	if raw, err := hex.DecodeString(suffix); !strings.HasPrefix(schema.name, "fleetops_visitor_") || err != nil || len(raw) != 16 || schema.oid == 0 {
		return errors.New("visitor schema ownership unavailable")
	}
	var owned bool
	err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1 AND oid=$2 AND nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`, schema.name, schema.oid).Scan(&owned)
	if err != nil || !owned {
		return errors.New("visitor schema ownership unconfirmed")
	}
	return nil
}

func (f visitorFactory) archive(pool *db.Pool, schema visitorSchemaOwner, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.confirmOwner(ctx, schema); err != nil {
		return err
	}
	if err := storageReady(f.evidenceDir); err != nil {
		return err
	}
	var size int64
	if err := pool.QueryRow(ctx, `SELECT
 (SELECT coalesce(sum(octet_length(to_jsonb(p)::text)),0) FROM fleet_proposals p)+
 (SELECT coalesce(sum(octet_length(to_jsonb(a)::text)),0) FROM fleet_audit a)+
 (SELECT coalesce(sum(octet_length(to_jsonb(r)::text)),0) FROM fleet_agent_runs r)+
 (SELECT coalesce(sum(octet_length(to_jsonb(m)::text)),0) FROM schema_migrations m)`).Scan(&size); err != nil {
		return err
	}
	if size > 16<<20 {
		return errors.New("visitor archive size budget exceeded")
	}
	var records json.RawMessage
	// One statement gives every application table and migration record the same
	// MVCC snapshot. Unknown tables prevent claiming a complete archive.
	err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
 'version',1,'schema',current_schema(),'schemaOid',(SELECT oid::bigint FROM pg_namespace WHERE nspname=current_schema()),
 'tableNames',(SELECT jsonb_agg(relname ORDER BY relname) FROM pg_class WHERE relnamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema()) AND relkind IN ('r','p')),
 'proposals',(SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY id),'[]') FROM fleet_proposals p),
 'audit',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM fleet_audit a),
 'agentRuns',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY id),'[]') FROM fleet_agent_runs r),
 'migrations',(SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY version),'[]') FROM schema_migrations m))`).Scan(&records)
	if err != nil {
		return err
	}
	if err := verifyVisitorArchive(records, schema); err != nil {
		return err
	}
	id, err := visitorRandom()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "database-final-"+id+".json")
	commit := f.archiveCommit
	if commit == nil {
		commit = commitVisitorArchive
	}
	if err := commit(path, records); err != nil {
		return err
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, records) {
		return errors.New("visitor archive readback unconfirmed")
	}
	if err := verifyVisitorArchive(stored, schema); err != nil {
		return err
	}
	if err := syncVisitorArchiveDirectory(dir); err != nil {
		return err
	}
	return syncVisitorArchiveDirectory(f.evidenceDir)
}

func verifyVisitorArchive(raw []byte, schema visitorSchemaOwner) error {
	var archive visitorArchive
	err := json.Unmarshal(raw, &archive)
	if err != nil || archive.Version != 1 || archive.Schema != schema.name || archive.SchemaOID != schema.oid ||
		strings.Join(archive.TableNames, ",") != "fleet_agent_runs,fleet_audit,fleet_proposals,schema_migrations" ||
		archive.Proposals == nil || archive.Audit == nil || archive.AgentRuns == nil || len(archive.Migrations) == 0 {
		return errors.New("visitor archive completeness unconfirmed")
	}
	return nil
}

func commitVisitorArchive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

func syncVisitorArchiveDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
