//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>

// TDBlockScreen is the full-screen reason shown over a blocked app.
@interface TDBlockScreen : NSObject
@property (strong) NSWindow *window;
@property (strong) NSTextField *mode;
@property (strong) NSTextField *reason;
@property pid_t pid;
- (void)goBack:(id)sender;
@end

@implementation TDBlockScreen
- (NSTextField *)label:(CGFloat)size color:(NSColor *)color bold:(BOOL)bold {
	NSTextField *field = [NSTextField wrappingLabelWithString:@""];
	field.font = bold ? [NSFont boldSystemFontOfSize:size] : [NSFont systemFontOfSize:size];
	field.textColor = color;
	field.alignment = NSTextAlignmentLeft;
	return field;
}

- (void)build {
	NSRect frame = NSScreen.mainScreen.frame;
	self.window = [[NSWindow alloc] initWithContentRect:frame styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
	self.window.level = NSStatusWindowLevel + 1;
	self.window.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary | NSWindowCollectionBehaviorStationary;
	self.window.backgroundColor = [NSColor colorWithRed:17/255.0 green:18/255.0 blue:19/255.0 alpha:1];
	self.window.releasedWhenClosed = NO;

	NSColor *muted = [NSColor colorWithRed:170/255.0 green:169/255.0 blue:164/255.0 alpha:1];
	self.mode = [self label:15 color:muted bold:YES];
	self.reason = [self label:30 color:[NSColor colorWithRed:238/255.0 green:238/255.0 blue:235/255.0 alpha:1] bold:NO];
	NSTextField *closed = [self label:16 color:muted bold:NO];
	closed.stringValue = @"This app is closed right now.";
	NSButton *back = [NSButton buttonWithTitle:@"Go back" target:self action:@selector(goBack:)];
	back.keyEquivalent = @"\r";

	NSStackView *stack = [NSStackView stackViewWithViews:@[self.mode, self.reason, closed, back]];
	stack.orientation = NSUserInterfaceLayoutOrientationVertical;
	stack.alignment = NSLayoutAttributeLeading;
	stack.spacing = 18;
	stack.translatesAutoresizingMaskIntoConstraints = NO;
	NSView *content = self.window.contentView;
	[content addSubview:stack];
	[NSLayoutConstraint activateConstraints:@[
		[stack.centerYAnchor constraintEqualToAnchor:content.centerYAnchor],
		[stack.leadingAnchor constraintEqualToAnchor:content.leadingAnchor constant:frame.size.width * 0.18],
		[stack.widthAnchor constraintLessThanOrEqualToConstant:frame.size.width * 0.6],
	]];
}

- (void)showMode:(NSString *)mode reason:(NSString *)reason pid:(pid_t)pid {
	if (self.window == nil) [self build];
	self.mode.stringValue = mode.uppercaseString;
	self.reason.stringValue = reason;
	self.pid = pid;
	[self.window setFrame:NSScreen.mainScreen.frame display:YES];
	[self.window orderFrontRegardless];
	[NSApp activateIgnoringOtherApps:YES];
	[self.window makeKeyWindow];
}

- (void)hide {
	[self.window orderOut:nil];
}

// goBack hides the blocked app, which returns to whatever was behind it.
- (void)goBack:(id)sender {
	[[NSRunningApplication runningApplicationWithProcessIdentifier:self.pid] hide];
	[self hide];
}
@end

static TDBlockScreen *td_screen;

static void td_ui_run(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	td_screen = [TDBlockScreen new];
	[NSApp run];
}

static void td_ui_stop(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp stop:nil];
		// stop takes effect after the next event.
		[NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0
			timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:YES];
	});
}

static void td_block_show(char *mode, char *reason, int pid) {
	NSString *modeText = [NSString stringWithUTF8String:mode];
	NSString *reasonText = [NSString stringWithUTF8String:reason];
	dispatch_async(dispatch_get_main_queue(), ^{ [td_screen showMode:modeText reason:reasonText pid:(pid_t)pid]; });
}

static void td_block_hide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ [td_screen hide]; });
}
*/
import "C"

import "unsafe"

// RunMain runs work while the macOS app loop runs on the calling thread,
// which must be the main thread (main locks it in init). The block screen
// needs that loop.
func RunMain(work func() error) error {
	result := make(chan error, 1)
	go func() {
		result <- work()
		C.td_ui_stop()
	}()
	C.td_ui_run()
	return <-result
}

// ShowBlock covers the screen with the reason; Go back hides the app pid.
func ShowBlock(mode, reason string, pid int) {
	cMode, cReason := C.CString(mode), C.CString(reason)
	defer C.free(unsafe.Pointer(cMode))
	defer C.free(unsafe.Pointer(cReason))
	C.td_block_show(cMode, cReason, C.int(pid))
}

func HideBlock() { C.td_block_hide() }

// BlockScreenSupported reports whether ShowBlock does anything here.
func BlockScreenSupported() bool { return true }
