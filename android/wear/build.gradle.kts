plugins {
    id("com.android.application")
}

android {
    // The watch app shares the phone app's id and signing key: the watch
    // link only carries data between same-signed apps with the same id.
    namespace = "cyou.rime.telemetry.wear"
    compileSdk = 36

    defaultConfig {
        applicationId = "cyou.rime.telemetry.collector"
        minSdk = 30
        targetSdk = 34
        versionCode = 1
        versionName = "0.1.0"
    }
}

dependencies {
    implementation("com.google.android.gms:play-services-wearable:19.0.0")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20260814")
}
