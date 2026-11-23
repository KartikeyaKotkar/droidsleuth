package evidence

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/KartikeyaKotkar/droidsleuth/internal/session"
)

func FuzzDiagnosticParsers(f *testing.F) {
	f.Add(uint8(0), java("10123", 42, target.Package))
	f.Add(uint8(0), logline("system", 900, "am_anr", "[0,42,com.example.app,0,timeout]"))
	f.Add(uint8(0), logline("root", 99, "DEBUG", "pid: 42, tid: 42, name: app >>> com.example.app <<<")+"\n"+logline("root", 99, "DEBUG", "uid: 10123")+"\n"+logline("root", 99, "DEBUG", "signal 11 (SIGSEGV), code 0 (SI_USER)"))
	f.Add(uint8(1), history(4))
	f.Add(uint8(1), "Permission Denial")
	f.Fuzz(func(t *testing.T, source uint8, raw string) {
		// Input allocation bounds complement separate 4 MiB transport tests.
		if len(raw) > 64<<10 {
			t.Skip()
		}
		var out []session.Evidence
		if source%2 == 0 {
			out, _ = Logcat(raw, target, "boot")
		} else {
			var err error
			out, err = ExitHistory(raw, target, "UTC")
			if err != nil {
				return
			}
		}
		for _, o := range out {
			if len(o.Text) > TextLimit || !utf8.ValidString(o.Text) {
				t.Fatal("unbounded or lossy source text")
			}
			if _, err := json.Marshal(o); err != nil {
				t.Fatal(err)
			}
			for _, e := range o.Events {
				if !selected(e.Process, target.Package) || e.PID <= 0 || int64(e.PID) > 2147483647 {
					t.Fatal("invalid process evidence")
				}
				if e.UID != nil && *e.UID != target.UID {
					t.Fatal("cross-UID evidence")
				}
				if e.User != nil && *e.User != target.User {
					t.Fatal("cross-user evidence")
				}
				if e.UID == nil && e.User == nil {
					t.Fatal("identity evidence lost")
				}
				if e.FirstLine < 1 || e.LastLine < e.FirstLine || e.LastLine > strings.Count(o.Text, "\n")+1 {
					t.Fatal("broken evidence reference")
				}
			}
		}
	})
}
