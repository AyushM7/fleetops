package db

import (
	"context"
	"errors"
	"testing"
)

func TestTransactionRequiresCallback(t *testing.T) {
	if err := (&Pool{}).Transaction(context.Background(), nil); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
}

func TestIntegrationTransactionCommitAndRollback(t *testing.T) {
	t.Parallel()
	pool, _ := testDatabase(t)
	if _, err := pool.Exec(context.Background(), "CREATE TABLE transaction_rows (n int)"); err != nil {
		t.Fatal(err)
	}
	if err := pool.Transaction(context.Background(), func(tx *Tx) error {
		if _, err := tx.Exec("INSERT INTO transaction_rows VALUES ($1)", 1); err != nil {
			return err
		}
		var n int
		return tx.QueryRow("SELECT n FROM transaction_rows").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("test callback abort")
	err := pool.Transaction(context.Background(), func(tx *Tx) error {
		if _, err := tx.Exec("INSERT INTO transaction_rows VALUES (2)"); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) || count(t, pool, "SELECT count(*) FROM transaction_rows") != 1 {
		t.Fatal("callback abort did not roll back")
	}
	err = pool.Transaction(context.Background(), func(tx *Tx) error { _, err := tx.Exec("SELECT 1/0"); return err })
	if !errors.Is(err, ErrQuery) {
		t.Fatal("SQL failure was not sanitized")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pool.Transaction(canceled, func(*Tx) error { t.Fatal("canceled transaction entered callback"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
