package main

import (
	"syscall"
	"unsafe"
)

var acceptanceMemoryProc = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

func acceptanceProcessMemory() (rss, private uint64, err error) {
	var counters struct {
		Size, PageFaultCount                                                      uint32
		PeakWorkingSet, WorkingSet, QuotaPeakPagedPool, QuotaPagedPool            uintptr
		QuotaPeakNonPagedPool, QuotaNonPagedPool, Pagefile, PeakPagefile, Private uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	ok, _, callErr := acceptanceMemoryProc.Call(^uintptr(0), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok == 0 {
		return 0, 0, callErr
	}
	return uint64(counters.WorkingSet), uint64(counters.Private), nil
}
