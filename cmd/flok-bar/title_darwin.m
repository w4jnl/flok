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

// setTemplateIconSized installs a template image at the given point size. systray pins every
// icon to 16pt, which is 27% smaller than the mark's 22pt design box; at 18pt the cursor bar
// and the frame stay readable while the ink height (the mark has a 12.5% optical margin)
// still matches a standard 16pt menu bar icon.
void setTemplateIconSized(const void *bytes, int length, double points) {
  NSData *data = [NSData dataWithBytes:bytes length:length];
  NSImage *image = [[NSImage alloc] initWithData:data];
  [image setTemplate:YES];
  [image setSize:NSMakeSize(points, points)];
  dispatch_async(dispatch_get_main_queue(), ^{
    id owner = [NSApp delegate];
    NSStatusItem *item = [owner valueForKey:@"statusItem"];
    if (item == nil) return;
    item.button.image = image;
    item.button.imagePosition = item.button.title.length ? NSImageLeft : NSImageOnly;
  });
}
