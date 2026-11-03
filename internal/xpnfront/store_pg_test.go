package xpnfront

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/openpak/x360-common/pgstate"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("X360_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("X360_TEST_DATABASE_URL not set")
	}
	return dsn
}

func pgSchema(t *testing.T) string {
	dsn := testDSN(t)
	b := make([]byte, 6)
	rand.Read(b)
	schema := "t_xpn_" + hex.EncodeToString(b)
	t.Cleanup(func() {
		if p, err := pgstate.Open(context.Background(), dsn, schema); err == nil {
			p.Pool().Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
			p.Close()
		}
	})
	return schema
}

// testState opens (or reopens, a restart) the state in schema.
func testState(t *testing.T, schema string) *State {
	t.Helper()
	s, err := OpenState(context.Background(), testDSN(t), schema, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestOpenRefusesWithoutDSN(t *testing.T) {
	if _, err := OpenState(context.Background(), "", Schema, ""); err == nil {
		t.Fatal("started without a database URL")
	}
}

func TestLegacyFileImported(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(file, []byte(`{"0009000000000001":"`+hex.EncodeToString(make([]byte, 60))+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	schema := pgSchema(t)
	s, err := OpenState(context.Background(), testDSN(t), schema, file)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	os.Remove(file)
	s = testState(t, schema)
	if len(s.Get(0x0009000000000001)) != 60 {
		t.Fatal("not imported")
	}
}
