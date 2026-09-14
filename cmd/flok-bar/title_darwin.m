#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// setTitleRuns sets the status item's title as coloured runs. fyne/systray only offers a
// plain-string title; its NSStatusItem lives in the app delegate's `statusItem` ivar, which
// KVC reaches without patching systray. Same AppKit cost as a plain title change.
void setTitleRuns(const char **texts, const double *rgb, int n) {
  NSMutableAttributedString *s = [[NSMutableAttributedString alloc] init];
  NSFont *font = [NSFont menuBarFontOfSize:0];
  for (int i = 0; i < n; i++) {
    NSString *t = [NSString stringWithUTF8String:texts[i]];
    NSMutableDictionary *attrs = [NSMutableDictionary dictionaryWithObject:font forKey:NSFontAttributeName];
    if (rgb[i*3] >= 0) {
      attrs[NSForegroundColorAttributeName] = [NSColor colorWithSRGBRed:rgb[i*3] green:rgb[i*3+1] blue:rgb[i*3+2] alpha:1];
    } else {
      attrs[NSForegroundColorAttributeName] = [NSColor labelColor];
    }
    [s appendAttributedString:[[NSAttributedString alloc] initWithString:t attributes:attrs]];
  }
  dispatch_async(dispatch_get_main_queue(), ^{
    id owner = [NSApp delegate];
    NSStatusItem *item = [owner valueForKey:@"statusItem"];
    if (item == nil) return;
    item.button.attributedTitle = s;
    item.button.imagePosition = s.length ? NSImageLeft : NSImageOnly;
  });
}
