import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val versionFile = rootProject.file("VERSION")
val projectVersion = versionFile.readText().trim()
val versionCodeFromProps = (project.findProperty("nyxveil.versionCode") as String?)?.toIntOrNull() ?: 10000

android {
    namespace = "ru.nyxveil.android"
    compileSdk = 35

    defaultConfig {
        applicationId = "ru.nyxveil.android"
        minSdk = 26
        targetSdk = 35
        versionCode = versionCodeFromProps
        versionName = projectVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables.useSupportLibrary = true

        buildConfigField("String", "CONTROL_PLANE_BASE_URL", "\"https://cp.nyxveil.ru:18443\"")
        buildConfigField("String", "NVP_PROTOCOL", "\"NVP/1\"")
    }

    signingConfigs {
        // Optional external signing via root keystore.properties (gitignored).
        val keystorePropsFile = rootProject.file("keystore.properties")
        if (keystorePropsFile.exists()) {
            val props = Properties().apply {
                keystorePropsFile.inputStream().use { load(it) }
            }
            val store = props.getProperty("storeFile")
            val storePass = props.getProperty("storePassword")
            val alias = props.getProperty("keyAlias")
            val keyPass = props.getProperty("keyPassword")
            if (!store.isNullOrBlank() && !storePass.isNullOrBlank() &&
                !alias.isNullOrBlank() && !keyPass.isNullOrBlank()
            ) {
                create("release") {
                    storeFile = rootProject.file(store)
                    storePassword = storePass
                    keyAlias = alias
                    keyPassword = keyPass
                }
            }
        }
    }

    buildTypes {
        debug {
            applicationIdSuffix = ".debug"
            versionNameSuffix = "-debug"
            isMinifyEnabled = false
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            signingConfigs.findByName("release")?.let { signingConfig = it }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    implementation(project(":bridge"))

    val nativeAar = rootProject.file("bridge/native/nyxveilbridge.aar")
    if (!nativeAar.exists()) {
        throw GradleException(
            "Missing ${nativeAar.path}. Run scripts/build-native.ps1 (or build-apk.ps1) first.",
        )
    }
    // Ships JNI (.so) + gomobile Java bindings into the APK.
    implementation(files(nativeAar))

    val composeBom = platform("androidx.compose:compose-bom:2024.10.01")
    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.7")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.7")
    implementation("androidx.navigation:navigation-compose:2.8.4")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")

    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.9.0")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.6.1")
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
}

// kotlinx.serialization plugin not required if we use manual JSON for CP for now
