package cyou.rime.telemetry.collector

import com.google.android.gms.wearable.DataEvent
import com.google.android.gms.wearable.DataEventBuffer
import com.google.android.gms.wearable.DataMapItem
import com.google.android.gms.wearable.Wearable
import com.google.android.gms.wearable.WearableListenerService
import java.time.Instant
import org.json.JSONObject

/**
 * Receives the walks the watch recorded away from the phone and queues them
 * for the server like any other record. Deleting the item afterwards tells
 * the watch it arrived; until then the watch link keeps offering it.
 */
class WatchRelayService : WearableListenerService() {
    override fun onDataChanged(events: DataEventBuffer) {
        val device = DeviceIdentity.get(this)
        for (event in events) {
            val item = event.dataItem
            if (event.type != DataEvent.TYPE_CHANGED || item.uri.path?.startsWith("/outside/") != true) continue
            val walk = DataMapItem.fromDataItem(item).dataMap.getString("walk") ?: continue
            val observation = runCatching { WatchWalks.observation(device, JSONObject(walk), Instant.now()) }.getOrNull() ?: continue
            HealthQueue(this).enqueue(listOf(observation))
            Wearable.getDataClient(this).deleteDataItems(item.uri)
        }
    }
}

/** A watch walk as an observation. Without Android so it can be tested. */
object WatchWalks {
    const val KIND = "watch.outside_walk"

    fun observation(deviceId: String, walk: JSONObject, collectedAt: Instant): LocalObservation {
        val id = walk.getString("session_id")
        require(id.isNotBlank() && walk.getJSONArray("points").length() > 0) { "empty walk" }
        return LocalObservation(
            sourceInstanceKey = "android:$deviceId:watch",
            sourceKind = "galaxy_watch",
            deviceId = deviceId,
            sourceRecordKey = "walk:$id",
            kind = KIND,
            recordType = "outside_walk",
            occurredAt = Instant.parse(walk.getString("start")).toString(),
            endedAt = Instant.parse(walk.getString("end")).toString(),
            collectedAt = collectedAt.toString(),
            sourceSchemaVersion = "telemetry-wear-1",
            payloadJson = walk.toString(),
        )
    }
}
