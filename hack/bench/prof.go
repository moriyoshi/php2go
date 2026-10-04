//go:build benchprof

// A CPU profile over a time window, for a program hack/bench.sh builds.
//
// Copied into the emitted module and built with `-tags benchprof`; the tag keeps
// it out of the repository's own build, where it has no main package to join.
// A window rather than the whole run because a compiled program exits through
// paths that never return here, and a profile that is not stopped is not
// written: the window has to close before the program does.
package main

import (
	"os"
	"runtime/pprof"
	"strconv"
	"time"
)

func init() {
	out := os.Getenv("PROF_OUT")
	if out == "" {
		return
	}
	start, _ := strconv.ParseFloat(os.Getenv("PROF_START"), 64)
	dur, _ := strconv.ParseFloat(os.Getenv("PROF_DUR"), 64)
	time.AfterFunc(time.Duration(start*float64(time.Second)), func() {
		f, err := os.Create(out)
		if err != nil {
			return
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return
		}
		time.AfterFunc(time.Duration(dur*float64(time.Second)), func() {
			pprof.StopCPUProfile()
			f.Close()
		})
	})
}
