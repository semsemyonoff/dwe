package hostid

import (
	"os/user"
	"runtime"
	"testing"
)

func TestCurrent(t *testing.T) {
	got := Current()

	if runtime.GOOS == "darwin" {
		if got.UID != "1000" || got.GID != "1000" {
			t.Fatalf("on darwin want 1000:1000, got %s:%s", got.UID, got.GID)
		}
		return
	}

	// On Linux/other, expect the real host IDs (falling back to 1000 only when
	// user.Current fails). Mirror the production ladder to compute the want.
	wantUID, wantGID := "1000", "1000"
	if u, err := user.Current(); err == nil {
		wantUID, wantGID = u.Uid, u.Gid
	}
	if got.UID != wantUID || got.GID != wantGID {
		t.Fatalf("want %s:%s, got %s:%s", wantUID, wantGID, got.UID, got.GID)
	}
}

func TestUIDGIDMatchCurrent(t *testing.T) {
	c := Current()
	if UID() != c.UID {
		t.Errorf("UID()=%q, Current().UID=%q", UID(), c.UID)
	}
	if GID() != c.GID {
		t.Errorf("GID()=%q, Current().GID=%q", GID(), c.GID)
	}
}
