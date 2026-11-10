package settings

import (
	"context"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/db"
)

func TestBool(t *testing.T) {
	ctx := context.Background()
	d := db.OpenTestMigrated(t)
	s := NewStore(d)

	if v, err := s.Bool(ctx, AnonymousBrowsing, false); err != nil || v {
		t.Errorf("unset = %v, %v; want default false", v, err)
	}
	if v, _ := s.Bool(ctx, AnonymousBrowsing, true); !v {
		t.Error("unset with default true returned false")
	}
	for _, want := range []bool{true, false, true} {
		if err := s.SetBool(ctx, AnonymousBrowsing, want); err != nil {
			t.Fatal(err)
		}
		if v, err := s.Bool(ctx, AnonymousBrowsing, !want); err != nil || v != want {
			t.Errorf("after SetBool(%v): %v, %v", want, v, err)
		}
	}

	d.Exec(`UPDATE settings SET value = 'maybe' WHERE key = ?`, AnonymousBrowsing)
	if v, err := s.Bool(ctx, AnonymousBrowsing, false); err == nil || v {
		t.Errorf("garbage value: %v, %v; want default and an error", v, err)
	}
}
