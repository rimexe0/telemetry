package cyou.rime.telemetry.collector

/**
 * The guided setup, in order. Required steps are asked until done; optional
 * steps can be skipped and are then not asked again unless setup is rerun.
 */
enum class SetupStep(
    val required: Boolean,
    val title: String,
    val why: String,
    val action: String,
) {
    PAIR(true, "Connect this phone",
        "Links the phone to your Telemetry server. Turn Tailscale on first, tap Connect here, then approve it in the Telemetry panel on your Mac.",
        "Connect"),
    NOTIFICATIONS(true, "Allow notifications",
        "Collection runs with a visible notification, and later the phone asks you short questions when something looks off.",
        "Allow"),
    USAGE_ACCESS(true, "Allow usage access",
        "Lets Telemetry see which app is open, so it can tell work from scrolling. Switch it on, then come back.",
        "Open setting"),
    HEALTH(false, "Allow health data",
        "Sleep and steps from Health Connect let Telemetry notice when you wake up and when you've moved.",
        "Allow"),
    LOCATION(false, "Allow location",
        "Lets Telemetry notice when you've gone outside.",
        "Allow"),
    BACKGROUND_LOCATION(false, "Allow location all the time",
        "Needed to notice going outside while the phone is in your pocket. Choose \"Allow all the time\", then come back.",
        "Open setting"),
    BATTERY(false, "Let it run in the background",
        "Stops Android from pausing collection to save battery.",
        "Allow"),
    START(true, "Start collecting",
        "Starts collection now. A small notification shows while it runs.",
        "Start");

    companion object {
        /**
         * The first step that is neither done nor skipped. Background location
         * is only asked once foreground location is granted.
         */
        fun next(done: Set<SetupStep>, skipped: Set<SetupStep>): SetupStep? = entries.firstOrNull { step ->
            step !in done && step !in skipped &&
                !(step == BACKGROUND_LOCATION && LOCATION !in done)
        }

        fun remainingRequired(done: Set<SetupStep>): Int = entries.count { it.required && it !in done }
    }
}
