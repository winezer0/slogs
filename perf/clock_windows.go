package main

import (
	"syscall"
	"unsafe"
)

var counterProc = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var counterAddress = counterProc.Addr()
var counterFrequency = func() int64 {
	var frequency int64
	ok, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency").Call(uintptr(unsafe.Pointer(&frequency)))
	if ok == 0 || frequency <= 0 {
		panic(err)
	}
	return frequency
}()

func preciseNow() int64 {
	var ticks int64
	ok, _, err := syscall.SyscallN(counterAddress, uintptr(unsafe.Pointer(&ticks)))
	if ok == 0 {
		panic(err)
	}
	return ticks
}
func preciseElapsed(start int64) uint64 {
	return uint64((preciseNow() - start) * 1000000000 / counterFrequency)
}
