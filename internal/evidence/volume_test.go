package evidence

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func volumeBatch(count int, unrelated bool) string {
	var b strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "1700000000.%06d 1000 900 900 I am_anr: [0,42,com.example.app,0,synthetic %d]\n", i, i)
		if unrelated {
			fmt.Fprintf(&b, "1700000000.%06d 10124 202 202 E AndroidRuntime: unrelated-private-sentinel-%d\n", i, i)
		}
	}
	return b.String()
}
func TestHighVolumeLogcatScope(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			raw := volumeBatch(n, true)
			out, bad := Logcat(raw, target, "boot")
			if bad || len(out) != n {
				t.Fatalf("batch=%d events=%d incomplete=%t", n, len(out), bad)
			}
			for _, o := range out {
				if len(o.Text) > TextLimit || strings.Contains(o.Text, "unrelated-private-sentinel") {
					t.Fatal("scope/size bound lost")
				}
			}
		})
	}
}
func BenchmarkCompatibilityLogcat(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			raw := volumeBatch(n, true)
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, bad := Logcat(raw, target, "boot")
				if bad || len(out) != n {
					b.Fatal("decode changed")
				}
			}
		})
	}
}

type volumeSource struct{ raw string }

func (s volumeSource) Shell(ctx context.Context, command string) (string, error) {
	if strings.Contains(command, "boot_id") {
		return "boot", nil
	}
	if strings.Contains(command, "timezone") {
		return "UTC", nil
	}
	if strings.Contains(command, "exit-info") {
		return "ACTIVITY MANAGER PROCESS EXIT INFO", nil
	}
	if strings.Contains(command, "droidsleuth crash tail") {
		return s.raw + crashTailBoundary, nil
	}
	return s.raw, nil
}
func TestSourceObservationLimitAndQueueCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, stop := Start(ctx, volumeSource{volumeBatch(10000, true)}, target)
	count := 0
	gap := false
	for count < 64 || !gap {
		select {
		case r := <-ch:
			if r.Evidence != nil {
				count++
			}
			if strings.Contains(r.Error, "observation limit") {
				gap = true
			}
		case <-ctx.Done():
			stop()
			t.Fatalf("count=%d gap=%t", count, gap)
		}
	}
	start := time.Now()
	cancel()
	stop()
	if time.Since(start) > time.Second {
		t.Fatal("cancellation blocked behind queue")
	}
	if count != 64 {
		t.Fatal("poll exceeded 64 selected observations")
	}
	// Also cancel while the bounded queue is full and nobody is consuming it.
	ctx2, cancel2 := context.WithCancel(context.Background())
	ch2, stop2 := Start(ctx2, volumeSource{volumeBatch(10000, true)}, target)
	defer cancel2()
	deadline := time.Now().Add(10 * time.Second)
	for len(ch2) < 16 {
		if time.Now().After(deadline) {
			cancel2()
			stop2()
			t.Fatal("source never filled bounded queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start = time.Now()
	cancel2()
	stop2()
	if time.Since(start) > time.Second {
		t.Fatal("full writer queue prevented cancellation")
	}
}
func TestRuntimeBlockLineBoundaries(t *testing.T) {
	base := java("10123", 42, target.Package)
	for _, n := range []int{127, 128, 129} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			raw := base
			// java() already has three lines.
			for i := 3; i < n; i++ {
				raw += "\n" + logline("10123", 42, "AndroidRuntime", "x")
			}
			out, bad := Logcat(raw, target, "boot")
			if len(out) != 1 || bad != (n > 128) {
				t.Fatalf("lines=%d evidence=%d gap=%t", n, len(out), bad)
			}
			if len(out[0].Text) > TextLimit {
				t.Fatal("block exceeded byte bound")
			}
		})
	}
}
