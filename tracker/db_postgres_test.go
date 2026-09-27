package tracker

import (
	"database/sql"
	"fmt"
	"net"
	"testing"
)

func TestFormatConnStr_Basic(t *testing.T) {
	cfg := PostgresConfig{
		Host: "localhost", Port: 5432, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	want := "host=localhost port=5432 user=u password=p dbname=db sslmode=disable"
	if got := formatConnStr(cfg); got != want {
		t.Errorf("formatConnStr = %q, want %q", got, want)
	}
}

func TestFormatConnStr_AllFields(t *testing.T) {
	cfg := PostgresConfig{
		Host: "db.example.com", Port: 5433, User: "tracker", Password: "s3cr3t",
		Database: "ocelot", SSLMode: "require",
	}
	want := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode)
	if got := formatConnStr(cfg); got != want {
		t.Errorf("formatConnStr = %q, want %q", got, want)
	}
}

func TestNewPostgresDB_InvalidServer_ReturnsError(t *testing.T) {
	cfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable", PoolSize: 4,
	}
	_, err := NewPostgresDB(cfg)
	if err == nil {
		t.Fatal("expected error connecting to port 1, got nil")
	}
}

func TestNewPostgresCluster_NoReplicas(t *testing.T) {
	masterCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	cluster, err := NewPostgresCluster(masterCfg, nil)
	if err != nil {
		t.Fatalf("NewPostgresCluster (no replicas) unexpected error: %v", err)
	}
	if cluster == nil {
		t.Fatal("expected non-nil cluster")
	}
	cluster.master.Close()
}

func TestNewPostgresCluster_WithReplicas(t *testing.T) {
	masterCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	repCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	cluster, err := NewPostgresCluster(masterCfg, []PostgresConfig{repCfg})
	if err != nil {
		t.Fatalf("NewPostgresCluster (with replica) unexpected error: %v", err)
	}
	if len(cluster.replicas) != 1 {
		t.Fatalf("expected 1 replica, got %d", len(cluster.replicas))
	}
	cluster.master.Close()
	cluster.replicas[0].Close()
}

func TestPostgresCluster_Write_FailsAtExec(t *testing.T) {
	masterCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	cluster, _ := NewPostgresCluster(masterCfg, nil)
	defer cluster.master.Close()
	_, err := cluster.Write("SELECT 1")
	if err == nil {
		t.Fatal("expected network error from Write on disconnected master")
	}
}

func TestPostgresCluster_Read_NoReplicas_Usesmaster(t *testing.T) {
	masterCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	cluster, _ := NewPostgresCluster(masterCfg, nil)
	defer cluster.master.Close()
	_, err := cluster.Read("SELECT 1")
	if err == nil {
		t.Fatal("expected network error from Read (no replicas) on disconnected master")
	}
}

func TestPostgresCluster_Read_WithReplicas_RoundRobin(t *testing.T) {
	masterCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	repCfg := PostgresConfig{
		Host: "127.0.0.1", Port: 1, User: "u", Password: "p",
		Database: "db", SSLMode: "disable",
	}
	cluster, _ := NewPostgresCluster(masterCfg, []PostgresConfig{repCfg})
	defer cluster.master.Close()
	defer cluster.replicas[0].Close()

	// First read uses replica[0] (idx 0); nextIdx becomes 1.
	_, err := cluster.Read("SELECT 1")
	if err == nil {
		t.Fatal("expected network error from Read (with replica)")
	}
	// Second read wraps back to replica[0]; exercises the increment path again.
	_, _ = cluster.Read("SELECT 1")
}

// openDisconnectedPostgresDB builds a *PostgresDB whose underlying sql.DB
// points to an unreachable server, so every method can be exercised to the
// point of failure without a live Postgres instance.
func openDisconnectedPostgresDB(t *testing.T) *PostgresDB {
	t.Helper()
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=u password=p dbname=db sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open failed unexpectedly: %v", err)
	}
	return &PostgresDB{
		db:      db,
		logger:  GetDefaultLogger(),
		metrics: GetMetricsRecorder(),
	}
}

func TestPostgresDB_Ping_Fails(t *testing.T) {
	pdb := openDisconnectedPostgresDB(t)
	defer pdb.Close()
	if err := pdb.Ping(); err == nil {
		t.Fatal("expected error from Ping on disconnected DB")
	}
}

func TestPostgresDB_Close(t *testing.T) {
	pdb := openDisconnectedPostgresDB(t)
	if err := pdb.Close(); err != nil {
		t.Fatalf("Close returned unexpected error: %v", err)
	}
}

func TestPostgresDB_CreateSchema_Fails(t *testing.T) {
	pdb := openDisconnectedPostgresDB(t)
	defer pdb.Close()
	if err := pdb.CreateSchema(); err == nil {
		t.Fatal("expected error from CreateSchema on disconnected DB")
	}
}

func TestPostgresDB_StorePeer_Fails(t *testing.T) {
	pdb := openDisconnectedPostgresDB(t)
	defer pdb.Close()
	peer := &Peer{UserID: 1, Port: 6881, IP: net.IP{127, 0, 0, 1}}
	if err := pdb.StorePeer("aabbccdd", "peer01", peer); err == nil {
		t.Fatal("expected network error from StorePeer on disconnected DB")
	}
}

func TestPostgresDB_LoadTorrents_Fails(t *testing.T) {
	pdb := openDisconnectedPostgresDB(t)
	defer pdb.Close()
	if _, err := pdb.LoadTorrents(); err == nil {
		t.Fatal("expected network error from LoadTorrents on disconnected DB")
	}
}
