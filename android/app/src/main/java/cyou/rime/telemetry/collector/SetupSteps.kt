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
    /** Said on the Now screen while missing, even when skipped in setup; null for extras. */
    val missing: String? = null,
) {
    PAIR(true, "Connect this phone",
        "Links the phone to your Telemetry server. Turn Tailscale on first, tap Connect here, then approve it in the Telemetry panel on your Mac.",
        "Connect", "Not connected to the server"),
    NOTIFICATIONS(true, "Allow notifications",
        "Collection runs with a visible notification, and later the phone asks you short questions when something looks off.",
        "Allow", "Notifications are off"),
    USAGE_ACCESS(true, "Allow usage access",
        "Lets Telemetry see which app is open, so it can tell work from scrolling. Switch it on, then come back.",
        "Open setting", "Usage access is off"),
    HEALTH(false, "Allow health data",
        "Sleep and steps from Health Connect let Telemetry notice when you wake up and when you've moved.",
        "Allow"),
    SAMSUNG_HEALTH(false, "Allow Samsung Health",
        "Reads your watch's heart rate, walks and workouts with their route, sleep, and steps straight from Samsung Health, read-only. Samsung Health needs \"Developer Mode for Data Read\" on: Settings → About Samsung Health, tap the version ten times.",
        "Allow", "Samsung Health is not readable"),
    LOCATION(false, "Allow location",
        "Lets Telemetry notice when you've gone outside.",
        "Allow"),
    BACKGROUND_LOCATION(false, "Allow location all the time",
        "Needed to notice going outside while the phone is in your pocket. Choose \"Allow all the time\", then come back.",
        "Open setting"),
    CALENDAR(false, "Allow calendar",
        "Lets Telemetry read your calendars on this phone, read-only, so your plans leave room for appointments. Nothing is written to them.",
        "Allow", "Calendar is not readable"),
    BATTERY(false, "Let it run in the background",
        "Stops Android from pausing collection to save battery.",
        "Allow", "Battery saving can pause it"),
    BLOCKING(false, "Turn on blocking",
        "Lets Telemetry close distracting apps during work and breaks and show you why. Open Telemetry blocking, switch it on and allow, then come back.",
        "Open setting", "Blocking is off"),
    NOTIFICATION_ACCESS(false, "Hide notifications from blocked apps",
        "Lets Telemetry remove notifications from apps that are blocked right now, so they can't pull you back in. It does not keep what they say. Switch Telemetry on, then come back.",
        "Open setting", "Blocked apps can still notify"),
    START(true, "Start collecting",
        "Starts collection now. A small notification shows while it runs.",
        "Start", "Collection is not started");

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

        /** What the Now screen names as missing, in setup order. */
        fun missing(done: Set<SetupStep>): List<SetupStep> = entries.filter { it.missing != null && it !in done }
    }
}
