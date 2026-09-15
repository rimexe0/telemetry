package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test

class CollectionBudgetTest {
    @Test
    fun normalSettingsKeepFreshCadenceAndUseMeteredLimit() {
        val settings = CollectionSettings()
        val budget = CollectionBudget.decide(settings, snapshot(metered = true))

        assertEquals(60_000L, budget.intervalMillis)
        assertEquals(256L * 1024L, budget.maxUploadBytes)
        assertEquals(null, budget.constrainedReason)
    }

    @Test
    fun heatPowerSaveAndLowBatterySlowToFiveMinutes() {
        val settings = CollectionSettings(cycleSeconds = 30)
        val budget = CollectionBudget.decide(
            settings,
            snapshot(
                batteryTemperatureC = 42.0,
                batteryLevelPercent = 10.0,
                charging = false,
                powerSave = true,
                thermalStatus = 3,
            ),
        )

        assertEquals(300_000L, budget.intervalMillis)
        assertEquals("thermal_status,battery_temperature,power_save,low_battery", budget.constrainedReason)
    }

    @Test
    fun lowBatteryDoesNotSlowWhileCharging() {
        val budget = CollectionBudget.decide(
            CollectionSettings(cycleSeconds = 60),
            snapshot(batteryLevelPercent = 5.0, charging = true),
        )

        assertEquals(60_000L, budget.intervalMillis)
        assertEquals(null, budget.constrainedReason)
    }

    @Test
    fun unsupportedThermalAndBatteryValuesDoNotInventAConstraint() {
        val budget = CollectionBudget.decide(
            CollectionSettings(cycleSeconds = 30),
            snapshot(
                batteryTemperatureC = null,
                batteryLevelPercent = null,
                charging = false,
                thermalStatus = null,
                metered = false,
            ),
        )

        assertEquals(30_000L, budget.intervalMillis)
        assertEquals(CollectionBudget.UNMETERED_UPLOAD_BYTES, budget.maxUploadBytes)
        assertEquals(null, budget.constrainedReason)
    }

    @Test
    fun settingsAreClampedToSafeRanges() {
        val settings = CollectionSettings(
            cycleSeconds = 1,
            healthMinutes = 999,
            meteredKiB = 0,
            meteredDailyMiB = 9999,
            batteryFloorPercent = 1,
            hotTemperatureC = 100,
        ).normalized()

        assertEquals(CollectionSettings.MIN_CYCLE_SECONDS, settings.cycleSeconds)
        assertEquals(CollectionSettings.MAX_HEALTH_MINUTES, settings.healthMinutes)
        assertEquals(CollectionSettings.MIN_METERED_KIB, settings.meteredKiB)
        assertEquals(CollectionSettings.MAX_METERED_DAILY_MIB, settings.meteredDailyMiB)
        assertEquals(CollectionSettings.MIN_BATTERY_FLOOR_PERCENT, settings.batteryFloorPercent)
        assertEquals(CollectionSettings.MAX_HOT_TEMPERATURE_C, settings.hotTemperatureC)
    }

    private fun snapshot(
        batteryTemperatureC: Double? = 25.0,
        batteryLevelPercent: Double? = 80.0,
        charging: Boolean = true,
        powerSave: Boolean = false,
        thermalStatus: Int? = 0,
        metered: Boolean = false,
    ): ResourceSnapshot = ResourceSnapshot(
        capturedAtEpochMillis = 1_000L,
        batteryLevelPercent = batteryLevelPercent,
        batteryTemperatureC = batteryTemperatureC,
        charging = charging,
        powerSaveMode = powerSave,
        thermalStatus = thermalStatus,
        uidTxBytes = null,
        uidRxBytes = null,
        uidTxDeltaBytes = null,
        uidRxDeltaBytes = null,
        processCpuPercent = null,
        processRssBytes = null,
        networkAvailable = true,
        networkValidated = true,
        networkMetered = metered,
    )
}
