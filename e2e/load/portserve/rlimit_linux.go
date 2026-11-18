package main

import (
	"fmt"
	"os"
	"syscall"
)

// raiseFileLimit lifts the soft limit of open files to the hard one, each listener and connection is a file
func raiseFileLimit() {
	var limit syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit)
	if err == nil {
		limit.Cur = limit.Max
		err = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "raise open files limit:", err)
	}
}
