package pipeline

import (
	"syscall"
	"time"
)

func cpuTime() time.Duration {
	var c, e, k, u syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	syscall.GetProcessTimes(h, &c, &e, &k, &u)
	return time.Duration((int64(k.HighDateTime)<<32|int64(k.LowDateTime))+(int64(u.HighDateTime)<<32|int64(u.LowDateTime))) * 100
}
