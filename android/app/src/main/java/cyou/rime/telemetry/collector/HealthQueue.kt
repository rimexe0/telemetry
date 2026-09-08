package cyou.rime.telemetry.collector

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import java.security.MessageDigest

data class LocalObservation(
    val sourceInstanceKey: String,
    val sourceKind: String,
    val deviceId: String,
    val sourceRecordKey: String,
    val kind: String,
    val recordType: String,
    val occurredAt: String,
    val endedAt: String?,
    val collectedAt: String,
    val sourceSchemaVersion: String,
    val payloadJson: String,
)

typealias HealthObservation = LocalObservation

data class HealthQueueStats(
    val logicalRecords: Long,
    val revisions: Long,
    val pendingRevisions: Long,
)

data class HealthQueueInsertResult(
    val insertedRevisions: Int,
)

data class QueuedObservation(
    val rowId: Long,
    val sourceInstanceKey: String,
    val sourceKind: String,
    val deviceId: String,
    val sourceRecordKey: String,
    val kind: String,
    val occurredAt: String,
    val endedAt: String?,
    val collectedAt: String,
    val sourceSchemaVersion: String,
    val revisionDigest: String,
    val payloadJson: String,
)

/**
 * App-private, append-only Health Connect delivery queue. A changed source
 * record keeps its old revision because the unique key includes the payload
 * digest; rerunning the same read is a no-op.
 */
class HealthQueue(context: Context) {
    private val helper = QueueDatabase(context.applicationContext)

    fun enqueue(records: List<LocalObservation>): HealthQueueInsertResult {
        if (records.isEmpty()) return HealthQueueInsertResult(0)
        val database = helper.writableDatabase
        var inserted = 0
        database.beginTransaction()
        try {
            records.forEach { record ->
                val values = ContentValues().apply {
                    put("source_instance_key", record.sourceInstanceKey)
                    put("source_kind", record.sourceKind)
                    put("device_id", record.deviceId)
                    put("source_record_key", record.sourceRecordKey)
                    put("kind", record.kind)
                    put("record_type", record.recordType)
                    put("revision_digest", sha256(record.payloadJson))
                    put("occurred_at", record.occurredAt)
                    record.endedAt?.let { put("ended_at", it) }
                    put("collected_at", record.collectedAt)
                    put("source_schema_version", record.sourceSchemaVersion)
                    put("payload_json", record.payloadJson)
                }
                if (database.insertWithOnConflict(
                        "health_observations",
                        null,
                        values,
                        SQLiteDatabase.CONFLICT_IGNORE,
                    ) != -1L
                ) {
                    inserted++
                }
            }
            database.setTransactionSuccessful()
        } finally {
            database.endTransaction()
        }
        return HealthQueueInsertResult(inserted)
    }

    fun stats(): HealthQueueStats {
        helper.readableDatabase.rawQuery(
            "SELECT COUNT(DISTINCT source_instance_key || char(0) || source_record_key), COUNT(*), SUM(CASE WHEN delivered_at IS NULL THEN 1 ELSE 0 END) FROM health_observations",
            null,
        ).use { cursor ->
            if (!cursor.moveToFirst()) return HealthQueueStats(0, 0, 0)
            return HealthQueueStats(cursor.getLong(0), cursor.getLong(1), cursor.getLong(2))
        }
    }

    fun pending(limit: Int): List<QueuedObservation> {
        require(limit in 1..500) { "pending limit must be between 1 and 500" }
        helper.readableDatabase.rawQuery(
            """
            SELECT row_id, source_instance_key, source_kind, device_id,
                   source_record_key, kind, occurred_at, ended_at, collected_at,
                   source_schema_version, revision_digest, payload_json
            FROM health_observations
            WHERE delivered_at IS NULL
            ORDER BY row_id
            LIMIT ?
            """.trimIndent(),
            arrayOf(limit.toString()),
        ).use { cursor ->
            val values = mutableListOf<QueuedObservation>()
            while (cursor.moveToNext()) {
                values += QueuedObservation(
                    rowId = cursor.getLong(0),
                    sourceInstanceKey = cursor.getString(1),
                    sourceKind = cursor.getString(2),
                    deviceId = cursor.getString(3),
                    sourceRecordKey = cursor.getString(4),
                    kind = cursor.getString(5),
                    occurredAt = cursor.getString(6),
                    endedAt = cursor.getStringOrNull(7),
                    collectedAt = cursor.getString(8),
                    sourceSchemaVersion = cursor.getString(9),
                    revisionDigest = cursor.getString(10),
                    payloadJson = cursor.getString(11),
                )
            }
            return values
        }
    }

