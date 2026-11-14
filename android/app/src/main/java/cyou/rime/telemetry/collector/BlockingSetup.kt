package cyou.rime.telemetry.collector

import android.content.Context

/**
 * The phone only enforces blocking; the panel decides what is blocked. This
 * turns on the root paths once, when the owner has granted them, and says how
 * far setup got. Blocking IO: call it off the main thread.
 */
object BlockingSetup {
    data class State(
        val accessibility: Boolean,
        val suspension: Boolean,
        val watcher: Boolean,
        val message: String,
    ) {
        val enforced: Boolean get() = suspension && watcher
    }

    fun ensure(context: Context): State {
        if (!PolicyEvents.blockingEnabled(context)) {
            return State(
                accessibility = false,
                suspension = false,
                watcher = false,
                message = "Turn on Telemetry blocking in Accessibility first.",
            )
        }
        if (RootShell.run("id").first != 0) {
            return State(
                accessibility = true,
                suspension = AppSuspender.enabled(context),
                watcher = GuardRoot.enabled(context),
                message = "Root was not granted. The block screen applies; the watcher cannot put it back if it is switched off.",
            )
        }
        if (!AppSuspender.enabled(context)) AppSuspender.setEnabled(context, true)
        if (!GuardRoot.enabled(context)) GuardRoot.setEnabled(context, true)
        GuardRoot.syncWithCache(context)
        val suspension = AppSuspender.enabled(context)
        val watcher = GuardRoot.enabled(context)
        return State(
            accessibility = true,
            suspension = suspension,
            watcher = watcher,
            message = AppSuspender.lastError(context) ?: GuardRoot.lastError(context)
                ?: if (suspension && watcher) {
                    "Enforced. The panel decides what is blocked; the 10-minute emergency stays on this phone."
                } else {
                    "Root answered, but not everything was set up."
                },
        )
    }

    /** What the stored state says, without asking root. */
    fun stored(context: Context): State = State(
        accessibility = PolicyEvents.blockingEnabled(context),
        suspension = AppSuspender.enabled(context),
        watcher = GuardRoot.enabled(context),
        message = AppSuspender.lastError(context) ?: GuardRoot.lastError(context).orEmpty(),
    )
}
