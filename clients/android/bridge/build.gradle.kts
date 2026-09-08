plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "ru.nyxveil.bridge"
    compileSdk = 35

    defaultConfig {
        minSdk = 26
        consumerProguardFiles("consumer-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

val nativeAar = file("native/nyxveilbridge.aar")
if (!nativeAar.exists()) {
    logger.warn("Missing ${nativeAar.path} — run scripts/build-native.ps1 before assemble.")
}

dependencies {
    // Compile against gomobile bindings; app also ships the AAR for JNI packaging.
    if (nativeAar.exists()) {
        compileOnly(files(nativeAar))
        testImplementation(files(nativeAar))
    }
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    testImplementation("junit:junit:4.13.2")
}
