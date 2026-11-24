//go:build !windows

package main

import "time"

var clockOrigin = time.Now()

func preciseNow() int64                 { return time.Since(clockOrigin).Nanoseconds() }
func preciseElapsed(start int64) uint64 { return uint64(preciseNow() - start) }
