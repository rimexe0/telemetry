package cyou.rime.telemetry.collector

import android.app.Activity
import android.content.Context

/** Built without Samsung's Health Data SDK (android/private/ is empty): nothing to read. */
object SamsungHealthSource {
    const val SUPPORTED = false

    fun installed(context: Context): Boolean = false

    fun collect(context: Context, queue: HealthQueue, deviceId: String): String = "not_built_in"

    fun request(activity: Activity, done: (Boolean) -> Unit) = done(false)

    fun grantedLastTime(context: Context): Boolean = false
}
