//go:build darwin

package desktop

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include "power_darwin.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"sync"
)

var (
	powerMu      sync.Mutex
	powerHandler func(sleeping bool)
)

//export tdPowerEvent
func tdPowerEvent(kind C.int) {
	powerMu.Lock()
	handler := powerHandler
	powerMu.Unlock()
	if handler != nil {
		handler(kind == 1)
	}
}

// WatchPower calls handler(true) before the system sleeps, blocking sleep
// until it returns, and handler(false) after waking.
func WatchPower(handler func(sleeping bool)) error {
	powerMu.Lock()
	powerHandler = handler
	powerMu.Unlock()
	registered := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		ok := C.td_power_register() != 0
		registered <- ok
		if ok {
			C.td_power_loop()
		}
	}()
	if !<-registered {
		return errors.New("could not register for power notifications")
	}
	return nil
}