    fun markDelivered(rowIds: List<Long>, deliveredAt: String): Int {
        if (rowIds.isEmpty()) return 0
        val database = helper.writableDatabase
        database.beginTransaction()
        var changed = 0
        try {
            rowIds.forEach { rowId ->
                val values = ContentValues().apply { put("delivered_at", deliveredAt) }
                changed += database.update(
                    "health_observations",
                    values,
                    "row_id = ? AND delivered_at IS NULL",
                    arrayOf(rowId.toString()),
                )
            }
            database.setTransactionSuccessful()
        } finally {
            database.endTransaction()
        }
        return changed
    }

    private class QueueDatabase(context: Context) : SQLiteOpenHelper(
        context,
        DATABASE_NAME,
        null,
        DATABASE_VERSION,
    ) {
        override fun onCreate(database: SQLiteDatabase) {
            database.execSQL(
                """
                CREATE TABLE health_observations (
                    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    source_instance_key TEXT NOT NULL,
                    source_kind TEXT NOT NULL,
                    device_id TEXT NOT NULL,
                    source_record_key TEXT NOT NULL,
                    kind TEXT NOT NULL,
                    record_type TEXT NOT NULL,
                    revision_digest TEXT NOT NULL,
                    occurred_at TEXT NOT NULL,
                    ended_at TEXT,
                    collected_at TEXT NOT NULL,
                    source_schema_version TEXT NOT NULL,
                    payload_json TEXT NOT NULL,
                    delivered_at TEXT,
                    UNIQUE(source_record_key, revision_digest)
                )
                """.trimIndent(),
            )
            database.execSQL(
                "CREATE INDEX health_observations_occurred_at ON health_observations(occurred_at)",
            )
            database.execSQL(
                "CREATE INDEX health_observations_record_type ON health_observations(record_type)",
            )
            database.execSQL(
                "CREATE INDEX health_observations_delivery ON health_observations(delivered_at, row_id)",
            )
        }

        override fun onUpgrade(database: SQLiteDatabase, oldVersion: Int, newVersion: Int) {
            if (oldVersion < 2) {
                database.execSQL("ALTER TABLE health_observations ADD COLUMN source_instance_key TEXT NOT NULL DEFAULT 'health_connect:legacy'")
                database.execSQL("ALTER TABLE health_observations ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'health_connect'")
                database.execSQL("ALTER TABLE health_observations ADD COLUMN device_id TEXT NOT NULL DEFAULT 'android:legacy'")
                database.execSQL("ALTER TABLE health_observations ADD COLUMN kind TEXT NOT NULL DEFAULT 'health_connect.legacy'")
                database.execSQL("ALTER TABLE health_observations ADD COLUMN delivered_at TEXT")
                database.execSQL("CREATE INDEX IF NOT EXISTS health_observations_delivery ON health_observations(delivered_at, row_id)")
            }
            if (newVersion > 2) {
                throw IllegalStateException("unsupported Health Connect queue schema upgrade")
            }
        }

        private companion object {
            const val DATABASE_NAME = "telemetry-health.sqlite3"
            const val DATABASE_VERSION = 2
        }
    }

    private companion object {
        fun sha256(value: String): String = MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(Charsets.UTF_8))
            .joinToString("") { byte -> "%02x".format(byte) }
    }

    private fun android.database.Cursor.getStringOrNull(index: Int): String? =
        if (isNull(index)) null else getString(index)
}
