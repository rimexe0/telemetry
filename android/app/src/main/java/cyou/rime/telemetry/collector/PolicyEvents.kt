package cyou.rime.telemetry.collector

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.provider.AlarmClock
import android.provider.Settings
import android.view.inputmethod.InputMethodManager
import org.json.JSONObject
import java.time.Instant

/** Records blocks and bypasses as ordinary queued observations. */
object PolicyEvents {
    fun record(context: Context, kind: String, key: String, decision: BlockDecision?, extra: Map<String, Any?> = emptyMap()) {
        val deviceId = DeviceIdentity.get(context)
        val now = Instant.now().toString()
        val payload = JSONObject()
            .put("mode", decision?.mode ?: JSONObject.NULL)
            .put("reason", decision?.reason ?: JSONObject.NULL)
            .put("policy_version", decision?.policyVersion ?: JSONObject.NULL)
        extra.forEach { (name, value) -> payload.put(name, value ?: JSONObject.NULL) }
        HealthQueue(context).enqueue(listOf(LocalObservation(
            sourceInstanceKey = "android:$deviceId:policy",
            sourceKind = "android_policy",
            deviceId = deviceId,
            sourceRecordKey = key,
            kind = kind,
            recordType = kind.substringAfter("android."),
            occurredAt = now,
            endedAt = null,
            collectedAt = now,
            sourceSchemaVersion = "android-policy-v1",
            payloadJson = payload.toString(),
        )))
    }

    fun blockingEnabled(context: Context): Boolean {
        val enabled = Settings.Secure.getString(context.contentResolver, Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES).orEmpty()
        val component = ComponentName(context, TelemetryAccessibilityService::class.java)
        return enabled.split(':').any { it.equals(component.flattenToString(), true) || it.equals(component.flattenToShortString(), true) }
    }

    /**
     * Home screens, system UI, settings, installer dialogs, the dialer, the
     * alarm clock and keyboards stay usable. Without the keyboard a day that
     * is locked until planned could not be planned.
     */
    fun systemSafe(context: Context): Set<String> {
        val home = context.packageManager.queryIntentActivities(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_HOME), 0)
            .map { it.activityInfo.packageName }
        val dialer = context.packageManager.queryIntentActivities(Intent(Intent.ACTION_DIAL), 0).map { it.activityInfo.packageName }
        val alarms = context.packageManager.queryIntentActivities(Intent(AlarmClock.ACTION_SHOW_ALARMS), 0).map { it.activityInfo.packageName }
        val keyboards = runCatching {
            context.getSystemService(InputMethodManager::class.java).enabledInputMethodList.map { it.packageName }
        }.getOrDefault(emptyList())
        return (home + dialer + alarms + keyboards + listOf(
            context.packageName, "android", "com.android.systemui", "com.android.settings",
            "com.android.permissioncontroller", "com.google.android.permissioncontroller",
        )).toSet()
    }
}
