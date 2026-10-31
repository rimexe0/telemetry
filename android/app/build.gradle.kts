import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

val samsungHealthSdk: File = rootProject.file("private/samsung-health-data-api-1.1.0.aar")

// Addresses come from -P, gradle.properties or android/local.properties.
val localProperties = Properties().also { properties ->
    rootProject.file("local.properties").takeIf { it.exists() }?.reader()?.use { properties.load(it) }
}
fun setting(name: String): String? = (project.findProperty(name) ?: localProperties.getProperty(name))?.toString()

android {
    namespace = "cyou.rime.telemetry.collector"
    compileSdk = 36

    buildFeatures {
        buildConfig = true
        compose = true
    }

    defaultConfig {
        applicationId = "cyou.rime.telemetry.collector"
        // Samsung's Health Data SDK needs Android 10.
        minSdk = if (samsungHealthSdk.exists()) 29 else 28
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        val telemetryServerUrl = setting("telemetryServerUrl")
            ?.trim()
            ?.trimEnd('/')
            ?.takeIf { it.isNotBlank() }
            ?: "https://server.example.ts.net:8443"
        buildConfigField("String", "TELEMETRY_SERVER_URL", "\"$telemetryServerUrl\"")
        // Device requests go sealed through this relay (a Cloudflare Tunnel to
        // the server's sealed door), so the phone does not need Tailscale. The
        // key is the server's public key from its log line "Sealed device requests".
        val telemetryRelayUrl = setting("telemetryRelayUrl")?.trim()?.trimEnd('/')
            ?: "https://relay.example.com"
        val telemetryRelayKey = setting("telemetryRelayKey")?.trim()
            ?: "BDQAImuIn7FQicgxZZT4ekXWleG0QJlUflNjYQnBUTU7m39PHHlg0TP3d08OeB1xffb1WIrv05kvkL38Zwa8mZk="
        buildConfigField("String", "TELEMETRY_RELAY_URL", "\"$telemetryRelayUrl\"")
        buildConfigField("String", "TELEMETRY_RELAY_KEY", "\"$telemetryRelayKey\"")
    }

    // Samsung Health is read directly through Samsung's Health Data SDK when
    // its file is in android/private/ (proprietary, so never committed);
    // without it the app builds with a reader that reports "not built in".
    val samsungSources = if (samsungHealthSdk.exists()) "src/samsung/java" else "src/samsungStub/java"
    sourceSets["main"].java.srcDir(samsungSources)
    sourceSets["main"].kotlin.srcDir(samsungSources)
}


dependencies {
    if (samsungHealthSdk.exists()) {
        implementation(files(samsungHealthSdk))
        implementation("org.jetbrains.kotlin:kotlin-parcelize-runtime:2.2.10")
        // The SDK's own dependencies, as openHealth's reader packages them.
        implementation("com.google.code.gson:gson:2.14.0")
        implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    }
    testImplementation("junit:junit:4.13.2")
    // JVM tests need real JSON behavior; Android's mockable jar only has stubs.
    testImplementation("org.json:json:20260814")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation(platform("androidx.compose:compose-bom:2026.05.01"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.health.connect:connect-client:1.1.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("androidx.work:work-runtime-ktx:2.10.1")
    implementation("com.google.android.gms:play-services-wearable:19.0.0")
}
