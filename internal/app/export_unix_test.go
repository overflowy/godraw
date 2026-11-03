//go:build darwin || linux

package app

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"godraw/internal/geo"
	"godraw/internal/stress"
)

// A small picture of a large page takes little memory, where its frame's buffer
// once went by the page: this one's 419 MB, outside the Go heap where only a whole
// process can see it. So it runs in one of its own, to see only this.
func TestSmallPictureTakesLittleMemory(t *testing.T) {
	const limitMB = 250
	if os.Getenv("GODRAW_PICTURE_PEAK") != "" {
		a := New(stress.Diagram(60), &testHost{})
		e := a.Editor()
		_, ids, _ := e.ExportBounds()
		img := a.RenderImage(geo.Box{W: 7000, H: 7000}, ids, 0.1)
		var u syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &u); err != nil {
			t.Fatal(err)
		}
		peak := u.Maxrss // bytes here, and KB on Linux
		if runtime.GOOS == "linux" {
			peak *= 1024
		}
		fmt.Printf("peak=%d image=%d\n", peak, img.Bounds().Dx())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSmallPictureTakesLittleMemory$", "-test.v")
	cmd.Env = append(os.Environ(), "GODRAW_PICTURE_PEAK=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m := regexp.MustCompile(`peak=(\d+) image=(\d+)`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("no peak in\n%s", out)
	}
	peak, _ := strconv.ParseInt(string(m[1]), 10, 64)
	t.Logf("peak %d MB for a picture %s px a side", peak/(1<<20), m[2])
	if peak > limitMB<<20 {
		t.Errorf("a picture of 700 px took %d MB at its peak, over %d", peak/(1<<20), limitMB)
	}
}
