//go:build darwin

package desktop

/*
#include <stdlib.h>
#include "prompt_darwin.h"
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"
)

var (
	promptMu     sync.Mutex
	promptAnswer chan int
)

//export tdPromptChoice
func tdPromptChoice(index C.int) {
	promptMu.Lock()
	answer := promptAnswer
	promptAnswer = nil
	promptMu.Unlock()
	if answer != nil {
		answer <- int(index)
	}
}

// Prompt shows a small card with a title, an optional detail line and one
// button per option, and returns the index of the button pressed, or -1 when
// it went away unanswered or another prompt is already showing. It takes no
// focus from the owner's app. RunMain must be running.
func Prompt(title, detail string, options []string, timeout time.Duration) int {
	promptMu.Lock()
	if promptAnswer != nil {
		promptMu.Unlock()
		return -1
	}
	answer := make(chan int, 1)
	promptAnswer = answer
	promptMu.Unlock()

	cTitle, cDetail := C.CString(title), C.CString(detail)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cDetail))
	list := (**C.char)(C.malloc(C.size_t(len(options)+1) * C.size_t(unsafe.Sizeof(uintptr(0)))))
	defer C.free(unsafe.Pointer(list))
	entries := unsafe.Slice(list, len(options)+1)
	for index, option := range options {
		entries[index] = C.CString(option)
		defer C.free(unsafe.Pointer(entries[index]))
	}
	C.td_prompt_show(cTitle, cDetail, list, C.int(len(options)), C.double(timeout.Seconds()))

	select {
	case index := <-answer:
		return index
	case <-time.After(timeout + 5*time.Second):
		// The native timer did not report back; do not wait forever.
		promptMu.Lock()
		promptAnswer = nil
		promptMu.Unlock()
		C.td_prompt_close()
		return -1
	}
}

// PromptSupported reports whether Prompt shows anything here.
func PromptSupported() bool { return true }
