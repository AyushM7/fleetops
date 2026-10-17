package db

import (
	"context"
	"testing"
)

func TestIntegrationReassignmentMigrationPreservesChargingHistory(t *testing.T) {
	pool, _ := testDatabase(t)
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.migrate(context.Background(), migrations[:3]); err != nil {
		t.Fatal(err)
	}
	const payload = `{"ID":"historical","Action":"set_charging","Request":{"RobotID":"R1","StationID":"S1"},"State":"approved"}`
	_, err = pool.Exec(context.Background(), `INSERT INTO fleet_proposals(id,runtime_id,state,execution,payload,submitted_by) VALUES('historical','prior-runtime','approved','succeeded',$1::jsonb,'operator')`, payload)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(context.Background(), `SELECT payload::text FROM fleet_proposals WHERE id='historical'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := pool.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after, execution string
	if err := pool.QueryRow(context.Background(), `SELECT payload::text,execution FROM fleet_proposals WHERE id='historical'`).Scan(&after, &execution); err != nil || before != after || execution != "succeeded" {
		t.Fatal("historical record changed", err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO fleet_proposals(id,runtime_id,state,execution,payload) VALUES('new','runtime','pending','none','{"ID":"new","Action":"reassign_unstarted_job","State":"pending","Reassignment":{"JobID":"J1","FromRobotID":"R1","ToRobotID":"R3"}}')`)
	if err != nil {
		t.Fatal("new action denied", err)
	}
	_, err = pool.Exec(context.Background(), `UPDATE fleet_proposals SET payload=jsonb_set(payload,'{Action}','"unsupported"') WHERE id='new'`)
	if err == nil {
		t.Fatal("unsupported action persisted")
	}
	_, err = pool.Exec(context.Background(), `UPDATE fleet_proposals SET payload=jsonb_set(payload,'{State}','"approved"') WHERE id='new'`)
	if err == nil { t.Fatal("migration removed proposal-state consistency") }
}
