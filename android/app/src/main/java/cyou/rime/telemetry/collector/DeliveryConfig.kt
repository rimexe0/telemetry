package cyou.rime.telemetry.collector

import android.content.Context
import android.util.Base64
import java.nio.charset.StandardCharsets
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

// Earlier server addresses: Fedora, then Datalix before its Tailscale name
// changed from vps1 to datalix (2026-10).
internal val RETIRED_SERVERS = setOf("http://100.64.0.2:18080", "https://old-server.example.ts.net:8443")

class DeliveryConfig(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences(NAME, Context.MODE_PRIVATE)

    var serverUrl: String
        get() {
            val saved = preferences.getString(KEY_SERVER_URL, null).orEmpty()
            if (saved.trimEnd('/') in RETIRED_SERVERS) {
                val destination = BuildConfig.TELEMETRY_SERVER_URL
                check(preferences.edit().putString(KEY_SERVER_URL, destination).commit())
                return destination
            }
            return saved.ifBlank { adopt(KEY_SERVER_URL, BuildConfig.TELEMETRY_SERVER_URL) }
        }
        set(value) = preferences.edit().putString(KEY_SERVER_URL, value.trim().trimEnd('/')).apply()

    /**
     * The relay that carries sealed device requests, and the server's public
     * key for it. Saved on the phone, so a build without built-in addresses
     * (the public release) keeps using what an earlier build or the setup
     * screen saved.
     */
    var relayUrl: String
        get() = preferences.getString(KEY_RELAY_URL, null).orEmpty().ifBlank { adopt(KEY_RELAY_URL, BuildConfig.TELEMETRY_RELAY_URL) }
        set(value) = preferences.edit().putString(KEY_RELAY_URL, value.trim().trimEnd('/')).apply()

    var relayKey: String
        get() = preferences.getString(KEY_RELAY_KEY, null).orEmpty().ifBlank { adopt(KEY_RELAY_KEY, BuildConfig.TELEMETRY_RELAY_KEY) }
        set(value) = preferences.edit().putString(KEY_RELAY_KEY, value.trim()).apply()

    /** Saves a value this build was made with, so later builds keep it. */
    private fun adopt(key: String, builtIn: String): String {
        if (builtIn.isNotBlank()) check(preferences.edit().putString(key, builtIn).commit())
        return builtIn
    }

    var pairingId: String?
        get() = preferences.getString(KEY_PAIRING_ID, null)
        private set(value) {
            preferences.edit().apply {
                if (value.isNullOrBlank()) remove(KEY_PAIRING_ID) else putString(KEY_PAIRING_ID, value)
            }.apply()
        }

    fun savePairing(id: String, secret: String) {
        require(id.isNotBlank()) { "pairing id is required" }
        require(secret.isNotBlank()) { "pairing secret is required" }
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        check(preferences.edit().putString(KEY_PAIRING_ID, id)
            .putString(KEY_PAIRING_SECRET, Base64.encodeToString(cipher.doFinal(secret.toByteArray(StandardCharsets.UTF_8)), Base64.NO_WRAP))
            .putString(KEY_PAIRING_SECRET_IV, Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .commit()) { "Unable to persist pairing" }
    }

    fun pairingServer(): String? = preferences.getString(KEY_PAIRING_SERVER, null)

    fun newPairingSecret(server: String): String {
        val existing = pairingSecret()
        if (existing != null) {
            val pending = pairingServer()
            // A pairing the server never answered, or one for an address that
            // no longer exists, can only be started again.
            if (pending == server) return existing
            require(pairingId == null || pending?.trimEnd('/') in RETIRED_SERVERS) { "Finish pending pairing before changing servers" }
            clearPairing()
        }
        check(preferences.edit().putString(KEY_PAIRING_SERVER, server).commit())
        val bytes = ByteArray(32)
        java.security.SecureRandom().nextBytes(bytes)
        val secret = Base64.encodeToString(bytes, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
        saveEncrypted(KEY_PAIRING_SECRET, KEY_PAIRING_SECRET_IV, secret)
        check(preferences.edit().remove(KEY_PAIRING_ID).commit())
        return secret
    }

    fun pairingSecret(): String? = readEncrypted(KEY_PAIRING_SECRET, KEY_PAIRING_SECRET_IV)

    fun clearPairing() {
        check(preferences.edit().remove(KEY_PAIRING_ID).remove(KEY_PAIRING_SECRET).remove(KEY_PAIRING_SECRET_IV).remove(KEY_PAIRING_SERVER).commit())
    }

    fun completePairing(server: String, secret: String) {
        require(pairingServer() == server && pairingSecret() == secret)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        check(preferences.edit()
            .putString(KEY_SERVER_URL, server)
            .putString(KEY_TOKEN, Base64.encodeToString(cipher.doFinal(secret.toByteArray(StandardCharsets.UTF_8)), Base64.NO_WRAP))
            .putString(KEY_IV, Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .remove(KEY_PAIRING_ID).remove(KEY_PAIRING_SECRET).remove(KEY_PAIRING_SECRET_IV).remove(KEY_PAIRING_SERVER)
            .commit()) { "Unable to persist paired credential" }
    }

    fun saveToken(token: String) {
        saveEncrypted(KEY_TOKEN, KEY_IV, token)
    }

    fun token(): String? {
        return readEncrypted(KEY_TOKEN, KEY_IV)
    }

    private fun saveEncrypted(valueKey: String, ivKey: String, value: String) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        check(preferences.edit()
            .putString(valueKey, Base64.encodeToString(cipher.doFinal(value.toByteArray(StandardCharsets.UTF_8)), Base64.NO_WRAP))
            .putString(ivKey, Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .commit()) { "Unable to persist credential" }
    }

    private fun readEncrypted(valueKey: String, ivKey: String): String? {
        val ciphertext = preferences.getString(valueKey, null) ?: return null
        val iv = preferences.getString(ivKey, null) ?: return null
        return runCatching {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(
                Cipher.DECRYPT_MODE,
                key(),
                GCMParameterSpec(128, Base64.decode(iv, Base64.NO_WRAP)),
            )
            String(
                cipher.doFinal(Base64.decode(ciphertext, Base64.NO_WRAP)),
                StandardCharsets.UTF_8,
            )
        }.getOrNull()
    }

    fun tokenFingerprint(): String? = token()?.let { value ->
        MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(StandardCharsets.UTF_8))
            .joinToString("") { byte -> "%02x".format(byte) }
            .take(12)
    }

    private fun key(): SecretKey {
        val store = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        val existing = store.getKey(KEY_ALIAS, null)
        if (existing is SecretKey) return existing
        val generator = KeyGenerator.getInstance(KeyStoreKeyAlgorithm, ANDROID_KEYSTORE)
        generator.init(android.security.keystore.KeyGenParameterSpec.Builder(
            KEY_ALIAS,
            android.security.keystore.KeyProperties.PURPOSE_ENCRYPT or
                android.security.keystore.KeyProperties.PURPOSE_DECRYPT,
        ).setBlockModes(android.security.keystore.KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(android.security.keystore.KeyProperties.ENCRYPTION_PADDING_NONE)
            .build())
        return generator.generateKey()
    }

    private companion object {
        const val NAME = "telemetry-delivery"
        const val KEY_SERVER_URL = "server_url"
        const val KEY_RELAY_URL = "relay_url"
        const val KEY_RELAY_KEY = "relay_key"
        const val KEY_TOKEN = "token_ciphertext"
        const val KEY_IV = "token_iv"
        const val KEY_PAIRING_SERVER = "pairing_server"
        const val KEY_PAIRING_ID = "pairing_id"
        const val KEY_PAIRING_SECRET = "pairing_secret_ciphertext"
        const val KEY_PAIRING_SECRET_IV = "pairing_secret_iv"
        const val KEY_ALIAS = "telemetry_delivery_token"
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KeyStoreKeyAlgorithm = "AES"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
    }
}
