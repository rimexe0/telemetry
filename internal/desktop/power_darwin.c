//go:build darwin

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <IOKit/IOMessage.h>
#include "power_darwin.h"

static io_connect_t td_root_port;

// Runs on the power thread. Sleep waits until the Go handler returns, which
// lets the collector announce it; macOS allows up to 30 s.
static void td_power_callback(void *refcon, io_service_t service, natural_t type, void *argument) {
	switch (type) {
	case kIOMessageCanSystemSleep:
		IOAllowPowerChange(td_root_port, (long)argument);
		break;
	case kIOMessageSystemWillSleep:
		tdPowerEvent(1);
		IOAllowPowerChange(td_root_port, (long)argument);
		break;
	case kIOMessageSystemHasPoweredOn:
		tdPowerEvent(2);
		break;
	}
}

// td_power_register must run on the thread that then calls td_power_loop.
int td_power_register(void) {
	IONotificationPortRef port = NULL;
	io_object_t notifier = 0;
	td_root_port = IORegisterForSystemPower(NULL, &port, td_power_callback, &notifier);
	if (td_root_port == 0) return 0;
	CFRunLoopAddSource(CFRunLoopGetCurrent(), IONotificationPortGetRunLoopSource(port), kCFRunLoopCommonModes);
	return 1;
}

void td_power_loop(void) { CFRunLoopRun(); }
