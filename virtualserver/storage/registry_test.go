package storage

import (
	"testing"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	d, err := NewLocalDriver(t.TempDir(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	r.Register(d)

	got, err := r.Get("local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "local" {
		t.Fatalf("got %s want local", got.Name())
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("nfs"); err == nil {
		t.Fatal("expected error for missing driver")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := NewRegistry()
	d, _ := NewLocalDriver(t.TempDir(), "n1")
	r.Register(d)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	d2, _ := NewLocalDriver(t.TempDir(), "n2")
	r.Register(d2)
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	if len(r.Names()) != 0 {
		t.Fatal("expected empty registry")
	}
	d, _ := NewLocalDriver(t.TempDir(), "n1")
	r.Register(d)
	names := r.Names()
	if len(names) != 1 || names[0] != "local" {
		t.Fatalf("unexpected names: %v", names)
	}
}
