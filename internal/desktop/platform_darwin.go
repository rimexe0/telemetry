//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices -framework CoreGraphics
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>

typedef struct {
	char *app;
	char *bundle;
	char *title;
	int ok;
} td_window;

static char *td_copy(NSString *value) {
	if (value == nil) return NULL;
	return strdup([value UTF8String]);
}

static int td_trusted(int prompt) {
	NSDictionary *options = @{(__bridge id)kAXTrustedCheckOptionPrompt: prompt ? @YES : @NO};
	return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options) ? 1 : 0;
}

// The frontmost normal window's owner. It needs no permission and is the
// fallback when Accessibility is not granted.
static pid_t td_front_pid(void) {
	CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (list == NULL) return -1;
	pid_t pid = -1;
	for (NSDictionary *window in (__bridge NSArray *)list) {
		if ([window[(__bridge id)kCGWindowLayer] intValue] != 0) continue;
		pid = [window[(__bridge id)kCGWindowOwnerPID] intValue];
		break;
	}
	CFRelease(list);
	return pid;
}

static td_window td_active(void) {
	@autoreleasepool {
		td_window result = {0};
		pid_t pid = -1;
		if (AXIsProcessTrusted()) {
			AXUIElementRef system = AXUIElementCreateSystemWide();
			// An unresponsive app must not stall sampling for the 6 s default.
			AXUIElementSetMessagingTimeout(system, 0.5);
			CFTypeRef app = NULL;
			if (AXUIElementCopyAttributeValue(system, kAXFocusedApplicationAttribute, &app) == kAXErrorSuccess && app != NULL) {
				AXUIElementGetPid((AXUIElementRef)app, &pid);
				AXUIElementSetMessagingTimeout((AXUIElementRef)app, 0.5);
				CFTypeRef window = NULL;
				if (AXUIElementCopyAttributeValue((AXUIElementRef)app, kAXFocusedWindowAttribute, &window) == kAXErrorSuccess && window != NULL) {
					CFTypeRef title = NULL;
					if (AXUIElementCopyAttributeValue((AXUIElementRef)window, kAXTitleAttribute, &title) == kAXErrorSuccess && title != NULL) {
						if (CFGetTypeID(title) == CFStringGetTypeID()) result.title = td_copy((__bridge NSString *)title);
						CFRelease(title);
					}
					CFRelease(window);
				}
				CFRelease(app);
			}
			CFRelease(system);
		}
		if (pid <= 0) pid = td_front_pid();
		if (pid <= 0) return result;
		NSRunningApplication *application = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
		if (application == nil) return result;
		result.app = td_copy(application.localizedName);
		result.bundle = td_copy(application.bundleIdentifier);
		result.ok = 1;
		return result;
	}
}

static double td_idle(void) {
	return CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, kCGAnyInputEventType);
}

static int td_locked(void) {
	CFDictionaryRef session = CGSessionCopyCurrentDictionary();
	if (session == NULL) return 1;
	int locked = 0;
	CFBooleanRef value = CFDictionaryGetValue(session, CFSTR("CGSSessionScreenIsLocked"));
	if (value != NULL && CFGetTypeID(value) == CFBooleanGetTypeID()) locked = CFBooleanGetValue(value) ? 1 : 0;
	CFRelease(session);
	return locked;
}
*/
import "C"

import (
	"time"
	"unsafe"
)

type darwin struct{}

// NewPlatform returns the macOS implementation.
func NewPlatform() (Platform, error) { return darwin{}, nil }

func (darwin) ActiveWindow() (Window, bool, error) {
	result := C.td_active()
	defer C.free(unsafe.Pointer(result.app))
	defer C.free(unsafe.Pointer(result.bundle))
	defer C.free(unsafe.Pointer(result.title))
	if result.ok == 0 {
		return Window{}, false, nil
	}
	return Window{App: goString(result.app), BundleID: goString(result.bundle), Title: goString(result.title)}, true, nil
}

func (darwin) IdleTime() (time.Duration, error) {
	return time.Duration(float64(C.td_idle()) * float64(time.Second)), nil
}

// ScreenLocked also reports true when there is no console session, for
// example at the login window after fast user switching.
func (darwin) ScreenLocked() (bool, error) { return C.td_locked() != 0, nil }

func (darwin) CanReadTitles(prompt bool) bool {
	flag := C.int(0)
	if prompt {
		flag = 1
	}
	return C.td_trusted(flag) != 0
}

func goString(value *C.char) string {
	if value == nil {
		return ""
	}
	return C.GoString(value)
}
