package main

import (
	"os"
	"strconv"
	"strings"
)

func acceptanceProcessMemory() (rss, private uint64, err error) {
	data, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] != "Rss:" && fields[0] != "Private_Clean:" && fields[0] != "Private_Dirty:" {
			continue
		}
		value, e := strconv.ParseUint(fields[1], 10, 64)
		if e != nil {
			return 0, 0, e
		}
		if fields[0] == "Rss:" {
			rss = value * 1024
		} else {
			private += value * 1024
		}
	}
	return rss, private, nil
}
