package deploy

import (
	"testing"

	"github.com/portdock/portdock/internal/templates"
)

func TestLastNumber(t *testing.T) {
	for in, want := range map[string]int64{"12\n": 12, "Warning: x\n3": 3, "  7  ": 7, "(integer) 5\n": -1, "": -1} {
		n, err := lastNumber(in)
		if want < 0 {
			if err == nil {
				t.Errorf("%q: no error", in)
			}
			continue
		}
		if err != nil || n != want {
			t.Errorf("%q: %d %v", in, n, err)
		}
	}
}

func TestEnginesVerifiable(t *testing.T) {
	for _, e := range templates.EngineList() {
		if !e.Verifiable() || e.CheckUnit == "" {
			t.Errorf("%s: backups cannot be test-restored", e.ID)
		}
		if (e.FromFileCmd("p", "f") != "") == e.Restore {
			t.Errorf("%s: a backup is either restored by a command or loaded from its file", e.ID)
		}
	}
}
