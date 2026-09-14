#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// flok-bar's AppKit extras on top of fyne/systray, which only offers a plain-string title and a
// template icon: coloured title runs and a tinted icon. systray keeps its NSStatusItem in the
// app delegate's `statusItem` ivar; KVC reaches it without patching systray. Every setter is
// one AppKit redraw, the same cost as systray's own.

static NSStatusItem *flokStatusItem(void) {
  id owner = [NSApp delegate];
  return [owner valueForKey:@"statusItem"];
}

// dark says whether the status item is drawn on a dark menu bar (the button's own appearance,
// which follows the menu bar rather than the app).
static BOOL flokDark(NSStatusItem *item) {
  NSAppearance *ap = item.button.effectiveAppearance;
  NSString *best = [ap bestMatchFromAppearancesWithNames:@[NSAppearanceNameAqua, NSAppearanceNameDarkAqua]];
  return [best isEqualToString:NSAppearanceNameDarkAqua];
}

static NSColor *flokColor(const double *rgb) {
  if (rgb[0] < 0) return [NSColor labelColor];
  return [NSColor colorWithSRGBRed:rgb[0] green:rgb[1] blue:rgb[2] alpha:1];
}

// setTitleRuns sets the title as coloured runs; each run carries a dark- and a light-menu-bar
// colour (a negative red means the label colour).
void setTitleRuns(const char **texts, const double *rgb, const double *rgbLight, int n) {
  NSMutableArray *parts = [NSMutableArray arrayWithCapacity:n];
  for (int i = 0; i < n; i++) [parts addObject:[NSString stringWithUTF8String:texts[i]]];
  NSMutableData *dark = [NSMutableData dataWithBytes:rgb length:sizeof(double) * 3 * n];
  NSMutableData *light = [NSMutableData dataWithBytes:rgbLight length:sizeof(double) * 3 * n];
  dispatch_async(dispatch_get_main_queue(), ^{
    NSStatusItem *item = flokStatusItem();
    if (item == nil) return;
    const double *pal = flokDark(item) ? (const double *)dark.bytes : (const double *)light.bytes;
    NSMutableAttributedString *s = [[NSMutableAttributedString alloc] init];
    NSFont *font = [NSFont menuBarFontOfSize:0];
    for (int i = 0; i < n; i++) {
      NSDictionary *attrs = @{NSFontAttributeName: font, NSForegroundColorAttributeName: flokColor(pal + i * 3)};
      [s appendAttributedString:[[NSAttributedString alloc] initWithString:parts[i] attributes:attrs]];
    }
    item.button.attributedTitle = s;
    item.button.imagePosition = s.length ? NSImageLeft : NSImageOnly;
  });
}

// The last icon request, so a menu bar appearance change can re-tint it (refreshIconAppearance).
static NSData *lastIcon;
static double lastPts, lastRGB[3], lastRGBLight[3];
static BOOL lastDark;

static NSImage *flokIconImage(NSData *data, double pts, const double *rgb) {
  NSImage *image = [[NSImage alloc] initWithData:data];
  [image setSize:NSMakeSize(pts, pts)];
  if (rgb[0] < 0) { // at rest: a template, macOS paints it in the menu bar's colour
    [image setTemplate:YES];
    return image;
  }
  NSColor *color = flokColor(rgb);
  NSImage *tinted = [NSImage imageWithSize:NSMakeSize(pts, pts) flipped:NO drawingHandler:^BOOL(NSRect r) {
    [image drawInRect:r fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1];
    [color set];
    NSRectFillUsingOperation(r, NSCompositingOperationSourceAtop); // keep the alpha, replace the ink
    return YES;
  }];
  [tinted setTemplate:NO];
  return tinted;
}

static void flokApplyIcon(void) {
  NSStatusItem *item = flokStatusItem();
  if (item == nil || lastIcon == nil) return;
  lastDark = flokDark(item);
  item.button.image = flokIconImage(lastIcon, lastPts, lastDark ? lastRGB : lastRGBLight);
  item.button.imagePosition = item.button.title.length ? NSImageLeft : NSImageOnly;
}

// setTemplateIconSized installs the icon at the given point size, tinted with the dark- or
// light-menu-bar colour (negative red = untinted template). systray pins every icon to 16pt,
// 27% below the mark's 22pt design box; at 18pt the frame and cursor stay readable while the
// ink height (the mark has a 12.5% optical margin) still matches a standard 16pt icon.
void setTemplateIconSized(const void *bytes, int length, double points, const double *rgb, const double *rgbLight) {
  NSData *data = [NSData dataWithBytes:bytes length:length];
  NSData *dark = [NSData dataWithBytes:rgb length:sizeof(double) * 3];   // blocks cannot capture C arrays
  NSData *light = [NSData dataWithBytes:rgbLight length:sizeof(double) * 3];
  dispatch_async(dispatch_get_main_queue(), ^{
    lastIcon = data;
    lastPts = points;
    memcpy(lastRGB, dark.bytes, sizeof lastRGB);
    memcpy(lastRGBLight, light.bytes, sizeof lastRGBLight);
    flokApplyIcon();
  });
}

// refreshIconAppearance re-tints the current icon when the menu bar switched between light
// and dark since it was set; a no-op otherwise (called from the bar's periodic render).
void refreshIconAppearance(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    NSStatusItem *item = flokStatusItem();
    if (item == nil || lastIcon == nil || lastRGB[0] < 0) return;
    if (flokDark(item) != lastDark) flokApplyIcon();
  });
}
