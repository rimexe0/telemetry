package cyou.rime.telemetry.collector

import android.content.Context

/**
 * Owner-configurable collection controls. These values describe how often the
 * app tries to collect and deliver data; they do not grant any source
 * permission or enable a source by themselves. (R-007, R-009, R-012, R-050.)
 */
data class CollectionSettings(
    val cycleSeconds: Int = DEFAULT_CYCLE_SECONDS,
    val healthMinutes: Int = DEFAULT_HEALTH_MINUTES,
    val meteredKiB: Int = DEFAULT_METERED_KIB,
    val meteredDailyMiB: Int = DEFAULT_METERED_DAILY_MIB,
    val batteryFloorPercent: Int = DEFAULT_BATTERY_FLOOR_PERCENT,
    val hotTemperatureC: Int = DEFAULT_HOT_TEMPERATURE_C,
) {
    fun normalized(): CollectionSettings = copy(
        cycleSeconds = cycleSeconds.coerceIn(MIN_CYCLE_SECONDS, MAX_CYCLE_SECONDS),
        healthMinutes = healthMinutes.coerceIn(MIN_HEALTH_MINUTES, MAX_HEALTH_MINUTES),
        meteredKiB = meteredKiB.coerceIn(MIN_METERED_KIB, MAX_METERED_KIB),
        meteredDailyMiB = meteredDailyMiB.coerceIn(MIN_METERED_DAILY_MIB, MAX_METERED_DAILY_MIB),
        batteryFloorPercent = batteryFloorPercent.coerceIn(
            MIN_BATTERY_FLOOR_PERCENT,
            MAX_BATTERY_FLOOR_PERCENT,
        ),
        hotTemperatureC = hotTemperatureC.coerceIn(MIN_HOT_TEMPERATURE_C, MAX_HOT_TEMPERATURE_C),
    )

    companion object {
        const val DEFAULT_CYCLE_SECONDS = 60
        const val DEFAULT_HEALTH_MINUTES = 5
        const val DEFAULT_METERED_KIB = 256
        const val DEFAULT_METERED_DAILY_MIB = 20
        const val DEFAULT_BATTERY_FLOOR_PERCENT = 15
        const val DEFAULT_HOT_TEMPERATURE_C = 42

        const val MIN_CYCLE_SECONDS = 30
        const val MAX_CYCLE_SECONDS = 300
        const val MIN_HEALTH_MINUTES = 1
        const val MAX_HEALTH_MINUTES = 30
        const val MIN_METERED_KIB = 16
        const val MAX_METERED_KIB = 1024
        const val MIN_METERED_DAILY_MIB = 1
        const val MAX_METERED_DAILY_MIB = 1024
        const val MIN_BATTERY_FLOOR_PERCENT = 5
        const val MAX_BATTERY_FLOOR_PERCENT = 50
        const val MIN_HOT_TEMPERATURE_C = 35
        const val MAX_HOT_TEMPERATURE_C = 50
    }
}

/** Small SharedPreferences adapter so settings remain available offline. */
class CollectionSettingsStore(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences(
        PREFERENCES_NAME,
        Context.MODE_PRIVATE,
    )

    val current: CollectionSettings
        get() = CollectionSettings(
            cycleSeconds = preferences.getInt(KEY_CYCLE_SECONDS, CollectionSettings.DEFAULT_CYCLE_SECONDS),
            healthMinutes = preferences.getInt(KEY_HEALTH_MINUTES, CollectionSettings.DEFAULT_HEALTH_MINUTES),
            meteredKiB = preferences.getInt(KEY_METERED_KIB, CollectionSettings.DEFAULT_METERED_KIB),
            meteredDailyMiB = preferences.getInt(
                KEY_METERED_DAILY_MIB,
                CollectionSettings.DEFAULT_METERED_DAILY_MIB,
            ),
            batteryFloorPercent = preferences.getInt(
                KEY_BATTERY_FLOOR_PERCENT,
                CollectionSettings.DEFAULT_BATTERY_FLOOR_PERCENT,
            ),
            hotTemperatureC = preferences.getInt(
                KEY_HOT_TEMPERATURE_C,
                CollectionSettings.DEFAULT_HOT_TEMPERATURE_C,
            ),
        ).normalized()

    fun save(settings: CollectionSettings): CollectionSettings {
        val normalized = settings.normalized()
        check(
            preferences.edit()
                .putInt(KEY_CYCLE_SECONDS, normalized.cycleSeconds)
                .putInt(KEY_HEALTH_MINUTES, normalized.healthMinutes)
                .putInt(KEY_METERED_KIB, normalized.meteredKiB)
                .putInt(KEY_METERED_DAILY_MIB, normalized.meteredDailyMiB)
                .putInt(KEY_BATTERY_FLOOR_PERCENT, normalized.batteryFloorPercent)
                .putInt(KEY_HOT_TEMPERATURE_C, normalized.hotTemperatureC)
                .commit(),
        ) { "Unable to persist collection settings" }
        return normalized
    }

    fun update(transform: (CollectionSettings) -> CollectionSettings): CollectionSettings =
        save(transform(current))

    private companion object {
        const val PREFERENCES_NAME = "telemetry-collection-settings"
        const val KEY_CYCLE_SECONDS = "cycle-seconds"
        const val KEY_HEALTH_MINUTES = "health-minutes"
        const val KEY_METERED_KIB = "metered-kib"
        const val KEY_METERED_DAILY_MIB = "metered-daily-mib"
        const val KEY_BATTERY_FLOOR_PERCENT = "battery-floor-percent"
        const val KEY_HOT_TEMPERATURE_C = "hot-temperature-c"
    }
}
