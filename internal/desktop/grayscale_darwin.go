//go:build darwin

package desktop

/*
#include <dlfcn.h>
#include <stdbool.h>

// macOS's grayscale colour filter, from the private UniversalAccess
// framework that System Settings → Accessibility → Display uses.
static void *td_ua(void) {
	static void *ua;
	if (!ua) ua = dlopen("/System/Library/PrivateFrameworks/UniversalAccess.framework/UniversalAccess", RTLD_LAZY);
	return ua;
}

static int td_grayscale_get(void) {
	void *ua = td_ua();
	if (!ua) return -1;
	bool (*isOn)(void) = (bool (*)(void))dlsym(ua, "UAGrayscaleIsEnabled");
	if (!isOn) return -1;
	return isOn() ? 1 : 0;
}

static int td_grayscale_set(int on) {
	void *ua = td_ua();
	if (!ua) return -1;
	void (*set)(bool) = (void (*)(bool))dlsym(ua, "UAGrayscaleSetEnabled");
	if (!set) return -1;
	set(on != 0);
	return 0;
}
*/
import "C"

import "errors"

var errGrayscale = errors.New("the grayscale filter is not available on this macOS")

// Grayscale reports whether the screen's grayscale filter is on.
func Grayscale() (bool, error) {
	switch C.td_grayscale_get() {
	case 1:
		return true, nil
	case 0:
		return false, nil
	}
	return false, errGrayscale
}

// SetGrayscale turns the screen's grayscale filter on or off.
func SetGrayscale(on bool) error {
	value := 0
	if on {
		value = 1
	}
	if C.td_grayscale_set(C.int(value)) != 0 {
		return errGrayscale
	}
	return nil
}
