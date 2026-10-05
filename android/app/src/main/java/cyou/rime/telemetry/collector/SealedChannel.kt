package cyou.rime.telemetry.collector

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.math.BigInteger
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.SecureRandom
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPublicKeySpec
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyAgreement
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * Reaches the server through a relay that must not read the traffic, such as
 * a Cloudflare Tunnel, so the phone does not need Tailscale. Each request,
 * with its path and the device token, is locked to the server's public key;
 * the answer is locked to a key only this request knows. The relay sees
 * sizes and timing only. The format is internal/sealed on the server.
 */
class SealedChannel(private val relayUrl: String, serverKey: String) {
    class Answer(val status: Int, val contentType: String, val body: ByteArray)

    private val serverPoint: ByteArray = Base64.getDecoder().decode(serverKey.trim())
    private val random = SecureRandom()

    init {
        require(serverPoint.size == POINT_SIZE && serverPoint[0] == 4.toByte()) { "server key is not an uncompressed P-256 point" }
    }

    /** Sends one request and returns the server's answer. Blocking IO; throws when the relay cannot be reached. */
    fun exchange(
        method: String,
        path: String,
        headers: Map<String, String>,
        body: ByteArray?,
        connectTimeoutMillis: Int = 10_000,
        readTimeoutMillis: Int = 30_000,
    ): Answer {
        val id = ByteArray(16).also(random::nextBytes).joinToString("") { "%02x".format(it) }
        val head = JSONObject()
            .put("method", method)
            .put("path", path)
            .put("headers", JSONObject(headers))
            .put("at", System.currentTimeMillis())
            .put("id", id)
        val (message, keys) = seal(head.toString().toByteArray(Charsets.UTF_8), body ?: ByteArray(0))
        val connection = URL(relayUrl.trimEnd('/') + "/").openConnection() as HttpURLConnection
        try {
            connection.requestMethod = "POST"
            connection.connectTimeout = connectTimeoutMillis
            connection.readTimeout = readTimeoutMillis
            connection.doOutput = true
            connection.useCaches = false
            connection.instanceFollowRedirects = false
            connection.setFixedLengthStreamingMode(message.size)
            connection.setRequestProperty("Content-Type", "application/octet-stream")
            connection.outputStream.use { it.write(message) }
            val status = connection.responseCode
            if (status != HttpURLConnection.HTTP_OK) {
                runCatching { connection.errorStream?.close() }
                throw SealedException("relay answered HTTP $status")
            }
            val sealed = connection.inputStream.use { it.readBytes() }
            return open(sealed, keys)
        } finally {
            connection.disconnect()
        }
    }

    /** Seals head and body; returns the message and the request's keys. */
    internal fun seal(head: ByteArray, body: ByteArray): Pair<ByteArray, Keys> {
        val generator = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1"), random) }
        val pair = generator.generateKeyPair()
        val ephemeral = pair.public as ECPublicKey
        val serverKey = KeyFactory.getInstance("EC").generatePublic(
            ECPublicKeySpec(
                ECPoint(BigInteger(1, serverPoint.copyOfRange(1, 33)), BigInteger(1, serverPoint.copyOfRange(33, 65))),
                ephemeral.params,
            ),
        )
        val agreement = KeyAgreement.getInstance("ECDH").apply {
            init(pair.private)
            doPhase(serverKey, true)
        }
        val sender = point(ephemeral)
        val keys = keys(agreement.generateSecret(), sender + serverPoint)
        val prefix = byteArrayOf(VERSION) + sender
        val plain = ByteArrayOutputStream().apply { write(head); write('\n'.code); write(body) }.toByteArray()
        return Pair(lock(keys.request, prefix, plain), keys)
    }

    internal fun open(sealed: ByteArray, keys: Keys): Answer {
        if (sealed.size < 1 + NONCE_SIZE + 16 || sealed[0] != VERSION) throw SealedException("unreadable answer")
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, SecretKeySpec(keys.answer, "AES"), GCMParameterSpec(128, sealed, 1, NONCE_SIZE))
        cipher.updateAAD(sealed, 0, 1)
        val plain = runCatching { cipher.doFinal(sealed, 1 + NONCE_SIZE, sealed.size - 1 - NONCE_SIZE) }
            .getOrElse { throw SealedException("answer failed to open") }
        val split = plain.indexOf('\n'.code.toByte())
        if (split < 0) throw SealedException("unreadable answer")
        val head = JSONObject(String(plain, 0, split, Charsets.UTF_8))
        return Answer(head.getInt("status"), head.optString("content_type"), plain.copyOfRange(split + 1, plain.size))
    }

    internal class Keys(val request: ByteArray, val answer: ByteArray)

    private fun lock(key: ByteArray, prefix: ByteArray, plain: ByteArray): ByteArray {
        val nonce = ByteArray(NONCE_SIZE).also(random::nextBytes)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce))
        cipher.updateAAD(prefix)
        return prefix + nonce + cipher.doFinal(plain)
    }

    private fun keys(secret: ByteArray, salt: ByteArray): Keys {
        val material = hkdf(secret, salt, INFO.toByteArray(Charsets.UTF_8), 64)
        return Keys(material.copyOfRange(0, 32), material.copyOfRange(32, 64))
    }

    class SealedException(message: String) : java.io.IOException(message)

    companion object {
        private const val VERSION: Byte = 1
        private const val POINT_SIZE = 65
        private const val NONCE_SIZE = 12
        private const val INFO = "telemetry sealed v1"

        /** The channel this build was made with, or null when it has none. */
        fun configured(): SealedChannel? {
            val url = BuildConfig.TELEMETRY_RELAY_URL
            val key = BuildConfig.TELEMETRY_RELAY_KEY
            if (url.isBlank() || key.isBlank()) return null
            return runCatching { SealedChannel(url, key) }.getOrNull()
        }

        private fun point(key: ECPublicKey): ByteArray =
            byteArrayOf(4) + fixed(key.w.affineX) + fixed(key.w.affineY)

        private fun fixed(value: BigInteger): ByteArray {
            val bytes = value.toByteArray()
            return when {
                bytes.size == 32 -> bytes
                bytes.size > 32 -> bytes.copyOfRange(bytes.size - 32, bytes.size)
                else -> ByteArray(32 - bytes.size) + bytes
            }
        }

        /** HKDF-SHA256 (RFC 5869). */
        internal fun hkdf(secret: ByteArray, salt: ByteArray, info: ByteArray, length: Int): ByteArray {
            val extract = Mac.getInstance("HmacSHA256").apply { init(SecretKeySpec(salt, "HmacSHA256")) }
            val prk = extract.doFinal(secret)
            val expand = Mac.getInstance("HmacSHA256").apply { init(SecretKeySpec(prk, "HmacSHA256")) }
            val out = ByteArrayOutputStream()
            var previous = ByteArray(0)
            var counter = 1
            while (out.size() < length) {
                expand.update(previous)
                expand.update(info)
                expand.update(counter.toByte())
                previous = expand.doFinal()
                out.write(previous)
                counter++
            }
            return out.toByteArray().copyOf(length)
        }
    }
}
