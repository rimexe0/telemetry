package cyou.rime.telemetry.collector

/**
 * A cheap, immutable decision made before each collection/upload attempt.
 * Keeping this policy free of Android APIs makes its safety behaviour testable
 * without a device. (R-009 and R-050.)
 */
data class CycleBudget(
    val intervalMillis: Long,
    val maxUploadBytes: Long,
    val constrainedReason: String?,
)

object CollectionBudget {
    const val SAFE_INTERVAL_SECONDS = 300
    const val UNMETERED_UPLOAD_BYTES = 1L * 1024L * 1024L

    fun decide(settings: CollectionSettings, snapshot: ResourceSnapshot): CycleBudget {
        val normalized = settings.normalized()
        val reasons = buildList {
            if (snapshot.thermalStatus != null && snapshot.thermalStatus >= 3) {
                add("thermal_status")
            }
            if (snapshot.batteryTemperatureC != null &&
                snapshot.batteryTemperatureC >= normalized.hotTemperatureC
            ) {
                add("battery_temperature")
            }
            if (snapshot.powerSaveMode) add("power_save")
            if (snapshot.charging == false &&
                snapshot.batteryLevelPercent != null &&
                snapshot.batteryLevelPercent <= normalized.batteryFloorPercent
            ) {
                add("low_battery")
            }
        }
        val intervalSeconds = if (reasons.isEmpty()) {
            normalized.cycleSeconds
        } else {
            maxOf(SAFE_INTERVAL_SECONDS, normalized.cycleSeconds)
        }
        val maxUploadBytes = if (snapshot.networkMetered) {
            normalized.meteredKiB.toLong() * 1024L
        } else {
            UNMETERED_UPLOAD_BYTES
        }
        return CycleBudget(
            intervalMillis = intervalSeconds * 1000L,
            maxUploadBytes = maxUploadBytes,
            constrainedReason = reasons.takeIf { it.isNotEmpty() }?.joinToString(","),
        )
    }

}
