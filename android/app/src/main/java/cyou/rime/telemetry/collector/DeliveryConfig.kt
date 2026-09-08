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

class DeliveryConfig(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences(NAME, Context.MODE_PRIVATE)

    var serverUrl: String
        get() = preferences.getString(KEY_SERVER_URL, "").orEmpty()
        set(value) = preferences.edit().putString(KEY_SERVER_URL, value.trim().trimEnd('/')).apply()

    fun saveToken(token: String) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ciphertext = cipher.doFinal(token.toByteArray(StandardCharsets.UTF_8))
        preferences.edit()
            .putString(KEY_TOKEN, Base64.encodeToString(ciphertext, Base64.NO_WRAP))
            .putString(KEY_IV, Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .apply()
    }

    fun token(): String? {
        val ciphertext = preferences.getString(KEY_TOKEN, null) ?: return null
        val iv = preferences.getString(KEY_IV, null) ?: return null
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(
            Cipher.DECRYPT_MODE,
            key(),
            GCMParameterSpec(128, Base64.decode(iv, Base64.NO_WRAP)),
        )
        return String(
            cipher.doFinal(Base64.decode(ciphertext, Base64.NO_WRAP)),
            StandardCharsets.UTF_8,
        )
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
        const val KEY_TOKEN = "token_ciphertext"
        const val KEY_IV = "token_iv"
        const val KEY_ALIAS = "telemetry_delivery_token"
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KeyStoreKeyAlgorithm = "AES"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
    }
}
