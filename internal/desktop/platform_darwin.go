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

// td_focused_pid asks Accessibility for the focused app. It fails with
// "cannot complete" on some systems, so callers fall back to the window list.
static pid_t td_focused_pid(AXError *error) {
	pid_t pid = -1;
	AXUIElementRef system = AXUIElementCreateSystemWide();
	// An unresponsive app must not stall sampling for the 6 s default.
	AXUIElementSetMessagingTimeout(system, 0.5);
	CFTypeRef app = NULL;
	*error = AXUIElementCopyAttributeValue(system, kAXFocusedApplicationAttribute, &app);
	if (*error == kAXErrorSuccess && app != NULL) {
		AXUIElementGetPid((AXUIElementRef)app, &pid);
		CFRelease(app);
	}
	CFRelease(system);
	return pid;
}

// td_title reads the title of pid's focused window, or of its main window.
static char *td_title(pid_t pid, AXError *error) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	AXUIElementSetMessagingTimeout(app, 0.5);
	char *result = NULL;
	CFTypeRef window = NULL;
	*error = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, &window);
	if (*error != kAXErrorSuccess || window == NULL) {
		*error = AXUIElementCopyAttributeValue(app, kAXMainWindowAttribute, &window);
	}
	if (*error == kAXErrorSuccess && window != NULL) {
		CFTypeRef title = NULL;
		*error = AXUIElementCopyAttributeValue((AXUIElementRef)window, kAXTitleAttribute, &title);
		if (*error == kAXErrorSuccess && title != NULL && CFGetTypeID(title) == CFStringGetTypeID()) {
			result = td_copy((__bridge NSString *)title);
		}
		if (title != NULL) CFRelease(title);
		CFRelease(window);
	}
	CFRelease(app);
	return result;
}

static td_window td_active(void) {
	@autoreleasepool {
		td_window result = {0};
		int trusted = AXIsProcessTrusted();
		AXError error = kAXErrorSuccess;
		pid_t pid = trusted ? td_focused_pid(&error) : -1;
		if (pid <= 0) pid = td_front_pid();
		if (pid <= 0) return result;
		NSRunningApplication *application = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
		if (application == nil) return result;
		result.app = td_copy(application.localizedName);
		result.bundle = td_copy(application.bundleIdentifier);
		if (trusted) result.title = td_title(pid, &error);
		result.ok = 1;
		return result;
	}
}

// td_diagnose reports each step's result for troubleshooting.
static char *td_diagnose(void) {
	@autoreleasepool {
		AXError focused = kAXErrorSuccess, title = kAXErrorSuccess;
		pid_t pid = td_focused_pid(&focused);
		pid_t front = td_front_pid();
		char *text = td_title(pid > 0 ? pid : front, &title);
		NSString *out = [NSString stringWithFormat:@"trusted=%d focused_app_error=%d focused_pid=%d windowlist_pid=%d title_error=%d title=%s",
			AXIsProcessTrusted() ? 1 : 0, focused, pid, front, title, text != NULL ? text : "(none)"];
		free(text);
		return td_copy(out);
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

// Diagnose describes what the Accessibility calls return right now.
func Diagnose() string {
	value := C.td_diagnose()
	defer C.free(unsafe.Pointer(value))
	return goString(value)
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
