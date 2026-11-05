package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestExitOnSignalSIGTERMExitsZero(t *testing.T) {
	code := make(chan int, 1)
	exitOnSignal(func(c int) { code <- c })
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit code %d, want 0", c)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("no exit after SIGTERM within bound")
	}
}
