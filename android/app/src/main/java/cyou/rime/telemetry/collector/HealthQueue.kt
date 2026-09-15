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

data class HealthQueuePendingStats(
    val pendingRevisions: Long,
    val oldestPendingOccurredAt: String?,
    val oldestPendingCollectedAt: String?,
)

data class HealthQueueDeliveryStats(
    val lastAttemptedAt: String?,
    val lastSuccessfulAt: String?,
    val attemptedBodyBytes: Long,
    val attemptedBatches: Long,
    val failedBatches: Long,
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
    private val helper = sharedHelper(context.applicationContext)

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

    /** Returns the oldest pending row in FIFO order without scanning payloads. */
    fun pendingStats(): HealthQueuePendingStats {
        helper.readableDatabase.rawQuery(
            """
            SELECT COUNT(*),
                   (SELECT occurred_at FROM health_observations
                    WHERE delivered_at IS NULL ORDER BY row_id LIMIT 1),
                   (SELECT collected_at FROM health_observations
                    WHERE delivered_at IS NULL ORDER BY row_id LIMIT 1)
            FROM health_observations
            WHERE delivered_at IS NULL
            """.trimIndent(),
            null,
        ).use { cursor ->
            if (!cursor.moveToFirst()) return HealthQueuePendingStats(0, null, null)
            return HealthQueuePendingStats(
                pendingRevisions = cursor.getLong(0),
                oldestPendingOccurredAt = cursor.getStringOrNull(1),
                oldestPendingCollectedAt = cursor.getStringOrNull(2),
            )
        }
    }

    /**
     * Persists delivery counters alongside the queue. It never changes or
     * removes a queued observation.
     */
    fun recordDeliveryAttempt(
        attemptedAt: String,
        bodyBytes: Long,
        successful: Boolean,
    ) {
        require(bodyBytes >= 0) { "bodyBytes must not be negative" }
        val database = helper.writableDatabase
        database.beginTransaction()
        try {
            ensureDeliveryMetricsRow(database)
            if (successful) {
                database.execSQL(
                    """
                    UPDATE delivery_metrics
                    SET last_attempted_at = ?,
                        last_successful_at = ?,
                        attempted_body_bytes = attempted_body_bytes + ?,
                        attempted_batches = attempted_batches + 1
                    WHERE singleton_id = 1
                    """.trimIndent(),
                    arrayOf(attemptedAt, attemptedAt, bodyBytes),
                )
            } else {
                database.execSQL(
                    """
                    UPDATE delivery_metrics
                    SET last_attempted_at = ?,
                        attempted_body_bytes = attempted_body_bytes + ?,
                        attempted_batches = attempted_batches + 1,
                        failed_batches = failed_batches + 1
                    WHERE singleton_id = 1
                    """.trimIndent(),
                    arrayOf(attemptedAt, bodyBytes),
                )
            }
            database.setTransactionSuccessful()
        } finally {
            database.endTransaction()
        }
    }

    fun deliveryStats(): HealthQueueDeliveryStats {
        helper.readableDatabase.rawQuery(
            """
            SELECT last_attempted_at, last_successful_at, attempted_body_bytes,
                   attempted_batches, failed_batches
            FROM delivery_metrics
            WHERE singleton_id = 1
            """.trimIndent(),
            null,
        ).use { cursor ->
            if (!cursor.moveToFirst()) return HealthQueueDeliveryStats(null, null, 0, 0, 0)
            return HealthQueueDeliveryStats(
                lastAttemptedAt = cursor.getStringOrNull(0),
                lastSuccessfulAt = cursor.getStringOrNull(1),
                attemptedBodyBytes = cursor.getLong(2),
                attemptedBatches = cursor.getLong(3),
                failedBatches = cursor.getLong(4),
            )
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
            createDeliveryMetrics(database)
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
            if (oldVersion < 3) {
                createDeliveryMetrics(database)
            }
            if (newVersion > 3) {
                throw IllegalStateException("unsupported Health Connect queue schema upgrade")
            }
        }

        private fun createDeliveryMetrics(database: SQLiteDatabase) {
            database.execSQL(
                """
                CREATE TABLE IF NOT EXISTS delivery_metrics (
                    singleton_id INTEGER PRIMARY KEY CHECK(singleton_id = 1),
                    last_attempted_at TEXT,
                    last_successful_at TEXT,
                    attempted_body_bytes INTEGER NOT NULL DEFAULT 0,
                    attempted_batches INTEGER NOT NULL DEFAULT 0,
                    failed_batches INTEGER NOT NULL DEFAULT 0
                )
                """.trimIndent(),
            )
            database.execSQL(
                "INSERT OR IGNORE INTO delivery_metrics (singleton_id) VALUES (1)",
            )
        }

        private companion object {
            const val DATABASE_NAME = "telemetry-health.sqlite3"
            const val DATABASE_VERSION = 3
        }
    }

    private fun ensureDeliveryMetricsRow(database: SQLiteDatabase) {
        database.execSQL("INSERT OR IGNORE INTO delivery_metrics (singleton_id) VALUES (1)")
    }

    private companion object {
        private val HELPER_LOCK = Any()
        @Volatile private var sharedQueueDatabase: QueueDatabase? = null

        private fun sharedHelper(context: Context): QueueDatabase =
            sharedQueueDatabase ?: synchronized(HELPER_LOCK) {
                sharedQueueDatabase ?: QueueDatabase(context).also { sharedQueueDatabase = it }
            }

        fun sha256(value: String): String = MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(Charsets.UTF_8))
            .joinToString("") { byte -> "%02x".format(byte) }
    }

    private fun android.database.Cursor.getStringOrNull(index: Int): String? =
        if (isNull(index)) null else getString(index)
}
