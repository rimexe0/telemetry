//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>

// TDBlockNotice hides a blocked app and shows its reason in a small card
// that takes no focus and disappears by itself, so the owner can switch to
// anything else at once. The app is hidden, never quit.
@interface TDBlockNotice : NSObject
@property (strong) NSPanel *panel;
@property (strong) NSTextField *title;
@property (strong) NSTextField *reason;
@property (strong) NSTimer *timer;
@end

@implementation TDBlockNotice
- (NSTextField *)label:(CGFloat)size color:(NSColor *)color bold:(BOOL)bold {
	NSTextField *field = [NSTextField wrappingLabelWithString:@""];
	field.font = bold ? [NSFont boldSystemFontOfSize:size] : [NSFont systemFontOfSize:size];
	field.textColor = color;
	return field;
}

- (void)build {
	self.panel = [[NSPanel alloc] initWithContentRect:NSMakeRect(0, 0, 440, 96)
		styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel backing:NSBackingStoreBuffered defer:NO];
	self.panel.level = NSStatusWindowLevel;
	self.panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary;
	self.panel.opaque = NO;
	self.panel.backgroundColor = NSColor.clearColor;
	self.panel.hasShadow = YES;
	self.panel.hidesOnDeactivate = NO;
	self.panel.ignoresMouseEvents = YES;

	NSView *card = [NSView new];
	card.wantsLayer = YES;
	card.layer.backgroundColor = [NSColor colorWithRed:17/255.0 green:18/255.0 blue:19/255.0 alpha:0.96].CGColor;
	card.layer.cornerRadius = 12;
	self.panel.contentView = card;

	self.title = [self label:13 color:[NSColor colorWithRed:170/255.0 green:169/255.0 blue:164/255.0 alpha:1] bold:YES];
	self.reason = [self label:15 color:[NSColor colorWithRed:238/255.0 green:238/255.0 blue:235/255.0 alpha:1] bold:NO];
	NSStackView *stack = [NSStackView stackViewWithViews:@[self.title, self.reason]];
	stack.orientation = NSUserInterfaceLayoutOrientationVertical;
	stack.alignment = NSLayoutAttributeLeading;
	stack.spacing = 6;
	stack.translatesAutoresizingMaskIntoConstraints = NO;
	[card addSubview:stack];
	[NSLayoutConstraint activateConstraints:@[
		[stack.leadingAnchor constraintEqualToAnchor:card.leadingAnchor constant:18],
		[stack.trailingAnchor constraintEqualToAnchor:card.trailingAnchor constant:-18],
		[stack.centerYAnchor constraintEqualToAnchor:card.centerYAnchor],
	]];
}

- (void)blockApp:(NSString *)title reason:(NSString *)reason pid:(pid_t)pid {
	[[NSRunningApplication runningApplicationWithProcessIdentifier:pid] hide];
	if (self.panel == nil) [self build];
	self.title.stringValue = title;
	self.reason.stringValue = reason;
	NSRect screen = NSScreen.mainScreen.visibleFrame;
	NSRect frame = self.panel.frame;
	[self.panel setFrameOrigin:NSMakePoint(NSMidX(screen) - frame.size.width / 2, NSMaxY(screen) - frame.size.height - 16)];
	[self.panel orderFrontRegardless];
	[self.timer invalidate];
	self.timer = [NSTimer scheduledTimerWithTimeInterval:6 repeats:NO block:^(NSTimer *timer) { [self hide]; }];
}

- (void)hide {
	[self.timer invalidate];
	[self.panel orderOut:nil];
}
@end

static TDBlockNotice *td_notice;

static void td_ui_run(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	td_notice = [TDBlockNotice new];
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

static void td_block_app(char *title, char *reason, int pid) {
	NSString *titleText = [NSString stringWithUTF8String:title];
	NSString *reasonText = [NSString stringWithUTF8String:reason];
	dispatch_async(dispatch_get_main_queue(), ^{ [td_notice blockApp:titleText reason:reasonText pid:(pid_t)pid]; });
}

static void td_block_hide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ [td_notice hide]; });
}
*/
import "C"

import "unsafe"

// RunMain runs work while the macOS app loop runs on the calling thread,
// which must be the main thread (main locks it in init). The block notice
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

// BlockApp hides the app pid (it keeps running) and shows title and reason
// in a small notice that disappears by itself.
func BlockApp(title, reason string, pid int) {
	cTitle, cReason := C.CString(title), C.CString(reason)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cReason))
	C.td_block_app(cTitle, cReason, C.int(pid))
}

// HideBlock removes the notice early.
func HideBlock() { C.td_block_hide() }

// BlockScreenSupported reports whether BlockApp does anything here.
func BlockScreenSupported() bool { return true }
