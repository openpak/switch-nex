package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// shutdownTimeout bounds the graceful stop; podman waits 10 s before SIGKILL.
const shutdownTimeout = 5 * time.Second

// shutdownFuncs run (in order, within shutdownTimeout) when the process is told to stop.
var shutdownFuncs []func()

// exitOnSignal turns SIGTERM/SIGINT into a clean exit 0. Go's default for an unhandled
// SIGTERM is status 2, which systemd reports as a failed unit on every stop or restart.
func exitOnSignal(exit func(int)) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-ch
		log.Printf("received %v, shutting down", s)
		done := make(chan struct{})
		go func() {
			for _, f := range shutdownFuncs {
				f()
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(shutdownTimeout):
			log.Printf("shutdown exceeded %v, exiting anyway", shutdownTimeout)
		}
		exit(0)
	}()
}
