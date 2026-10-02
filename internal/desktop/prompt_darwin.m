//go:build darwin

#import <AppKit/AppKit.h>
#include "prompt_darwin.h"

// A button that takes the first click although its window never becomes key.
@interface TDPromptButton : NSButton
@end

@implementation TDPromptButton
- (BOOL)acceptsFirstMouse:(NSEvent *)event {
	return YES;
}
@end

// TDPrompt is a small card near the top of the screen with a line of text
// and a few buttons. It takes no focus, so the owner's app keeps the
// keyboard, and it goes away by itself.
@interface TDPrompt : NSObject
@property (strong) NSPanel *panel;
@property (strong) NSTimer *timer;
@end

@implementation TDPrompt
- (NSTextField *)label:(NSString *)text size:(CGFloat)size color:(NSColor *)color bold:(BOOL)bold {
	NSTextField *field = [NSTextField wrappingLabelWithString:text];
	field.font = bold ? [NSFont boldSystemFontOfSize:size] : [NSFont systemFontOfSize:size];
	field.textColor = color;
	return field;
}

- (void)show:(NSString *)title detail:(NSString *)detail options:(NSArray<NSString *> *)options seconds:(double)seconds {
	[self close];
	NSMutableArray<NSView *> *rows = [NSMutableArray array];
	[rows addObject:[self label:title size:15 color:[NSColor colorWithRed:238/255.0 green:238/255.0 blue:235/255.0 alpha:1] bold:YES]];
	if (detail.length > 0) {
		[rows addObject:[self label:detail size:12 color:[NSColor colorWithRed:170/255.0 green:169/255.0 blue:164/255.0 alpha:1] bold:NO]];
	}
	NSMutableArray<NSView *> *buttons = [NSMutableArray array];
	for (NSUInteger index = 0; index < options.count; index++) {
		TDPromptButton *button = [TDPromptButton buttonWithTitle:options[index] target:self action:@selector(choose:)];
		button.tag = (NSInteger)index;
		[buttons addObject:button];
	}
	NSStackView *choices = [NSStackView stackViewWithViews:buttons];
	// Several long task names read better one per line.
	choices.orientation = options.count > 3 ? NSUserInterfaceLayoutOrientationVertical : NSUserInterfaceLayoutOrientationHorizontal;
	choices.alignment = options.count > 3 ? NSLayoutAttributeLeading : NSLayoutAttributeCenterY;
	choices.spacing = 6;
	[rows addObject:choices];

	NSStackView *stack = [NSStackView stackViewWithViews:rows];
	stack.orientation = NSUserInterfaceLayoutOrientationVertical;
	stack.alignment = NSLayoutAttributeLeading;
	stack.spacing = 8;
	stack.translatesAutoresizingMaskIntoConstraints = NO;

	NSView *card = [NSView new];
	// The card is always dark, so its buttons must draw for a dark background.
	card.appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];
	card.wantsLayer = YES;
	card.layer.backgroundColor = [NSColor colorWithRed:17/255.0 green:18/255.0 blue:19/255.0 alpha:0.97].CGColor;
	card.layer.cornerRadius = 12;
	[card addSubview:stack];
	[NSLayoutConstraint activateConstraints:@[
		[stack.leadingAnchor constraintEqualToAnchor:card.leadingAnchor constant:16],
		[stack.trailingAnchor constraintEqualToAnchor:card.trailingAnchor constant:-16],
		[stack.topAnchor constraintEqualToAnchor:card.topAnchor constant:14],
		[stack.bottomAnchor constraintEqualToAnchor:card.bottomAnchor constant:-14],
		[card.widthAnchor constraintEqualToConstant:460],
	]];
	[card layoutSubtreeIfNeeded];
	NSSize size = card.fittingSize;

	self.panel = [[NSPanel alloc] initWithContentRect:NSMakeRect(0, 0, size.width, size.height)
		styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel backing:NSBackingStoreBuffered defer:NO];
	self.panel.level = NSStatusWindowLevel;
	self.panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary;
	self.panel.opaque = NO;
	self.panel.backgroundColor = NSColor.clearColor;
	self.panel.hasShadow = YES;
	self.panel.hidesOnDeactivate = NO;
	self.panel.contentView = card;
	NSRect screen = NSScreen.mainScreen.visibleFrame;
	[self.panel setFrame:NSMakeRect(NSMidX(screen) - size.width / 2, NSMaxY(screen) - size.height - 16, size.width, size.height) display:YES];
	[self.panel orderFrontRegardless];
	__weak TDPrompt *weak = self;
	self.timer = [NSTimer scheduledTimerWithTimeInterval:seconds repeats:NO block:^(NSTimer *timer) {
		[weak close];
		tdPromptChoice(-1);
	}];
}

- (void)choose:(NSButton *)sender {
	int index = (int)sender.tag;
	[self close];
	tdPromptChoice(index);
}

- (void)close {
	[self.timer invalidate];
	self.timer = nil;
	[self.panel orderOut:nil];
	self.panel = nil;
}
@end

static TDPrompt *td_prompt;

void td_prompt_show(const char *title, const char *detail, const char **options, int count, double seconds) {
	NSString *titleText = [NSString stringWithUTF8String:title];
	NSString *detailText = [NSString stringWithUTF8String:detail];
	NSMutableArray<NSString *> *list = [NSMutableArray array];
	for (int index = 0; index < count; index++) {
		[list addObject:[NSString stringWithUTF8String:options[index]]];
	}
	dispatch_async(dispatch_get_main_queue(), ^{
		if (td_prompt == nil) td_prompt = [TDPrompt new];
		[td_prompt show:titleText detail:detailText options:list seconds:seconds];
	});
}

void td_prompt_close(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ [td_prompt close]; });
}
