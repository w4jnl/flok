// The widget extension: flok's Live Activity in the Dynamic Island and on the Lock Screen
// (Shared/FlokActivity.swift for the data, Shared/LiveViews.swift for the faces). A tap opens
// the app on the most urgent agent; a row in the expanded island or on the Lock Screen opens
// that one.
import ActivityKit
import SwiftUI
import WidgetKit

@main
struct FlokWidgets: WidgetBundle {
    var body: some Widget {
        FlokLiveActivity()
    }
}

struct FlokLiveActivity: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: FlokActivityAttributes.self) { context in
            LiveLockScreen(attributes: context.attributes, state: context.state)
                .activityBackgroundTint(Brand.ground)
                .activitySystemActionForegroundColor(Brand.ink)
                .widgetURL(link(context.state))
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    HStack(spacing: 6) {
                        LiveCompactLeading(state: context.state)
                        Brand.wordmark(12)
                    }
                }
                DynamicIslandExpandedRegion(.trailing) {
                    LiveCounts(state: context.state, size: 12)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    LiveExpandedBottom(state: context.state)
                }
            } compactLeading: {
                LiveCompactLeading(state: context.state)
            } compactTrailing: {
                LiveCompactTrailing(attributes: context.attributes, state: context.state)
            } minimal: {
                LiveMinimal(state: context.state)
            }
            .widgetURL(link(context.state))
            .keylineTint(LiveStyle.color(context.state.dominant))
        }
    }

    private func link(_ state: FlokActivityAttributes.ContentState) -> URL {
        state.rows.first?.link ?? URL(string: "flok://home")!
    }
}
